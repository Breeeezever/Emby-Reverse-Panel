package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newPanelEndpointTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	a.panelHost, a.routeDomain, a.panelListenPort = "panel.example.com", "example.com", 6551
	a.panelBindHost, a.panelBindLoopback, a.panelTLSEnabled = "127.0.0.1", false, true
	a.restartCh = make(chan struct{})
	a.panelCertificates = newPanelCertificateManager(filepath.Join(t.TempDir(), "test.db"), nil)
	if _, _, err := a.db.SavePanelEndpointSettings(a.panelHost, a.routeDomain, a.panelListenPort, true); err != nil {
		t.Fatal(err)
	}
	cert, key := testImportedCertificate(t, []string{"*.example.com"}, 1, time.Now().Add(90*24*time.Hour))
	if err := importPanelCertificate(a.db, a.panelCertificates, "upload", "", "", cert, key, true); err != nil {
		t.Fatal(err)
	}
	return a
}

func panelEndpointRequest(t *testing.T, handler http.HandlerFunc, host string, port int) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(panelSettingsRequest{PanelDomain: host, WildcardDomain: "*.example.com", ListenPort: port})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodPost, "https://panel.example.com:6551/api/panel-settings", bytes.NewReader(body)))
	return response
}

func panelValidationFromResponse(t *testing.T, response *httptest.ResponseRecorder) panelEndpointValidation {
	t.Helper()
	var result panelEndpointValidation
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPanelEndpointRejectsCloudflarePortBeforeSaving(t *testing.T) {
	for name, addresses := range map[string][]string{
		"IPv4":                   {"104.21.2.150"},
		"IPv6":                   {"2606:4700:3033::ac43:8154"},
		"mixed direct and proxy": {"192.0.2.10", "2606:4700:3033::ac43:8154"},
	} {
		t.Run(name, func(t *testing.T) {
			a := newPanelEndpointTestApp(t)
			a.panelDNSResolver = dynamicIPResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
				var answers []net.IPAddr
				for _, address := range addresses {
					answers = append(answers, net.IPAddr{IP: net.ParseIP(address)})
				}
				return answers, nil
			})
			before, _ := a.db.PanelSettings()
			preview := panelEndpointRequest(t, a.handlePanelSettingsCheck, "other.example.com", 6551)
			validation := panelValidationFromResponse(t, preview)
			if preview.Code != http.StatusOK || validation.CanSave || validation.CanRestart {
				t.Fatalf("unsafe preview: %d %s", preview.Code, preview.Body.String())
			}
			response := panelEndpointRequest(t, a.handlePanelSettings, "other.example.com", 6551)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Cloudflare") || !strings.Contains(response.Body.String(), "6551") || !strings.Contains(response.Body.String(), "仅 DNS") {
				t.Fatalf("unsupported proxy save: %d %s", response.Code, response.Body.String())
			}
			after, _ := a.db.PanelSettings()
			if after != before || a.restartPending.Load() {
				t.Fatal("rejected candidate mutated configuration or scheduled restart")
			}
		})
	}
}

func TestPanelEndpointRechecksDNSOnSaveAndRestart(t *testing.T) {
	a := newPanelEndpointTestApp(t)
	preview := panelEndpointRequest(t, a.handlePanelSettingsCheck, "next.example.com", 6551)
	if !panelValidationFromResponse(t, preview).CanRestart {
		t.Fatal(preview.Body.String())
	}
	before, _ := a.db.PanelSettings()
	cloudflare := dynamicIPResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("172.67.129.84")}}, nil
	})
	a.panelDNSResolver = cloudflare
	if response := panelEndpointRequest(t, a.handlePanelSettings, "next.example.com", 6551); response.Code != http.StatusConflict {
		t.Fatalf("save trusted stale preview: %d %s", response.Code, response.Body.String())
	}
	after, _ := a.db.PanelSettings()
	if after != before {
		t.Fatal("failed save changed settings")
	}
	// A record can turn orange-cloud after a successful save, so restart must
	// inspect it again even when the domain and listener are unchanged.
	response := httptest.NewRecorder()
	a.handleSystemRestart(response, httptest.NewRequest(http.MethodPost, "/api/system/restart", nil))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Cloudflare") || a.restartPending.Load() {
		t.Fatalf("restart accepted changed DNS: %d %s", response.Code, response.Body.String())
	}
}

