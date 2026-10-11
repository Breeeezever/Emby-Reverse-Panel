package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTelegramAPI struct {
	mu       sync.Mutex
	calls    []string
	messages []map[string]any
	updates  func(map[string]any) []telegramUpdate
}

func (f *fakeTelegramAPI) client() *http.Client {
	return &http.Client{CheckRedirect: telegramBotHTTPClient.CheckRedirect, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.telegram.org" {
			return nil, fmt.Errorf("unexpected destination")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		parts := strings.Split(r.URL.Path, "/")
		method := parts[len(parts)-1]
		f.mu.Lock()
		f.calls = append(f.calls, method)
		if method == "sendMessage" || method == "editMessageText" {
			f.messages = append(f.messages, body)
		}
		f.mu.Unlock()
		result := any(map[string]any{"message_id": 1})
		if method == "getUpdates" {
			result = []telegramUpdate{}
			if f.updates != nil {
				result = f.updates(body)
			}
		}
		data, _ := json.Marshal(map[string]any{"ok": true, "result": result})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
	})}
}
func (f *fakeTelegramAPI) lastText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		return ""
	}
	s, _ := f.messages[len(f.messages)-1]["text"].(string)
	return s
}
func newTestTelegramBot(t *testing.T) (*telegramBot, *fakeTelegramAPI) {
	t.Helper()
	a := newTestApp(t)
	a.dynamicRouteKey = bytes.Repeat([]byte{0x71}, 32)
	if err := a.pm.ConfigureDynamicDiscovery(a.dynamicRouteKey, "panel.example.test", 19093, nil); err != nil {
		t.Fatal(err)
	}
	token, err := encryptTelegramBotToken("123456:bot-secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg := TelegramReportSettings{Bot: &TelegramBotSettings{Enabled: true, AdminIDs: []int64{101, 202}, PublicURL: "https://panel.example.test:8443"}, ScheduleTime: "20:00", Frequency: "daily", Weekday: 1}
	if err = a.db.saveTelegramReportSettings(cfg, token, true); err != nil {
		t.Fatal(err)
	}
	saved, err := a.db.telegramBotSettings()
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeTelegramAPI{}
	bot := &telegramBot{app: a, token: "123456:bot-secret", settings: saved, client: fake.client(), sessions: map[int64]*telegramSession{}}
	t.Cleanup(func() {
		sites, _ := a.db.ListSites()
		for _, s := range sites {
			_ = a.pm.StopSite(s.ID)
		}
	})
	return bot, fake
}
func testTelegramMessage(id int64, text string) telegramUpdate {
	return telegramUpdate{ID: 1, Message: &telegramMessage{ID: 1, Date: time.Now().Unix(), From: telegramUser{ID: id}, Chat: telegramChat{ID: id, Type: "private"}, Text: text}}
}
func testTelegramCallback(id int64, data string) telegramUpdate {
	return telegramUpdate{ID: 2, Callback: &telegramCallback{ID: "query", From: telegramUser{ID: id}, Data: data, Message: &telegramMessage{ID: 1, Date: time.Now().Unix(), From: telegramUser{ID: 999, IsBot: true}, Chat: telegramChat{ID: id, Type: "private"}}}}
}
func tgHandle(t *testing.T, b *telegramBot, u telegramUpdate) {
	t.Helper()
	if err := b.handleUpdate(context.Background(), u); err != nil {
		t.Fatal(err)
	}
}
func tgText(t *testing.T, b *telegramBot, s string) {
	t.Helper()
	tgHandle(t, b, testTelegramMessage(101, s))
}
func tgChoose(t *testing.T, b *telegramBot, s string) {
	t.Helper()
	session := b.sessions[101]
	if session == nil {
		t.Fatalf("no session for %s", s)
	}
	tgHandle(t, b, testTelegramCallback(101, "w:"+session.Nonce+":"+s))
}

func TestTelegramBotRejectsUnauthorizedAndGroupUpdates(t *testing.T) {
	b, f := newTestTelegramBot(t)
	group := testTelegramMessage(101, "/report")
	group.Message.Chat = telegramChat{ID: -100, Type: "supergroup"}
	forged := testTelegramMessage(101, "/sites")
	forged.Message.Chat.ID = 303
	bot := testTelegramMessage(101, "/add")
	bot.Message.From.IsBot = true
	inline := telegramUpdate{Callback: &telegramCallback{From: telegramUser{ID: 101}, Data: "report"}}
	old := testTelegramMessage(101, "/report")
	old.Message.Date = time.Now().Add(-11 * time.Minute).Unix()
	for _, u := range []telegramUpdate{testTelegramMessage(303, "/report"), testTelegramCallback(303, "report"), group, forged, bot, inline, old} {
		tgHandle(t, b, u)
	}
	if len(f.calls) != 0 || len(b.sessions) != 0 {
		t.Fatalf("unauthorized updates caused effects: %v", f.calls)
	}
	tgText(t, b, "/start")
	if !strings.Contains(f.lastText(), "Meridian 管理") {
		t.Fatal(f.lastText())
	}
}
func TestTelegramBotAddEditReadToggleDeleteAndDuplicateConfirmation(t *testing.T) {
	b, f := newTestTelegramBot(t)
	tgText(t, b, "/add")
	tgText(t, b, "影院")
	tgChoose(t, b, "http")
	tgText(t, b, "127.0.0.1")
	for _, bad := range []string{"888888", "0", "-1", "1.5", "1e3"} {
		tgText(t, b, bad)
		if b.sessions[101].Step != "port" {
			t.Fatalf("invalid port %s advanced wizard", bad)
		}
	}
	tgChoose(t, b, "8096")
	tgChoose(t, b, "path")
	tgText(t, b, "/cinema")
	// /cinema is form input, not a command: path prefixes must remain usable.
	if b.sessions[101].Step != "playback" {
		t.Fatalf("path entry failed: %s", f.lastText())
	}
	tgChoose(t, b, "auto")
	confirm := "w:" + b.sessions[101].Nonce + ":yes"
	sites, _ := b.app.db.ListSites()
	if len(sites) != 0 {
		t.Fatal("created before confirmation")
	}
	tgHandle(t, b, testTelegramCallback(202, confirm))
	sites, _ = b.app.db.ListSites()
	if len(sites) != 0 {
		t.Fatal("other administrator confirmed draft")
	}
	tgHandle(t, b, testTelegramCallback(101, confirm))
	tgHandle(t, b, testTelegramCallback(101, confirm))
	sites, _ = b.app.db.ListSites()
	if len(sites) != 1 {
		t.Fatalf("created %d sites: %s", len(sites), f.lastText())
	}
	site := sites[0]
	if site.TargetURL != "http://127.0.0.1:8096" || !site.DynamicDiscoveryEnabled || site.IngressMode != "path" || !b.app.pm.IsRunning(site.ID) {
		t.Fatalf("wrong site: %+v", site)
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("site:%d:address", site.ID)))
	if !strings.Contains(f.lastText(), "https://panel.example.test:8443/cinema/") {
		t.Fatal(f.lastText())
	}
	// Edit one field, preserving discovery and all other existing settings.
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("edit:%d:quota", site.ID)))
	tgText(t, b, "100")
	tgChoose(t, b, "yes")
	updated, _ := b.app.db.GetSite(site.ID)
	if updated.TrafficQuota != 100*1073741824 || !updated.DynamicDiscoveryEnabled {
		t.Fatalf("edit damaged config: %+v", updated)
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("site:%d:traffic", site.ID)))
	if !strings.Contains(f.lastText(), "100.00 GB") {
		t.Fatal(f.lastText())
	}
	for _, mode := range []string{"direct", "manual", "auto"} {
		tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("edit:%d:playback", site.ID)))
		tgChoose(t, b, mode)
		if mode == "manual" {
			tgChoose(t, b, "same")
		}
		tgChoose(t, b, "yes")
		edited, _ := b.app.db.GetSite(site.ID)
		if (edited.MainVideoStreamMode == "direct") != (mode == "direct") || edited.DynamicDiscoveryEnabled != (mode == "auto") {
			t.Fatalf("mode %s saved incorrectly: %+v", mode, edited)
		}
	}
	for _, mode := range []string{"passthrough", "infuse", "client", "web"} {
		tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("edit:%d:ua", site.ID)))
		tgChoose(t, b, mode)
		tgChoose(t, b, "yes")
		edited, _ := b.app.db.GetSite(site.ID)
		if edited.UAMode != mode {
			t.Fatalf("ua %s not saved: %s", mode, f.lastText())
		}
	}
	tgText(t, b, "/report")
	if !strings.Contains(f.lastText(), "Meridian 日报") {
		t.Fatal(f.lastText())
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("site:%d:toggle", site.ID)))
	toggle := "w:" + b.sessions[101].Nonce + ":yes"
	tgHandle(t, b, testTelegramCallback(101, toggle))
	tgHandle(t, b, testTelegramCallback(101, toggle))
	updated, _ = b.app.db.GetSite(site.ID)
	if updated.Enabled || b.app.pm.IsRunning(site.ID) {
		t.Fatal("duplicate confirmation toggled back on")
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("site:%d:delete", site.ID)))
	tgChoose(t, b, "yes")
	sites, _ = b.app.db.ListSites()
	if len(sites) != 0 {
		t.Fatalf("site not deleted: %s", f.lastText())
	}
}

