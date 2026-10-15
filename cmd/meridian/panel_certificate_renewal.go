package main

import (
	"context"
	"crypto/x509"
	"errors"
	"log"
	"strings"
	"time"
)

// Expiry never changes the listener protocol. Keep HTTPS alive and retry
// renewal; a successful renewal is hot-loaded without restarting sites.
func runPanelCertificateRenewalScheduler(ctx context.Context, db *DB, manager *panelCertificateManager) {
	if db == nil || manager == nil {
		return
	}
	check := func() {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if err := reloadExternalPanelCertificate(db, manager); err != nil && !errors.Is(err, errCertificateIssuanceBusy) {
			log.Printf("[panel-certificate] external certificate reload: %v", err)
		}
		if err := renewPanelCertificateIfDue(checkCtx, db, manager); err != nil && !errors.Is(err, errCertificateIssuanceBusy) && !errors.Is(err, context.Canceled) {
			log.Printf("[panel-certificate] automatic renewal failed: %v", err)
		}
	}
	check()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			check()
		case <-ctx.Done():
			return
		}
	}
}

func renewPanelCertificateIfDue(ctx context.Context, db *DB, manager *panelCertificateManager) (operationErr error) {
	if manager == nil || !manager.operationMu.TryLock() {
		return errCertificateIssuanceBusy
	}
	defer manager.operationMu.Unlock()
	saved, err := db.PanelSettings()
	if err != nil {
		return err
	}
	settings := manager.maintenanceSettings(saved)
	if !settings.TLSEnabled || !settings.Configured || settings.PanelDomain == "" || settings.RouteDomain == "" || settings.CertificateSource != "acme" {
		return nil
	}
	prior := db.panelRenewalStatus(settings)
	if next, err := time.Parse(time.RFC3339, prior.NextAttemptAt); err == nil && time.Now().Before(next) {
		return nil
	}
	renewed := false
	defer func() {
		if errors.Is(operationErr, context.Canceled) {
			return
		}
		next := time.Now().Add(panelCertificateRenewalInterval)
		if operationErr != nil {
			next = panelCertificateRetryTime(operationErr)
			operationErr = safePanelCertificateError(operationErr, settings)
		}
		db.recordPanelRenewal(settings, renewed, operationErr, next)
	}()
	if settings.ACMEStaging {
		return errors.New("旧配置使用 ACME 测试环境；请在面板申请正式证书后启用自动续签")
	}
	if jwtSecretEphemeral || strings.TrimSpace(settings.ACMEEmail) == "" || strings.TrimSpace(settings.ACMETokenCiphertext) == "" {
		return errors.New("自动续签缺少有效邮箱、DNS Token 或持久密钥")
	}
	pair, loadErr := manager.maintenanceCertificate()
	if loadErr == nil && pair != nil && len(pair.Certificate) > 0 {
		leaf, parseErr := x509.ParseCertificate(pair.Certificate[0])
		if parseErr == nil && certificateCoversPanel(leaf, settings.PanelDomain, settings.RouteDomain) == nil &&
			!time.Now().Before(leaf.NotBefore) && time.Until(leaf.NotAfter) > panelCertificateRenewalWindow {
			return nil
		}
	}
	token, err := decryptPanelACMEToken(settings.ACMETokenCiphertext)
	if err != nil {
		return errors.New("无法解密已保存的 DNS API Token")
	}
	provider := strings.ToLower(strings.TrimSpace(settings.ACMEDNSProvider))
	if provider != "" && provider != "cloudflare" {
		return errors.New("自动续签暂仅支持 Cloudflare DNS")
	}
	issued, err := manager.requestCertificate(ctx, settings.ACMEEmail, token, settings.PanelDomain, settings.RouteDomain, false)
	if err != nil {
		return err
	}
	issued, err = manager.validateProductionCertificate(issued, settings)
	if err != nil {
		return err
	}
	if err := manager.installMaintainedCertificate(issued, settings, saved); err != nil {
		return err
	}
	renewed = true
	log.Printf("[panel-certificate] certificate renewed for %s", settings.PanelDomain)
	return nil
}
