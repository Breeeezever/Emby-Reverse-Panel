package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Use actual DNS packets so the tests cover resolver routing, negative answers,
// split TXT strings, and the distinction between recursive and authority data.
func panelDNSTestServer(t *testing.T, answer func(dnsmessage.Question) (dnsmessage.RCode, []dnsmessage.Resource)) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, peer, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			var query dnsmessage.Message
			if err := query.Unpack(buf[:n]); err != nil || len(query.Questions) != 1 {
				continue
			}
			code, records := answer(query.Questions[0])
			response := dnsmessage.Message{
				Header:    dnsmessage.Header{ID: query.ID, Response: true, Authoritative: true, RecursionAvailable: true, RCode: code},
				Questions: query.Questions,
				Answers:   records,
			}
			packet, err := response.Pack()
			if err == nil {
				_, _ = conn.WriteTo(packet, peer)
			}
		}
	}()
	t.Cleanup(func() { _ = conn.Close(); <-done })
	return conn.LocalAddr().String()
}

func panelDNSTestNS(q dnsmessage.Question) []dnsmessage.Resource {
	var records []dnsmessage.Resource
	for _, host := range []string{"ns1.example.com.", "ns2.example.com."} {
		name, _ := dnsmessage.NewName(host)
		records = append(records, dnsmessage.Resource{
			Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeNS, Class: dnsmessage.ClassINET, TTL: 1800},
			Body:   &dnsmessage.NSResource{NS: name},
		})
	}
	return records
}

func panelDNSTestTXT(q dnsmessage.Question, value string) []dnsmessage.Resource {
	return []dnsmessage.Resource{{
		Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeTXT, Class: dnsmessage.ClassINET, TTL: 60},
		Body:   &dnsmessage.TXTResource{TXT: []string{value[:len(value)/2], value[len(value)/2:]}},
	}}
}

func panelDNSTestChecker(recursive, ns1, ns2 string) panelDNSPropagationChecker {
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		switch address {
		case "ns1.example.com.:53":
			address = ns1
		case "ns2.example.com.:53":
			address = ns2
		default:
			address = recursive
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return panelDNSPropagationChecker{
		resolver: &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dial(ctx, network, "recursive")
		}},
		dialContext:   dial,
		timeout:       time.Second,
		lookupTimeout: 200 * time.Millisecond,
		pollInterval:  10 * time.Millisecond,
	}
}

func TestPanelDNSPropagationBypassesNegativeRecursiveCache(t *testing.T) {
	recursive := panelDNSTestServer(t, func(q dnsmessage.Question) (dnsmessage.RCode, []dnsmessage.Resource) {
		if q.Type == dnsmessage.TypeNS {
			return dnsmessage.RCodeSuccess, panelDNSTestNS(q)
		}
		return dnsmessage.RCodeNameError, nil
	})
	var authorityQueries atomic.Int32
	authority := panelDNSTestServer(t, func(q dnsmessage.Question) (dnsmessage.RCode, []dnsmessage.Resource) {
		authorityQueries.Add(1)
		return dnsmessage.RCodeSuccess, panelDNSTestTXT(q, "current-challenge")
	})
	c := panelDNSTestChecker(recursive, authority, authority)
	if _, err := c.resolver.LookupTXT(context.Background(), "_acme-challenge.example.com."); err == nil {
		t.Fatal("fixture must reproduce the old recursive lookup failure")
	}
	if err := c.wait(context.Background(), "_acme-challenge.example.com", "current-challenge", "example.com"); err != nil {
		t.Fatal(err)
	}
	if authorityQueries.Load() < 2 {
		t.Fatal("did not confirm the record on both authoritative servers")
	}
}

func TestPanelDNSPropagationWaitsForEveryAuthority(t *testing.T) {
	recursive := panelDNSTestServer(t, func(q dnsmessage.Question) (dnsmessage.RCode, []dnsmessage.Resource) {
		if q.Type == dnsmessage.TypeNS {
			return dnsmessage.RCodeSuccess, panelDNSTestNS(q)
		}
		return dnsmessage.RCodeNameError, nil
	})
	ns1 := panelDNSTestServer(t, func(q dnsmessage.Question) (dnsmessage.RCode, []dnsmessage.Resource) {
		return dnsmessage.RCodeSuccess, panelDNSTestTXT(q, "current-challenge")
	})
	var queries atomic.Int32
	ns2 := panelDNSTestServer(t, func(q dnsmessage.Question) (dnsmessage.RCode, []dnsmessage.Resource) {
		if queries.Add(1) == 1 {
			return dnsmessage.RCodeSuccess, panelDNSTestTXT(q, "old-challenge")
		}
		return dnsmessage.RCodeSuccess, panelDNSTestTXT(q, "current-challenge")
	})
	c := panelDNSTestChecker(recursive, ns1, ns2)
	if err := c.wait(context.Background(), "_acme-challenge.example.com", "current-challenge", "example.com"); err != nil {
		t.Fatal(err)
	}
	if queries.Load() < 2 {
		t.Fatal("accepted propagation before the second authority had the current record")
	}
}

func TestPanelDNSPropagationRejectsWrongTXTAndReportsTimeout(t *testing.T) {
	server := panelDNSTestServer(t, func(q dnsmessage.Question) (dnsmessage.RCode, []dnsmessage.Resource) {
		if q.Type == dnsmessage.TypeNS {
			return dnsmessage.RCodeSuccess, panelDNSTestNS(q)
		}
		return dnsmessage.RCodeSuccess, panelDNSTestTXT(q, "other-client-secret")
	})
	c := panelDNSTestChecker(server, server, server)
	c.timeout = 80 * time.Millisecond
	err := c.wait(context.Background(), "_acme-challenge.example.com", "expected-secret", "example.com")
	if err == nil || !strings.Contains(err.Error(), "did not propagate") || !strings.Contains(err.Error(), "expected TXT record is not visible") {
		t.Fatalf("unexpected propagation result: %v", err)
	}
	if strings.Contains(err.Error(), "other-client-secret") || strings.Contains(err.Error(), "expected-secret") {
		t.Fatal("error disclosed TXT challenge values")
	}
}

func TestPanelDNSPropagationRecursiveFallback(t *testing.T) {
	server := panelDNSTestServer(t, func(q dnsmessage.Question) (dnsmessage.RCode, []dnsmessage.Resource) {
		if q.Type == dnsmessage.TypeNS {
			return dnsmessage.RCodeSuccess, panelDNSTestNS(q)
		}
		return dnsmessage.RCodeSuccess, panelDNSTestTXT(q, "current-challenge")
	})
	c := panelDNSTestChecker(server, server, server)
	c.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, errors.New("direct DNS is blocked")
	}
	if err := c.wait(context.Background(), "_acme-challenge.example.com", "current-challenge", "example.com"); err != nil {
		t.Fatal(err)
	}
}

func TestPanelDNSPropagationBoundsBlockedDNSAndCancellation(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		c := panelDNSTestChecker("", "", "")
		c.timeout = 60 * time.Millisecond
		c.resolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		if cancelParent {
			cancel()
		}
		start := time.Now()
		err := c.wait(ctx, "_acme-challenge.example.com", "current-challenge", "example.com")
		cancel()
		if err == nil || time.Since(start) > time.Second {
			t.Fatalf("blocked resolver escaped the overall timeout: %v", err)
		}
		if cancelParent && !errors.Is(err, context.Canceled) {
			t.Fatalf("parent cancellation not preserved: %v", err)
		}
	}
}
