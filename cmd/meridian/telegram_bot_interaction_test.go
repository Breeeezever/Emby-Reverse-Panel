package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTelegramBotMenusAndWizardStayInSameMessage(t *testing.T) {
	b, f := newTestTelegramBot(t)
	tgText(t, b, "/start")
	tgHandle(t, b, testTelegramCallback(101, "sites:0"))
	tgHandle(t, b, testTelegramCallback(101, "add"))
	tgText(t, b, "New cinema")
	tgChoose(t, b, "http")
	tgText(t, b, "127.0.0.1")
	tgChoose(t, b, "8096")
	tgHandle(t, b, testTelegramCallback(101, "cancel"))
	sent, edited := 0, 0
	for _, method := range f.calls {
		if method == "sendMessage" {
			sent++
		}
		if method == "editMessageText" {
			edited++
		}
	}
	if sent != 1 || edited != 7 {
		t.Fatalf("navigation sent %d new messages and %d edits", sent, edited)
	}
	for _, m := range f.messages[1:] {
		if m["message_id"] != float64(1) || m["chat_id"] != float64(101) {
			t.Fatalf("edited wrong panel: %v", m)
		}
	}
	// A callback already identifies its bot message after a process restart.
	b.panels = nil
	callback := testTelegramCallback(101, "home")
	callback.Callback.Message.ID = 77
	tgHandle(t, b, callback)
	if f.messages[len(f.messages)-1]["message_id"] != float64(77) {
		t.Fatal("restart did not adopt clicked message")
	}
	// Another administrator gets a separate panel.
	tgHandle(t, b, testTelegramMessage(202, "/start"))
	if f.messages[len(f.messages)-1]["message_id"] != nil || b.panels[101] != 77 {
		t.Fatal("administrator panels mixed")
	}
}

func TestTelegramReminderActionSnoozesCurrentExpiry(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	site, err := b.app.db.CreateSiteRecord(Site{Name: "Reminder", TargetURL: "https://origin.example", ListenPort: freePort(t), IngressMode: ingressModePath, PathPrefix: "/reminder", StreamHosts: "[]", FailoverTargets: "[]"})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	days := 0
	account, err := b.app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "demo", ExpiresAt: time.Now().Add(2 * time.Hour).Format(time.RFC3339), ReminderDays: &days})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	if err := b.handleReminderAction(context.Background(), 101, account.ID, "snooze"); err != nil {
		t.Fatalf("snooze reminder: %v", err)
	}
	status, until, err := b.app.db.accountReminderAction(account.ID, account.ExpiresAt, days)
	if err != nil || status != "snoozed" || until == "" {
		t.Fatalf("snooze status=%q until=%q err=%v", status, until, err)
	}
	if !strings.Contains(fake.lastText(), "明天") {
		t.Fatalf("confirmation=%q", fake.lastText())
	}
}