func TestPanelEndpointDirectDNSCanSaveAndApply(t *testing.T) {
	for _, address := range []string{"192.0.2.20", "2001:db8::20"} {
		t.Run(address, func(t *testing.T) {
			a := newPanelEndpointTestApp(t)
			a.panelDNSResolver = dynamicIPResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
			})
			preview := panelEndpointRequest(t, a.handlePanelSettingsCheck, "next.example.com", 6551)
			v := panelValidationFromResponse(t, preview)
			if !v.CanSave || !v.CanRestart {
				t.Fatalf("valid endpoint rejected: %s", preview.Body.String())
			}
			if response := panelEndpointRequest(t, a.handlePanelSettings, "next.example.com", 6551); response.Code != http.StatusOK {
				t.Fatalf("save failed: %d %s", response.Code, response.Body.String())
			}
			if a.panelHost != "panel.example.com" || a.restartPending.Load() {
				t.Fatal("saving unexpectedly changed the active listener")
			}
			response := httptest.NewRecorder()
			a.handleSystemRestart(response, httptest.NewRequest(http.MethodPost, "/api/system/restart", nil))
			if response.Code != http.StatusOK || !a.restartPending.Load() || !strings.Contains(response.Body.String(), "https://next.example.com:6551") {
				t.Fatalf("restart failed: %d %s", response.Code, response.Body.String())
			}
			before, _ := a.db.PanelSettings()
			lateSave := panelEndpointRequest(t, a.handlePanelSettings, "late.example.com", 6551)
			after, _ := a.db.PanelSettings()
			if lateSave.Code != http.StatusConflict || after != before {
				t.Fatal("save changed settings after restart validation completed")
			}
		})
	}
}

func TestPanelEndpointDNSFailureNeverPersistsCandidate(t *testing.T) {
	for name, resolve := range map[string]dynamicIPResolverFunc{
		"NXDOMAIN": func(context.Context, string) ([]net.IPAddr, error) {
			return nil, &net.DNSError{IsNotFound: true, Err: "not found"}
		},
		"timeout": func(context.Context, string) ([]net.IPAddr, error) {
			return nil, &net.DNSError{IsTimeout: true, Err: "timeout"}
		},
		"empty":           func(context.Context, string) ([]net.IPAddr, error) { return nil, nil },
		"invalid address": func(context.Context, string) ([]net.IPAddr, error) { return []net.IPAddr{{IP: net.IPv4zero}}, nil },
		"cancellation": func(ctx context.Context, _ string) ([]net.IPAddr, error) {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("DNS lookup is not bounded")
			}
			return nil, context.Canceled
		},
	} {
		t.Run(name, func(t *testing.T) {
			a := newPanelEndpointTestApp(t)
			a.panelDNSResolver = resolve
			before, _ := a.db.PanelSettings()
			response := panelEndpointRequest(t, a.handlePanelSettings, "unknown.example.com", 6551)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "dns_unavailable") {
				t.Fatalf("DNS failure accepted: %d %s", response.Code, response.Body.String())
			}
			after, _ := a.db.PanelSettings()
			if after != before {
				t.Fatal("DNS failure changed settings")
			}
		})
	}
}

