package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExtremePresetIncludesAllSupportedDiscovery(t *testing.T) {
	site := Site{DynamicDiscoveryEnabled: true, DynamicProfile: dynamicProfileExtreme,
		DynamicDiscoverySources: []string{dynamicDiscoverySourceRedirect}, DynamicAllowHTTPSDowngrade: false}
	policy, err := newDynamicRedirectPolicy(site, true)
	if err != nil {
		t.Fatal(err)
	}
	if !policy.allowHTTPSDowngrade || !dynamicDiscoverySourcesEqual(policy.sources, allDynamicDiscoverySources()) {
		t.Fatalf("Extreme runtime retained old opt-outs: %#v", policy)
	}
	if err := normalizeDynamicSitePolicy(&site); err != nil {
		t.Fatal(err)
	}
	if !site.DynamicAllowHTTPSDowngrade || !dynamicDiscoverySourcesEqual(site.DynamicDiscoverySources, allDynamicDiscoverySources()) {
		t.Fatal("saved Extreme policy retained opt-outs")
	}
	legacy := Site{DynamicProfile: dynamicProfileExtreme, DynamicPolicyRevision: 1,
		StoredDynamicDiscoverySources: `["redirect","hls","dash"]`, StoredDynamicDomainRules: `[]`}
	if err := hydrateStoredDynamicSitePolicy(&legacy, 1, 0); err != nil {
		t.Fatal(err)
	}
	if !legacy.DynamicAllowHTTPSDowngrade || !dynamicDiscoverySourcesEqual(legacy.DynamicDiscoverySources, allDynamicDiscoverySources()) {
		t.Fatal("legacy Extreme site did not expose the effective preset")
	}
}

