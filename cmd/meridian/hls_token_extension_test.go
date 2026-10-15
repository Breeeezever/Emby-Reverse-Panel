package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHLSResolverTokenExtension(t *testing.T) {
	for _, tc := range []struct {
		name, profile, token string
		wantOK               bool
	}{
		{"extreme token", dynamicProfileExtreme, "Zml4dHVyZS10b2tlbg==", true},
		{"extreme URL-safe token", dynamicProfileExtreme, "Fixture_Ab-12", true},
		{"compatible rejects extension", dynamicProfileCompatible, "Fixture", false},
		{"empty", dynamicProfileExtreme, "", false},
		{"absolute URL", dynamicProfileExtreme, "https://cdn.example.com/key", false},
		{"relative URL", dynamicProfileExtreme, "../key.bin", false},
		{"URI attribute", dynamicProfileExtreme, `URI="https://cdn.example.com/key"`, false},
		{"variable", dynamicProfileExtreme, "{$token}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := newStructuredDiscoveryTestIssuerForProfile(t, tc.profile)
			line := "#EXT-X-TOKEN=" + tc.token
			body := "#EXTM3U\n#EXT-X-VERSION:3\n" + line + "\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\n../hls/1/video.m3u8?s=fixture%2Bsignature\n"
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://cdn.example.com/stream/http_resolver/opaque/media/1?s=master", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp := &http.Response{StatusCode: http.StatusOK, Request: req,
				Header: http.Header{"Content-Type": {"application/vnd.apple.mpegurl"}},
				Body:   io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
			err = rewriteDynamicStructuredResponseExpected(resp, issuer, true, dynamicDiscoverySourceHLS, 0, false)
			if !tc.wantOK {
				if err == nil || len(issuer.state.capabilities) != 0 {
					t.Fatalf("unsafe or disabled token extension accepted: err=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolver master playlist rejected: %v", err)
			}
			defer resp.Body.Close()
			payload, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			routes := structuredCapabilityRoutes(string(payload))
			if len(routes) != 1 || !strings.Contains(string(payload), line+"\n") {
				t.Fatal("token changed or child playlist was not proxied")
			}
			claims, err := openDynamicCapability(issuer.key, capabilityTokenFromRoute(t, routes[0]))
			if err != nil {
				t.Fatal(err)
			}
			if claims.Target != "https://cdn.example.com/stream/http_resolver/opaque/hls/1/video.m3u8?s=fixture%2Bsignature" || claims.Kind != dynamicCapabilityKindManifest {
				t.Fatal("child playlist lost its resolver base, signature, or manifest kind")
			}
		})
	}
}
