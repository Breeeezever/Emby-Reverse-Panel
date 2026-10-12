package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func accountWorkflowSite(t *testing.T, app *App, name string) *Site {
	t.Helper()
	site, err := app.db.CreateSiteRecord(Site{Name: name, TargetURL: "https://" + name + ".example/emby", ListenPort: freePort(t), IngressMode: ingressModePath, PathPrefix: "/" + name, PlaybackMode: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.db.SetSiteEnabled(site.ID, false); err != nil {
		t.Fatal(err)
	}
	site.Enabled = false
	return site
}

func TestAccountReminderUpgradePreservesExistingMemo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	site, err := db.CreateSiteRecord(Site{Name: "legacy", TargetURL: "http://127.0.0.1:8096", ListenPort: 18080, IngressMode: ingressModePath, PathPrefix: "/legacy", PlaybackMode: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "legacy-user", Password: "legacy-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("DROP TABLE account_reminder_deliveries"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("ALTER TABLE site_accounts DROP COLUMN reminder_days"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for i := 0; i < 2; i++ {
		db, err = openDB(path)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := db.getSiteAccount(site.ID, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := accountPlaintext(*stored)
		if err != nil || plain.Password != "legacy-password" || plain.ReminderDays != nil {
			t.Fatalf("upgrade changed existing account: %+v %v", plain, err)
		}
		db.Close()
	}
}

func TestSiteDataJSONExportImportsIntoFreshDatabase(t *testing.T) {
	app := newTestApp(t)
	site := accountWorkflowSite(t, app, "roundtrip")
	days := 2
	if _, err := app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "demo", Password: "roundtrip-password", ReminderDays: &days}); err != nil {
		t.Fatal(err)
	}
	data, _, _, err := app.exportSiteData("json", "", 0, siteExportOptions{true, true})
	if err != nil {
		t.Fatal(err)
	}
	target := newTestApp(t)
	req := siteImportRequest{Format: "json", Data: string(data)}
	preview, err := target.previewSiteImport(context.Background(), req)
	if err != nil || !preview.Valid {
		t.Fatalf("JSON roundtrip preview=%+v %v", preview, err)
	}
	result := target.applySiteImport(context.Background(), preview)
	if result.Created != 1 || result.Failed != 0 {
		t.Fatalf("JSON roundtrip import=%+v", result)
	}
	sites, _ := target.db.ListSites()
	accounts, _ := target.db.listSiteAccounts(sites[0].ID)
	if len(accounts) != 1 {
		t.Fatal("account missing")
	}
	plain, _ := accountPlaintext(accounts[0])
	if plain.Password != "roundtrip-password" || plain.ReminderDays == nil || *plain.ReminderDays != 2 {
		t.Fatalf("JSON roundtrip memo=%+v", plain)
	}
}

func importWorkflowRequest(t *testing.T, app *App, req siteImportRequest) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.handleSiteDataImport(w, httptest.NewRequest(http.MethodPost, "/api/site-data/import", bytes.NewReader(data)))
	return w
}

func TestAccountMemoExposesPasswordsOnlyInAuthenticatedWorkspace(t *testing.T) {
	app := newTestApp(t)
	site := accountWorkflowSite(t, app, "memo")
	days := 7
	account, err := app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "user", Password: " visible password ", SecurityPassword: "security", ExpiresAt: "2030-11-09", ReminderDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(account.PasswordCiphertext, "visible password") {
		t.Fatal("password stored without encryption")
	}
	w := httptest.NewRecorder()
	app.handleSiteAccounts(w, httptest.NewRequest(http.MethodGet, "/api/sites/1/accounts", nil), site.ID, "")
	var memos []siteAccountMemo
	if err := json.Unmarshal(w.Body.Bytes(), &memos); err != nil {
		t.Fatal(err)
	}
	if len(memos) != 1 || memos[0].Password != " visible password " || memos[0].SecurityPassword != "security" || memos[0].ReminderDays == nil || *memos[0].ReminderDays != 7 {
		t.Fatalf("memo=%+v", memos)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("memo response is cacheable")
	}
	reminders := httptest.NewRecorder()
	app.handleAccountReminders(reminders, httptest.NewRequest(http.MethodGet, "/api/account-reminders", nil))
	if strings.Contains(reminders.Body.String(), "visible password") || strings.Contains(reminders.Body.String(), `"security_password":`) {
		t.Fatal("reminder metadata contains credentials")
	}
	updated, err := app.db.saveSiteAccount(site.ID, account.ID, SiteAccountInput{Note: "changed"})
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := accountPlaintext(*updated)
	if plain.Password != " visible password " || plain.ReminderDays == nil {
		t.Fatal("partial update discarded existing memo")
	}
	updated, err = app.db.saveSiteAccount(site.ID, account.ID, SiteAccountInput{ClearPassword: true, ClearSecurity: true, ClearReminder: true, ClearExpiry: true, ClearNote: true})
	if err != nil {
		t.Fatal(err)
	}
	plain, _ = accountPlaintext(*updated)
	if plain.Password != "" || plain.SecurityPassword != "" || plain.ReminderDays != nil || plain.ExpiresAt != "" || plain.Note != "" {
		t.Fatalf("memo clear failed: %+v", plain)
	}
}

