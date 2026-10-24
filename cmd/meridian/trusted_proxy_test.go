package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTrustedProxyDefaultsAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		trusted []string
		denied  []string
	}{
		{"empty", "", []string{"127.0.0.1", "::1", "::ffff:127.0.0.1"}, []string{"127.0.0.2", "172.17.0.1", "192.168.1.1", "198.51.100.1"}},
		{"whitespace", " \t\n", []string{"127.0.0.1", "::1"}, []string{"10.0.0.1"}},
		{"disabled", "none", nil, []string{"127.0.0.1", "::1", "198.51.100.1"}},
		{"disabled case insensitive", " NoNe ", nil, []string{"127.0.0.1", "::1"}},
		{"custom replaces defaults", "192.0.2.8/32", []string{"192.0.2.8"}, []string{"127.0.0.1", "::1", "192.0.2.9"}},
		{"custom and local", "192.0.2.8/32, 127.0.0.1/32, ::1/128", []string{"192.0.2.8", "127.0.0.1", "::1"}, []string{"192.0.2.9", "127.0.0.2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trusted, err := parseTrustedProxyCIDRs(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			for _, ip := range tc.trusted {
				if !isTrustedProxy(net.ParseIP(ip), trusted) {
					t.Errorf("%s should be trusted", ip)
				}
			}
			for _, ip := range tc.denied {
				if isTrustedProxy(net.ParseIP(ip), trusted) {
					t.Errorf("%s should not be trusted", ip)
				}
			}
		})
	}
	for _, value := range []string{"none,127.0.0.1/32", "127.0.0.1/32,none", "invalid", "127.0.0.1"} {
		if _, err := parseTrustedProxyCIDRs(value); err == nil {
			t.Errorf("invalid setting %q accepted", value)
		}
	}
}

func TestDefaultProxyClientIdentityAndForwarding(t *testing.T) {
	target, err := url.Parse("http://upstream.example/socket")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		setting   string
		peer      string
		realIP    []string
		wantIP    string
		wantHTTPS bool
	}{
		{"same host nginx", "", "127.0.0.1:50000", []string{"198.51.100.42"}, "198.51.100.42", true},
		{"same host ipv6 nginx", "", "[::1]:50000", []string{"2001:db8::42"}, "2001:db8::42", true},
		{"direct client spoof", "", "198.51.100.7:50000", []string{"203.0.113.9"}, "198.51.100.7", false},
		{"unconfigured container spoof", "", "172.17.0.1:50000", []string{"203.0.113.9"}, "172.17.0.1", false},
		{"other loopback spoof", "", "127.0.0.2:50000", []string{"203.0.113.9"}, "127.0.0.2", false},
		{"disabled local trust", "none", "127.0.0.1:50000", []string{"203.0.113.9"}, "127.0.0.1", false},
		{"custom excludes local", "192.0.2.8/32", "127.0.0.1:50000", []string{"203.0.113.9"}, "127.0.0.1", false},
		{"custom proxy", "192.0.2.8/32", "192.0.2.8:50000", []string{"198.51.100.42"}, "198.51.100.42", true},
		{"missing real ip ignores xff", "", "127.0.0.1:50000", nil, "127.0.0.1", true},
		{"invalid real ip ignores xff", "", "127.0.0.1:50000", []string{"invalid"}, "127.0.0.1", true},
		{"multiple real ip values", "", "127.0.0.1:50000", []string{"198.51.100.42", "203.0.113.9"}, "127.0.0.1", true},
		{"real ip chain", "", "127.0.0.1:50000", []string{"203.0.113.9, 198.51.100.42"}, "127.0.0.1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trusted, err := parseTrustedProxyCIDRs(tc.setting)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://panel.example/socket", nil)
			req.RemoteAddr = tc.peer
			for _, value := range tc.realIP {
				req.Header.Add("X-Real-IP", value)
			}
			req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
			req.Header.Set("X-Forwarded-Proto", "https")
			if got := requestClientKey(req, trusted); got != tc.wantIP {
				t.Fatalf("request log / login limiter identity = %q, want %q", got, tc.wantIP)
			}
			if got := requestIsHTTPS(req, trusted); got != tc.wantHTTPS {
				t.Errorf("HTTPS = %v, want %v", got, tc.wantHTTPS)
			}
			header := req.Header.Clone()
			prepareUpstreamHeaders(header, req, UAHeaderPolicy{}, trusted)
			wsHeader := prepareWebSocketUpstreamHeadersWithTrustedProxies(req, target, UAHeaderPolicy{}, trusted)
			wantProto := "http"
			if tc.wantHTTPS {
				wantProto = "https"
			}
			for kind, h := range map[string]http.Header{"http": header, "websocket": wsHeader} {
				for key, want := range map[string]string{"X-Real-IP": tc.wantIP, "X-Forwarded-For": tc.wantIP, "X-Forwarded-Proto": wantProto} {
					if got := h.Get(key); got != want {
						t.Errorf("%s %s = %q, want %q", kind, key, got, want)
					}
				}
			}
		})
	}
}
