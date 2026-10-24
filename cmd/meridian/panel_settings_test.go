package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeManagedPanelSettings(t *testing.T) {
	settings, err := normalizeManagedPanelSettings("Panel.Example.com.", "Example.com.")
	if err != nil {
		t.Fatalf("normalize managed settings: %v", err)
	}
	if settings.PanelDomain != "panel.example.com" || settings.RouteDomain != "example.com" || !settings.TLSEnabled {
		t.Fatalf("unexpected settings: %+v", settings)
	}
	for _, tc := range [][2]string{
		{"a.b.example.com", "example.com"},
		{"panel.example.com", ""},
	} {
		if _, err := normalizeManagedPanelSettings(tc[0], tc[1]); err == nil {
			t.Fatalf("normalizeManagedPanelSettings(%q, %q) unexpectedly succeeded", tc[0], tc[1])
		}
	}
}

func TestManagedPanelAtNodeBaseDomain(t *testing.T) {
	for _, domain := range []string{"example.com", "media.example.com"} {
		settings, err := normalizeManagedPanelSettings(domain, domain)
		if err != nil {
			t.Fatal(err)
		}
		domains := panelACMECertificateDomains(settings)
		if len(domains) != 2 || domains[0] != "*."+domain || domains[1] != domain {
			t.Fatalf("ACME domains = %v", domains)
		}
		certPEM, keyPEM := testImportedCertificate(t, domains, 719, time.Now().Add(24*time.Hour))
		issued, err := parseImportedPanelCertificate(certPEM, keyPEM, settings)
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range []string{domain, "cinema." + domain} {
			if err := issued.certificate.Leaf.VerifyHostname(host); err != nil {
				t.Fatalf("certificate does not cover %s: %v", host, err)
			}
		}
		if err := issued.certificate.Leaf.VerifyHostname("deep.cinema." + domain); err == nil {
			t.Fatal("wildcard unexpectedly covers multiple labels")
		}
	}
	settings, err := normalizeManagedPanelSettings("panel.example.com", "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if domains := panelACMECertificateDomains(settings); len(domains) != 1 || domains[0] != "*.example.com" {
		t.Fatalf("legacy wildcard order changed: %v", domains)
	}
}

func TestRetainedGatewayPreventsChangingOnlyNativeBackendPort(t *testing.T) {
	app := newTestApp(t)
	app.routeDirect, app.panelTLSEnabled = true, true
	app.panelListenPort = 9443
	var err error
	app.trustedProxies, err = parseTrustedProxyCIDRs("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.validatePanelPortChange(9443); err != nil {
		t.Fatalf("unchanged port must allow certificate maintenance: %v", err)
	}
	if err := app.validatePanelPortChange(freePort(t)); err == nil {
		t.Fatal("changing the backend port must not strand the retained public gateway")
	}
}

func TestNormalizeManagedPanelPrefixAcceptsWildcardForm(t *testing.T) {
	settings, err := normalizeManagedPanelPrefix("panel", "*.example.com")
	if err != nil {
		t.Fatalf("normalize panel prefix: %v", err)
	}
	if settings.PanelDomain != "panel.example.com" || settings.RouteDomain != "example.com" {
		t.Fatalf("unexpected normalized wildcard settings: %+v", settings)
	}
	if got := wildcardDomainForSettings(settings); got != "*.example.com" {
		t.Fatalf("wildcard domain = %q", got)
	}
}

