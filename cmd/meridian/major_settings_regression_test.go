package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testImportedCertificate(t *testing.T, names []string, serial int64, expires time.Time) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: names, NotBefore: time.Now().Add(-time.Hour), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
func TestImportedCertificateValidationAndReload(t *testing.T) {
	app := newTestApp(t)
	dir := t.TempDir()
	manager := newPanelCertificateManager(filepath.Join(dir, "test.db"), nil)
	_, _, err := app.db.SavePanelEndpointSettings("panel.example.com", "", 9090, false)
	if err != nil {
		t.Fatal(err)
	}
	cert, key := testImportedCertificate(t, []string{"panel.example.com"}, 1, time.Now().Add(90*24*time.Hour))
	if err := importPanelCertificate(app.db, manager, "upload", "", "", cert, key, false); err != nil {
		t.Fatal(err)
	}
	settings, _ := app.db.PanelSettings()
	if !settings.TLSEnabled || settings.CertificateSource != "upload" || settings.CertificatePath != "" {
		t.Fatalf("settings=%+v", settings)
	}
	wrongCert, wrongKey := testImportedCertificate(t, []string{"wrong.example.com"}, 2, time.Now().Add(time.Hour))
	expiredCert, expiredKey := testImportedCertificate(t, []string{"panel.example.com"}, 3, time.Now().Add(-time.Minute))
	for _, test := range []struct {
		name string
		c, k []byte
	}{{"mismatched pair", cert, wrongKey}, {"mismatched domain", wrongCert, wrongKey}, {"expired", expiredCert, expiredKey}, {"empty", nil, key}} {
		t.Run(test.name, func(t *testing.T) {
			if importPanelCertificate(app.db, manager, "upload", "", "", test.c, test.k, false) == nil {
				t.Fatal("invalid certificate accepted")
			}
			installed, _ := os.ReadFile(manager.certFile)
			if !bytes.Equal(installed, cert) {
				t.Fatal("invalid import replaced current certificate")
			}
		})
	}
	certPath, keyPath := filepath.Join(dir, "external.crt"), filepath.Join(dir, "external.key")
	if err := os.WriteFile(certPath, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	if err := importPanelCertificate(app.db, manager, "path", certPath, keyPath, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	renewedCert, renewedKey := testImportedCertificate(t, []string{"panel.example.com"}, 4, time.Now().Add(91*24*time.Hour))
	if err := os.WriteFile(certPath, renewedCert, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, renewedKey, 0600); err != nil {
		t.Fatal(err)
	}
	if err := reloadExternalPanelCertificate(app.db, manager); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	serial := manager.currentCertificate.Leaf.SerialNumber.Int64()
	manager.mu.Unlock()
	if serial != 4 {
		t.Fatalf("renewed serial=%d", serial)
	}
	if err := os.WriteFile(keyPath, wrongKey, 0600); err != nil {
		t.Fatal(err)
	}
	if reloadExternalPanelCertificate(app.db, manager) == nil {
		t.Fatal("invalid renewal accepted")
	}
	installed, _ := os.ReadFile(manager.certFile)
	if !bytes.Equal(installed, renewedCert) {
		t.Fatal("invalid renewal replaced valid certificate")
	}
	// Persist failure must restore both certificate and key.
	if _, err := app.db.db.Exec(`CREATE TRIGGER reject_cert BEFORE UPDATE OF certificate_source ON panel_settings BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if importPanelCertificate(app.db, manager, "upload", "", "", cert, key, true) == nil {
		t.Fatal("database failure ignored")
	}
	installed, _ = os.ReadFile(manager.certFile)
	if !bytes.Equal(installed, renewedCert) {
		t.Fatal("database failure did not restore certificate")
	}
	manager.mu.Lock()
	serial = manager.currentCertificate.Leaf.SerialNumber.Int64()
	manager.mu.Unlock()
	if serial != 4 {
		t.Fatal("failed import changed live certificate")
	}
}
func TestImportedCertificateRequiresWildcardWhenConfigured(t *testing.T) {
	cert, key := testImportedCertificate(t, []string{"panel.example.com"}, 1, time.Now().Add(time.Hour))
	settings := PanelSettings{PanelDomain: "panel.example.com", RouteDomain: "example.com"}
	if _, err := parseImportedPanelCertificate(cert, key, settings); err == nil {
		t.Fatal("missing wildcard accepted")
	}
	cert, key = testImportedCertificate(t, []string{"*.example.com"}, 2, time.Now().Add(time.Hour))
	if _, err := parseImportedPanelCertificate(cert, key, settings); err != nil {
		t.Fatal(err)
	}
}
func TestFixedAppearanceAndProbeSettingsCannotBeOverridden(t *testing.T) {
	app := newTestApp(t)
	settings := defaultSystemSettings()
	settings.UIRadius = 1
	settings.ProbeTimeoutMS = 10
	settings.PingCacheMinutes = 999
	if err := app.db.saveSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	settings, err := app.db.loadSystemSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.UIRadius != 24 || settings.ProbeTimeoutMS != 5000 || settings.PingCacheMinutes != 10 {
		t.Fatalf("fixed settings overridden: %+v", settings)
	}
}
func TestAdministratorSixCharacterPasswordPolicy(t *testing.T) {
	for _, password := range []string{"123456", "中文密码六字", strings.Repeat("a", 72)} {
		if err := validateAdminPassword(password); err != nil {
			t.Fatalf("valid password rejected: %v", err)
		}
	}
	for _, password := range []string{"12345", "中文五字符", strings.Repeat("a", 73), strings.Repeat("中", 25), string([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})} {
		if validateAdminPassword(password) == nil {
			t.Fatal("invalid password accepted")
		}
	}
}
func TestReleaseCheckRejectsUnexpectedVersions(t *testing.T) {
	for _, test := range []struct {
		tag     string
		preview bool
		ok      bool
	}{{"v1.13.0", false, true}, {"v1.12.4", false, true}, {"v1.13.0-rc1", false, false}, {"v1.13.0", true, false}, {"../../payload", false, false}} {
		t.Run(test.tag, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": test.tag, "prerelease": test.preview, "body": "notes", "html_url": "javascript:bad"})
			}))
			defer server.Close()
			release, err := fetchUpdateRelease(context.Background(), server.Client(), server.URL, "v1.12.4")
			if (err == nil) != test.ok {
				t.Fatalf("result=%+v err=%v", release, err)
			}
			if test.ok {
				if !strings.HasPrefix(release.URL, "https://github.com/snnabb/Meridian/releases/tag/") {
					t.Fatal("untrusted release URL")
				}
				if release.Available != (test.tag == "v1.13.0") {
					t.Fatal("incorrect version comparison")
				}
			}
		})
	}
	if versionNewer("v1.9.99", "v1.10.0") || versionNewer("v1.13.0", "unknown") || versionNewer("v99999999999999999999999.0.0", "v1.0.0") {
		t.Fatal("invalid version compared as newer")
	}
}
func TestUpdateAgentSerializesJobsAndReportsFailure(t *testing.T) {
	for _, runErr := range []error{nil, errors.New("internal sensitive detail")} {
		started := make(chan string, 1)
		finish := make(chan struct{})
		agent := &updateAgent{job: updateJob{State: "idle"}, run: func(version string) error { started <- version; <-finish; return runErr }}
		post := func(body string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			agent.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/update", strings.NewReader(body)))
			return w
		}
		if w := post(`{"version":"v1.13.0","command":"arbitrary"}`); w.Code != 400 {
			t.Fatal("unknown field accepted")
		}
		if w := post(`{"version":"v1.13.0;id"}`); w.Code != 400 {
			t.Fatal("invalid version accepted")
		}
		if w := post(`{"version":"v1.13.0"}`); w.Code != 202 {
			t.Fatalf("start status %d", w.Code)
		}
		if version := <-started; version != "v1.13.0" {
			t.Fatal("wrong version")
		}
		if w := post(`{"version":"v1.13.0"}`); w.Code != 409 {
			t.Fatal("duplicate update accepted")
		}
		close(finish)
		deadline := time.After(time.Second)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
	wait:
		for {
			select {
			case <-deadline:
				t.Fatal("update did not finish")
			case <-ticker.C:
				agent.mu.Lock()
				state := agent.job.State
				agent.mu.Unlock()
				if state != "running" {
					break wait
				}
			}
		}
		w := httptest.NewRecorder()
		agent.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/update", nil))
		var job updateJob
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		want := "completed"
		if runErr != nil {
			want = "failed"
		}
		if job.State != want || strings.Contains(job.Message, "sensitive") {
			t.Fatalf("job=%+v", job)
		}
	}
}
func TestDirectPlaybackInfoKeepsMetadataAndAbsolutizesMedia(t *testing.T) {
	input := `{"MediaSources":[{"DirectStreamUrl":"/emby/Videos/1/stream?api_key=client","TranscodingUrl":"https://cloud.example/video.m3u8","RequiredHttpHeaders":{"Referer":"https://cloud.example/"}}],"ImageUrl":"/Items/1/Images/Primary"}`
	request := httptest.NewRequest(http.MethodPost, "https://origin.example/emby/Items/1/PlaybackInfo?secret=origin", nil)
	response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(input)), Request: request}
	if err := rewriteSiteStructuredResponse(response, true, nil, true, dynamicDiscoverySourcePlaybackInfo); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	source := payload["MediaSources"].([]any)[0].(map[string]any)
	if source["DirectStreamUrl"] != "https://origin.example/emby/Videos/1/stream?api_key=client" || source["TranscodingUrl"] != "https://cloud.example/video.m3u8" || payload["ImageUrl"] != "/Items/1/Images/Primary" {
		t.Fatalf("rewritten=%s", body)
	}
	for _, path := range []string{"/Items/1/Images/Primary?Static=true", "/System/Info/Public?Download=true", "/Items/1/Images/Primary/stream.mp4", "/Videos/1/Subtitles/2/Stream?Static=true"} {
		u, _ := url.Parse(path)
		if isMainVideoStreamURL(u) {
			t.Errorf("API or poster classified as media: %s", path)
		}
	}
}

func TestDirectPlaybackPathIngressNeverRedirectsBackToProxy(t *testing.T) {
	app := newTestApp(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/Videos/") {
			if r.Header.Get("Range") != "bytes=0-1" {
				t.Errorf("media probe Range=%q", r.Header.Get("Range"))
			}
			_, _ = io.WriteString(w, "media must not pass through")
			return
		}
		_, _ = io.WriteString(w, "API still proxied")
	}))
	defer upstream.Close()
	site := Site{ID: 709, Name: "direct path", ListenPort: freePort(t), PathPrefix: "/watch", IngressMode: ingressModePath, TargetURL: upstream.URL + "/base?secret=server", PlaybackMode: "direct", MainVideoStreamMode: mainVideoStreamModeDirect, StreamHosts: "[]", UAMode: passthroughUAMode}
	if err := app.pm.StartSite(site); err != nil {
		t.Fatal(err)
	}
	defer app.pm.StopSite(site.ID)
	router := app.publicHostRouter(http.NotFoundHandler())
	for _, path := range []string{"/Videos/1/stream", "/Videos/1/master.m3u8", "/Videos/1/segment.ts"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://panel.example/watch"+path+"?api_key=client", nil))
		if w.Code != 307 || w.Header().Get("Location") != upstream.URL+"/base"+path+"?api_key=client" || w.Body.Len() != 0 {
			t.Fatalf("direct response: status=%d location=%q body=%q", w.Code, w.Header().Get("Location"), w.Body.String())
		}
	}
	for _, path := range []string{"/Items/1/Images/Primary?Static=true", "/System/Info/Public"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://panel.example/watch"+path, nil))
		if w.Code != 200 || w.Body.String() != "API still proxied" {
			t.Fatalf("API response=%d %q", w.Code, w.Body.String())
		}
	}
}
func TestDirectPlaybackCloudRedirectDoesNotFetchMedia(t *testing.T) {
	policy := redirectRuntimePolicy(dynamicProfileCompatible, true)
	_, state := redirectRuntimeState(t, policy.limits, dynamicIPResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
	}))
	defer state.close()
	target, _ := url.Parse("https://origin.example.com/base/Videos/1/stream")
	req := redirectRuntimeEligibleRequest(http.MethodGet, target.String())
	req = req.WithContext(context.WithValue(req.Context(), mainVideoDirectFallbackContextKey{}, target))
	transport := &redirectFollowTransport{mainVideoDirect: true, dynamicPolicy: policy, dynamicState: state, base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return redirectRuntimeResponse(r, 302, []string{"https://cloud.example.com/signed-opaque-id?sign=client"}, io.NopCloser(strings.NewReader("redirect"))), nil
	}), dynamicTransportFactory: func(*url.URL, []net.IP, *dynamicSelfTargetPolicy) (*http.Transport, error) {
		t.Fatal("direct cloud media must not be fetched by proxy")
		return nil, errors.New("unexpected fetch")
	}}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 307 || resp.Header.Get("Location") != "https://cloud.example.com/signed-opaque-id?sign=client" {
		t.Fatalf("direct cloud response=%d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
