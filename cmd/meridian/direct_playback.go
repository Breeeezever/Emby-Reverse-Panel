package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Direct playback keeps media URLs outside Meridian, including relative Emby
// URLs that would otherwise lose a shared ingress prefix in some clients.
// Metadata, artwork, authentication and playback-state APIs still use the proxy.
func rewriteSiteStructuredResponse(resp *http.Response, direct bool, issuer *dynamicCapabilityIssuer, rewriteRelative bool, source string) error {
	if !direct {
		return rewriteDynamicStructuredResponseExpected(resp, issuer, rewriteRelative, source, 0, false)
	}
	if source != dynamicDiscoverySourcePlaybackInfo || resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.Body == nil || resp.Request == nil || resp.Request.URL == nil {
		return nil
	}
	const limit = 16 << 20
	var reader io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		decoded, err := gzip.NewReader(resp.Body)
		if err != nil {
			return err
		}
		defer decoded.Close()
		reader = decoded
	} else if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return errors.New("unsupported PlaybackInfo encoding")
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return err
	}
	if len(body) > limit {
		return errors.New("PlaybackInfo response too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return errors.New("invalid PlaybackInfo response")
	}
	base := *resp.Request.URL
	base.RawQuery = ""
	base.Fragment = ""
	base.User = nil
	rewriteDirectPlaybackURLs(payload, &base, 0)
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Uncompressed = true
	for _, header := range []string{"Content-Encoding", "ETag", "Content-MD5", "Content-Range"} {
		resp.Header.Del(header)
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	return nil
}

func rewriteDirectPlaybackURLs(value any, base *url.URL, depth int) {
	if depth > 64 {
		return
	}
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if strings.EqualFold(key, "RequiredHttpHeaders") {
				continue
			}
			if strings.EqualFold(key, "DirectStreamUrl") || strings.EqualFold(key, "TranscodingUrl") {
				if raw, ok := child.(string); ok && raw != "" && !strings.ContainsAny(raw, "\\\r\n") {
					parsed, err := url.Parse(raw)
					if err == nil && parsed.User == nil {
						resolved := base.ResolveReference(parsed)
						if resolved.Scheme == "http" || resolved.Scheme == "https" {
							item[key] = resolved.String()
						}
					}
				}
			} else {
				rewriteDirectPlaybackURLs(child, base, depth+1)
			}
		}
	case []any:
		for _, child := range item {
			rewriteDirectPlaybackURLs(child, base, depth+1)
		}
	}
}
