package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
)

func lifecycleCertificate(t *testing.T, names []string, serial int64, expires time.Time, roots *x509.CertPool) *issuedPanelCertificate {
	t.Helper()
	cert, key := testImportedCertificate(t, names, serial, expires)
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pair.Leaf = leaf
	if roots != nil {
		roots.AddCert(leaf)
	}
	return &issuedPanelCertificate{certPEM: cert, keyPEM: key, certificate: pair}
}

func lifecycleACMEApp(t *testing.T, expires time.Time) (*App, PanelSettings) {
	t.Helper()
	previousEphemeral := jwtSecretEphemeral
	jwtSecretEphemeral = false
	t.Cleanup(func() { jwtSecretEphemeral = previousEphemeral })
	a := newPanelEndpointTestApp(t)
	m := a.panelCertificates
	m.trustRoots = x509.NewCertPool()
	issued := lifecycleCertificate(t, []string{"*.example.com"}, 101, expires, m.trustRoots)
	if err := m.install(issued, true); err != nil {
		t.Fatal(err)
	}
	token, err := encryptPanelACMEToken("original-working-review-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.db.savePanelACMECertificateSettings("admin@example.com", "cloudflare", token); err != nil {
		t.Fatal(err)
	}
	settings, err := a.db.PanelSettings()
	if err != nil {
		t.Fatal(err)
	}
	m.bindRuntimeSettings(settings)
	return a, settings
}

func certificateIssueRequest(t *testing.T, a *App, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	a.handlePanelCertificateIssue(w, httptest.NewRequest(http.MethodPost, "https://panel.example.com:6551/api/panel-certificate/issue", strings.NewReader(body)))
	return w
}

func TestExpiredCertificateRenewsOnSameHTTPSListener(t *testing.T) {
	a, settings := lifecycleACMEApp(t, time.Now().Add(-time.Hour))
	m := a.panelCertificates
	newCert := lifecycleCertificate(t, []string{"*.example.com"}, 102, time.Now().Add(90*24*time.Hour), m.trustRoots)
	config, enabled, err := m.tlsConfig(true)
	if err != nil || !enabled {
		t.Fatalf("HTTPS: %v", err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("secure")) }))
	server.TLS = config
	server.StartTLS()
	defer server.Close()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: m.trustRoots, ServerName: settings.PanelDomain, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	if response, err := client.Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("expired certificate unexpectedly trusted")
	}
	m.issuer = func(ctx context.Context, email, token, host, route string, staging bool) (*issuedPanelCertificate, error) {
		if host != settings.PanelDomain || route != settings.RouteDomain || staging {
			t.Fatal("wrong renewal target")
		}
		return newCert, nil
	}
	if err := renewPanelCertificateIfDue(context.Background(), a.db, m); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("renewal did not recover same listener: %v", err)
	}
	defer response.Body.Close()
	if data, _ := io.ReadAll(response.Body); string(data) != "secure" {
		t.Fatalf("body=%s", data)
	}
	after, _ := a.db.PanelSettings()
	if !after.TLSEnabled || after != settings {
		t.Fatal("renewal changed endpoint or protocol")
	}
	state := a.db.panelRenewalStatus(settings)
	if state.LastSuccessAt == "" || state.LastError != "" || state.NextAttemptAt == "" {
		t.Fatalf("missing renewal success: %+v", state)
	}
}

