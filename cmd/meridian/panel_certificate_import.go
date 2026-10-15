package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxPanelCertificateBytes = 512 << 10
const maxPanelPrivateKeyBytes = 64 << 10

func certificateCoversPanel(cert *x509.Certificate, panelDomain, routeDomain string) error {
	if cert == nil || panelDomain == "" {
		return errors.New("请先保存面板域名")
	}
	if err := cert.VerifyHostname(panelDomain); err != nil {
		return errors.New("证书不匹配面板域名")
	}
	if routeDomain != "" {
		wildcard := "*." + strings.ToLower(routeDomain)
		found := false
		for _, name := range cert.DNSNames {
			if strings.ToLower(name) == wildcard {
				found = true
				break
			}
		}
		if !found {
			return errors.New("证书不包含配置的节点泛域名")
		}
	}
	return nil
}

func parseImportedPanelCertificate(certPEM, keyPEM []byte, settings PanelSettings) (*issuedPanelCertificate, error) {
	if len(certPEM) == 0 || len(certPEM) > maxPanelCertificateBytes || len(keyPEM) == 0 || len(keyPEM) > maxPanelPrivateKeyBytes {
		return nil, errors.New("证书或私钥为空、或文件过大")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, errors.New("无法读取证书与私钥，请检查 PEM 格式、私钥是否加密以及两者是否匹配")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, errors.New("证书内容无效")
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errors.New("证书尚未生效或已经过期")
	}
	if err := certificateCoversPanel(leaf, settings.PanelDomain, settings.RouteDomain); err != nil {
		return nil, err
	}
	pair.Leaf = leaf
	return &issuedPanelCertificate{certPEM: certPEM, keyPEM: keyPEM, certificate: pair}, nil
}

func readPanelCertificateFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return nil, errors.New("证书与私钥路径必须是服务器上的绝对路径")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("证书路径必须指向可读取且大小合适的普通文件")
	}
	// Symlinks are allowed for certificate managers such as certbot. The opened
	// target must be a regular, bounded file readable by the service account.
	file, err := os.Open(path) // #nosec G304 G703 -- authenticated administrator-selected source, read-only, bounded and never returned to the caller.
	if err != nil {
		return nil, errors.New("无法读取证书文件，请检查路径和服务账户的读取权限")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("证书路径必须指向大小合适的普通文件")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("读取证书文件失败或文件过大")
	}
	return data, nil
}

func importPanelCertificate(db *DB, manager *panelCertificateManager, source, certPath, keyPath string, certPEM, keyPEM []byte, activate bool) error {
	if manager == nil || !manager.operationMu.TryLock() {
		return errCertificateIssuanceBusy
	}
	defer manager.operationMu.Unlock()
	settings, err := db.PanelSettings()
	if err != nil {
		return err
	}
	return importPanelCertificateLocked(db, manager, settings, source, certPath, keyPath, certPEM, keyPEM, activate)
}

// The caller holds operationMu across validation, installation and persistence.
func importPanelCertificateLocked(db *DB, manager *panelCertificateManager, settings PanelSettings, source, certPath, keyPath string, certPEM, keyPEM []byte, activate bool) error {
	var err error
	if !settings.Configured || settings.PanelDomain == "" || settings.ListenPort == 0 {
		return errors.New("请先保存面板域名和监听端口")
	}
	switch source {
	case "path":
		certPath, keyPath = strings.TrimSpace(certPath), strings.TrimSpace(keyPath)
		if certPath == keyPath {
			return errors.New("证书与私钥应使用不同的文件")
		}
		certPEM, err = readPanelCertificateFile(certPath, maxPanelCertificateBytes)
		if err != nil {
			return err
		}
		keyPEM, err = readPanelCertificateFile(keyPath, maxPanelPrivateKeyBytes)
		if err != nil {
			return err
		}
	case "upload":
		certPath, keyPath = "", ""
	default:
		return errors.New("请选择服务器路径或上传文件")
	}
	issued, err := parseImportedPanelCertificate(certPEM, keyPEM, settings)
	if err != nil {
		return err
	}
	backup, err := manager.backupInstalledFiles()
	if err != nil {
		return err
	}
	if err = manager.install(issued, false); err != nil {
		_ = manager.restoreInstalledFiles(backup)
		return err
	}
	if err = db.setPanelCertificateSource(source, certPath, keyPath); err != nil {
		_ = manager.restoreInstalledFiles(backup)
		return err
	}
	if activate {
		updated := settings
		updated.CertificateSource, updated.CertificatePath, updated.PrivateKeyPath = source, certPath, keyPath
		updated.TLSEnabled = true
		manager.activateForSettings(issued, updated)
	}
	updated, err := db.PanelSettings()
	if err == nil {
		next := time.Now().Add(panelCertificateRenewalInterval)
		if source == "path" {
			next = time.Now().Add(time.Minute)
		}
		db.recordPanelRenewal(updated, true, nil, next)
	}
	return nil
}

