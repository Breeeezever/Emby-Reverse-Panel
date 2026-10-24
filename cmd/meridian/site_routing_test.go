package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSubdomainAndPathIngressUseSharedHTTPSListener(t *testing.T) {
	app := newTestApp(t)
	app.panelHost, app.routeDomain = "media.example.test", "media.example.test"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.URL.RequestURI())
	}))
	defer upstream.Close()
	certPEM, keyPEM := testImportedCertificate(t, []string{"media.example.test", "*.media.example.test"}, 721, time.Now().Add(time.Hour))
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("could not trust isolated test certificate")
	}

	// Occupy the database's internal site port: neither shared mode should
	// attempt to bind it or tell clients to connect to it.
	reserved, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer reserved.Close()
	port := reserved.Addr().(*net.TCPAddr).Port
	for i, mode := range []string{ingressModeHost, ingressModePath} {
		site := Site{ID: int64(720 + i), Name: mode, ListenPort: port, IngressMode: mode,
			PublicHost: "cinema.media.example.test", PathPrefix: "/cinema", TargetURL: upstream.URL,
			PlaybackMode: "direct", StreamHosts: "[]", UAMode: passthroughUAMode}
		if mode == ingressModeHost {
			site.PathPrefix = ""
		} else {
			site.PublicHost = ""
		}
		if err := app.pm.StartSite(site); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = app.pm.StopSite(site.ID) })
	}
	shared := httptest.NewUnstartedServer(app.publicHostRouter(http.NotFoundHandler()))
	shared.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	shared.StartTLS()
	defer shared.Close()
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, shared.Listener.Addr().String())
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for _, hostPath := range []string{"cinema.media.example.test/System/Info/Public", "media.example.test/cinema/System/Info/Public"} {
		response, err := client.Get("https://" + hostPath)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "/System/Info/Public" {
			t.Fatalf("%s: status=%d body=%s err=%v", hostPath, response.StatusCode, body, err)
		}
	}
}

func TestStripIngressPathPrefixNormalizesOnlyLiteralBoundarySlashes(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{"/watch/System/Info/Public", "/System/Info/Public"},
		{"/watch//System/Info/Public", "/System/Info/Public"},
		{"/watch///emby/videos/1/original.mp4", "/emby/videos/1/original.mp4"},
		{"/watch////", "/"},
		{"/watch", "/"},
		{"/watch//media/a//b.mkv", "/media/a//b.mkv"},
		{"/watch//media/a%2Fb.mkv", "/media/a%2Fb.mkv"},
		{"/watch/%2Fmedia/a.mkv", "/%2Fmedia/a.mkv"},
		{"/watch//%2Fmedia/a.mkv", "/%2Fmedia/a.mkv"},
		{"/watch-other//System/Info/Public", "/watch-other//System/Info/Public"},
	} {
		t.Run(test.path, func(t *testing.T) {
			const query = "?api_key=client-visible&signature=a%2fb%2B&x=1&x=2"
			u, err := url.Parse("https://panel.example" + test.path + query)
			if err != nil {
				t.Fatal(err)
			}
			stripIngressPathPrefix(u, "/watch")
			if got := u.RequestURI(); got != test.want+query {
				t.Fatalf("stripped request URI = %q, want %q", got, test.want+query)
			}
			if u.Scheme != "https" || u.Host != "panel.example" {
				t.Fatalf("ingress stripping changed authority: %s", u)
			}
		})
	}
}

func TestPathIngressClientTrailingSlashWorksForAPIAndVideo(t *testing.T) {
	for _, mode := range []string{mainVideoStreamModeProxy, mainVideoStreamModeDirect} {
		t.Run(mode, func(t *testing.T) {
			app := newTestApp(t)
			upstreamPaths := make(chan string, 3)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamPaths <- r.URL.RequestURI()
				if mode == mainVideoStreamModeDirect && strings.Contains(r.URL.Path, "/videos/") && r.Header.Get("Range") != "bytes=0-1" {
					t.Errorf("direct video probe Range = %q", r.Header.Get("Range"))
				}
				_, _ = io.WriteString(w, "upstream response")
			}))
			defer upstream.Close()
			site := Site{
				ID: 710, Name: "client trailing slash", ListenPort: freePort(t),
				PathPrefix: "/watch", IngressMode: ingressModePath, TargetURL: upstream.URL + "/base",
				PlaybackMode: "direct", MainVideoStreamMode: mode, StreamHosts: "[]", UAMode: passthroughUAMode,
			}
			if err := app.pm.StartSite(site); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = app.pm.StopSite(site.ID) })
			router := app.publicHostRouter(http.NotFoundHandler())
			for _, test := range []struct {
				path string
				want string
			}{
				{"/watch//System/Info/Public", "/base/System/Info/Public"},
				{"/watch///emby/System/Info/Public", "/base/emby/System/Info/Public"},
				{"/watch//emby/watch/videos/42/original.mp4?api_key=client", "/base/emby/videos/42/original.mp4?api_key=client"},
			} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://panel.example"+test.path, nil))
				if got := <-upstreamPaths; got != test.want {
					t.Fatalf("upstream URI = %q, want %q", got, test.want)
				}
				if mode == mainVideoStreamModeDirect && strings.Contains(test.want, "/videos/") {
					if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != upstream.URL+test.want || w.Body.Len() != 0 {
						t.Fatalf("direct video response = %d, Location %q, body %q", w.Code, w.Header().Get("Location"), w.Body.String())
					}
				} else if w.Code != http.StatusOK || w.Body.String() != "upstream response" {
					t.Fatalf("proxied response = %d, body %q", w.Code, w.Body.String())
				}
			}
		})
	}
}