func TestFailedRenewalPreservesHTTPSAndBacksOff(t *testing.T) {
	a, settings := lifecycleACMEApp(t, time.Now().Add(-time.Hour))
	m := a.panelCertificates
	calls := 0
	m.issuer = func(context.Context, string, string, string, string, bool) (*issuedPanelCertificate, error) {
		calls++
		return nil, &acme.Error{StatusCode: http.StatusTooManyRequests, Detail: "rate limited original-working-review-token", Header: http.Header{"Retry-After": []string{"7200"}}}
	}
	before, _ := os.ReadFile(m.certFile)
	if err := renewPanelCertificateIfDue(context.Background(), a.db, m); err == nil || strings.Contains(err.Error(), "original-working-review-token") {
		t.Fatalf("unsafe failure: %v", err)
	}
	state := a.db.panelRenewalStatus(settings)
	if state.LastError == "" || strings.Contains(state.LastError, "original-working-review-token") {
		t.Fatal("missing or unredacted failure")
	}
	next, err := time.Parse(time.RFC3339, state.NextAttemptAt)
	if err != nil || time.Until(next) < 119*time.Minute {
		t.Fatal("Retry-After was ignored")
	}
	if err := renewPanelCertificateIfDue(context.Background(), a.db, m); err != nil || calls != 1 {
		t.Fatal("retry did not back off")
	}
	after, _ := a.db.PanelSettings()
	cert, _ := os.ReadFile(m.certFile)
	if after != settings || !after.TLSEnabled || !bytes.Equal(before, cert) || m.currentCertificate == nil {
		t.Fatal("failure changed TLS or certificate")
	}
	// Simulate expiry of the retry delay and recover without re-enabling TLS.
	if _, err := a.db.db.Exec("UPDATE panel_certificate_renewals SET next_attempt_at='' "); err != nil {
		t.Fatal(err)
	}
	newCert := lifecycleCertificate(t, []string{"*.example.com"}, 103, time.Now().Add(90*24*time.Hour), m.trustRoots)
	m.issuer = func(context.Context, string, string, string, string, bool) (*issuedPanelCertificate, error) {
		return newCert, nil
	}
	if err := renewPanelCertificateIfDue(context.Background(), a.db, m); err != nil {
		t.Fatal(err)
	}
	if state := a.db.panelRenewalStatus(settings); state.LastError != "" || state.LastSuccessAt == "" {
		t.Fatal("successful retry did not clear failure")
	}
}

func TestACMERenewsActiveDomainWithoutReplacingPendingCertificate(t *testing.T) {
	a, original := lifecycleACMEApp(t, time.Now().Add(time.Hour))
	m := a.panelCertificates
	if _, _, err := a.db.SavePanelEndpointSettings("panel.another.net", "another.net", 6551, true); err != nil {
		t.Fatal(err)
	}
	pending := lifecycleCertificate(t, []string{"*.another.net"}, 201, time.Now().Add(90*24*time.Hour), nil)
	if err := importPanelCertificate(a.db, m, "upload", "", "", pending.certPEM, pending.keyPEM, false); err != nil {
		t.Fatal(err)
	}
	renewed := lifecycleCertificate(t, []string{"*.example.com"}, 202, time.Now().Add(90*24*time.Hour), m.trustRoots)
	m.issuer = func(ctx context.Context, email, token, host, route string, staging bool) (*issuedPanelCertificate, error) {
		if host != original.PanelDomain || route != original.RouteDomain || token != "original-working-review-token" || staging {
			t.Fatal("renewed pending settings instead of active settings")
		}
		return renewed, nil
	}
	if err := renewPanelCertificateIfDue(context.Background(), a.db, m); err != nil {
		t.Fatal(err)
	}
	if err := m.currentCertificate.Leaf.VerifyHostname(original.PanelDomain); err != nil {
		t.Fatal(err)
	}
	if m.currentCertificate.Leaf.SerialNumber.Int64() != 202 {
		t.Fatal("active certificate not renewed")
	}
	onDisk, _ := os.ReadFile(m.certFile)
	if !bytes.Equal(onDisk, pending.certPEM) {
		t.Fatal("renewal overwrote pending certificate")
	}
	saved, _ := a.db.PanelSettings()
	status := a.panelCertificateStatus(saved)
	if !status.Renewal.Pending || status.Renewal.Domain != original.PanelDomain || status.Renewal.LastSuccessAt == "" {
		t.Fatalf("status confuses active/pending: %+v", status.Renewal)
	}
	// A fresh process intentionally loads the saved certificate.
	restarted := &panelCertificateManager{certFile: m.certFile, keyFile: m.keyFile, accountDir: m.accountDir}
	if _, _, err := restarted.tlsConfig(true); err != nil {
		t.Fatal(err)
	}
	if err := restarted.currentCertificate.Leaf.VerifyHostname(saved.PanelDomain); err != nil {
		t.Fatal(err)
	}
}

