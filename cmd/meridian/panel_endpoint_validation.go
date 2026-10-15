package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

type panelEndpointResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type panelEndpointCheck struct {
	Code        string `json:"code"`
	Field       string `json:"field,omitempty"`
	State       string `json:"state"`
	Message     string `json:"message"`
	RestartOnly bool   `json:"restart_only,omitempty"`
}

type panelEndpointValidation struct {
	CanSave    bool                 `json:"can_save"`
	CanRestart bool                 `json:"can_restart"`
	TargetURL  string               `json:"target_url"`
	Checks     []panelEndpointCheck `json:"checks"`
}

func (v *panelEndpointValidation) add(code, field, state, message string, restartOnly bool) {
	v.Checks = append(v.Checks, panelEndpointCheck{code, field, state, message, restartOnly})
	if state == "blocked" {
		v.CanRestart = false
		if !restartOnly {
			v.CanSave = false
		}
	}
}

func (a *App) panelEndpointValidationError(w http.ResponseWriter, validation panelEndpointValidation, restarting bool) {
	message := "配置检查未通过"
	for _, check := range validation.Checks {
		if check.State == "blocked" && (restarting || !check.RestartOnly) {
			message = check.Message
			break
		}
	}
	a.jsonResponse(w, http.StatusConflict, map[string]any{"error": message, "validation": validation})
}

func panelSettingsCandidate(req panelSettingsRequest, current PanelSettings) (PanelSettings, error) {
	var normalized PanelSettings
	var err error
	if strings.TrimSpace(req.PanelDomain) != "" {
		var routeDomain string
		if strings.TrimSpace(req.WildcardDomain) != "" {
			routeDomain, err = normalizeWildcardDomain(req.WildcardDomain)
		}
		if err == nil {
			normalized, err = normalizeStoredPanelSettings(req.PanelDomain, routeDomain, current.TLSEnabled)
		}
	} else {
		normalized, err = normalizeManagedPanelPrefix(req.PanelPrefix, req.WildcardDomain)
	}
	if err != nil {
		return PanelSettings{}, err
	}
	if err := validatePanelListenPort(req.ListenPort); err != nil {
		return PanelSettings{}, err
	}
	current.PanelDomain, current.RouteDomain = normalized.PanelDomain, normalized.RouteDomain
	current.ListenPort, current.Configured = req.ListenPort, true
	return current, nil
}

// An external proxy's public port is distinct from the backend listener. Use
// the trusted request scheme and its Host port for public checks and redirects.
func (a *App) publicPanelEndpoint(settings PanelSettings, r *http.Request) PanelSettings {
	if !a.managedExternalTLSProxy() || r == nil {
		return settings
	}
	settings.TLSEnabled = requestIsHTTPS(r, a.trustedProxies)
	settings.ListenPort = 80
	if settings.TLSEnabled {
		settings.ListenPort = 443
	}
	if _, port, err := net.SplitHostPort(r.Host); err == nil {
		if parsed, err := strconv.Atoi(port); err == nil && validatePanelListenPort(parsed) == nil {
			settings.ListenPort = parsed
		}
	}
	return settings
}

func (a *App) panelRestartAccessURL(settings PanelSettings, r *http.Request) string {
	return panelSettingsAccessURL(a.publicPanelEndpoint(settings, r))
}

func (a *App) beginPanelConfiguration(w http.ResponseWriter) (func(), bool) {
	unlock := func() {}
	if a.panelCertificates != nil {
		if !a.panelCertificates.operationMu.TryLock() {
			a.jsonErr(w, http.StatusConflict, "另一项面板或证书操作正在进行，请稍后重试")
			return nil, false
		}
		unlock = a.panelCertificates.operationMu.Unlock
	}
	if a.restartPending.Load() {
		unlock()
		a.jsonErr(w, http.StatusConflict, "面板正在重启，请在新地址就绪后再修改配置")
		return nil, false
	}
	return unlock, true
}