func TestBootstrapPanelSettingsSynchronizesOnlyEnvironmentPanelDomain(t *testing.T) {
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.BootstrapPanelSettings("panel.example.com", "example.com", false, 9090)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO sites (name, listen_port, public_host, target_url) VALUES ('media', 19001, 'media.example.com', 'http://127.0.0.1:8096')`); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"Other.Example.net.", "", "panel.example.com"} {
		settings, err := db.BootstrapPanelSettings(domain, "different.example.org", true, 8080)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := normalizePublicHost(domain)
		if settings.PanelDomain != want || settings.RouteDomain != first.RouteDomain || settings.TLSEnabled != first.TLSEnabled || settings.ListenPort != first.ListenPort {
			t.Fatalf("domain synchronization changed unrelated endpoint settings: %+v", settings)
		}
		var host string
		if err := db.db.QueryRow("SELECT public_host FROM sites WHERE name='media'").Scan(&host); err != nil {
			t.Fatal(err)
		}
		if host != "media.example.com" {
			t.Fatalf("site host changed: %s", host)
		}
	}
}

func TestBootstrapPanelSettingsInitializesPortWithoutDomains(t *testing.T) {
	db, err := openDB(filepath.Join(t.TempDir(), "panel-settings-port.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	first, err := db.BootstrapPanelSettings("", "", false, 9090)
	if err != nil {
		t.Fatalf("bootstrap empty settings: %v", err)
	}
	if first.Configured || first.PanelDomain != "" || first.RouteDomain != "" || first.TLSEnabled {
		t.Fatalf("empty bootstrap unexpectedly configured domains or TLS: %#v", first)
	}
	if first.ListenPort != 9090 {
		t.Fatalf("empty bootstrap listen_port=%d, want 9090", first.ListenPort)
	}

	persisted, err := db.PanelSettings()
	if err != nil {
		t.Fatalf("read persisted settings: %v", err)
	}
	if persisted.ListenPort != 9090 || persisted.Configured {
		t.Fatalf("persisted empty bootstrap=%#v, want port-only initialization", persisted)
	}

	second, err := db.BootstrapPanelSettings("", "", false, 8080)
	if err != nil {
		t.Fatalf("second empty bootstrap: %v", err)
	}
	if second.ListenPort != 9090 {
		t.Fatalf("second empty bootstrap overwrote listen_port=%d, want 9090", second.ListenPort)
	}
}

func TestSaveManagedPanelSettingsMigratesPrefixHosts(t *testing.T) {
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := db.db.Exec(`INSERT INTO sites (name, listen_port, public_host, target_url) VALUES (?, ?, ?, ?)`, "One", 19001, "one.old.example.com", "http://127.0.0.1:8096"); err != nil {
		t.Fatalf("insert site: %v", err)
	}
	if _, err := db.BootstrapPanelSettings("panel.old.example.com", "old.example.com", false, 9090); err != nil {
		t.Fatalf("bootstrap old settings: %v", err)
	}
	settings, migrated, err := db.SaveManagedPanelSettings("panel.example.com", "example.com", 9090, false)
	if err != nil {
		t.Fatalf("save managed settings: %v", err)
	}
	if migrated != 1 || settings.PanelDomain != "panel.example.com" {
		t.Fatalf("unexpected migration result: settings=%+v migrated=%d", settings, migrated)
	}
	var host string
	if err := db.db.QueryRow("SELECT public_host FROM sites WHERE name=?", "One").Scan(&host); err != nil {
		t.Fatalf("read migrated site: %v", err)
	}
	if host != "one.example.com" {
		t.Fatalf("migrated site host = %q, want one.example.com", host)
	}
}

func TestSaveManagedPanelSettingsPreservesACMECredentials(t *testing.T) {
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SavePanelACMECredentials("admin@example.com", "cloudflare", "v1:ciphertext", true); err != nil {
		t.Fatal(err)
	}
	settings, _, err := db.SaveManagedPanelSettings("panel.example.com", "example.com", 9443, true)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ACMEEmail != "admin@example.com" || settings.ACMEDNSProvider != "cloudflare" || settings.ACMETokenCiphertext != "v1:ciphertext" || !settings.ACMEStaging {
		t.Fatalf("ACME credentials changed while saving panel settings: %+v", settings)
	}
}

func TestManagedExternalTLSProxyRejectsNative443(t *testing.T) {
	app := &App{panelBindLoopback: true, panelTLSEnabled: false, panelHost: "panel.example.com"}
	if !app.managedExternalTLSProxy() {
		t.Fatal("expected the loopback HTTP panel behind an external TLS proxy")
	}
	app.panelTLSEnabled = true
	if app.managedExternalTLSProxy() {
		t.Fatal("native TLS panel must not be treated as an external proxy")
	}
	app.panelTLSEnabled = false
	app.panelHost = ""
	if app.managedExternalTLSProxy() {
		t.Fatal("domain-less local panel must not be treated as an external proxy")
	}
}

func TestPanelSettingsHandlerRejects443BehindExternalTLSProxy(t *testing.T) {
	app := newTestApp(t)
	app.panelHost = "panel.example.com"
	app.panelBindLoopback = true
	app.panelTLSEnabled = false
	app.panelListenPort = 9090
	if _, err := app.db.BootstrapPanelSettings("panel.example.com", "example.com", false, 9090); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(panelSettingsRequest{PanelDomain: "panel.example.com", WildcardDomain: "*.example.com", ListenPort: 443})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/panel-settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	app.handlePanelSettings(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
	}
	settings, err := app.db.PanelSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.ListenPort != 9090 {
		t.Fatalf("rejected request changed listen port to %d", settings.ListenPort)
	}
}
