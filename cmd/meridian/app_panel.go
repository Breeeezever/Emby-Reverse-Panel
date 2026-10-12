package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	snap, err := a.pm.TrafficSnapshot()
	if err != nil {
		a.jsonErr(w, http.StatusInternalServerError, "dashboard unavailable")
		return
	}
	snap.PanelDomain = a.panelHost
	snap.PanelAccessURL = a.panelAccessURL()
	a.jsonOK(w, snap)
}

func (a *App) panelAccessURL() string {
	if a == nil || a.panelHost == "" {
		return ""
	}
	scheme := "http"
	defaultPort := 80
	if a.panelTLSEnabled {
		scheme = "https"
		defaultPort = 443
	}
	host := a.panelHost
	if a.panelListenPort != defaultPort {
		host = net.JoinHostPort(host, strconv.Itoa(a.panelListenPort))
	}
	return scheme + "://" + host
}

// GET /api/ingress-capabilities exposes only coarse deployment state so the
// site form can avoid proposing host-only mode when the backend must reject it.
func (a *App) handleIngressCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	scheme := "http"
	if a.pm.siteTLSConfigSnapshot() != nil {
		scheme = "https"
	}
	a.jsonOK(w, map[string]interface{}{
		"site_listener_scheme":       scheme,
		"app_version":                appVersion,
		"host_only_available":        a.pm.HostOnlyIngressSafe(),
		"path_ingress_available":     true,
		"panel_access_url":           a.panelAccessURL(),
		"route_domain":               a.routeDomain,
		"domain_prefix_available":    a.routeDomain != "",
		"panel_tls_enabled":          a.panelTLSEnabled,
		"panel_bind_loopback":        a.panelBindLoopback,
		"trusted_proxy_configured":   len(a.trustedProxies) > 0,
		"upstream_headers_available": a.pm.UpstreamHeadersAvailable(),
		"max_playback_addresses":     maxPlaybackAddresses,
	})
}

func (a *App) handlePanelCertificate(w http.ResponseWriter, r *http.Request) {
	if a.panelCertificates == nil {
		a.jsonErr(w, http.StatusServiceUnavailable, "certificate management is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Cache-Control", "no-store")
		settings, err := a.db.PanelSettings()
		if err != nil {
			a.jsonErr(w, http.StatusInternalServerError, "failed to read panel settings")
			return
		}
		a.jsonOK(w, a.panelCertificateStatus(settings))
	case http.MethodPost:
		a.handlePanelCertificateIssue(w, r)
	default:
		a.jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

type panelSettingsRequest struct {
	PanelDomain    string `json:"panel_domain"`
	PanelPrefix    string `json:"panel_prefix"`
	WildcardDomain string `json:"wildcard_domain"`
	ListenPort     int    `json:"listen_port"`
}

// An HTTP panel bound to loopback with a domain or trusted proxy configuration
// depends on an external proxy. A missing PANEL_DOMAIN does not remove that
// dependency: changing the backend port or protocol still breaks the proxy.
func (a *App) managedExternalTLSProxy() bool {
	return a != nil && a.panelBindLoopback && !a.panelTLSEnabled &&
		(strings.TrimSpace(a.panelHost) != "" || len(a.trustedProxies) > 0)
}

func (a *App) validatePanelPortChange(port int) error {
	if err := validatePanelListenPort(port); err != nil {
		return err
	}
	if port == a.panelListenPort {
		return nil // The current process already owns this port.
	}
	if a.managedExternalTLSProxy() {
		return errors.New("当前面板由外部 HTTPS 反向代理管理，不能只在面板内修改监听端口；请使用安装器执行端口切换")
	}
	if err := checkPanelListenPort(a.panelBindHost, port); err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("面板监听端口 %d 不可用：已被其他程序占用，请选择其他端口", port)
		}
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("面板监听端口 %d 不可用：当前服务账户没有绑定权限；请选择 1024 以上的可用端口，或调整服务的监听权限", port)
		}
		return fmt.Errorf("面板监听端口 %d 不可用，请选择其他端口：%w", port, err)
	}
	return nil
}

