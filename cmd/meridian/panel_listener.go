package main

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
)

type panelListenerSpec struct {
	network string
	address string
}

type panelListenerFailure struct {
	spec panelListenerSpec
	err  error
}

// Probe with the same bind addresses as startup, releasing every socket before
// returning. This catches existing conflicts; availability can still change
// before restart, so the restart endpoint performs this check again.
func checkPanelListenPort(bindAddress string, port int) error {
	listeners, failures, err := listenPanel(bindAddress, port)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	if err != nil {
		return err
	}
	for _, failure := range failures {
		// A working IPv6 socket must not mask an occupied IPv4 port (or vice
		// versa). An unsupported address family can still use startup's fallback.
		if errors.Is(failure.err, syscall.EADDRINUSE) {
			return failure.err
		}
	}
	return nil
}

func panelListenerSpecs(bindAddress string, port int) ([]panelListenerSpec, error) {
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("panel port must be between 1 and 65535, got %d", port)
	}
	bindAddress = strings.TrimSpace(bindAddress)
	portText := strconv.Itoa(port)
	// Keep the empty/default configuration loopback-only. The startup path uses
	// panelListenAddress for security decisions, so treating an empty value as a
	// wildcard here would make the effective listener disagree with those checks.
	if bindAddress == "" {
		bindAddress = "127.0.0.1"
	}
	if bindAddress == "0.0.0.0" || bindAddress == "::" {
		return []panelListenerSpec{
			{network: "tcp4", address: net.JoinHostPort("0.0.0.0", portText)},
			{network: "tcp6", address: net.JoinHostPort("::", portText)},
		}, nil
	}
	ip := net.ParseIP(bindAddress)
	if ip == nil {
		return nil, fmt.Errorf("PANEL_BIND_ADDR must be an IP address, got %q", bindAddress)
	}
	if ip.To4() != nil {
		return []panelListenerSpec{{network: "tcp4", address: net.JoinHostPort(bindAddress, portText)}}, nil
	}
	return []panelListenerSpec{{network: "tcp6", address: net.JoinHostPort(bindAddress, portText)}}, nil
}

func listenPanel(bindAddress string, port int) ([]net.Listener, []panelListenerFailure, error) {
	specs, err := panelListenerSpecs(bindAddress, port)
	if err != nil {
		return nil, nil, err
	}
	listeners := make([]net.Listener, 0, len(specs))
	failures := make([]panelListenerFailure, 0)
	for _, spec := range specs {
		listener, listenErr := net.Listen(spec.network, spec.address)
		if listenErr != nil {
			failures = append(failures, panelListenerFailure{spec: spec, err: listenErr})
			continue
		}
		listeners = append(listeners, listener)
	}
	if len(listeners) == 0 {
		if len(failures) == 0 {
			return nil, nil, fmt.Errorf("no panel listeners were configured")
		}
		return nil, failures, fmt.Errorf("all panel listeners failed: %v", failures[0].err)
	}
	return listeners, failures, nil
}
