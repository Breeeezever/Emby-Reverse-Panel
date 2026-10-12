package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestPanelDomainEnvironmentPersistsAcrossContainerRecreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meridian.db")
	for _, step := range []struct {
		env     string
		allowed []string
		denied  []string
	}{
		{"old.example.com", []string{"old.example.com"}, []string{"new.example.com"}},
		{"new.example.com", []string{"new.example.com"}, []string{"old.example.com"}},
		{"", []string{"old.example.com", "new.example.com"}, nil},
		{"", []string{"another.example.com"}, nil},
		{"third.example.com", []string{"third.example.com"}, []string{"new.example.com"}},
	} {
		t.Run("domain="+step.env, func(t *testing.T) {
			// Reopen the same data volume, as Docker does when replacing a container.
			db, err := openDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			settings, err := db.BootstrapPanelSettings(step.env, "", false, 9090)
			if err != nil {
				t.Fatal(err)
			}
			if settings.PanelDomain != step.env || settings.PanelDomainSource != panelDomainSourceEnvironment || settings.ListenPort != 9090 || settings.TLSEnabled || settings.Configured != (step.env != "") {
				t.Fatalf("unexpected persisted settings: %+v", settings)
			}
			app := &App{db: db, pm: NewProxyManager(db, nil), panelHost: settings.PanelDomain}
			router := app.publicHostRouter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			check := func(host string, want int) {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
				resp := httptest.NewRecorder()
				router.ServeHTTP(resp, req)
				if resp.Code != want {
					t.Fatalf("Host %s: status=%d want=%d", host, resp.Code, want)
				}
			}
			for _, host := range step.allowed {
				check(host, http.StatusNoContent)
			}
			for _, host := range step.denied {
				check(host, http.StatusMisdirectedRequest)
			}
		})
	}
}

func TestPanelDomainEnvironmentPreservesManualSettings(t *testing.T) {
	for _, nativeTLS := range []bool{false, true} {
		db, err := openDB(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.BootstrapPanelSettings("env.example.com", "", false, 9090); err != nil {
			t.Fatal(err)
		}
		if err := db.SavePanelACMECredentials("admin@example.com", "cloudflare", "saved-ciphertext", true); err != nil {
			t.Fatal(err)
		}
		saved, _, err := db.SavePanelEndpointSettings("saved.example.com", "", 9443, nativeTLS)
		if err != nil {
			t.Fatal(err)
		}
		if saved.PanelDomainSource != panelDomainSourceDatabase {
			t.Fatalf("manual save did not take control: %+v", saved)
		}
		for _, env := range []string{"", "env.example.com", "changed.example.com"} {
			got, err := db.BootstrapPanelSettings(env, "", false, 8080)
			if err != nil {
				t.Fatal(err)
			}
			if got != saved {
				t.Fatalf("environment %q changed manual settings: got=%+v want=%+v", env, got, saved)
			}
		}
	}
}

func TestLegacyPanelDomainMigration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		extraSQL   string
		env        string
		wantDomain string
		wantSource string
	}{
		{"remove old env domain", "", "", "", panelDomainSourceEnvironment},
		{"replace old env domain", "", "new.example.com", "new.example.com", panelDomainSourceEnvironment},
		{"keep native TLS", "tls_enabled=1", "", "old.example.com", panelDomainSourceDatabase},
		{"keep route configuration", "route_domain='example.com'", "", "old.example.com", panelDomainSourceDatabase},
		{"keep ACME configuration", "acme_token_ciphertext='saved-ciphertext'", "", "old.example.com", panelDomainSourceDatabase},
		{"keep imported certificate", "certificate_source='upload'", "", "old.example.com", panelDomainSourceDatabase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "meridian.db")
			db, err := openDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.db.Exec("ALTER TABLE panel_settings DROP COLUMN panel_domain_source"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.db.Exec("UPDATE panel_settings SET configured=1, panel_domain='old.example.com', listen_port=9090"); err != nil {
				t.Fatal(err)
			}
			if tc.extraSQL != "" {
				if _, err := db.db.Exec("UPDATE panel_settings SET " + tc.extraSQL); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			db, err = openDB(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			before, err := db.PanelSettings()
			if err != nil {
				t.Fatal(err)
			}
			after, err := db.BootstrapPanelSettings(tc.env, "", false, 8080)
			if err != nil {
				t.Fatal(err)
			}
			if after.PanelDomain != tc.wantDomain || after.PanelDomainSource != tc.wantSource {
				t.Fatalf("unexpected migration: %+v", after)
			}
			expected := before
			expected.PanelDomain = tc.wantDomain
			expected.PanelDomainSource = tc.wantSource
			expected.Configured = expected.PanelDomain != "" || expected.RouteDomain != "" || expected.TLSEnabled
			if after != expected {
				t.Fatalf("migration changed unrelated settings: got=%+v want=%+v", after, expected)
			}
		})
	}
}

func TestPanelDomainEnvironmentRejectsInvalidOrConflictingHost(t *testing.T) {
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	before, err := db.BootstrapPanelSettings("panel.example.com", "", false, 9090)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO sites (name, listen_port, public_host, target_url) VALUES ('media', 19001, 'media.example.com', 'http://127.0.0.1:8096')`); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"https://invalid.example.com/path", "MEDIA.EXAMPLE.COM."} {
		if _, err := db.BootstrapPanelSettings(domain, "", false, 9090); err == nil {
			t.Fatalf("invalid/conflicting domain %q accepted", domain)
		}
		after, err := db.PanelSettings()
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatalf("failed bootstrap changed database: before=%+v after=%+v", before, after)
		}
	}
}

func TestPanelCertificateAndRollbackPreserveDomainOwnership(t *testing.T) {
	db, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	original, err := db.BootstrapPanelSettings("panel.example.com", "", false, 9090)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.setPanelCertificateSource("upload", "", ""); err != nil {
		t.Fatal(err)
	}
	// Certificate expiry may disable TLS temporarily; the saved domain must
	// survive a restart without PANEL_DOMAIN even while HTTPS is disabled.
	if err := db.SetPanelTLSEnabled(false); err != nil {
		t.Fatal(err)
	}
	settings, err := db.BootstrapPanelSettings("", "", false, 9090)
	if err != nil {
		t.Fatal(err)
	}
	if settings.PanelDomain != original.PanelDomain || settings.PanelDomainSource != panelDomainSourceDatabase {
		t.Fatalf("certificate configuration lost its domain: %+v", settings)
	}
	// A failed certificate operation restores the previous ownership, too.
	if err := db.restorePanelSettings(original); err != nil {
		t.Fatal(err)
	}
	settings, err = db.BootstrapPanelSettings("", "", false, 9090)
	if err != nil {
		t.Fatal(err)
	}
	if settings.PanelDomain != "" || settings.PanelDomainSource != panelDomainSourceEnvironment {
		t.Fatalf("rollback did not restore environment domain control: %+v", settings)
	}
}

func TestHealthcheckPanelDomainUsesEffectiveDatabaseValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meridian.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("PANEL_DOMAIN", "stale.example.com")
	for _, domain := range []string{"", "current.example.com"} {
		if _, err := db.db.Exec("UPDATE panel_settings SET panel_domain=?", domain); err != nil {
			t.Fatal(err)
		}
		if got := healthcheckPanelDomain(path); got != domain {
			t.Fatalf("healthcheck domain=%q want=%q", got, domain)
		}
	}
}