func TestSiteExportAllFormatsKeepSitesWithoutAccounts(t *testing.T) {
	b, _ := newTestTelegramBot(t)
	app := b.app
	with := accountWorkflowSite(t, app, "with-account")
	without := accountWorkflowSite(t, app, "without-account")
	with.FailoverTargets = `["https://backup.example/emby"]`
	if err := app.db.UpdateSiteRecord(*with); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.saveSiteAccount(with.ID, 0, SiteAccountInput{Username: "memo-user", Password: "memo-secret", SecurityPassword: "memo-security"}); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "csv", "client"} {
		for _, opts := range []siteExportOptions{{}, {IncludeAccounts: true}, {true, true}} {
			t.Run(fmt.Sprintf("%s/%v", format, opts), func(t *testing.T) {
				data, _, _, err := app.exportSiteData(format, "multi", 0, opts)
				if err != nil {
					t.Fatal(err)
				}
				for _, required := range []string{with.Name, without.Name, "backup.example"} {
					if !strings.Contains(string(data), required) {
						t.Fatalf("missing %s in %s", required, data)
					}
				}
				if strings.Contains(string(data), "memo-user") != opts.IncludeAccounts {
					t.Fatal("include_accounts ignored")
				}
				if strings.Contains(string(data), "memo-secret") != (opts.IncludeAccounts && opts.IncludePasswords) {
					t.Fatal("include_passwords ignored")
				}
				if format == "client" && (!strings.Contains(string(data), "https://panel.example.test:8443/without-account/") || strings.Contains(string(data), "http://panel.example.test")) {
					t.Fatalf("incorrect external proxy URL: %s", data)
				}
				if format == "csv" {
					rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff"))).ReadAll()
					if err != nil || len(rows) != 3 {
						t.Fatalf("invalid CSV rows=%d error=%v", len(rows), err)
					}
					records, err := parseSiteCSV(string(data))
					if err != nil || len(records) != 2 || len(records[1].Accounts) != 0 {
						t.Fatalf("CSV round trip failed: %+v %v", records, err)
					}
				}
			})
		}
	}
}

func TestSiteImportPreviewChoicesAndPasswordPreservation(t *testing.T) {
	app := newTestApp(t)
	existing := accountWorkflowSite(t, app, "existing")
	account, err := app.db.saveSiteAccount(existing.ID, 0, SiteAccountInput{Username: "memo-user", Password: "keep-password", SecurityPassword: "keep-security"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.db.Exec("UPDATE sites SET traffic_used=12345 WHERE id=?", existing.ID); err != nil {
		t.Fatal(err)
	}
	req := siteImportRequest{Format: "json", Data: `{"version":1,"sites":[{"name":"existing","target_url":"https://existing.example/emby","accounts":[{"username":"memo-user","note":"updated note"}]},{"name":"new-site","target_url":"https://new.example/emby"}]}`, Preview: true}
	response := importWorkflowRequest(t, app, req)
	var preview siteImportPreview
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &preview) != nil || !preview.Valid {
		t.Fatalf("preview=%s", response.Body.String())
	}
	if len(preview.Items) != 2 || preview.Items[0].Action != "skip" || preview.Items[1].Action != "new" {
		t.Fatalf("preview items=%+v", preview.Items)
	}
	sites, _ := app.db.ListSites()
	if len(sites) != 1 {
		t.Fatal("preview wrote a site")
	}
	req.DuplicateAction = "update"
	response = importWorkflowRequest(t, app, req)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &preview) != nil || !preview.Valid {
		t.Fatalf("update preview=%s", response.Body.String())
	}
	req.Preview = false
	req.PreviewToken = preview.Token
	response = importWorkflowRequest(t, app, req)
	var result siteImportResult
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Created != 1 || result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("import=%s", response.Body.String())
	}
	stored, _ := app.db.getSiteAccount(existing.ID, account.ID)
	plain, _ := accountPlaintext(*stored)
	if plain.Password != "keep-password" || plain.SecurityPassword != "keep-security" || plain.Note != "updated note" {
		t.Fatalf("password omission damaged memo: %+v", plain)
	}
	site, _ := app.db.GetSite(existing.ID)
	if site.TrafficUsed != 12345 || site.Enabled {
		t.Fatal("import changed counters or enabled state")
	}
	response = importWorkflowRequest(t, app, req)
	if response.Code != 409 {
		t.Fatalf("stale confirmation accepted: %s", response.Body.String())
	}
	req.Preview = true
	req.Actions = map[int]string{0: "new", 1: "skip"}
	response = importWorkflowRequest(t, app, req)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &preview) != nil || !preview.Valid {
		t.Fatalf("new duplicate preview=%s", response.Body.String())
	}
	if preview.Items[0].Name == existing.Name || preview.Items[0].PathPrefix == existing.PathPrefix {
		t.Fatalf("duplicate did not receive distinct name/path: %+v", preview.Items[0])
	}
	req.Preview = false
	req.PreviewToken = preview.Token
	response = importWorkflowRequest(t, app, req)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Created != 1 || result.Skipped != 1 {
		t.Fatalf("new duplicate=%s", response.Body.String())
	}
}

