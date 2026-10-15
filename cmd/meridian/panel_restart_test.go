package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelRestartPolicyOnlyPermitsSavedReadinessURL(t *testing.T) {
	a := newTestApp(t)
	if _, _, err := a.db.SavePanelEndpointSettings("next.example.com", "example.com", 14430, true); err != nil {
		t.Fatal(err)
	}
	handler := securityHeaders(a.panelRestartSecurityPolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})), nil)
	for _, path := range []string{"/", "/index.html", "/js/app.js", "/api/auth/check"} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "http://old.example.com"+path, nil))
		policy := resp.Header().Get("Content-Security-Policy")
		expect := "connect-src 'self'"
		if path == "/" || path == "/index.html" {
			expect += " https://next.example.com:14430/api/panel-ready"
		}
		if !strings.HasSuffix(policy, expect) || !strings.Contains(policy, "script-src 'self';") {
			t.Fatalf("%s policy: %s", path, policy)
		}
	}
	for _, domain := range []string{"example.com; connect-src *", "example.com/path", "user@example.com"} {
		if target := panelSettingsAccessURL(PanelSettings{PanelDomain: domain, ListenPort: 443, TLSEnabled: true}); target != "" {
			t.Fatalf("invalid domain yielded CSP target: %q", target)
		}
	}
}

func TestPanelReadyRejectsOldHostAndPendingRestart(t *testing.T) {
	a := newTestApp(t)
	a.panelHost = "next.example.com"
	a.restartCh = make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/panel-ready", a.handlePanelReady)
	mux.HandleFunc("/api/auth/check", cors(a.handleAuthCheck))
	handler := a.publicHostRouter(mux)
	probe := func(method, host, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://"+host+path, nil)
		r.Header.Set("Origin", "https://old.example.com")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	ready := probe(http.MethodGet, a.panelHost, "/api/panel-ready")
	if ready.Code != http.StatusOK || strings.TrimSpace(ready.Body.String()) != `{"ready":true}` || ready.Header().Get("Access-Control-Allow-Origin") != "*" || ready.Header().Get("Cache-Control") != "no-store" || ready.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("readiness response: %d %v %s", ready.Code, ready.Header(), ready.Body.String())
	}
	if resp := probe(http.MethodGet, "old.example.com", "/api/panel-ready"); resp.Code != http.StatusMisdirectedRequest {
		t.Fatalf("old host was accepted: %d", resp.Code)
	}
	if resp := probe(http.MethodPost, a.panelHost, "/api/panel-ready"); resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("readiness accepted POST: %d", resp.Code)
	}
	if resp := probe(http.MethodGet, a.panelHost, "/api/auth/check"); resp.Code != http.StatusForbidden {
		t.Fatalf("auth endpoint allowed cross-origin probe: %d", resp.Code)
	}
	a.requestRestart()
	if resp := probe(http.MethodGet, a.panelHost, "/api/panel-ready"); resp.Code != http.StatusServiceUnavailable || !strings.Contains(resp.Body.String(), `"ready":false`) {
		t.Fatalf("old process reported ready after restart requested: %d %s", resp.Code, resp.Body.String())
	}
}

func TestPanelRestartPolicyUsesTrustedPublicProxyAddress(t *testing.T) {
	a := newTestApp(t)
	a.panelHost, a.panelListenPort = "old.example.com", 19095
	var err error
	a.trustedProxies, err = parseTrustedProxyCIDRs("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.db.SavePanelEndpointSettings("next.example.com", "", 19095, false); err != nil {
		t.Fatal(err)
	}
	handler := securityHeaders(a.panelRestartSecurityPolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})), a.trustedProxies)
	for _, peer := range []string{"127.0.0.1:12345", "192.0.2.10:12345"} {
		request := httptest.NewRequest(http.MethodGet, "http://old.example.com:8443/", nil)
		request.RemoteAddr = peer
		request.Header.Set("X-Forwarded-Proto", "https")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		scheme := "http"
		if strings.HasPrefix(peer, "127.") {
			scheme = "https"
		}
		policy := response.Header().Get("Content-Security-Policy")
		if !strings.Contains(policy, scheme+"://next.example.com:8443/api/panel-ready") || strings.Contains(policy, ":19095") {
			t.Fatalf("proxy public policy for %s: %s", peer, policy)
		}
	}
}