func TestSnoozedReminderResendsOnceAfterRestart(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	site := accountWorkflowSite(t, b.app, "snoozed")
	now := time.Now().UTC().Truncate(time.Second)
	days := 0
	account, err := b.app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "snoozed-user", ExpiresAt: now.Add(-time.Hour).Format(time.RFC3339), ReminderDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	// Include receipts written by an older build without an attempt ledger.
	for _, chatID := range []int64{101, 202} {
		if _, err := b.app.db.db.Exec(`INSERT INTO account_reminder_deliveries(account_id, expires_at, reminder_days, chat_id) VALUES(?,?,?,?)`, account.ID, account.ExpiresAt, days, chatID); err != nil {
			t.Fatal(err)
		}
	}
	until := now.Add(24 * time.Hour)
	if err := b.app.db.saveAccountReminderAction(account.ID, account.ExpiresAt, days, "snoozed", until, "test"); err != nil {
		t.Fatal(err)
	}
	worker := &telegramBot{app: b.app, settings: b.settings, token: b.token, client: fake.client()}
	if err := worker.deliverAccountReminders(context.Background(), until.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 0 {
		t.Fatal("snoozed reminder sent before its due time")
	}
	for _, at := range []time.Time{until, until.Add(time.Minute)} {
		if err := worker.deliverAccountReminders(context.Background(), at); err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.messages) != 2 {
		t.Fatalf("got %d reminders, want one per administrator", len(fake.messages))
	}
	records, err := b.app.db.listAccountReminderRecords(site.ID, account.ID)
	if err != nil || len(records) != 4 {
		t.Fatalf("legacy and new delivery history: count=%d, err=%v", len(records), err)
	}
}

func TestTelegramPanelEditErrorsAvoidDuplicateMessages(t *testing.T) {
	for _, tc := range []struct {
		name, description   string
		code                int
		wantSend, wantError bool
	}{
		{"unchanged", "Bad Request: message is not modified: specified new message content and reply markup are exactly the same", 400, false, false},
		{"deleted", "Bad Request: message to edit not found", 400, true, false},
		{"uneditable", "Bad Request: message can't be edited", 400, true, false},
		{"blocked", "Forbidden: bot was blocked by the user", 403, false, true},
		{"rate limited", "Too Many Requests", 429, false, true},
		{"unknown failure", "Bad Request: invalid request", 400, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := newTestTelegramBot(t)
			b.panels = map[int64]int64{101: 25}
			calls := []string{}
			b.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				calls = append(calls, method)
				var payload map[string]any
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if method == "sendMessage" {
					if payload["message_id"] != nil {
						t.Error("new message retained edit ID")
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":26}}`))}, nil
				}
				// Empty controls clear the previous inline keyboard during an edit.
				markup := payload["reply_markup"].(map[string]any)
				if len(markup["inline_keyboard"].([]any)) != 0 {
					t.Fatal("stale keyboard retained")
				}
				body, _ := json.Marshal(map[string]any{"ok": false, "error_code": tc.code, "description": tc.description})
				return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}
			err := b.send(context.Background(), 101, "same content", nil)
			if (err != nil) != tc.wantError || slices.Contains(calls, "sendMessage") != tc.wantSend {
				t.Fatalf("err=%v calls=%v", err, calls)
			}
			if tc.wantSend && b.panels[101] != 26 {
				t.Fatal("replacement panel not remembered")
			}
		})
	}
	t.Run("network error", func(t *testing.T) {
		b, _ := newTestTelegramBot(t)
		b.panels = map[int64]int64{101: 25}
		calls := 0
		b.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("network failure") })}
		if err := b.send(context.Background(), 101, "content", nil); err == nil || calls != 1 {
			t.Fatalf("ambiguous edit sent duplicate message: calls=%d err=%v", calls, err)
		}
	})
}

func TestTelegramCommandsReplaceLegacyListsAndLocalizedOverrides(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	var calls []map[string]any
	b.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		payload["method"] = method
		calls = append(calls, payload)
		if method == "sendMessage" || method == "editMessageText" {
			t.Fatal("command registration sent a message")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`))}, nil
	})}
	if err := b.syncCommands(context.Background()); err != nil {
		t.Fatal(err)
	}
	scopes := map[string]bool{}
	for _, call := range calls {
		if call["method"] != "setMyCommands" {
			continue
		}
		scope := call["scope"].(map[string]any)
		key := fmt.Sprintf("%v/%v/%v", scope["type"], scope["chat_id"], call["language_code"])
		scopes[key] = true
		commands := call["commands"].([]any)
		want := meridianTelegramCommands()
		if len(commands) != len(want) {
			t.Fatal("partial command update would retain legacy commands")
		}
		for i, command := range commands {
			if command.(map[string]any)["command"] != want[i].Command {
				t.Fatal("wrong command list")
			}
		}
	}
	for _, key := range []string{"default/<nil>/", "all_private_chats/<nil>/zh", "chat/101/", "chat/202/en"} {
		if !scopes[key] {
			t.Fatalf("missing scope %s", key)
		}
	}
	count := len(calls)
	if err := b.syncAdministratorCommands(context.Background(), telegramUser{ID: 101, LanguageCode: "ru-RU"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != count+1 || calls[len(calls)-1]["language_code"] != "ru" {
		t.Fatal("localized legacy command override was not replaced")
	}
	count = len(calls)
	if err := b.syncAdministratorCommands(context.Background(), telegramUser{ID: 101, LanguageCode: "ru-RU"}); err != nil || len(calls) != count {
		t.Fatal("unchanged command menu registered on every click")
	}
}

func TestTelegramCommandRegistrationToleratesNewAdminWithoutChat(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	globalCommands := 0
	b.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload struct {
			Scope struct {
				Type string `json:"type"`
			} `json:"scope"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload.Scope.Type == "chat" {
			return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))}, nil
		}
		globalCommands++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`))}, nil
	})}
	if err := b.syncCommands(context.Background()); err != nil || globalCommands != 7 {
		t.Fatalf("new admin blocked bot startup: %v calls=%d", err, globalCommands)
	}
	if b.commandScopes["chat:101/"] {
		t.Fatal("failed chat scope marked synced")
	}
}