func TestPathRenewalKeepsPendingAndActiveCertificatesSeparate(t *testing.T) {
	a := newPanelEndpointTestApp(t)
	m := a.panelCertificates
	dir := t.TempDir()
	writePair := func(name string, issued *issuedPanelCertificate) (string, string) {
		t.Helper()
		certPath, keyPath := filepath.Join(dir, name+".pem"), filepath.Join(dir, name+".key")
		if err := os.WriteFile(certPath, issued.certPEM, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, issued.keyPEM, 0600); err != nil {
			t.Fatal(err)
		}
		return certPath, keyPath
	}
	active := lifecycleCertificate(t, []string{"*.example.com"}, 301, time.Now().Add(90*24*time.Hour), nil)
	certPath, keyPath := writePair("active", active)
	if err := importPanelCertificate(a.db, m, "path", certPath, keyPath, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.db.SavePanelEndpointSettings("panel.another.net", "another.net", 6551, true); err != nil {
		t.Fatal(err)
	}
	pending := lifecycleCertificate(t, []string{"*.another.net"}, 302, time.Now().Add(90*24*time.Hour), nil)
	newCertPath, newKeyPath := writePair("pending", pending)
	if err := importPanelCertificate(a.db, m, "path", newCertPath, newKeyPath, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	writePair("pending", lifecycleCertificate(t, []string{"*.another.net"}, 303, time.Now().Add(91*24*time.Hour), nil))
	if err := reloadExternalPanelCertificate(a.db, m); err != nil {
		t.Fatal(err)
	}
	if m.currentCertificate.Leaf.SerialNumber.Int64() != 301 {
		t.Fatal("pending source update changed live certificate")
	}
	writePair("active", lifecycleCertificate(t, []string{"*.example.com"}, 304, time.Now().Add(91*24*time.Hour), nil))
	if err := reloadExternalPanelCertificate(a.db, m); err != nil {
		t.Fatal(err)
	}
	if m.currentCertificate.Leaf.SerialNumber.Int64() != 304 {
		t.Fatal("active source did not reload")
	}
	disk, _ := os.ReadFile(m.certFile)
	if !bytes.Equal(disk, pending.certPEM) {
		t.Fatal("active reload overwrote saved certificate")
	}
	if err := os.WriteFile(keyPath, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reloadExternalPanelCertificate(a.db, m); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	if m.currentCertificate.Leaf.SerialNumber.Int64() != 304 {
		t.Fatal("failed reload changed live certificate")
	}
	saved, _ := a.db.PanelSettings()
	if state := a.db.panelRenewalStatus(m.maintenanceSettings(saved)); state.LastError == "" {
		t.Fatal("reload failure not persisted")
	}
}

func TestStagingIssuanceDoesNotChangeServiceCertificateOrSettings(t *testing.T) {
	a, before := lifecycleACMEApp(t, time.Now().Add(90*24*time.Hour))
	m := a.panelCertificates
	disk, _ := os.ReadFile(m.certFile)
	staged := lifecycleCertificate(t, []string{"*.example.com"}, 401, time.Now().Add(90*24*time.Hour), nil)
	m.issuer = func(ctx context.Context, email, token, host, route string, staging bool) (*issuedPanelCertificate, error) {
		if !staging {
			t.Fatal("not a staging request")
		}
		return staged, nil
	}
	w := certificateIssueRequest(t, a, `{"staging":true,"email":"test@example.com","dns_api_token":"replacement-test-only-token"}`)
	var result panelCertificateStatus
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.TestIssued {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	after, _ := a.db.PanelSettings()
	now, _ := os.ReadFile(m.certFile)
	if before != after || !bytes.Equal(disk, now) || m.currentCertificate.Leaf.SerialNumber.Int64() != 101 {
		t.Fatal("test issuance changed live state")
	}
	if _, err := os.Stat(filepath.Join(m.accountDir, "acme-test", "fullchain.pem")); err != nil {
		t.Fatal(err)
	}
}

func TestProductionSwitchIssuesNewCertificateAndCommitsAtomically(t *testing.T) {
	for _, failure := range []string{"", "issuer", "database", "untrusted"} {
		t.Run(failure, func(t *testing.T) {
			a, settings := lifecycleACMEApp(t, time.Now().Add(90*24*time.Hour))
			m := a.panelCertificates
			if err := a.db.SavePanelACMECredentials(settings.ACMEEmail, "cloudflare", settings.ACMETokenCiphertext, true); err != nil {
				t.Fatal(err)
			}
			before, _ := a.db.PanelSettings()
			m.bindRuntimeSettings(before)
			disk, _ := os.ReadFile(m.certFile)
			roots := m.trustRoots
			if failure == "untrusted" {
				roots = nil
			}
			issued := lifecycleCertificate(t, []string{"*.example.com"}, 501, time.Now().Add(90*24*time.Hour), roots)
			calls := 0
			m.issuer = func(ctx context.Context, email, token, host, route string, staging bool) (*issuedPanelCertificate, error) {
				calls++
				if staging {
					t.Fatal("production request used staging")
				}
				if failure == "issuer" {
					return nil, errors.New("denied replacement-production-token")
				}
				return issued, nil
			}
			if failure == "database" {
				if _, err := a.db.db.Exec(`CREATE TRIGGER reject_certificate BEFORE UPDATE OF certificate_source ON panel_settings BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			w := certificateIssueRequest(t, a, `{"staging":false,"dns_api_token":"replacement-production-token"}`)
			if calls != 1 {
				t.Fatal("environment switch reused old certificate")
			}
			after, _ := a.db.PanelSettings()
			now, _ := os.ReadFile(m.certFile)
			if failure != "" {
				if w.Code < 400 || before != after || !bytes.Equal(disk, now) || m.currentCertificate.Leaf.SerialNumber.Int64() != 101 {
					t.Fatalf("failure changed state: %d %s", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "replacement-production-token") {
					t.Fatal("response exposed token")
				}
			} else {
				if w.Code != 200 || after.ACMEStaging || !after.TLSEnabled || !bytes.Equal(now, issued.certPEM) || m.currentCertificate.Leaf.SerialNumber.Int64() != 501 {
					t.Fatalf("switch failed: %d %s", w.Code, w.Body.String())
				}
				if token, err := decryptPanelACMEToken(after.ACMETokenCiphertext); err != nil || token != "replacement-production-token" {
					t.Fatal("new credentials not committed")
				}
			}
		})
	}
}

func TestUntrustedCertificateIsNotReusedForProduction(t *testing.T) {
	a, _ := lifecycleACMEApp(t, time.Now().Add(90*24*time.Hour))
	a.panelCertificates.trustRoots = x509.NewCertPool()
	calls := 0
	a.panelCertificates.issuer = func(context.Context, string, string, string, string, bool) (*issuedPanelCertificate, error) {
		calls++
		return nil, errors.New("offline test")
	}
	w := certificateIssueRequest(t, a, `{"staging":false}`)
	if calls != 1 || w.Code != 502 {
		t.Fatalf("untrusted certificate reused: %d %s", w.Code, w.Body.String())
	}
}

func TestCertificateReuseRequiresUnchangedWorkingCredentials(t *testing.T) {
	for _, newToken := range []bool{false, true} {
		t.Run(map[bool]string{false: "reuse", true: "changed token"}[newToken], func(t *testing.T) {
			a, before := lifecycleACMEApp(t, time.Now().Add(90*24*time.Hour))
			a.db.recordPanelRenewal(before, true, nil, time.Now().Add(time.Hour))
			prior := a.db.panelRenewalStatus(before)
			calls := 0
			a.panelCertificates.issuer = func(context.Context, string, string, string, string, bool) (*issuedPanelCertificate, error) {
				calls++
				return nil, errors.New("invalid replacement token")
			}
			body := `{"staging":false}`
			if newToken {
				body = `{"staging":false,"dns_api_token":"invalid-replacement-token-value"}`
			}
			w := certificateIssueRequest(t, a, body)
			after, _ := a.db.PanelSettings()
			if after != before {
				t.Fatal("reuse or rejected token changed renewal credentials")
			}
			if newToken {
				if calls != 1 || w.Code != 502 {
					t.Fatalf("new token not validated: %d %s", w.Code, w.Body.String())
				}
			} else {
				if calls != 0 || w.Code != 200 || !strings.Contains(w.Body.String(), `"certificate_reused":true`) {
					t.Fatalf("valid certificate not reused: %d %s", w.Code, w.Body.String())
				}
				if now := a.db.panelRenewalStatus(after); now.LastSuccessAt != prior.LastSuccessAt {
					t.Fatal("reuse erased renewal history")
				}
			}
		})
	}
}

func TestCertificateRenewalStatusSurvivesDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := PanelSettings{PanelDomain: "panel.example.com", RouteDomain: "example.com", CertificateSource: "acme"}
	db.recordPanelRenewal(settings, true, nil, time.Now().Add(time.Hour))
	db.recordPanelRenewal(settings, false, errors.New("DNS validation timed out"), time.Now().Add(time.Hour))
	before := db.panelRenewalStatus(settings)
	db.Close()
	db, err = openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if after := db.panelRenewalStatus(settings); after != before || after.LastSuccessAt == "" || after.LastError == "" {
		t.Fatalf("history lost: %+v", after)
	}
}