func TestTelegramBotAccountManagementAndLaunchImport(t *testing.T) {
	b, f := newTestTelegramBot(t)
	site, err := b.app.db.CreateSiteRecord(Site{
		Name: "account-site", IngressMode: ingressModePath, PathPrefix: "/account-site",
		ListenPort: 12346, TargetURL: "http://127.0.0.1:8096", PlaybackMode: "direct", UAMode: "passthrough",
	})
	if err != nil {
		t.Fatal(err)
	}

	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("site:%d:accounts", site.ID)))
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("account:%d:new", site.ID)))
	tgText(t, b, "demo")
	tgChoose(t, b, "same")
	tgChoose(t, b, "secret")
	tgChoose(t, b, "second")
	tgChoose(t, b, "skip")
	tgChoose(t, b, "skip")
	tgChoose(t, b, "yes")
	accounts, err := b.app.db.listSiteAccounts(site.ID)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%d err=%v", len(accounts), err)
	}
	plain, err := accountPlaintext(accounts[0])
	if err != nil || plain.Username != "demo" || plain.Password != "secret" || plain.SecurityPassword != "second" {
		t.Fatalf("account=%+v err=%v", plain, err)
	}

	// The site-specific export menu must route through the site ID and return
	// the requested client format instead of treating "export" as an account ID.
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("account:%d:export", site.ID)))
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("account:%d:export:original", site.ID)))
	if !strings.Contains(f.lastText(), "demo") || !strings.Contains(f.lastText(), "http://127.0.0.1:8096") {
		t.Fatalf("account export=%s", f.lastText())
	}

	tgText(t, b, "/add")
	tgChoose(t, b, "launch")
	tgText(t, b, "**launch-site**\n· 用户名称 | `launch-user`\n· 用户密码 | `launch-pass`\nhttps://origin.example/emby\nhttps://backup.example/emby")
	tgChoose(t, b, "yes")
	sites, err := b.app.db.ListSites()
	if err != nil || len(sites) != 2 {
		t.Fatalf("sites=%d err=%v", len(sites), err)
	}
	var imported *Site
	for i := range sites {
		if sites[i].Name == "launch-site" {
			imported = &sites[i]
		}
	}
	if imported == nil || imported.TargetURL != "https://origin.example/emby" || len(imported.FailoverTargetList) != 1 || imported.FailoverTargetList[0] != "https://backup.example/emby" {
		t.Fatalf("launch site=%+v", imported)
	}
	importedAccounts, err := b.app.db.listSiteAccounts(imported.ID)
	if err != nil || len(importedAccounts) != 1 {
		t.Fatalf("launch accounts=%d err=%v", len(importedAccounts), err)
	}
}
func TestTelegramBotRejectsStaleDraftAndRevokedAdministrator(t *testing.T) {
	b, f := newTestTelegramBot(t)
	site, err := b.app.db.CreateSiteRecord(Site{Name: "original", IngressMode: ingressModePath, PathPrefix: "/one", ListenPort: 12345, TargetURL: "http://127.0.0.1:8096", PlaybackMode: "direct", UAMode: "passthrough"})
	if err != nil {
		t.Fatal(err)
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("edit:%d:name", site.ID)))
	tgText(t, b, "from bot")
	if _, err = b.app.db.db.Exec(`UPDATE sites SET name='from panel' WHERE id=?`, site.ID); err != nil {
		t.Fatal(err)
	}
	tgChoose(t, b, "yes")
	current, _ := b.app.db.GetSite(site.ID)
	if current.Name != "from panel" || !strings.Contains(f.lastText(), "其他操作修改") {
		t.Fatalf("stale edit overwrote panel: %+v %s", current, f.lastText())
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("site:%d:delete", site.ID)))
	nonce := b.sessions[101].Nonce
	b.sessions[101].Expires = time.Now().Add(-time.Second)
	tgHandle(t, b, testTelegramCallback(101, "w:"+nonce+":yes"))
	if _, err = b.app.db.GetSite(site.ID); err != nil {
		t.Fatal("expired confirmation deleted site")
	}
	tgText(t, b, "/add")
	before := len(f.calls)
	if _, err = b.app.db.db.Exec(`UPDATE telegram_bot_settings SET admin_ids='[202]',revision=revision+1`); err != nil {
		t.Fatal(err)
	}
	tgText(t, b, "unauthorized edit")
	tgHandle(t, b, testTelegramCallback(101, "report"))
	if len(f.calls) != before {
		t.Fatal("revoked user received replies")
	}
}
func TestTelegramBotSiteAddressUsesActualListenerAndPublicPort(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	for _, tc := range []struct {
		site         Site
		origin, want string
	}{
		{Site{IngressMode: ingressModePath, PathPrefix: "/one"}, "https://panel.example.test:8443", "https://panel.example.test:8443/one/"},
		{Site{IngressMode: ingressModeHost, PublicHost: "movies.example.test"}, "https://panel.example.test:8443", "https://movies.example.test:8443"},
		{Site{IngressMode: ingressModePort, ListenPort: 8096}, "https://panel.example.test:8443", "http://panel.example.test:8096"},
		{Site{IngressMode: ingressModePort, ListenPort: 8096}, "http://[::1]:9090", "http://[::1]:8096"},
	} {
		got, err := b.app.telegramSiteAddress(tc.site, tc.origin)
		if err != nil || got != tc.want {
			t.Fatalf("address=%q, %v want %q", got, err, tc.want)
		}
	}
	b.app.pm.SetSiteTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	got, _ := b.app.telegramSiteAddress(Site{IngressMode: ingressModePort, ListenPort: 9443}, b.settings.PublicURL)
	if got != "https://panel.example.test:9443" {
		t.Fatal(got)
	}
}
func TestTelegramAPIErrorRedactsCredentialsAndDisallowsRedirects(t *testing.T) {
	token := "123456:private-token"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection failed at %s", r.URL)
	})}
	err := telegramAPICall(context.Background(), client, token, "sendMessage", map[string]any{}, nil)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("unsafe error: %v", err)
	}
	if !errors.Is(telegramBotHTTPClient.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("bot follows redirects")
	}
	client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"ok":false,"description":"` + token + `"}`))}, nil
	})
	err = telegramAPICall(context.Background(), client, token, "sendMessage", nil, nil)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("unsafe response error: %v", err)
	}
}
func TestTelegramBotSettingsAtomicValidationAndReportingIndependence(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	for _, body := range []string{
		`{"bot":{"enabled":true,"admin_ids":[-100],"public_url":"https://panel.test"}}`,
		`{"bot":{"enabled":true,"admin_ids":[101],"public_url":"https://panel.test:888888"}}`,
		`{"bot":{"enabled":true,"admin_ids":[],"public_url":"https://panel.test"}}`,
		`{"bot":{"enabled":true,"admin_ids":[101],"public_url":"https://user:secret@panel.test"}}`,
		`{"bot":{"enabled":true,"admin_ids":[101],"public_url":"https://panel.test/api"}}`,
	} {
		rr := httptest.NewRecorder()
		b.app.handleTelegramReport(rr, httptest.NewRequest(http.MethodPost, "/api/telegram-report", strings.NewReader(body)))
		if rr.Code != 400 {
			t.Fatalf("invalid config status %d: %s", rr.Code, rr.Body.String())
		}
	}
	saved, _ := b.app.db.telegramBotSettings()
	if saved.Revision != b.settings.Revision || len(saved.AdminIDs) != 2 {
		t.Fatal("invalid save changed bot")
	}
	stored, _ := b.app.db.telegramReportSettings()
	if stored.Enabled || !stored.Bot.Enabled {
		t.Fatal("report and bot enablement coupled")
	}
	rr := httptest.NewRecorder()
	b.app.handleTelegramReport(rr, httptest.NewRequest(http.MethodGet, "/api/telegram-report", nil))
	if strings.Contains(rr.Body.String(), "bot-secret") || strings.Contains(rr.Body.String(), "ciphertext") || strings.Contains(rr.Body.String(), "update_offset") {
		t.Fatal("public settings leaked credentials/runtime state")
	}
	// Invalid bot configuration must roll back even if the report update itself is valid.
	stored.ScheduleTime = "01:30"
	stored.Bot.AdminIDs = []int64{-1}
	if err := b.app.db.saveTelegramReportSettings(stored.TelegramReportSettings, "", false); err == nil {
		t.Fatal("invalid DB settings accepted")
	}
	fresh, _ := b.app.db.telegramReportSettings()
	if fresh.ScheduleTime == "01:30" {
		t.Fatal("report partially saved on invalid bot settings")
	}
}
func TestTelegramBotPollingSkipsBacklogAndPersistsClaims(t *testing.T) {
	b, f := newTestTelegramBot(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	call := 0
	f.updates = func(body map[string]any) []telegramUpdate {
		call++
		switch call {
		case 1:
			if body["offset"] != float64(-1) {
				t.Errorf("initial offset %v", body["offset"])
			}
			old := testTelegramMessage(101, "/add")
			old.ID = 40
			return []telegramUpdate{old}
		case 2:
			if body["offset"] != float64(41) {
				t.Errorf("next offset %v", body["offset"])
			}
			next := testTelegramMessage(101, "/start")
			next.ID = 41
			return []telegramUpdate{next, next}
		default:
			cancel()
			return nil
		}
	}
	b.run(ctx)
	if len(f.messages) != 1 || b.sessions[101] != nil {
		t.Fatalf("backlog or duplicate executed: messages %d", len(f.messages))
	}
	saved, _ := b.app.db.telegramBotSettings()
	if saved.Offset != 42 {
		t.Fatalf("offset=%d", saved.Offset)
	}
}

func TestTelegramBotSettingsPreserveOffsetUnlessAuthorizationChanges(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	if !b.storeOffset(987) {
		t.Fatal("store offset")
	}
	original, _ := b.app.db.telegramReportSettings()
	original.ScheduleTime = "21:00"
	if err := b.app.db.saveTelegramReportSettings(original.TelegramReportSettings, "", false); err != nil {
		t.Fatal(err)
	}
	same, _ := b.app.db.telegramBotSettings()
	if same.Offset != 987 || same.Revision != b.settings.Revision {
		t.Fatalf("report-only change reset bot: %+v", same)
	}
	rr := httptest.NewRecorder()
	b.app.handleTelegramReport(rr, httptest.NewRequest(http.MethodPost, "/api/telegram-report", strings.NewReader(`{"chat_id":"","bot":{"enabled":false,"admin_ids":[101],"public_url":"https://panel.example.test:8443"}}`)))
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	disabled, _ := b.app.db.telegramBotSettings()
	if disabled.Enabled || disabled.Offset != -1 || disabled.Revision <= same.Revision {
		t.Fatalf("authorization change retained old offset: %+v", disabled)
	}
	// Clearing a destination is distinct from omitting it in a partial update.
	if _, err := b.app.db.db.Exec(`UPDATE telegram_report_settings SET chat_id='-100123' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	b.app.handleTelegramReport(rr, httptest.NewRequest(http.MethodPost, "/api/telegram-report", strings.NewReader(`{"chat_id":""}`)))
	saved, _ := b.app.db.telegramReportSettings()
	if rr.Code != 200 || saved.ChatID != "" {
		t.Fatalf("clear chat ID failed: %s", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	b.app.handleTelegramReport(rr, httptest.NewRequest(http.MethodPost, "/api/telegram-report", strings.NewReader(`{"clear_bot_token":true}`)))
	saved, _ = b.app.db.telegramReportSettings()
	if rr.Code != 200 || saved.BotTokenCiphertext != "" || saved.Enabled || saved.Bot.Enabled {
		t.Fatal("removing token did not disable bot/report")
	}
}

func TestTelegramBotEditsPreserveSecretsAndValidateConflicts(t *testing.T) {
	b, f := newTestTelegramBot(t)
	stored, err := mergeUpstreamHeaders("[]", []UpstreamHeaderInput{{Name: "X-Origin-Secret", Value: stringPointer("keep-this-secret")}}, b.app.pm.upstreamHeaderKey, "http://127.0.0.1:8096")
	if err != nil {
		t.Fatal(err)
	}
	site, err := b.app.db.CreateSiteRecord(Site{Name: "private origin", IngressMode: ingressModePath, PathPrefix: "/secret-origin", ListenPort: 12345, TargetURL: "http://127.0.0.1:8096", PlaybackMode: "direct", UAMode: "passthrough", StoredUpstreamHeaders: stored})
	if err != nil {
		t.Fatal(err)
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("edit:%d:name", site.ID)))
	tgText(t, b, "renamed")
	tgChoose(t, b, "yes")
	edited, _ := b.app.db.GetSite(site.ID)
	if edited.Name != "renamed" || edited.StoredUpstreamHeaders != stored {
		t.Fatal("unrelated edit dropped encrypted headers")
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("edit:%d:target", site.ID)))
	tgChoose(t, b, "http")
	tgText(t, b, "127.0.0.1")
	tgChoose(t, b, "8097")
	if !strings.Contains(f.lastText(), "固定请求头会被清除") {
		t.Fatal("authority-change confirmation omitted credential consequence")
	}
	tgChoose(t, b, "yes")
	edited, _ = b.app.db.GetSite(site.ID)
	if edited.StoredUpstreamHeaders != "[]" {
		t.Fatal("secret followed new authority")
	}
	for _, m := range f.messages {
		if strings.Contains(fmt.Sprint(m), "keep-this-secret") {
			t.Fatal("bot echoed upstream secret")
		}
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("edit:%d:failovers", site.ID)))
	tgText(t, b, strings.Repeat("http://127.0.0.1:8098\n", 8))
	if b.sessions[101].Step != "failovers" {
		t.Fatal("oversized failover list advanced")
	}
	tgText(t, b, "http://127.0.0.1:8098\nhttp://127.0.0.1:8099")
	tgChoose(t, b, "yes")
	edited, _ = b.app.db.GetSite(site.ID)
	if len(edited.FailoverLines) != 2 {
		t.Fatalf("failovers not saved: %s", f.lastText())
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("edit:%d:cache", site.ID)))
	tgChoose(t, b, "on")
	tgChoose(t, b, "yes")
	edited, _ = b.app.db.GetSite(site.ID)
	if !edited.AssetCacheEnabled || len(edited.FailoverLines) != 2 {
		t.Fatal("cache edit did not preserve failovers")
	}
}

