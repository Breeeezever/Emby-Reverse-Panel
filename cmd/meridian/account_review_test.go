package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSiteAndAccountCreationRollsBackTogether(t *testing.T) {
	app := newTestApp(t)
	// Fail on the second row after a valid first account was already inserted.
	_, err := app.db.db.Exec(`CREATE TRIGGER reject_fixture_account BEFORE INSERT ON site_accounts WHEN NEW.username='fail' BEGIN SELECT RAISE(ABORT, 'fixture write failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"name": "atomic", "target_url": "https://origin.example", "ingress_mode": "path", "path_prefix": "/atomic", "accounts": []SiteAccountInput{{Username: "first", Password: "one"}, {Username: "fail", Password: "two"}}}
	create := func() *httptest.ResponseRecorder {
		data, _ := json.Marshal(payload)
		rr := httptest.NewRecorder()
		app.handleSites(rr, httptest.NewRequest(http.MethodPost, "/api/sites", bytes.NewReader(data)))
		return rr
	}
	rr := create()
	if rr.Code < 400 {
		t.Fatalf("failed insert reported success: %s", rr.Body.String())
	}
	sites, _ := app.db.ListSites()
	var accounts int
	_ = app.db.db.QueryRow("SELECT COUNT(*) FROM site_accounts").Scan(&accounts)
	if len(sites) != 0 || accounts != 0 {
		t.Fatal("failed create left partial site/account data")
	}
	if _, exists := app.pm.PathPrefixSiteID("/atomic"); exists {
		t.Fatal("failed create exposed route")
	}
	_, _ = app.db.db.Exec("DROP TRIGGER reject_fixture_account")
	rr = create()
	if rr.Code != 201 {
		t.Fatalf("retry failed: %s", rr.Body.String())
	}
	sites, _ = app.db.ListSites()
	saved, _ := app.db.listSiteAccounts(sites[0].ID)
	if len(sites) != 1 || len(saved) != 2 {
		t.Fatal("retry did not create exactly one complete site")
	}
}

func TestSiteCreateValidatesDraftsBeforeWriting(t *testing.T) {
	for _, inputs := range [][]SiteAccountInput{
		{{Username: "demo", ExpiresAt: "invalid-date"}},
		{{Username: "demo"}, {Username: " demo "}},
	} {
		app := newTestApp(t)
		data, _ := json.Marshal(map[string]any{"name": "invalid", "target_url": "https://origin.example", "ingress_mode": "path", "path_prefix": "/invalid", "accounts": inputs})
		rr := httptest.NewRecorder()
		app.handleSites(rr, httptest.NewRequest("POST", "/api/sites", bytes.NewReader(data)))
		sites, _ := app.db.ListSites()
		if rr.Code != 400 || len(sites) != 0 {
			t.Fatalf("invalid draft persisted: %d %s", rr.Code, rr.Body.String())
		}
	}
}

func TestAccountPartialUpdatesPreserveOtherWriters(t *testing.T) {
	app := newTestApp(t)
	site := accountWorkflowSite(t, app, "concurrent")
	item, err := app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		ready := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, input := range []SiteAccountInput{{Password: fmt.Sprintf("pass-%d", i)}, {Note: fmt.Sprintf("note-%d", i)}} {
			wg.Go(func() { <-ready; _, err := app.db.saveSiteAccount(site.ID, item.ID, input); errs <- err })
		}
		close(ready)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		stored, _ := app.db.getSiteAccount(site.ID, item.ID)
		plain, _ := accountPlaintext(*stored)
		if plain.Password != fmt.Sprintf("pass-%d", i) || plain.Note != fmt.Sprintf("note-%d", i) {
			t.Fatal("parallel partial update overwrote another field")
		}
	}
}

func TestAccountSecretsHaveNoReservedValues(t *testing.T) {
	app := newTestApp(t)
	site := accountWorkflowSite(t, app, "opaque")
	item, err := app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "demo", Password: "original"})
	if err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{" ", " skip ", "\x00__KEEP__\x00"} {
		stored, err := app.db.saveSiteAccount(site.ID, item.ID, SiteAccountInput{Password: password})
		if err != nil {
			t.Fatal(err)
		}
		plain, err := accountPlaintext(*stored)
		if err != nil || plain.Password != password {
			t.Fatal("password bytes were changed")
		}
	}
	stored, err := app.db.saveSiteAccount(site.ID, item.ID, SiteAccountInput{ClearPassword: true})
	if err != nil || stored.PasswordSet {
		t.Fatal("explicit clear did not remove password")
	}
}

func TestTelegramManualAccountPreservesLiteralControls(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	site := accountWorkflowSite(t, b.app, "literal")
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("account:%d:new", site.ID)))
	tgText(t, b, "name")
	tgText(t, b, "same")
	tgText(t, b, "skip")
	tgText(t, b, " /literal-password ")
	tgChoose(t, b, "skip")
	tgChoose(t, b, "skip")
	tgText(t, b, "skip")
	tgChoose(t, b, "yes")
	accounts, _ := b.app.db.listSiteAccounts(site.ID)
	if len(accounts) != 1 {
		t.Fatal("manual account not saved")
	}
	plain, _ := accountPlaintext(accounts[0])
	if plain.Username != "same" || plain.Password != "skip" || plain.SecurityPassword != " /literal-password " || plain.Note != "skip" {
		t.Fatalf("literal input interpreted as control: %+v", plain)
	}
	for _, value := range []string{"clear", "keep", " leading and trailing "} {
		tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("account:%d:%d:edit", site.ID, accounts[0].ID)))
		tgChoose(t, b, "password")
		tgText(t, b, value)
		tgChoose(t, b, "yes")
		saved, _ := b.app.db.getSiteAccount(site.ID, accounts[0].ID)
		plain, _ := accountPlaintext(*saved)
		if plain.Password != value {
			t.Fatal("edited password was interpreted as a button")
		}
	}
	tgHandle(t, b, testTelegramCallback(101, fmt.Sprintf("account:%d:%d:edit", site.ID, accounts[0].ID)))
	tgChoose(t, b, "password")
	tgChoose(t, b, "clear")
	tgChoose(t, b, "yes")
	saved, _ := b.app.db.getSiteAccount(site.ID, accounts[0].ID)
	if saved.PasswordSet {
		t.Fatal("clear button stopped working")
	}
}

func TestAccountReminderUsesConfiguredTimezone(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	site := accountWorkflowSite(t, b.app, "zone")
	settings := b.app.db.currentSystemSettings()
	settings.ScheduleTimezoneName = "Asia/Shanghai"
	settings.ScheduleTimezone = 480
	if err := b.app.db.saveSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	expiry, err := b.app.db.parseSiteAccountExpiry("2030-11-09")
	expected := time.Date(2030, 11, 8, 16, 0, 0, 0, time.UTC)
	if err != nil || !expiry.Equal(expected) {
		t.Fatalf("expiry=%v err=%v", expiry, err)
	}
	explicit, err := b.app.db.parseSiteAccountExpiry("2030-11-09T00:00:00Z")
	if err != nil || !explicit.Equal(expected.Add(8*time.Hour)) {
		t.Fatal("explicit timezone was overridden")
	}
	days := 1
	_, err = b.app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "demo", ExpiresAt: "2030-11-09", ReminderDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	baseline := len(fake.messages)
	due := expected.Add(-24 * time.Hour)
	for _, now := range []time.Time{due.Add(-time.Second), due, due.Add(time.Minute)} {
		if err := b.deliverAccountReminders(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		want := baseline
		if !now.Before(due) {
			want += len(b.settings.AdminIDs)
		}
		if len(fake.messages) != want {
			t.Fatalf("reminder at wrong time or duplicated at %v", now)
		}
	}
}

func TestStandaloneMultiwordLaunchTitlesDoNotInventAccounts(t *testing.T) {
	for _, title := range []string{"My Server", "My Media Server", "测试 影院"} {
		parsed, err := parseSiteMessageRecord("**" + title + "**\nhttps://origin.example")
		if err != nil || parsed.Name != title || len(parsed.Accounts) != 0 {
			t.Fatalf("title became credentials: %+v %v", parsed, err)
		}
	}
}

func TestAccountExportPreservesNativePanelPort(t *testing.T) {
	app := newTestApp(t)
	app.panelHost = "panel.example"
	app.panelListenPort = 14430
	app.panelTLSEnabled = true
	site := accountWorkflowSite(t, app, "exportport")
	for _, tc := range []struct{ mode, host, want string }{
		{ingressModePath, "", "https://panel.example:14430/exportport/"},
		{ingressModeHost, "media.example", "https://media.example:14430"},
	} {
		site.IngressMode, site.PublicHost = tc.mode, tc.host
		lines := app.siteProxyLines(*site)
		if len(lines) != 1 || lines[0] != tc.want {
			t.Fatalf("wrong export address: %v", lines)
		}
	}
	app.panelHost = "2001:db8::1"
	site.IngressMode = ingressModePath
	site.PublicHost = ""
	if !strings.Contains(strings.Join(app.siteProxyLines(*site), ""), "[2001:db8::1]:14430") {
		t.Fatal("invalid IPv6 address")
	}
}

func TestImportNewSiteRollsBackAccountsAndSupportsLargeMemo(t *testing.T) {
	app := newTestApp(t)
	_, err := app.db.db.Exec(`CREATE TRIGGER reject_import_account BEFORE INSERT ON site_accounts WHEN NEW.username='fail' BEGIN SELECT RAISE(ABORT, 'fixture'); END`)
	if err != nil {
		t.Fatal(err)
	}
	req := siteImportRequest{Format: "json", Data: `{"version":1,"sites":[{"name":"imported","target_url":"https://origin.example","accounts":[{"username":"ok"},{"username":"fail"}]}]}`}
	preview, err := app.previewSiteImport(context.Background(), req)
	if err != nil || !preview.Valid {
		t.Fatalf("bad preview: %v", err)
	}
	result := app.applySiteImport(context.Background(), preview)
	sites, _ := app.db.ListSites()
	if result.Failed != 1 || len(sites) != 0 {
		t.Fatal("import left a partial site")
	}
	_, _ = app.db.db.Exec("DROP TRIGGER reject_import_account")
	// Under the documented 2 MB file limit but over the ordinary API's 512 KB cap.
	accounts := make([]SiteAccountInput, 300)
	for i := range accounts {
		accounts[i] = SiteAccountInput{Username: fmt.Sprintf("account-%d", i), Note: strings.Repeat("n", 1900)}
	}
	data, _ := json.Marshal(siteDataEnvelope{Version: 1, Sites: []siteImportRecord{{Name: "large", TargetURL: "https://origin.example", Accounts: accounts}}})
	req.Data = string(data)
	preview, err = app.previewSiteImport(context.Background(), req)
	if err != nil || !preview.Valid {
		t.Fatalf("large preview: %v %+v", err, preview.Items)
	}
	result = app.applySiteImport(context.Background(), preview)
	if result.Created != 1 || result.Failed != 0 {
		t.Fatalf("large import failed: %+v", result)
	}
	sites, _ = app.db.ListSites()
	saved, _ := app.db.listSiteAccounts(sites[0].ID)
	if len(saved) != 300 || sites[0].Enabled {
		t.Fatal("large import incomplete or enabled")
	}
}