func TestExtremeRewritesTenThousandHLSSegments(t *testing.T) {
	const segments = 10000
	issuer := newStructuredDiscoveryTestIssuerForProfile(t, dynamicProfileExtreme)
	var manifest strings.Builder
	manifest.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-DEFINE:NAME=\"sig\",VALUE=\"fixture%2Bsignature\"\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n")
	for index := 0; index < segments; index++ {
		fmt.Fprintf(&manifest, "#EXTINF:2,\n%d.ts?sig={$sig}\n", index)
	}
	manifest.WriteString("#EXT-X-ENDLIST\n")
	request := httptest.NewRequest(http.MethodGet, "https://cdn.example.com/movie/index.m3u8", nil)
	response := &http.Response{StatusCode: http.StatusOK, Request: request,
		Header: http.Header{"Content-Type": {"application/vnd.apple.mpegurl"}},
		Body:   io.NopCloser(strings.NewReader(manifest.String())), ContentLength: int64(manifest.Len())}
	if err := rewriteDynamicStructuredResponseExpected(response, issuer, true, dynamicDiscoverySourceHLS, 0, false); err != nil {
		t.Fatalf("long Extreme playlist rejected: %v", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	routes := structuredCapabilityRoutes(string(payload))
	if len(routes) != segments+1 || !strings.Contains(string(payload), "#EXT-X-ENDLIST") {
		t.Fatalf("rewritten routes=%d, want %d including key", len(routes), segments+1)
	}
	for _, index := range []int{0, 1, segments} {
		claims, err := openDynamicCapability(issuer.key, capabilityTokenFromRoute(t, routes[index]))
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("https://cdn.example.com/movie/%d.ts?sig=fixture%%2Bsignature", index-1)
		if index == 0 {
			want = "https://cdn.example.com/movie/key.bin"
		}
		if claims.Target != want {
			t.Fatalf("route %d lost its URL or signature", index)
		}
	}
}

func TestExtremeSmallManifestsCanParseConcurrently(t *testing.T) {
	issuer := newStructuredDiscoveryTestIssuerForProfile(t, dynamicProfileExtreme)
	payload := "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nsegment.ts\n#EXT-X-ENDLIST\n"
	makeResponse := func(body io.ReadCloser) *http.Response {
		return &http.Response{StatusCode: 200, Request: httptest.NewRequest(http.MethodGet, "https://cdn.example.com/movie/index.m3u8", nil),
			Header: http.Header{"Content-Type": {"application/vnd.apple.mpegurl"}}, Body: body, ContentLength: int64(len(payload))}
	}
	reader, writer := io.Pipe()
	first := makeResponse(reader)
	done := make(chan error, 1)
	go func() {
		done <- rewriteDynamicStructuredResponseExpected(first, issuer, true, dynamicDiscoverySourceHLS, 0, false)
	}()
	defer func() {
		_ = writer.Close()
		_ = first.Body.Close()
	}()
	deadline := time.Now().Add(time.Second)
	for issuer.state.availableParseMemory() == globalDynamicMaxSiteParseMemoryBytes {
		if time.Now().After(deadline) {
			t.Fatal("first manifest did not begin reading")
		}
		time.Sleep(time.Millisecond)
	}
	second := makeResponse(io.NopCloser(strings.NewReader(payload)))
	if err := rewriteDynamicStructuredResponseExpected(second, issuer, true, dynamicDiscoverySourceHLS, 0, false); err != nil {
		_ = writer.Close()
		<-done
		t.Fatalf("small concurrent manifest rejected: %v", err)
	}
	_ = second.Body.Close()
	if _, err := io.WriteString(writer, payload); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if remaining := issuer.state.availableParseMemory(); remaining != globalDynamicMaxSiteParseMemoryBytes {
		t.Fatalf("parse memory leaked: available=%d", remaining)
	}
}

func TestExtremeParseMemoryGrowthRespectsSharedBudget(t *testing.T) {
	issuer := newStructuredDiscoveryTestIssuerForProfile(t, dynamicProfileExtreme)
	release, grow, ok := issuer.state.acquireParseBudget(8 << 20)
	if !ok {
		t.Fatal("initial parse reservation failed")
	}
	defer release()
	otherRelease, ok := issuer.state.acquireParse(8 << 20)
	if !ok {
		t.Fatal("concurrent parse reservation failed")
	}
	defer otherRelease()
	if !grow(56<<20) || grow(57<<20) {
		t.Fatal("growth did not honor the shared site memory budget")
	}
	release()
	if grow(8 << 20) {
		t.Fatal("released reservation grew again")
	}
	otherRelease()
	if issuer.state.availableParseMemory() != globalDynamicMaxSiteParseMemoryBytes {
		t.Fatal("parse memory leaked after growth/release")
	}
}

func TestExtremeRewritesFiveThousandDASHTemplates(t *testing.T) {
	issuer := newStructuredDiscoveryTestIssuerForProfile(t, dynamicProfileExtreme)
	var manifest strings.Builder
	manifest.WriteString(`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011"><Period><AdaptationSet>`)
	for index := 0; index < 5000; index++ {
		fmt.Fprintf(&manifest, `<Representation id="v%d"><SegmentTemplate media="chunk-%d-$Number$.m4s"/></Representation>`, index, index)
	}
	manifest.WriteString(`</AdaptationSet></Period></MPD>`)
	request := httptest.NewRequest(http.MethodGet, "https://cdn.example.com/movie/index.mpd", nil)
	response := &http.Response{StatusCode: http.StatusOK, Request: request,
		Header: http.Header{"Content-Type": {"application/dash+xml"}},
		Body:   io.NopCloser(strings.NewReader(manifest.String())), ContentLength: int64(manifest.Len())}
	if err := rewriteDynamicStructuredResponseExpected(response, issuer, true, dynamicDiscoverySourceDASH, 0, false); err != nil {
		t.Fatalf("long DASH manifest rejected: %v", err)
	}
	defer response.Body.Close()
	if len(issuer.state.capabilities) != 5000 {
		t.Fatalf("DASH capability count=%d, want 5000", len(issuer.state.capabilities))
	}
}

func TestExtremeFollowsLongRedirectChains(t *testing.T) {
	const hops = 24
	for _, mode := range []string{"same authority", "configured authorities", "dynamic authority"} {
		t.Run(mode, func(t *testing.T) {
			policy := redirectRuntimePolicy(dynamicProfileExtreme, true)
			_, state := redirectRuntimeState(t, policy.limits, dynamicIPResolverFunc(func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
			}))
			defer state.close()
			calls := 0
			transport := &redirectFollowTransport{dynamicPolicy: policy, dynamicState: state,
				followUnknownRedirects: true, configuredAuthorities: map[string]bool{"http://origin.example.com": true, "http://other.example.com": true},
				playbackHosts: map[string]bool{"http://origin.example.com": true, "http://other.example.com": true}}
			transport.base = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls > hops {
					return redirectRuntimeResponse(req, 200, nil, io.NopCloser(strings.NewReader("video"))), nil
				}
				host := "origin.example.com"
				if mode == "configured authorities" && calls%2 != 0 {
					host = "other.example.com"
				} else if mode == "dynamic authority" {
					host = "cdn.example.com"
				}
				return redirectRuntimeResponse(req, 302, []string{fmt.Sprintf("http://%s/Videos/1/hop-%d", host, calls)}, nil), nil
			})
			captures := make(chan redirectRuntimeDialCapture, hops+1)
			transport.dynamicTransportFactory = redirectRuntimeFactory(captures, func(req *http.Request) string {
				calls++
				if calls > hops {
					return "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nvideo"
				}
				return fmt.Sprintf("HTTP/1.1 302 Found\r\nLocation: http://cdn.example.com/Videos/1/hop-%d\r\nContent-Length: 0\r\n\r\n", calls)
			})
			resp, err := transport.RoundTrip(redirectRuntimeEligibleRequest(http.MethodGet, "http://origin.example.com/Videos/1/start"))
			if err != nil {
				t.Fatalf("%s chain: %v", mode, err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || string(body) != "video" || calls != hops+1 {
				t.Fatalf("status=%d body=%q err=%v calls=%d", resp.StatusCode, body, err, calls)
			}
		})
	}
}