func TestPanelEndpointPreviewIncludesReservedPortsAndHostConflicts(t *testing.T) {
	for _, conflict := range []string{"site_port", "site_host", "browser_port"} {
		t.Run(conflict, func(t *testing.T) {
			a := newPanelEndpointTestApp(t)
			port, host := 6551, "next.example.com"
			if conflict == "site_port" {
				// An inactive site still owns its configured port.
				_, err := a.db.db.Exec(`INSERT INTO sites(name,listen_port,ingress_mode,target_url,enabled) VALUES('reserved',6551,'port','http://192.0.2.1',0)`)
				if err != nil {
					t.Fatal(err)
				}
			} else if conflict == "site_host" {
				_, err := a.db.db.Exec(`INSERT INTO sites(name,listen_port,public_host,ingress_mode,target_url,enabled) VALUES('reserved',19001,'next.example.com','host','http://192.0.2.1',0)`)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				a.panelListenPort, port = 6000, 6000
			}
			preview := panelEndpointRequest(t, a.handlePanelSettingsCheck, host, port)
			if panelValidationFromResponse(t, preview).CanSave {
				t.Fatalf("conflict preview accepted: %s", preview.Body.String())
			}
			if response := panelEndpointRequest(t, a.handlePanelSettings, host, port); response.Code != http.StatusConflict {
				t.Fatalf("conflict save accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPanelEndpointExternalProxyUsesPublicPortAndRejectsNativeTLS(t *testing.T) {
	a := newPanelEndpointTestApp(t)
	a.panelBindLoopback, a.panelTLSEnabled, a.panelListenPort = true, false, 19095
	settings, _, err := a.db.SavePanelEndpointSettings(a.panelHost, a.routeDomain, 19095, false)
	if err != nil {
		t.Fatal(err)
	}
	a.panelDNSResolver = dynamicIPResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("104.21.2.150")}}, nil
	})
	r := httptest.NewRequest(http.MethodPost, "https://panel.example.com:8443/api/panel-settings/check", nil)
	v := a.validatePanelEndpoint(r.Context(), settings, settings, r)
	if !v.CanSave || !v.CanRestart || v.TargetURL != "https://panel.example.com:8443" {
		t.Fatalf("validated backend port as public endpoint: %+v", v)
	}
	for _, handler := range []http.HandlerFunc{a.handlePanelCertificateImport, a.handlePanelCertificateIssue} {
		response := httptest.NewRecorder()
		handler(response, httptest.NewRequest(http.MethodPost, "https://panel.example.com:8443/api/panel-certificate", strings.NewReader(`{}`)))
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "外部 HTTPS 反向代理") {
			t.Fatalf("native TLS bypass: %d %s", response.Code, response.Body.String())
		}
		after, _ := a.db.PanelSettings()
		if after != settings {
			t.Fatal("rejected certificate operation changed settings")
		}
	}
}

func TestPanelEndpointCertificateMismatchAllowsStagingButNotRestart(t *testing.T) {
	a := newPanelEndpointTestApp(t)
	settings, _ := a.db.PanelSettings()
	candidate := settings
	candidate.PanelDomain, candidate.RouteDomain = "panel.other.example", "other.example"
	v := a.validatePanelEndpoint(context.Background(), settings, candidate, nil)
	if !v.CanSave || v.CanRestart {
		t.Fatalf("certificate prerequisite not distinguished from save: %+v", v)
	}
	if _, _, err := a.db.SavePanelEndpointSettings(candidate.PanelDomain, candidate.RouteDomain, candidate.ListenPort, true); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	a.handleSystemRestart(response, httptest.NewRequest(http.MethodPost, "/api/system/restart", nil))
	if response.Code != http.StatusConflict || a.restartPending.Load() || !strings.Contains(response.Body.String(), "证书") {
		t.Fatalf("restart accepted mismatching certificate: %d %s", response.Code, response.Body.String())
	}
}

func TestPanelEndpointCloudflareHTTPSetupCanStageHTTPSPort(t *testing.T) {
	a := newPanelEndpointTestApp(t)
	a.panelTLSEnabled, a.panelListenPort = false, 8443
	settings, _, err := a.db.SavePanelEndpointSettings(a.panelHost, a.routeDomain, 8443, false)
	if err != nil {
		t.Fatal(err)
	}
	a.panelDNSResolver = dynamicIPResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("104.21.2.150")}}, nil
	})
	v := a.validatePanelEndpoint(context.Background(), settings, settings, nil)
	if !v.CanSave || v.CanRestart {
		t.Fatalf("HTTP setup state: %+v", v)
	}
	settings.TLSEnabled = true
	v = a.validatePanelEndpoint(context.Background(), settings, settings, nil)
	if !v.CanSave || !v.CanRestart {
		t.Fatalf("HTTPS setup state: %+v", v)
	}
}