func TestTelegramBotUpgradeKeepsReportsAndStartsManagementDisabled(t *testing.T) {
	path := t.TempDir() + "/upgrade.db"
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	token, err := encryptTelegramBotToken("123456:legacy-report")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.saveTelegramReportSettings(TelegramReportSettings{Enabled: true, ChatID: "-100123", ScheduleTime: "21:10", Frequency: "weekly", Weekday: 5}, token, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`DROP TABLE telegram_bot_settings`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s, err := reopened.telegramReportSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.Enabled || s.ChatID != "-100123" || s.ScheduleTime != "21:10" || s.Frequency != "weekly" || s.Weekday != 5 || s.BotTokenCiphertext != token || s.Bot == nil || s.Bot.Enabled {
		t.Fatalf("upgrade altered report config: enabled=%v chat=%q bot=%+v", s.Enabled, s.ChatID, s.Bot)
	}
}

func TestTelegramBotSupervisorStopsPollingWhenDisabled(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	oldClient := telegramBotHTTPClient
	started := make(chan struct{})
	stopped := make(chan struct{})
	var startOnce, stopOnce sync.Once
	telegramBotHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":true}`))}, nil
		}
		var input struct {
			Offset int64 `json:"offset"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Offset < 0 {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":[]}`))}, nil
		}
		startOnce.Do(func() { close(started) })
		<-r.Context().Done()
		stopOnce.Do(func() { close(stopped) })
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); b.app.runTelegramBot(ctx) }()
	defer func() { cancel(); <-done; telegramBotHTTPClient = oldClient }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not start")
	}
	rr := httptest.NewRecorder()
	b.app.handleTelegramReport(rr, httptest.NewRequest(http.MethodPost, "/api/telegram-report", strings.NewReader(`{"bot":{"enabled":false,"admin_ids":[101],"public_url":"https://panel.example.test:8443"}}`)))
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("disabled bot kept polling")
	}
}