func (a *App) handlePanelSettings(w http.ResponseWriter, r *http.Request) {
	unlock, ok := a.beginPanelConfiguration(w)
	if !ok {
		return
	}
	defer unlock()
	if r.Method != http.MethodPost {
		a.jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req panelSettingsRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		a.jsonErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	current, err := a.db.PanelSettings()
	if err != nil {
		a.jsonErr(w, http.StatusInternalServerError, "failed to read panel settings")
		return
	}
	candidate, err := panelSettingsCandidate(req, current)
	if err != nil {
		a.jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	validation := a.validatePanelEndpoint(r.Context(), current, candidate, r)
	if !validation.CanSave {
		a.panelEndpointValidationError(w, validation, false)
		return
	}
	settings, migrated, err := a.db.SavePanelEndpointSettings(candidate.PanelDomain, candidate.RouteDomain, candidate.ListenPort, candidate.TLSEnabled)
	if err != nil {
		a.jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	status := a.panelCertificateStatus(settings)
	log.Printf("panel settings saved for %s and *.%s (migrated sites: %d, restart required: %t)", settings.PanelDomain, settings.RouteDomain, migrated, status.RestartRequired)
	a.jsonOK(w, status)
}

func (a *App) panelCertificateStatus(settings PanelSettings) panelCertificateStatus {
	status := a.panelCertificates.status(settings, a.panelHost, a.routeDomain, a.panelListenPort, a.panelTLSEnabled)
	status.ActivePanelDomain = a.panelHost
	status.ActiveAccessURL = a.panelAccessURL()
	status.SavedTLSEnabled = settings.TLSEnabled
	status.ExternalProxy = a.managedExternalTLSProxy()
	status.ACMEEmail = settings.ACMEEmail
	status.ACMEDNSProvider = settings.ACMEDNSProvider
	status.ACMEStaging = settings.ACMEStaging
	status.DNSAPITokenConfigured = settings.ACMETokenCiphertext != ""
	status.AutoRenewEnabled = settings.CertificateSource == "acme" && !jwtSecretEphemeral && settings.ACMEEmail != "" && settings.ACMETokenCiphertext != ""
	if settings.ACMETokenCiphertext == "" {
		return status
	}
	if _, err := decryptPanelACMEToken(settings.ACMETokenCiphertext); err != nil {
		status.AutoRenewEnabled = false
	}
	return status
}

func (a *App) handlePanelCertificateIssue(w http.ResponseWriter, r *http.Request) {
	if a.panelCertificates == nil {
		a.jsonErr(w, http.StatusServiceUnavailable, "证书管理不可用")
		return
	}
	unlock, ok := a.beginPanelConfiguration(w)
	if !ok {
		return
	}
	defer unlock()
	if r.Method != http.MethodPost {
		a.jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Provider   string `json:"dns_provider"`
		Email      string `json:"email"`
		APIToken   string `json:"dns_api_token"`
		UseStaging bool   `json:"staging"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		a.jsonErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	settings, err := a.db.PanelSettings()
	if err != nil {
		a.jsonErr(w, http.StatusInternalServerError, "failed to read panel settings")
		return
	}
	if !settings.Configured || settings.PanelDomain == "" || settings.RouteDomain == "" || settings.ListenPort == 0 {
		a.jsonErr(w, http.StatusConflict, "请先保存面板前缀、泛域名和监听端口")
		return
	}
	tlsCandidate := settings
	tlsCandidate.TLSEnabled = true
	validation := a.validatePanelEndpoint(r.Context(), settings, tlsCandidate, r)
	if !validation.CanSave {
		a.panelEndpointValidationError(w, validation, false)
		return
	}
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(settings.ACMEDNSProvider))
	}
	if provider == "" {
		provider = "cloudflare"
	}
	if provider != "cloudflare" {
		a.jsonErr(w, http.StatusBadRequest, "only Cloudflare DNS is currently supported")
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		email = settings.ACMEEmail
	}
	token := strings.TrimSpace(req.APIToken)
	if token == "" && settings.ACMETokenCiphertext != "" {
		var decryptErr error
		token, decryptErr = decryptPanelACMEToken(settings.ACMETokenCiphertext)
		if decryptErr != nil {
			a.jsonErr(w, http.StatusConflict, "已保存的 DNS API Token 无法解密，请重新填写后保存")
			return
		}
	}
	if jwtSecretEphemeral {
		a.jsonErr(w, http.StatusConflict, "当前 JWT_SECRET 不是持久密钥，无法安全保存自动续签凭据；请先配置稳定密钥")
		return
	}
	if err := validatePanelCertificateRequest(email, token); err != nil {
		a.jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ciphertext, err := encryptPanelACMEToken(token)
	if err != nil {
		a.jsonErr(w, http.StatusInternalServerError, "无法加密 DNS API Token")
		return
	}
	if err := a.db.SavePanelACMECredentials(email, provider, ciphertext, req.UseStaging); err != nil {
		a.jsonErr(w, http.StatusInternalServerError, "无法保存自动续签凭据")
		return
	}
	settings, err = a.db.PanelSettings()
	if err != nil {
		a.jsonErr(w, http.StatusInternalServerError, "failed to read panel settings")
		return
	}
	currentStatus := a.panelCertificateStatus(settings)
	if certificateCanBeReused(currentStatus) {
		if !settings.TLSEnabled {
			settings, _, err = a.db.SaveManagedPanelSettings(settings.PanelDomain, settings.RouteDomain, settings.ListenPort, true)
			if err != nil {
				a.jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			currentStatus = a.panelCertificateStatus(settings)
		}
		if err := a.db.setPanelCertificateSource("acme", "", ""); err != nil {
			a.jsonErr(w, 500, "无法保存证书来源")
			return
		}
		currentStatus.CertificateSource = "acme"
		currentStatus.CertificateReused = true
		a.jsonOK(w, currentStatus)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	issued, err := a.panelCertificates.issueCloudflare(ctx, email, token, settings.PanelDomain, settings.RouteDomain, req.UseStaging)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errCertificateIssuanceBusy) {
			status = http.StatusConflict
		} else if validateErr := validatePanelCertificateRequest(email, token); validateErr != nil {
			status = http.StatusBadRequest
		}
		log.Printf("panel certificate request failed for %s: %v", settings.RouteDomain, err)
		a.jsonErr(w, status, err.Error())
		return
	}
	backup, err := a.panelCertificates.backupInstalledFiles()
	if err != nil {
		a.jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.panelCertificates.install(issued, false); err != nil {
		_ = a.panelCertificates.restoreInstalledFiles(backup)
		a.jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	settings, migrated, err := a.db.SaveManagedPanelSettings(settings.PanelDomain, settings.RouteDomain, settings.ListenPort, true)
	if err != nil {
		_ = a.panelCertificates.restoreInstalledFiles(backup)
		a.jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.db.setPanelCertificateSource("acme", "", ""); err != nil {
		_ = a.panelCertificates.restoreInstalledFiles(backup)
		a.jsonErr(w, 500, "无法保存证书来源")
		return
	}
	settings.CertificateSource, settings.CertificatePath, settings.PrivateKeyPath = "acme", "", ""
	status := a.panelCertificateStatus(settings)
	if !status.RestartRequired {
		a.panelCertificates.activate(issued)
	}
	log.Printf("panel wildcard certificate installed for *.%s (migrated sites: %d, restart required: %t)", settings.RouteDomain, migrated, status.RestartRequired)
	a.jsonOK(w, status)
}

func normalizePanelCertificateDomains(panelPrefix, wildcardDomain, legacyPanelDomain, legacyRouteDomain string) (PanelSettings, error) {
	if strings.TrimSpace(panelPrefix) != "" || strings.TrimSpace(wildcardDomain) != "" {
		return normalizeManagedPanelPrefix(panelPrefix, wildcardDomain)
	}
	return normalizeManagedPanelSettings(legacyPanelDomain, legacyRouteDomain)
}

func (a *App) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	unlock, ok := a.beginPanelConfiguration(w)
	if !ok {
		return
	}
	defer unlock()
	settings, err := a.db.PanelSettings()
	if err != nil {
		a.jsonErr(w, http.StatusInternalServerError, "failed to read panel settings")
		return
	}
	validation := a.validatePanelEndpoint(r.Context(), settings, settings, r)
	if !validation.CanRestart {
		a.panelEndpointValidationError(w, validation, true)
		return
	}
	a.requestRestart()
	a.jsonOK(w, map[string]interface{}{
		"restarting":   true,
		"redirect_url": validation.TargetURL,
	})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
