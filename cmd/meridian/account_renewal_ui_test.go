package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTelegramAccountRenewalPresetsAndCustomDuration(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	site, err := b.app.db.CreateSiteRecord(Site{
		Name:        "Renewal UI",
		TargetURL:   "https://origin.example",
		ListenPort:  freePort(t),
		IngressMode: ingressModePath,
		PathPrefix:  "/renewal-ui",
		StreamHosts: "[]", FailoverTargets: "[]",
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	account, err := b.app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{
		Username:  "renewal-user",
		ExpiresAt: time.Now().AddDate(0, 0, 2).Format("2006-01-02"),
	})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}

	renew := fmt.Sprintf("account:%d:%d:renew", site.ID, account.ID)
	tgHandle(t, b, testTelegramCallback(101, renew))
	if session := b.sessions[101]; session == nil || session.Step != "renew_days" {
		t.Fatalf("preset renewal did not start: %+v", b.sessions[101])
	}
	fake.mu.Lock()
	presetMarkup, _ := json.Marshal(fake.messages[len(fake.messages)-1]["reply_markup"])
	fake.mu.Unlock()
	if !strings.Contains(string(presetMarkup), "一月（30天）") || !strings.Contains(string(presetMarkup), "一季度（90天）") || !strings.Contains(string(presetMarkup), "一年（365天）") || !strings.Contains(string(presetMarkup), "自定义天数") {
		t.Fatalf("preset choices missing: %s", presetMarkup)
	}
	tgChoose(t, b, "90")
	if session := b.sessions[101]; session == nil || session.Step != "confirm" || !strings.Contains(session.Summary, "90 天") {
		t.Fatalf("preset confirmation missing: %+v", b.sessions[101])
	}
	tgChoose(t, b, "yes")
	updated, err := b.app.db.getSiteAccount(site.ID, account.ID)
	if err != nil {
		t.Fatalf("read preset renewal: %v", err)
	}
	history, err := b.app.db.listAccountRenewals(site.ID, account.ID)
	if err != nil || len(history) != 1 || history[0].DurationDays != 90 || updated.ExpiresAt == account.ExpiresAt {
		t.Fatalf("preset renewal history=%+v account=%+v err=%v", history, updated, err)
	}
	if !strings.Contains(fake.lastText(), "已续期 90 天") {
		t.Fatalf("preset result missing: %s", fake.lastText())
	}

	tgHandle(t, b, testTelegramCallback(101, renew))
	tgChoose(t, b, "custom")
	if session := b.sessions[101]; session == nil || session.Step != "renew_custom_days" || !strings.Contains(fake.lastText(), "1–3650") {
		t.Fatalf("custom renewal prompt missing: %+v / %s", b.sessions[101], fake.lastText())
	}
	tgChoose(t, b, "back")
	if session := b.sessions[101]; session == nil || session.Step != "renew_days" {
		t.Fatalf("custom back did not return to presets: %+v", b.sessions[101])
	}
	tgChoose(t, b, "custom")
	tgText(t, b, "3650")
	if session := b.sessions[101]; session == nil || session.Step != "confirm" || !strings.Contains(session.Summary, "3650 天") {
		t.Fatalf("custom confirmation missing: %+v", b.sessions[101])
	}
	tgChoose(t, b, "yes")
	history, err = b.app.db.listAccountRenewals(site.ID, account.ID)
	if err != nil || len(history) != 2 || history[0].DurationDays != 3650 || history[1].DurationDays != 90 {
		t.Fatalf("custom renewal history=%+v err=%v", history, err)
	}
}

func TestTelegramSiteListMarksSitesWithSavedAccounts(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	withoutAccount, err := b.app.db.CreateSiteRecord(Site{
		Name: "No account", TargetURL: "https://one.example", ListenPort: freePort(t),
		IngressMode: ingressModePath, PathPrefix: "/one", StreamHosts: "[]", FailoverTargets: "[]",
	})
	if err != nil {
		t.Fatalf("create site without account: %v", err)
	}
	withAccount, err := b.app.db.CreateSiteRecord(Site{
		Name: "Has account", TargetURL: "https://two.example", ListenPort: freePort(t),
		IngressMode: ingressModePath, PathPrefix: "/two", StreamHosts: "[]", FailoverTargets: "[]",
	})
	if err != nil {
		t.Fatalf("create site with account: %v", err)
	}
	if _, err := b.app.db.saveSiteAccount(withAccount.ID, 0, SiteAccountInput{Username: "saved-user"}); err != nil {
		t.Fatalf("save account: %v", err)
	}
	sites, err := b.app.db.ListSites()
	if err != nil {
		t.Fatalf("list sites: %v", err)
	}
	for _, site := range sites {
		switch site.ID {
		case withoutAccount.ID:
			if site.AccountCount != 0 {
				t.Fatalf("site without account count=%d", site.AccountCount)
			}
		case withAccount.ID:
			if site.AccountCount != 1 {
				t.Fatalf("site account count=%d", site.AccountCount)
			}
		}
	}
	if err := b.listSites(context.Background(), 101, "accounts", 0); err != nil {
		t.Fatalf("list sites in Telegram: %v", err)
	}
	if !strings.Contains(fake.lastText(), "👤数字表示") {
		t.Fatalf("marker help missing: %s", fake.lastText())
	}
	fake.mu.Lock()
	markup, _ := json.Marshal(fake.messages[len(fake.messages)-1]["reply_markup"])
	fake.mu.Unlock()
	if !strings.Contains(string(markup), "👤1 Has account") || strings.Contains(string(markup), "👤0 No account") {
		t.Fatalf("site account markers=%s", markup)
	}
}
