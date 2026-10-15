package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDomainlessProxyRejectsEndpointAndCertificateChanges(t *testing.T) {
	app := newTestApp(t)
	app.panelListenPort = 19095
	app.panelCertificates = &panelCertificateManager{}
	var err error
	app.trustedProxies, err = parseTrustedProxyCIDRs("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	before, err := app.db.BootstrapPanelSettings("", "", false, 19095)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"panel_domain":"panel.example.com","wildcard_domain":"*.example.com","listen_port":18443}`
	resp := httptest.NewRecorder()
	app.handlePanelSettings(resp, httptest.NewRequest(http.MethodPost, "/api/panel-settings", strings.NewReader(body)))
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "反向代理") {
		t.Fatalf("port change: %d %s", resp.Code, resp.Body.String())
	}
	after, err := app.db.PanelSettings()
	if err != nil || after != before {
		t.Fatalf("rejected port change mutated settings: %+v, %v", after, err)
	}
	// Saving a domain on the existing backend port is still valid. The active
	// process has no panelHost until restart, but must reject native TLS now.
	body = `{"panel_domain":"panel.example.com","wildcard_domain":"*.example.com","listen_port":19095}`
	resp = httptest.NewRecorder()
	app.handlePanelSettings(resp, httptest.NewRequest(http.MethodPost, "/api/panel-settings", strings.NewReader(body)))
	if resp.Code != http.StatusOK {
		t.Fatalf("domain change: %d %s", resp.Code, resp.Body.String())
	}
	before, err = app.db.PanelSettings()
	if err != nil {
		t.Fatal(err)
	}
	resp = httptest.NewRecorder()
	app.handlePanelCertificateIssue(resp, httptest.NewRequest(http.MethodPost, "/api/panel-certificate", strings.NewReader(`{}`)))
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "反向代理") {
		t.Fatalf("certificate issue: %d %s", resp.Code, resp.Body.String())
	}
	after, err = app.db.PanelSettings()
	if err != nil || after != before {
		t.Fatalf("rejected certificate request mutated settings: %+v, %v", after, err)
	}
}

func TestPanelEndpointRejectsOccupiedPortBeforeSaveAndRestart(t *testing.T) {
	app := newTestApp(t)
	app.panelListenPort = 9090
	app.panelBindHost = "127.0.0.1"
	app.panelCertificates = &panelCertificateManager{}
	before, err := app.db.BootstrapPanelSettings("panel.example.com", "example.com", false, 9090)
	if err != nil {
		t.Fatal(err)
	}
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { occupied.Close() })
	port := occupied.Addr().(*net.TCPAddr).Port
	body, err := json.Marshal(panelSettingsRequest{PanelDomain: "panel.example.com", WildcardDomain: "*.example.com", ListenPort: port})
	if err != nil {
		t.Fatal(err)
	}
	resp := httptest.NewRecorder()
	app.handlePanelSettings(resp, httptest.NewRequest(http.MethodPost, "/api/panel-settings", bytes.NewReader(body)))
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "不可用") {
		t.Fatalf("occupied port save: %d %s", resp.Code, resp.Body.String())
	}
	after, err := app.db.PanelSettings()
	if err != nil || after != before {
		t.Fatalf("rejected save mutated settings: %+v, %v", after, err)
	}
	// A previously saved port can be taken by another service before restart.
	if _, _, err := app.db.SavePanelEndpointSettings("panel.example.com", "example.com", port, false); err != nil {
		t.Fatal(err)
	}
	resp = httptest.NewRecorder()
	app.handleSystemRestart(resp, httptest.NewRequest(http.MethodPost, "/api/system/restart", nil))
	if resp.Code != http.StatusConflict || !strings.Contains(resp.Body.String(), "不可用") {
		t.Fatalf("occupied port restart: %d %s", resp.Code, resp.Body.String())
	}
	if err := occupied.Close(); err != nil {
		t.Fatal(err)
	}
	resp = httptest.NewRecorder()
	app.handlePanelSettings(resp, httptest.NewRequest(http.MethodPost, "/api/panel-settings", bytes.NewReader(body)))
	if resp.Code != http.StatusOK {
		t.Fatalf("available port save: %d %s", resp.Code, resp.Body.String())
	}
	// The availability probe must release its sockets so restart can bind.
	available, err := net.Listen("tcp4", occupied.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer available.Close()
	app.panelListenPort = port
	if err := app.validatePanelPortChange(port); err != nil {
		t.Fatalf("unchanged active port should not be probed: %v", err)
	}
}

func TestPanelPortProbeRejectsIPv4ConflictWithIPv6Available(t *testing.T) {
	occupied, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if err := checkPanelListenPort("0.0.0.0", occupied.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("IPv6 availability must not hide the occupied IPv4 port")
	}
}
