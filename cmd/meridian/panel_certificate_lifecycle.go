package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

type panelCertificateDetails struct {
	DNSNames     []string `json:"dns_names"`
	Fingerprint  string   `json:"fingerprint"`
	ExpiresAt    string   `json:"expires_at"`
	Valid        bool     `json:"valid"`
	Trusted      bool     `json:"trusted"`
	TrustMessage string   `json:"trust_message"`
	Source       string   `json:"source"`
}

type panelCertificateRenewalStatus struct {
	Domain        string `json:"domain"`
	Source        string `json:"source"`
	LastCheckedAt string `json:"last_checked_at"`
	LastSuccessAt string `json:"last_success_at"`
	LastError     string `json:"last_error"`
	NextAttemptAt string `json:"next_attempt_at"`
	Pending       bool   `json:"pending"`
}

func describePanelCertificate(pair *tls.Certificate, settings PanelSettings, roots *x509.CertPool) panelCertificateDetails {
	result := panelCertificateDetails{Source: settings.CertificateSource}
	if pair == nil || len(pair.Certificate) == 0 {
		return result
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return result
	}
	sum := sha256.Sum256(pair.Certificate[0])
	result.Fingerprint = hex.EncodeToString(sum[:])
	result.DNSNames = append([]string(nil), leaf.DNSNames...)
	result.ExpiresAt = leaf.NotAfter.UTC().Format(time.RFC3339)
	result.Valid = !time.Now().Before(leaf.NotBefore) && time.Now().Before(leaf.NotAfter)
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		if cert, err := x509.ParseCertificate(der); err == nil {
			intermediates.AddCert(cert)
		}
	}
	// Domain coverage is checked separately. Trust describes the chain, not
	// whether a saved hostname has already been switched to this certificate.
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates})
	result.Trusted = err == nil
	switch {
	case result.Trusted:
		result.TrustMessage = "服务器信任链校验通过；客户端仍需信任对应 CA"
	case strings.Contains(strings.ToUpper(leaf.Issuer.CommonName), "STAGING"):
		result.TrustMessage = "ACME 测试证书，普通客户端不信任；请申请正式证书"
	case !result.Valid:
		result.TrustMessage = "证书已过期或尚未生效"
	default:
		result.TrustMessage = "服务器无法验证证书信任链；请检查完整证书链，内部 CA 或自签名证书需由客户端单独信任"
	}
	return result
}

func samePanelCertificateChain(a, b *tls.Certificate) bool {
	if len(a.Certificate) != len(b.Certificate) {
		return false
	}
	for i := range a.Certificate {
		if !bytes.Equal(a.Certificate[i], b.Certificate[i]) {
			return false
		}
	}
	return true
}

func (m *panelCertificateManager) bindRuntimeSettings(settings PanelSettings) {
	m.mu.Lock()
	m.runtimeSettings = &settings
	m.mu.Unlock()
}

// Saved settings are a candidate for the next restart. Background maintenance
// always uses the settings of the currently running listener, including its
// certificate source and credentials, until an explicit activation succeeds.
func (m *panelCertificateManager) maintenanceSettings(saved PanelSettings) PanelSettings {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeSettings != nil {
		return *m.runtimeSettings
	}
	return saved // Startup/CLI: no running listener has been bound yet.
}

func samePanelCertificateBinding(a, b PanelSettings) bool {
	return a.PanelDomain == b.PanelDomain && a.RouteDomain == b.RouteDomain &&
		a.CertificateSource == b.CertificateSource && a.CertificatePath == b.CertificatePath &&
		a.PrivateKeyPath == b.PrivateKeyPath && a.ACMEStaging == b.ACMEStaging
}

func samePanelEndpoint(a, b PanelSettings) bool {
	return a.PanelDomain == b.PanelDomain && a.RouteDomain == b.RouteDomain &&
		a.ListenPort == b.ListenPort && a.TLSEnabled == b.TLSEnabled
}

