package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

type panelDNSPropagationChecker struct {
	resolver      *net.Resolver
	dialContext   func(context.Context, string, string) (net.Conn, error)
	timeout       time.Duration
	lookupTimeout time.Duration
	pollInterval  time.Duration
}

func waitForTXTRecord(ctx context.Context, name, value, zone string) error {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	checker := panelDNSPropagationChecker{
		resolver:      net.DefaultResolver,
		dialContext:   dialer.DialContext,
		timeout:       2 * time.Minute,
		lookupTimeout: 5 * time.Second,
		pollInterval:  5 * time.Second,
	}
	return checker.wait(ctx, name, value, zone)
}

func (c panelDNSPropagationChecker) wait(ctx context.Context, name, value, zone string) error {
	// Bound DNS calls themselves as well as the interval between attempts.
	waitCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	var authorities []*net.NS
	var lastErr error
	for {
		if err := waitCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("DNS-01 record %s did not propagate within %s: %v; check DNS resolution and outbound UDP/TCP port 53", name, c.timeout, lastErr)
		}
		if len(authorities) == 0 {
			lookupCtx, lookupCancel := context.WithTimeout(waitCtx, c.lookupTimeout)
			authorities, lastErr = c.resolver.LookupNS(lookupCtx, strings.TrimSuffix(zone, ".")+".")
			lookupCancel()
		}
		lookupCtx, lookupCancel := context.WithTimeout(waitCtx, c.lookupTimeout)
		err := c.check(lookupCtx, name, value, authorities)
		lookupCancel()
		if err == nil {
			return nil
		}
		// Keep the last completed DNS result instead of replacing it with a
		// final lookup canceled by the overall propagation deadline.
		if waitCtx.Err() == nil || lastErr == nil {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
		case <-ticker.C:
		}
	}
}

func (c panelDNSPropagationChecker) check(ctx context.Context, name, value string, authorities []*net.NS) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		authoritative bool
		err           error
	}
	results := make(chan result, len(authorities)+1)
	// Keep the recursive lookup as a fallback for networks that cannot reach
	// authoritative DNS directly. A cached negative answer must not gate the
	// authoritative checks: it may outlive the entire propagation timeout.
	go func() {
		results <- result{err: checkPanelTXTRecord(ctx, c.resolver, name, value, "system DNS")}
	}()
	for _, authority := range authorities {
		go func() {
			server := net.JoinHostPort(authority.Host, "53")
			resolver := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return c.dialContext(ctx, network, server)
				},
			}
			results <- result{authoritative: true, err: checkPanelTXTRecord(ctx, resolver, name, value, authority.Host)}
		}()
	}
	var failures []error
	readyAuthorities := 0
	for range len(authorities) + 1 {
		select {
		case <-ctx.Done():
			return errors.Join(append(failures, ctx.Err())...)
		case r := <-results:
			if r.err != nil {
				failures = append(failures, r.err)
				continue
			}
			if !r.authoritative {
				return nil
			}
			readyAuthorities++
			if readyAuthorities == len(authorities) {
				return nil
			}
		}
	}
	return errors.Join(failures...)
}

func checkPanelTXTRecord(ctx context.Context, resolver *net.Resolver, name, value, server string) error {
	records, err := resolver.LookupTXT(ctx, strings.TrimSuffix(name, ".")+".")
	if err != nil {
		return fmt.Errorf("%s: %w", server, err)
	}
	for _, record := range records {
		if record == value {
			return nil
		}
	}
	// Never include TXT values (including other clients' challenges) in errors.
	return fmt.Errorf("%s: expected TXT record is not visible yet", server)
}