// One read-only inspection is shared by preview, save, certificate changes and
// restart. Every mutation repeats it; a successful preview is never authority
// to skip a later DNS, socket or site-conflict check.
func (a *App) validatePanelEndpoint(ctx context.Context, current, candidate PanelSettings, r *http.Request) panelEndpointValidation {
	v := panelEndpointValidation{CanSave: true, CanRestart: true, TargetURL: a.panelRestartAccessURL(candidate, r)}
	if candidate.PanelDomain == "" || v.TargetURL == "" {
		v.add("invalid_endpoint", "panel_domain", "blocked", "请填写有效的面板域名和监听端口", false)
		return v
	}
	if err := a.validatePanelPortChange(candidate.ListenPort); err != nil {
		v.add("listener_unavailable", "listen_port", "blocked", err.Error(), false)
	} else {
		v.add("listener_available", "listen_port", "ok", "监听端口检查通过", false)
	}
	if _, err := planPanelHostMigrations(a.db.db, current, candidate); err != nil {
		v.add("site_conflict", "panel_domain", "blocked", err.Error(), false)
	} else {
		v.add("site_routes_available", "", "ok", "面板地址与站点配置无冲突", false)
	}
	if a.managedExternalTLSProxy() {
		if candidate.TLSEnabled {
			v.add("external_tls_proxy", "", "blocked", "当前面板由外部 HTTPS 反向代理管理，不能在面板内启用原生证书监听；请在代理中配置证书", false)
		} else {
			v.add("external_proxy", "", "warning", "当前使用外部反向代理；新域名须先配置到代理，监听端口是内部回源端口，面板不会自动修改代理配置", false)
		}
	}
	publicSettings := a.publicPanelEndpoint(candidate, r)
	if browserRestrictedPanelPort(publicSettings.ListenPort) {
		v.add("browser_restricted_port", "listen_port", "blocked", fmt.Sprintf("浏览器禁止访问端口 %d，请选择其他 HTTP/HTTPS 端口", publicSettings.ListenPort), false)
	}
	resolver := a.panelDNSResolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dnsCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	answers, err := resolver.LookupIPAddr(dnsCtx, candidate.PanelDomain)
	if err != nil || len(answers) == 0 {
		message := "域名尚未解析到 IP 地址，请先添加或修正 A / AAAA 记录，等待解析生效后重试"
		var dnsErr *net.DNSError
		if err != nil && (!errors.As(err, &dnsErr) || !dnsErr.IsNotFound) {
			message = "DNS 查询失败或超时，暂时无法验证新地址；请检查服务器的 DNS 连接后重试"
		}
		v.add("dns_unavailable", "panel_domain", "blocked", message, false)
	} else {
		addresses := make(map[string]bool)
		cloudflare := false
		for _, answer := range answers {
			if answer.IP.To16() == nil || (!answer.IP.IsGlobalUnicast() && !answer.IP.IsLoopback()) {
				continue
			}
			addresses[answer.IP.String()] = true
			cloudflare = cloudflare || isCloudflareProxyIP(answer.IP)
		}
		ips := make([]string, 0, len(addresses))
		for address := range addresses {
			ips = append(ips, address)
		}
		sort.Strings(ips)
		if len(ips) == 0 {
			v.add("dns_unavailable", "panel_domain", "blocked", "DNS 没有返回可用的单播 IP 地址，请修正 A / AAAA 记录后重试", false)
			return v
		}
		if len(ips) > 8 {
			ips = append(ips[:8], "…")
		}
		v.add("dns_resolved", "panel_domain", "ok", "DNS 解析："+strings.Join(ips, "、"), false)
		if cloudflare && !cloudflarePanelPortAllowed(publicSettings.ListenPort, publicSettings.TLSEnabled) {
			// HTTP setup can stage a supported HTTPS port before applying its
			// first certificate. It must not restart in HTTP on that port.
			restartOnly := !publicSettings.TLSEnabled && cloudflarePanelPortAllowed(publicSettings.ListenPort, true)
			message := fmt.Sprintf("%s 解析到 Cloudflare 代理，但普通 Cloudflare 代理不支持 %s 端口 %d；请将这个域名对应的 DNS 记录改为“仅 DNS”，或选择 Cloudflare 支持且本机可用的端口。单独记录会优先于泛域名记录", candidate.PanelDomain, map[bool]string{true: "HTTPS", false: "HTTP"}[publicSettings.TLSEnabled], publicSettings.ListenPort)
			if restartOnly {
				message = fmt.Sprintf("Cloudflare 的端口 %d 只支持 HTTPS；可以先保存，但必须应用有效证书后才能重启", publicSettings.ListenPort)
			}
			v.add("cloudflare_port_unsupported", "listen_port", "blocked", message, restartOnly)
		} else if cloudflare {
			v.add("cloudflare_port_supported", "", "warning", "Cloudflare 代理端口受支持；请确认该记录的回源 IP、SSL 模式与回源端口也已正确配置", false)
		}
	}
	if candidate.TLSEnabled {
		status := a.panelCertificateStatus(candidate)
		switch {
		case !status.Configured:
			v.add("certificate_missing", "", "blocked", "尚未配置 HTTPS 证书；可以保存待应用设置，配置证书后才能重启", true)
		case !status.CertificateCurrent:
			v.add("certificate_mismatch", "panel_domain", "blocked", "现有证书不覆盖新的面板域名或节点泛域名；可以保存待应用设置，换成匹配证书后才能重启", true)
		case !status.CertificateValid:
			v.add("certificate_expired", "", "blocked", "HTTPS 证书已过期或尚未生效，请更新证书后再重启", true)
		default:
			v.add("certificate_valid", "", "ok", "HTTPS 证书有效且匹配", false)
		}
	}
	return v
}