func (a *App) handlePanelCertificateImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.jsonErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		Source          string `json:"source"`
		CertificatePath string `json:"certificate_path"`
		PrivateKeyPath  string `json:"private_key_path"`
		CertificatePEM  string `json:"certificate_pem"`
		PrivateKeyPEM   string `json:"private_key_pem"` // #nosec G117 -- request-only secret, persisted to a private file and never returned or logged.
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		a.jsonErr(w, 400, "证书请求无效或过大")
		return
	}
	if a.panelCertificates == nil {
		a.jsonErr(w, http.StatusServiceUnavailable, "证书管理不可用")
		return
	}
	unlock, ok := a.beginPanelConfiguration(w)
	if !ok {
		return
	}
	defer unlock()
	settings, err := a.db.PanelSettings()
	if err != nil {
		a.jsonErr(w, 500, "无法读取 TLS 设置")
		return
	}
	tlsCandidate := settings
	tlsCandidate.TLSEnabled = true
	validation := a.validatePanelEndpoint(r.Context(), settings, tlsCandidate, r)
	if !validation.CanSave {
		a.panelEndpointValidationError(w, validation, false)
		return
	}
	activate := a.panelTLSEnabled && settings.PanelDomain == a.panelHost && settings.RouteDomain == a.routeDomain && settings.ListenPort == a.panelListenPort
	err = importPanelCertificateLocked(a.db, a.panelCertificates, settings, req.Source, req.CertificatePath, req.PrivateKeyPath, []byte(req.CertificatePEM), []byte(req.PrivateKeyPEM), activate)
	if err != nil {
		code := 400
		if errors.Is(err, errCertificateIssuanceBusy) {
			code = 409
		}
		a.jsonErr(w, code, err.Error())
		return
	}
	settings, err = a.db.PanelSettings()
	if err != nil {
		a.jsonErr(w, 500, "无法读取 TLS 设置")
		return
	}
	a.jsonOK(w, a.panelCertificateStatus(settings))
}

func reloadExternalPanelCertificate(db *DB, manager *panelCertificateManager) (operationErr error) {
	if db == nil || manager == nil {
		return nil
	}
	if !manager.operationMu.TryLock() {
		return errCertificateIssuanceBusy
	}
	defer manager.operationMu.Unlock()
	saved, err := db.PanelSettings()
	if err != nil {
		return err
	}
	settings := manager.maintenanceSettings(saved)
	if settings.CertificateSource != "path" || !settings.TLSEnabled {
		return nil
	}
	renewed := false
	defer func() {
		if operationErr != nil {
			operationErr = safePanelCertificateError(operationErr, settings)
		}
		db.recordPanelRenewal(settings, renewed, operationErr, time.Now().Add(time.Minute))
	}()
	certPEM, err := readPanelCertificateFile(settings.CertificatePath, maxPanelCertificateBytes)
	if err != nil {
		return err
	}
	keyPEM, err := readPanelCertificateFile(settings.PrivateKeyPath, maxPanelPrivateKeyBytes)
	if err != nil {
		return err
	}
	issued, err := parseImportedPanelCertificate(certPEM, keyPEM, settings)
	if err != nil {
		return err
	}
	current, currentErr := manager.maintenanceCertificate()
	// Compare against the served certificate, not a file for pending settings.
	if currentErr == nil && current != nil && samePanelCertificateChain(current, &issued.certificate) {
		return nil
	}
	if err := manager.installMaintainedCertificate(issued, settings, saved); err != nil {
		return fmt.Errorf("重新加载路径证书失败: %w", err)
	}
	renewed = true
	return nil
}
