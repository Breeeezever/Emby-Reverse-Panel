package main

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

func panelSettingsAccessURL(settings PanelSettings) string {
	normalized, err := normalizeStoredPanelSettings(settings.PanelDomain, "", settings.TLSEnabled)
	if err != nil || normalized.PanelDomain == "" || validatePanelListenPort(settings.ListenPort) != nil {
		return ""
	}
	scheme, defaultPort := "http", 80
	if settings.TLSEnabled {
		scheme, defaultPort = "https", 443
	}
	host := normalized.PanelDomain
	if settings.ListenPort != defaultPort {
		host = net.JoinHostPort(host, strconv.Itoa(settings.ListenPort))
	}
	return scheme + "://" + host
}

// Saving an endpoint reloads the document so its policy permits a readiness
// probe at exactly that saved address. Authenticated APIs remain same-origin.
func (a *App) panelRestartSecurityPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/index.html") {
			if settings, err := a.db.PanelSettings(); err == nil {
				if target := a.panelRestartAccessURL(settings, r); target != "" {
					policy := w.Header().Get("Content-Security-Policy")
					w.Header().Set("Content-Security-Policy", strings.Replace(policy, "connect-src 'self'", "connect-src 'self' "+target+"/api/panel-ready", 1))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// This credential-free signal is the only cross-origin panel endpoint. The
// public Host router still rejects a candidate hostname until it is active.
func (a *App) handlePanelReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		a.jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.restartPending.Load() {
		a.jsonResponse(w, http.StatusServiceUnavailable, map[string]bool{"ready": false})
		return
	}
	a.jsonOK(w, map[string]bool{"ready": true})
}