func (a *App) handlePanelSettingsCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		a.jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req panelSettingsRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		a.jsonErr(w, http.StatusBadRequest, "配置请求无效")
		return
	}
	current, err := a.db.PanelSettings()
	if err != nil {
		a.jsonErr(w, http.StatusInternalServerError, "无法读取面板设置")
		return
	}
	candidate, err := panelSettingsCandidate(req, current)
	if err != nil {
		a.jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonOK(w, a.validatePanelEndpoint(r.Context(), current, candidate, r))
}

// Cloudflare's published proxy ranges, checked 2026-10-12. DNS hosting at
// Cloudflare alone is not evidence of proxying; only the returned IPs count.
// Sources: https://www.cloudflare.com/ips-v4 and /ips-v6.
var cloudflareProxyNetworks = func() []netip.Prefix {
	var prefixes []netip.Prefix
	for _, cidr := range strings.Fields(`173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22
141.101.64.0/18 108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22
198.41.128.0/17 162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22
2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32 2405:8100::/32 2a06:98c0::/29 2c0f:f248::/32`) {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}()

func isCloudflareProxyIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	for _, prefix := range cloudflareProxyNetworks {
		if prefix.Contains(address.Unmap()) {
			return true
		}
	}
	return false
}

// https://developers.cloudflare.com/fundamentals/reference/network-ports/
func cloudflarePanelPortAllowed(port int, tlsEnabled bool) bool {
	if tlsEnabled {
		switch port {
		case 443, 2053, 2083, 2087, 2096, 8443:
			return true
		}
	} else {
		switch port {
		case 80, 8080, 8880, 2052, 2082, 2086, 2095:
			return true
		}
	}
	return false
}

// Fetch's bad-port list applies even when the OS can bind the port.
// https://fetch.spec.whatwg.org/#port-blocking
func browserRestrictedPanelPort(port int) bool {
	switch port {
	case 1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95,
		101, 102, 103, 104, 109, 110, 111, 113, 115, 117, 119, 123, 135, 137, 139, 143, 161, 179,
		389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601,
		636, 989, 990, 993, 995, 1719, 1720, 1723, 2049, 3659, 4045, 5060, 5061, 6000, 6566,
		6665, 6666, 6667, 6668, 6669, 6697, 10080:
		return true
	}
	return false
}