func TestSiteImportPreviewRejectsInvalidRowsAndCanSkipThem(t *testing.T) {
	app := newTestApp(t)
	req := siteImportRequest{Format: "json", Data: `{"version":1,"sites":[{"name":"bad","target_url":"file:///etc/passwd"},{"name":"good","target_url":"https://good.example"}]}`, Preview: true}
	response := importWorkflowRequest(t, app, req)
	var preview siteImportPreview
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &preview) != nil || preview.Valid || preview.Items[0].Error == "" {
		t.Fatalf("invalid row preview=%s", response.Body.String())
	}
	sites, _ := app.db.ListSites()
	if len(sites) != 0 {
		t.Fatal("invalid preview wrote data")
	}
	req.Actions = map[int]string{0: "skip"}
	response = importWorkflowRequest(t, app, req)
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &preview) != nil || !preview.Valid {
		t.Fatalf("skip preview=%s", response.Body.String())
	}
	req.Preview, req.PreviewToken = false, preview.Token
	response = importWorkflowRequest(t, app, req)
	var result siteImportResult
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Created != 1 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("skip import=%s", response.Body.String())
	}
}

func TestAccountReminderLeadTimeAndPersistentDeduplication(t *testing.T) {
	b, fake := newTestTelegramBot(t)
	site := accountWorkflowSite(t, b.app, "remind")
	now := time.Now().Truncate(time.Second)
	expiry := now.AddDate(0, 0, 10)
	days := 3
	account, err := b.app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{Username: "renew-me", Password: "never-send-password", ExpiresAt: expiry.Format(time.RFC3339), ReminderDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.deliverAccountReminders(context.Background(), expiry.AddDate(0, 0, -4)); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 0 {
		t.Fatal("reminded too early")
	}
	if err := b.deliverAccountReminders(context.Background(), expiry.AddDate(0, 0, -3)); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 2 {
		t.Fatalf("got %d reminders, want both administrators", len(fake.messages))
	}
	if strings.Contains(fake.lastText(), "never-send-password") {
		t.Fatal("reminder contains password")
	}
	// A fresh worker has no in-memory delivery state; persisted receipts still apply.
	restarted := &telegramBot{app: b.app, settings: b.settings, token: b.token, client: fake.client()}
	if err := restarted.deliverAccountReminders(context.Background(), expiry); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 2 {
		t.Fatal("restart repeated expiry reminder")
	}
	next := expiry.AddDate(0, 1, 0)
	if _, err := b.app.db.saveSiteAccount(site.ID, account.ID, SiteAccountInput{ExpiresAt: next.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	if err := b.deliverAccountReminders(context.Background(), next.AddDate(0, 0, -3)); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 4 {
		t.Fatal("renewed account did not receive next reminder")
	}
	if _, err := b.app.db.saveSiteAccount(site.ID, account.ID, SiteAccountInput{ExpiresAt: next.AddDate(0, 1, 0).Format(time.RFC3339), ClearReminder: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.deliverAccountReminders(context.Background(), next.AddDate(0, 1, 0)); err != nil {
		t.Fatal(err)
	}
	if len(fake.messages) != 4 {
		t.Fatal("disabled reminder was sent")
	}
}
