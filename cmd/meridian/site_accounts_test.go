package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeSiteAccountInputMirrorsNames(t *testing.T) {
	got, err := normalizeSiteAccountInput(SiteAccountInput{AccountName: "  demo  "}, nil)
	if err != nil {
		t.Fatalf("normalize account: %v", err)
	}
	if got.AccountName != "demo" || got.Username != "demo" {
		t.Fatalf("mirrored account=%+v", got)
	}
	got, err = normalizeSiteAccountInput(SiteAccountInput{Username: "  user  "}, nil)
	if err != nil {
		t.Fatalf("normalize username: %v", err)
	}
	if got.AccountName != "user" || got.Username != "user" {
		t.Fatalf("mirrored username=%+v", got)
	}
}

func TestSiteAccountSecretRoundTrip(t *testing.T) {
	ciphertext, err := encryptSiteAccountSecret("p@ssword")
	if err != nil {
		t.Fatalf("encrypt account secret: %v", err)
	}
	if ciphertext == "" || strings.Contains(ciphertext, "p@ssword") {
		t.Fatalf("ciphertext exposes plaintext: %q", ciphertext)
	}
	plaintext, err := decryptSiteAccountSecret(ciphertext)
	if err != nil || plaintext != "p@ssword" {
		t.Fatalf("decrypt account secret=%q err=%v", plaintext, err)
	}
}

func TestBackupPasswordMinimumIsSixBytes(t *testing.T) {
	if err := validateBackupPassword("123456"); err != nil {
		t.Fatalf("six-byte backup password rejected: %v", err)
	}
	if err := validateBackupPassword("12345"); err == nil {
		t.Fatal("five-byte backup password accepted")
	}
}

func TestReencryptRestoredSiteAccountSecrets(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "restore.db")
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	site, err := db.CreateSiteRecord(Site{Name: "restore", ListenPort: freePort(t), IngressMode: ingressModePath, PathPrefix: "/restore", TargetURL: "http://127.0.0.1:8096", StreamHosts: "[]", FailoverTargets: "[]"})
	if err != nil {
		db.Close()
		t.Fatalf("create test site: %v", err)
	}
	oldKey := []byte("old-account-secret-012345678901234")
	newKey := []byte("new-account-secret-012345678901234")
	password, err := encryptSiteAccountSecretWithSecret("old-password", oldKey)
	if err != nil {
		db.Close()
		t.Fatalf("encrypt old password: %v", err)
	}
	security, err := encryptSiteAccountSecretWithSecret("old-security", oldKey)
	if err != nil {
		db.Close()
		t.Fatalf("encrypt old security password: %v", err)
	}
	if _, err := db.db.Exec(`INSERT INTO site_accounts (site_id, account_name, username, password_ciphertext, security_password_ciphertext) VALUES (?, ?, ?, ?, ?)`, site.ID, "demo", "demo", password, security); err != nil {
		db.Close()
		t.Fatalf("insert account: %v", err)
	}
	db.Close()
	key := bytes.Repeat([]byte("k"), 32)
	if err := reencryptRestoredSecrets(dbPath, key, oldKey, key, key, newKey, key); err != nil {
		t.Fatalf("migrate account secrets: %v", err)
	}
	reopened, err := openDB(dbPath)
	if err != nil {
		t.Fatalf("reopen test database: %v", err)
	}
	defer reopened.Close()
	accounts, err := reopened.listSiteAccounts(site.ID)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("migrated accounts=%d err=%v", len(accounts), err)
	}
	plain, err := decryptSiteAccountSecretWithSecrets(accounts[0].PasswordCiphertext, [][]byte{newKey})
	if err != nil || plain != "old-password" {
		t.Fatalf("migrated password=%q err=%v", plain, err)
	}
	plain, err = decryptSiteAccountSecretWithSecrets(accounts[0].SecurityPasswordCiphertext, [][]byte{newKey})
	if err != nil || plain != "old-security" {
		t.Fatalf("migrated security password=%q err=%v", plain, err)
	}
}