func (m *panelCertificateManager) maintenanceCertificate() (*tls.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeSettings != nil && m.currentCertificate != nil {
		return m.currentCertificate, nil
	}
	cert, err := tls.LoadX509KeyPair(m.certFile, m.keyFile)
	return &cert, err
}

func (m *panelCertificateManager) activateForSettings(issued *issuedPanelCertificate, settings PanelSettings) {
	m.mu.Lock()
	certificate := issued.certificate
	m.currentCertificate = &certificate
	m.runtimeSettings = &settings
	m.mu.Unlock()
}

// A pending certificate belongs to the saved endpoint. Renewing the running
// endpoint may update its in-memory certificate, but must not overwrite that
// pending file. A restart deliberately selects the saved settings and files.
func (m *panelCertificateManager) installMaintainedCertificate(issued *issuedPanelCertificate, active, saved PanelSettings) error {
	if samePanelCertificateBinding(active, saved) {
		backup, err := m.backupInstalledFiles()
		if err != nil {
			return err
		}
		if err = m.install(issued, false); err != nil {
			return errors.Join(err, m.restoreInstalledFiles(backup))
		}
	}
	m.activateForSettings(issued, active)
	return nil
}

func (m *panelCertificateManager) requestCertificate(ctx context.Context, email, token, panelDomain, routeDomain string, staging bool) (*issuedPanelCertificate, error) {
	if m.issuer != nil {
		return m.issuer(ctx, email, token, panelDomain, routeDomain, staging)
	}
	return m.issueCloudflare(ctx, email, token, panelDomain, routeDomain, staging)
}

// Staging is a diagnostic request, never a candidate service certificate.
// Keep even its account key and PEM output in a separate directory.
func (m *panelCertificateManager) testCertificate(ctx context.Context, email, token string, settings PanelSettings) error {
	if m.accountDir == "" {
		return errors.New("可写的 TLS 数据目录不可用")
	}
	dir := filepath.Join(m.accountDir, "acme-test")
	testManager := &panelCertificateManager{
		certFile: filepath.Join(dir, "fullchain.pem"), keyFile: filepath.Join(dir, "privkey.pem"),
		accountDir: dir, httpClient: m.httpClient, issuer: m.issuer,
	}
	m.mu.Lock()
	m.issuing = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.issuing = false; m.mu.Unlock() }()
	issued, err := testManager.requestCertificate(ctx, email, token, settings.PanelDomain, settings.RouteDomain, true)
	if err != nil {
		return err
	}
	if issued == nil {
		return errors.New("测试签发未返回证书")
	}
	// Validate the returned pair and names even though it will never be served.
	issued, err = parseImportedPanelCertificate(issued.certPEM, issued.keyPEM, settings)
	if err != nil {
		return err
	}
	return testManager.install(issued, false)
}

func (m *panelCertificateManager) validateProductionCertificate(issued *issuedPanelCertificate, settings PanelSettings) (*issuedPanelCertificate, error) {
	if issued == nil {
		return nil, errors.New("签发未返回证书")
	}
	validated, err := parseImportedPanelCertificate(issued.certPEM, issued.keyPEM, settings)
	if err != nil {
		return nil, err
	}
	if !describePanelCertificate(&validated.certificate, settings, m.trustRoots).Trusted {
		return nil, errors.New("正式签发返回的证书未通过服务器信任链校验，已保留原证书")
	}
	return validated, nil
}

func canReusePanelACMECertificate(status panelCertificateStatus, previous PanelSettings) bool {
	return previous.CertificateSource == "acme" && !previous.ACMEStaging &&
		status.CertificateTrusted && certificateCanBeReused(status)
}