func TestExtremeRedirectDeadlineIsTotal(t *testing.T) {
	policy := redirectRuntimePolicy(dynamicProfileExtreme, true)
	calls := 0
	transport := &redirectFollowTransport{dynamicPolicy: policy, redirectTimeout: 60 * time.Millisecond,
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if calls > 1 {
				select {
				case <-req.Context().Done():
					return nil, req.Context().Err()
				case <-time.After(25 * time.Millisecond):
				}
			}
			return redirectRuntimeResponse(req, 302, []string{fmt.Sprintf("/Videos/1/hop-%d", calls)}, nil), nil
		})}
	start := time.Now()
	resp, err := transport.RoundTrip(redirectRuntimeEligibleRequest(http.MethodGet, "http://origin.example.com/Videos/1/start"))
	failure := redirectRuntimeAssertError(t, err, dynamicObservationReasonRedirectTimeout)
	if resp != nil || failure.statusCode != http.StatusGatewayTimeout || calls < 2 || time.Since(start) > time.Second {
		t.Fatalf("total timeout: resp=%v status=%d calls=%d elapsed=%s", resp, failure.statusCode, calls, time.Since(start))
	}
}

func TestExtremeRedirectBudgetEndsBeforeStreamingAndParsing(t *testing.T) {
	issuer := newStructuredDiscoveryTestIssuerForProfile(t, dynamicProfileExtreme)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	var outbound context.Context
	transport := &redirectFollowTransport{dynamicPolicy: issuer.policy, redirectTimeout: 20 * time.Millisecond,
		base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/final.m3u8" {
				return redirectRuntimeResponse(req, 302, []string{"/final.m3u8"}, nil), nil
			}
			outbound = req.Context()
			response := redirectRuntimeResponse(req, 200, nil, io.NopCloser(strings.NewReader("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nsegment.ts\n#EXT-X-ENDLIST\n")))
			response.Header.Set("Content-Type", "application/vnd.apple.mpegurl")
			response.ContentLength = -1
			return response, nil
		})}
	req := redirectRuntimeEligibleRequest(http.MethodGet, "https://origin.example.com/Videos/1/start")
	req = req.WithContext(context.WithValue(parent, dynamicRequestEligibleContextKey{}, true))
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	time.Sleep(40 * time.Millisecond)
	if outbound.Err() != nil {
		t.Fatal("redirect timer canceled the final stream")
	}
	if err := rewriteDynamicStructuredResponseExpected(resp, issuer, true, dynamicDiscoverySourceHLS, 0, false); err != nil {
		t.Fatalf("closing the upstream body canceled manifest parsing: %v", err)
	}
	if context.Cause(resp.Request.Context()) != nil || outbound.Err() == nil {
		t.Fatal("body cleanup did not release transport context independently of parsing")
	}
	cancel()
	if resp.Request.Context().Err() == nil {
		t.Fatal("completed redirect detached client cancellation")
	}
}