func TestSiteDataImportAllocatesPathPortAndDisablesSite(t *testing.T) {
	app := newTestApp(t)
	app.panelListenPort = 9090
	body := `{"format":"json","data":"{\"version\":1,\"sites\":[{\"name\":\"Imported\",\"target_url\":\"http://127.0.0.1:8096\",\"ingress_mode\":\"path\",\"path_prefix\":\"imported\",\"accounts\":[{\"account_name\":\"demo\",\"password\":\"secret\"}]}]}"}`
	rr := httptest.NewRecorder()
	app.handleSiteDataImport(rr, httptest.NewRequest(http.MethodPost, "/api/site-data/import", bytes.NewBufferString(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", rr.Code, rr.Body.String())
	}
	sites, err := app.db.ListSites()
	if err != nil || len(sites) != 1 {
		t.Fatalf("imported sites=%d err=%v", len(sites), err)
	}
	if sites[0].Enabled || sites[0].ListenPort == 0 {
		t.Fatalf("imported site enabled=%v port=%d", sites[0].Enabled, sites[0].ListenPort)
	}
	accounts, err := app.db.listSiteAccounts(sites[0].ID)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("imported accounts=%d err=%v", len(accounts), err)
	}
	plain, err := accountPlaintext(accounts[0])
	if err != nil || plain.Username != "demo" || plain.Password != "secret" {
		t.Fatalf("imported account=%+v err=%v", plain, err)
	}
}

func TestSiteMessageParse(t *testing.T) {
	app := newTestApp(t)
	message := "**我的站点**\n· 用户名称 | `demo`\n· 用户密码 | `secret`\n· 安全密码 | `second`\n· 当前线路：\nhttps://origin.example/emby"
	rr := httptest.NewRecorder()
	app.handleSiteMessageParse(rr, httptest.NewRequest(http.MethodPost, "/api/site-data/parse-message", strings.NewReader(`{"text":`+mustJSON(t, message)+`}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("parse status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got siteImportRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if got.Name != "我的站点" || got.TargetURL != "https://origin.example/emby" || len(got.Accounts) != 1 || got.Accounts[0].Password != "secret" {
		t.Fatalf("parsed record=%+v", got)
	}
}

func TestSiteAccountClientModesAndExport(t *testing.T) {
	app := newTestApp(t)
	site, err := app.db.CreateSiteRecord(Site{Name: "Client", TargetURL: "https://origin.example/emby", ListenPort: freePort(t), IngressMode: ingressModePath, PathPrefix: "/client", StreamHosts: "[]", FailoverTargets: `["https://backup.example/emby"]`})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	item, err := app.db.saveSiteAccount(site.ID, 0, SiteAccountInput{AccountName: "label", Username: "demo", Password: "secret"})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	for _, mode := range []string{"original", "proxy", "multi"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/sites/"+strconv.FormatInt(site.ID, 10)+"/accounts/"+strconv.FormatInt(item.ID, 10)+"?line_mode="+mode, nil)
		app.handleSiteAccounts(rr, req, site.ID, strconv.FormatInt(item.ID, 10))
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "demo") {
			t.Fatalf("client mode %s status=%d body=%s", mode, rr.Code, rr.Body.String())
		}
	}
	rr := httptest.NewRecorder()
	app.handleSiteAccounts(rr, httptest.NewRequest(http.MethodGet, "/api/sites/1/accounts/1?line_mode=bad", nil), site.ID, strconv.FormatInt(item.ID, 10))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid client mode status=%d body=%s", rr.Code, rr.Body.String())
	}
	export := httptest.NewRecorder()
	app.handleSiteDataExport(export, httptest.NewRequest(http.MethodGet, "/api/site-data/export?format=client&line_mode=original", nil))
	if export.Code != http.StatusOK || !strings.Contains(export.Body.String(), "https://origin.example/emby") || strings.Contains(export.Body.String(), "secret") == false {
		t.Fatalf("client export status=%d body=%s", export.Code, export.Body.String())
	}
}

func TestSiteMessageParseShorthandAndTimestamp(t *testing.T) {
	app := newTestApp(t)
	message := "**OK Oxxo xVCo2PF8&#x20;**[https://link00.okemby.org:8443（主线路）](https://link00.okemby.org:8443（主线路）)\n· 到期时间 | `2026-11-09 22:08:59.342128`"
	rr := httptest.NewRecorder()
	app.handleSiteMessageParse(rr, httptest.NewRequest(http.MethodPost, "/api/site-data/parse-message", strings.NewReader(`{"text":`+mustJSON(t, message)+`}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("parse status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got siteImportRecord
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if got.Name != "OK" || got.TargetURL != "https://link00.okemby.org:8443" || len(got.Accounts) != 1 {
		t.Fatalf("parsed shorthand record=%+v", got)
	}
	if got.Accounts[0].Username != "Oxxo" || got.Accounts[0].Password != "xVCo2PF8" || got.Accounts[0].ExpiresAt != "2026-11-09 22:08:59.342128" {
		t.Fatalf("parsed shorthand account=%+v", got.Accounts[0])
	}
	if _, err := parseSiteAccountExpiry(got.Accounts[0].ExpiresAt); err != nil {
		t.Fatalf("timestamp expiry rejected: %v", err)
	}
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