func panelCertificateRenewalKey(settings PanelSettings) string {
	// Include the encrypted credentials so correcting a token resets backoff.
	data, _ := json.Marshal([]string{settings.PanelDomain, settings.RouteDomain, settings.CertificateSource,
		settings.CertificatePath, settings.PrivateKeyPath, settings.ACMEEmail, settings.ACMETokenCiphertext,
		strconv.FormatBool(settings.ACMEStaging)})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (d *DB) panelRenewalStatus(settings PanelSettings) panelCertificateRenewalStatus {
	result := panelCertificateRenewalStatus{Domain: settings.PanelDomain, Source: settings.CertificateSource}
	err := d.db.QueryRow(`SELECT last_checked_at, last_success_at, last_error, next_attempt_at
		FROM panel_certificate_renewals WHERE target_key=?`, panelCertificateRenewalKey(settings)).Scan(
		&result.LastCheckedAt, &result.LastSuccessAt, &result.LastError, &result.NextAttemptAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("[panel-certificate] read renewal status: %v", err)
	}
	return result
}

func (d *DB) recordPanelRenewal(settings PanelSettings, renewed bool, operationErr error, next time.Time) {
	now := time.Now().UTC().Format(time.RFC3339)
	success, message := "", ""
	if renewed {
		success = now
	}
	if operationErr != nil {
		message = safePanelCertificateError(operationErr, settings).Error()
	}
	_, err := d.db.Exec(`INSERT INTO panel_certificate_renewals
		(target_key,last_checked_at,last_success_at,last_error,next_attempt_at) VALUES (?,?,?,?,?)
		ON CONFLICT(target_key) DO UPDATE SET last_checked_at=excluded.last_checked_at,
		last_success_at=CASE WHEN excluded.last_success_at='' THEN last_success_at ELSE excluded.last_success_at END,
		last_error=excluded.last_error,next_attempt_at=excluded.next_attempt_at`,
		panelCertificateRenewalKey(settings), now, success, message, next.UTC().Format(time.RFC3339))
	if err != nil {
		log.Printf("[panel-certificate] persist renewal status: %v", err)
	}
}

func safePanelCertificateError(err error, settings PanelSettings) error {
	message := err.Error()
	if settings.ACMETokenCiphertext != "" {
		message = strings.ReplaceAll(message, settings.ACMETokenCiphertext, "[已隐藏]")
		if token, decryptErr := decryptPanelACMEToken(settings.ACMETokenCiphertext); decryptErr == nil && token != "" {
			message = strings.ReplaceAll(message, token, "[已隐藏]")
		}
	}
	if len(message) > 2000 {
		message = string([]rune(message)[:min(500, len([]rune(message)))])
	}
	return errors.New(message)
}

func panelCertificateRetryTime(err error) time.Time {
	next := time.Now().Add(time.Hour)
	var acmeErr *acme.Error
	if errors.As(err, &acmeErr) {
		value := acmeErr.Header.Get("Retry-After")
		if seconds, parseErr := strconv.Atoi(value); parseErr == nil && seconds > 0 && seconds <= 30*24*3600 {
			if retry := time.Now().Add(time.Duration(seconds) * time.Second); retry.After(next) {
				next = retry
			}
		} else if retry, parseErr := time.Parse(time.RFC1123, value); parseErr == nil && retry.After(next) {
			next = retry
		}
	}
	return next
}

// The endpoint has already been saved. Enabling its certificate and replacing
// renewal credentials is one SQL statement, after issuance and file validation.
func (d *DB) savePanelACMECertificateSettings(email, provider, ciphertext string) error {
	_, err := d.db.Exec(`UPDATE panel_settings SET tls_enabled=1,panel_domain_source='database',
		certificate_source='acme',certificate_path='',private_key_path='',acme_email=?,acme_dns_provider=?,
		acme_token_ciphertext=?,acme_staging=0,updated_at=CURRENT_TIMESTAMP WHERE id=1`, email, provider, ciphertext)
	if err != nil {
		return fmt.Errorf("保存证书与续签配置失败: %w", err)
	}
	return nil
}