func TestExtremeRedirectCyclesAndHistoryBudget(t *testing.T) {
	for _, mode := range []string{"cycle", "history memory"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			transport := &redirectFollowTransport{dynamicPolicy: redirectRuntimePolicy(dynamicProfileExtreme, true),
				configuredAuthorities: map[string]bool{"http://origin.example.com": true, "http://other.example.com": true},
				playbackHosts:         map[string]bool{"http://origin.example.com": true, "http://other.example.com": true},
				base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls++
					if calls > 1000 {
						t.Fatal("redirect history consumed an unbounded working set")
					}
					target := fmt.Sprintf("http://origin.example.com/Videos/1/%d?sig=%s", calls, strings.Repeat("x", 2048))
					if mode == "cycle" {
						target = "http://other.example.com/Videos/1/start"
						if calls%2 == 0 {
							target = "http://origin.example.com/Videos/1/start"
						}
					}
					return redirectRuntimeResponse(req, 302, []string{target}, nil), nil
				})}
			resp, err := transport.RoundTrip(redirectRuntimeEligibleRequest(http.MethodGet, "http://origin.example.com/Videos/1/start"))
			want := dynamicObservationReasonRedirectLoop
			if mode == "history memory" {
				want = dynamicObservationReasonCapacityLimit
			}
			redirectRuntimeAssertError(t, err, want)
			if resp != nil {
				t.Fatal("rejected chain returned a response")
			}
		})
	}
}

func TestExtremeTimeoutObservationMigratesAndPreservesHistory(t *testing.T) {
	db := newTestApp(t).db
	site, err := db.CreateSite("deadline", freePort(t), "https://origin.example.com", "", "direct", "[]", "infuse", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Model an installed database before redirect_timeout was introduced.
	if _, err := db.db.Exec("DROP TABLE dynamic_observations;" + dynamicObservationPreTimeoutTableDDL + dynamicObservationIndexesDDL); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if _, err := db.db.Exec(`INSERT INTO dynamic_observations VALUES (?, 'https://origin.example.com:443', 'redirect', 'configured', 'response', 'denied', 'response_failure', 0, ?, ?, 7)`, site.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("upgrade installed schema: %v", err)
	}
	db.EnqueueDynamicObservation(dynamicObservationEvent{SiteID: site.ID, CanonicalAuthority: "https://origin.example.com:443",
		Source: dynamicObservationSourceRedirect, TargetKind: dynamicObservationTargetConfigured,
		Decision: dynamicObservationDecisionDenied, ReasonCode: dynamicObservationReasonRedirectTimeout})
	rows, err := db.ListDynamicObservations(site.ID)
	if err != nil || len(rows) != 2 || db.DroppedDynamicObservations() != 0 {
		t.Fatalf("timeout observation unavailable after migration: rows=%+v err=%v", rows, err)
	}
	for _, row := range rows {
		if row.ReasonCode == dynamicObservationReasonResponseFailure && row.Count != 7 {
			t.Fatal("migration changed existing observation counts")
		}
	}
	if err := db.migrate(); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	rows, err = db.ListDynamicObservations(site.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("repeat migration lost history: rows=%+v err=%v", rows, err)
	}
}
