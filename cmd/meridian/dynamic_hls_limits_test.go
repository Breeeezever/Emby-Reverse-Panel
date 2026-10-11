package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAutomaticProxyExtremeLongHLSManifest(t *testing.T) {
	// A two-hour VOD can contain far more than Compatible's 1024 URLs.
	// Include an encryption key, which also consumes a URL reservation.
	const segments = 1868
	for _, profile := range []string{dynamicProfileCompatible, dynamicProfileExtreme} {
		t.Run(profile, func(t *testing.T) {
			issuer := newStructuredDiscoveryTestIssuerForProfile(t, profile)
			policy, err := newDynamicRedirectPolicy(Site{
				DynamicDiscoveryEnabled: true,
				DynamicProfile:          profile,
			}, true)
			if err != nil {
				t.Fatal(err)
			}
			issuer.policy = policy
			var manifest strings.Builder
			manifest.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin?sig=key-fixture\"\n")
			for index := 0; index < segments; index++ {
				fmt.Fprintf(&manifest, "#EXTINF:4,\nsegment-%d.ts?sig=fixture%%2Bsignature\n", index)
			}
			manifest.WriteString("#EXT-X-ENDLIST\n")
			base := mustStructuredURL(t, "https://cdn.example.com/movie/index.m3u8")
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base.String(), nil)
			if err != nil {
				t.Fatal(err)
			}
			response := &http.Response{
				StatusCode: http.StatusOK, Request: request,
				Header: http.Header{"Content-Type": {"application/vnd.apple.mpegurl"}},
				Body:   io.NopCloser(strings.NewReader(manifest.String())), ContentLength: int64(manifest.Len()),
			}
			err = rewriteDynamicStructuredResponseExpected(response, issuer, true, dynamicDiscoverySourceHLS, 0, false)
			if profile == dynamicProfileCompatible {
				var failure *dynamicProxyError
				if !errors.As(err, &failure) || failure.reasonCode != dynamicObservationReasonCapacityLimit {
					t.Fatalf("over-limit manifest error = %v; want capacity_limit", err)
				}
				if len(issuer.state.capabilities) != 0 {
					t.Fatal("rejected manifest retained partially issued capabilities")
				}
				return
			}
			if err != nil {
				t.Fatalf("configured Extreme rejected %d segments and a key: %v", segments, err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			routes := structuredCapabilityRoutes(string(body))
			if len(routes) != segments+1 || !bytes.Contains(body, []byte("#EXT-X-ENDLIST")) {
				t.Fatalf("rewritten playlist has %d routes; want %d and ENDLIST", len(routes), segments+1)
			}
			for index, route := range routes {
				claims, err := openDynamicCapability(issuer.key, capabilityTokenFromRoute(t, route))
				if err != nil {
					t.Fatal(err)
				}
				want := fmt.Sprintf("https://cdn.example.com/movie/segment-%d.ts?sig=fixture%%2Bsignature", index-1)
				if index == 0 {
					want = "https://cdn.example.com/movie/key.bin?sig=key-fixture"
				}
				if claims.Target != want {
					t.Fatalf("route %d changed the upstream URL or signature", index)
				}
			}
		})
	}
}
