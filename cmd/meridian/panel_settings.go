package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	panelSettingsRowID           = 1
	panelDomainSourceEnvironment = "environment"
	panelDomainSourceDatabase    = "database"
)

type PanelSettings struct {
	PanelDomainSource   string `json:"-"`
	CertificateSource   string `json:"certificate_source"`
	CertificatePath     string `json:"certificate_path,omitempty"`
	PrivateKeyPath      string `json:"private_key_path,omitempty"`
	PanelDomain         string `json:"panel_domain"`
	RouteDomain         string `json:"route_domain"`
	ListenPort          int    `json:"listen_port"`
	TLSEnabled          bool   `json:"tls_enabled"`
	Configured          bool   `json:"-"`
	ACMEEmail           string `json:"-"`
	ACMEDNSProvider     string `json:"-"`
	ACMETokenCiphertext string `json:"-"`
	ACMEStaging         bool   `json:"-"`
}

// normalizeWildcardDomain accepts the UI form (*.example.com) and stores the
// DNS suffix without the wildcard marker so routing code can continue to build
// concrete hosts such as movie.example.com.
func normalizeWildcardDomain(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "*.") {
		value = strings.TrimPrefix(value, "*.")
	}
	return normalizeRouteDomain(value)
}

func normalizeManagedPanelPrefix(panelPrefix, wildcardDomain string) (PanelSettings, error) {
	if !strings.HasPrefix(strings.TrimSpace(wildcardDomain), "*.") {
		return PanelSettings{}, errors.New("节点泛域名必须以 *. 开头，例如 *.example.com")
	}
	routeDomain, err := normalizeWildcardDomain(wildcardDomain)
	if err != nil {
		return PanelSettings{}, fmt.Errorf("节点泛域名无效: %w", err)
	}
	prefix, err := normalizeRoutePrefix(panelPrefix)
	if err != nil {
		return PanelSettings{}, fmt.Errorf("面板访问域名前缀无效: %w", err)
	}
	return normalizeManagedPanelSettings(prefix+"."+routeDomain, routeDomain)
}

func panelPrefixForSettings(settings PanelSettings) string {
	if prefix, ok := routePrefixFromConfiguredHost(settings.PanelDomain, settings.RouteDomain); ok {
		return prefix
	}
	return ""
}

func wildcardDomainForSettings(settings PanelSettings) string {
	if settings.RouteDomain == "" {
		return ""
	}
	return "*." + settings.RouteDomain
}

func scanPanelSettings(scanner interface{ Scan(...any) error }) (PanelSettings, error) {
	var settings PanelSettings
	var tlsEnabled, configured, acmeStaging int
	err := scanner.Scan(
		&settings.PanelDomain, &settings.RouteDomain, &settings.ListenPort, &tlsEnabled, &configured,
		&settings.ACMEEmail, &settings.ACMEDNSProvider, &settings.ACMETokenCiphertext, &acmeStaging, &settings.CertificateSource, &settings.CertificatePath, &settings.PrivateKeyPath, &settings.PanelDomainSource,
	)
	settings.TLSEnabled = tlsEnabled != 0
	settings.Configured = configured != 0
	settings.ACMEStaging = acmeStaging != 0
	return settings, err
}

func (d *DB) PanelSettings() (PanelSettings, error) {
	if d == nil || d.db == nil {
		return PanelSettings{}, errors.New("panel settings database is unavailable")
	}
	return scanPanelSettings(d.db.QueryRow(`
		SELECT panel_domain, route_domain, listen_port, tls_enabled, configured,
			acme_email, acme_dns_provider, acme_token_ciphertext, acme_staging, certificate_source, certificate_path, private_key_path, panel_domain_source
		FROM panel_settings WHERE id=?`, panelSettingsRowID))
}

func normalizeStoredPanelSettings(panelDomain, routeDomain string, tlsEnabled bool) (PanelSettings, error) {
	panelDomain, err := normalizePublicHost(panelDomain)
	if err != nil {
		return PanelSettings{}, fmt.Errorf("invalid panel domain: %w", err)
	}
	routeDomain, err = normalizeRouteDomain(routeDomain)
	if err != nil {
		return PanelSettings{}, fmt.Errorf("invalid route domain: %w", err)
	}
	return PanelSettings{
		PanelDomain: panelDomain,
		RouteDomain: routeDomain,
		TLSEnabled:  tlsEnabled,
		Configured:  panelDomain != "" || routeDomain != "" || tlsEnabled,
	}, nil
}

func normalizeManagedPanelSettings(panelDomain, routeDomain string) (PanelSettings, error) {
	settings, err := normalizeStoredPanelSettings(panelDomain, routeDomain, true)
	if err != nil {
		return PanelSettings{}, err
	}
	if settings.PanelDomain == "" || settings.RouteDomain == "" {
		return PanelSettings{}, errors.New("面板访问域名和节点基础域名不能为空")
	}
	suffix := "." + settings.RouteDomain
	if !strings.HasSuffix(settings.PanelDomain, suffix) {
		return PanelSettings{}, errors.New("面板访问域名必须属于节点基础域名")
	}
	label := strings.TrimSuffix(settings.PanelDomain, suffix)
	if label == "" || strings.Contains(label, ".") {
		return PanelSettings{}, errors.New("面板访问域名必须是节点基础域名下的一层子域名")
	}
	if _, err := normalizeRoutePrefix(label); err != nil {
		return PanelSettings{}, fmt.Errorf("面板访问域名前缀无效: %w", err)
	}
	settings.Configured = true
	settings.TLSEnabled = true
	return settings, nil
}

func validatePanelListenPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("监听端口必须在 1 到 65535 之间")
	}
	return nil
}

// legacyPanelDomainSource recognizes the old, domain-only HTTP bootstrap used
// by external reverse proxies. Older versions did not record provenance. Keep
// existing route/TLS/certificate configurations under database control rather
// than removing them merely because startup environment values are absent.
func legacyPanelDomainSource(settings PanelSettings) string {
	if !settings.Configured || (!settings.TLSEnabled && settings.RouteDomain == "" &&
		settings.ACMEEmail == "" && settings.ACMETokenCiphertext == "" &&
		(settings.CertificateSource == "" || settings.CertificateSource == "acme") &&
		settings.CertificatePath == "" && settings.PrivateKeyPath == "") {
		return panelDomainSourceEnvironment
	}
	return panelDomainSourceDatabase
}

func (d *DB) BootstrapPanelSettings(panelDomain, routeDomain string, tlsEnabled bool, listenPort int) (PanelSettings, error) {
	if err := validatePanelListenPort(listenPort); err != nil {
		return PanelSettings{}, err
	}
	current, err := d.PanelSettings()
	if err != nil {
		return PanelSettings{}, err
	}
	candidate := current
	if candidate.ListenPort == 0 {
		candidate.ListenPort = listenPort
	}
	if !current.Configured {
		initial, err := normalizeStoredPanelSettings(panelDomain, routeDomain, tlsEnabled)
		if err != nil {
			return PanelSettings{}, err
		}
		candidate.PanelDomain = initial.PanelDomain
		candidate.RouteDomain = initial.RouteDomain
		candidate.TLSEnabled = initial.TLSEnabled
		candidate.PanelDomainSource = panelDomainSourceEnvironment
		if initial.TLSEnabled {
			candidate.PanelDomainSource = panelDomainSourceDatabase
		}
	} else {
		if candidate.PanelDomainSource == "" || candidate.PanelDomainSource == "legacy" {
			candidate.PanelDomainSource = legacyPanelDomainSource(current)
		}
		if candidate.PanelDomainSource == panelDomainSourceEnvironment {
			// Only PANEL_DOMAIN is synchronized. Removing it removes the old
			// Host restriction without changing ports, route hosts or TLS.
			candidate.PanelDomain, err = normalizePublicHost(panelDomain)
			if err != nil {
				return PanelSettings{}, fmt.Errorf("invalid panel domain: %w", err)
			}
		}
	}
	candidate.Configured = candidate.PanelDomain != "" || candidate.RouteDomain != "" || candidate.TLSEnabled
	if candidate == current {
		return current, nil
	}
	if candidate.PanelDomain != "" && candidate.PanelDomain != current.PanelDomain {
		var conflicts bool
		if err := d.db.QueryRow("SELECT EXISTS(SELECT 1 FROM sites WHERE public_host = ? COLLATE NOCASE)", candidate.PanelDomain).Scan(&conflicts); err != nil {
			return PanelSettings{}, err
		}
		if conflicts {
			return PanelSettings{}, errors.New("面板访问域名与站点冲突")
		}
	}
	_, err = d.db.Exec(`
		UPDATE panel_settings
		SET panel_domain=?, route_domain=?, listen_port=?, tls_enabled=?, configured=?, panel_domain_source=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`,
		candidate.PanelDomain, candidate.RouteDomain, candidate.ListenPort, sqliteBool(candidate.TLSEnabled), sqliteBool(candidate.Configured), candidate.PanelDomainSource, panelSettingsRowID)
	if err != nil {
		return PanelSettings{}, err
	}
	return d.PanelSettings()
}

func routePrefixFromConfiguredHost(host, routeDomain string) (string, bool) {
	if host == "" || routeDomain == "" {
		return "", false
	}
	suffix := "." + routeDomain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	prefix := strings.TrimSuffix(host, suffix)
	if prefix == "" || strings.Contains(prefix, ".") {
		return "", false
	}
	normalized, err := normalizeRoutePrefix(prefix)
	return normalized, err == nil && normalized == prefix
}

type panelHostMigration struct {
	SiteID int64
	Host   string
}

type panelSettingsQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

// Preview and commit use the same routing plan. The commit calls this inside
// its transaction again so a stale preview cannot allow conflicting sites.
func planPanelHostMigrations(queryer panelSettingsQueryer, current, candidate PanelSettings) ([]panelHostMigration, error) {
	rows, err := queryer.Query("SELECT id, public_host, ingress_mode, listen_port FROM sites ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var migrations []panelHostMigration
	assigned := make(map[string]int64)
	for rows.Next() {
		var siteID int64
		var host, mode string
		var port int
		if err := rows.Scan(&siteID, &host, &mode, &port); err != nil {
			return nil, err
		}
		if ingressUsesPort(mode) && port == candidate.ListenPort {
			return nil, fmt.Errorf("监听端口 %d 已分配给站点 %d，请选择其他端口", port, siteID)
		}
		if host == "" {
			continue
		}
		host, err = normalizePublicHost(host)
		if err != nil {
			return nil, fmt.Errorf("站点 %d 的访问域名无效: %w", siteID, err)
		}
		newHost := host
		if current.RouteDomain != candidate.RouteDomain {
			if prefix, ok := routePrefixFromConfiguredHost(host, current.RouteDomain); ok {
				newHost, err = routeHostForPrefix(prefix, candidate.RouteDomain)
				if err != nil {
					return nil, err
				}
			}
		}
		if newHost == candidate.PanelDomain {
			return nil, fmt.Errorf("面板访问域名与站点 %d 冲突", siteID)
		}
		key := strings.ToLower(newHost)
		if otherID, exists := assigned[key]; exists && otherID != siteID {
			return nil, fmt.Errorf("修改节点基础域名后站点 %d 与站点 %d 的访问域名冲突", otherID, siteID)
		}
		assigned[key] = siteID
		if newHost != host {
			migrations = append(migrations, panelHostMigration{SiteID: siteID, Host: newHost})
		}
	}
	return migrations, rows.Err()
}

func (d *DB) SaveManagedPanelSettings(panelDomain, routeDomain string, listenPort int, tlsEnabled bool) (PanelSettings, int, error) {
	candidate, err := normalizeManagedPanelSettings(panelDomain, routeDomain)
	if err != nil {
		return PanelSettings{}, 0, err
	}
	return d.savePanelEndpointSettings(candidate, listenPort, tlsEnabled)
}

func (d *DB) SavePanelEndpointSettings(panelDomain, routeDomain string, listenPort int, tlsEnabled bool) (PanelSettings, int, error) {
	candidate, err := normalizeStoredPanelSettings(panelDomain, routeDomain, tlsEnabled)
	if err != nil {
		return PanelSettings{}, 0, err
	}
	if candidate.PanelDomain == "" {
		return PanelSettings{}, 0, errors.New("面板域名不能为空")
	}
	return d.savePanelEndpointSettings(candidate, listenPort, tlsEnabled)
}

func (d *DB) savePanelEndpointSettings(candidate PanelSettings, listenPort int, tlsEnabled bool) (PanelSettings, int, error) {
	if err := validatePanelListenPort(listenPort); err != nil {
		return PanelSettings{}, 0, err
	}
	candidate.ListenPort = listenPort
	candidate.TLSEnabled = tlsEnabled
	tx, err := d.db.Begin()
	if err != nil {
		return PanelSettings{}, 0, err
	}
	defer tx.Rollback()

	current, err := scanPanelSettings(tx.QueryRow(`
		SELECT panel_domain, route_domain, listen_port, tls_enabled, configured,
			acme_email, acme_dns_provider, acme_token_ciphertext, acme_staging, certificate_source, certificate_path, private_key_path, panel_domain_source
		FROM panel_settings WHERE id=?`, panelSettingsRowID))
	if err != nil {
		return PanelSettings{}, 0, err
	}
	migrations, err := planPanelHostMigrations(tx, current, candidate)
	if err != nil {
		return PanelSettings{}, 0, err
	}
	for _, migration := range migrations {
		if _, err := tx.Exec("UPDATE sites SET public_host=?, updated_at=CURRENT_TIMESTAMP WHERE id=?", migration.Host, migration.SiteID); err != nil {
			return PanelSettings{}, 0, err
		}
	}
	if _, err := tx.Exec(`
		UPDATE panel_settings
		SET panel_domain=?, route_domain=?, listen_port=?, tls_enabled=?, configured=1, panel_domain_source='database', updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, candidate.PanelDomain, candidate.RouteDomain, candidate.ListenPort, sqliteBool(candidate.TLSEnabled), panelSettingsRowID); err != nil {
		return PanelSettings{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return PanelSettings{}, 0, err
	}
	persisted, err := d.PanelSettings()
	if err != nil {
		return PanelSettings{}, 0, err
	}
	return persisted, len(migrations), nil
}

func (d *DB) SavePanelACMECredentials(email, provider, tokenCiphertext string, staging bool) error {
	if d == nil || d.db == nil {
		return errors.New("panel settings database is unavailable")
	}
	_, err := d.db.Exec(`
		UPDATE panel_settings
		SET acme_email=?, acme_dns_provider=?, acme_token_ciphertext=?, acme_staging=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, email, provider, tokenCiphertext, sqliteBool(staging), panelSettingsRowID)
	return err
}

// SetPanelTLSEnabled changes only the active panel transport. Certificate and
// ACME files remain intact so HTTPS can be re-enabled or renewed later.
func (d *DB) SetPanelTLSEnabled(enabled bool) error {
	if d == nil || d.db == nil {
		return errors.New("panel settings database is unavailable")
	}
	_, err := d.db.Exec(`
		UPDATE panel_settings
		SET tls_enabled=?, panel_domain_source=CASE WHEN ? THEN 'database' ELSE panel_domain_source END, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, sqliteBool(enabled), sqliteBool(enabled), panelSettingsRowID)
	return err
}

func (d *DB) restorePanelSettings(settings PanelSettings) error {
	if d == nil || d.db == nil {
		return errors.New("panel settings database is unavailable")
	}
	_, err := d.db.Exec(`
		UPDATE panel_settings
		SET panel_domain=?, route_domain=?, listen_port=?, tls_enabled=?, configured=?,
			acme_email=?, acme_dns_provider=?, acme_token_ciphertext=?, acme_staging=?, certificate_source=?, certificate_path=?, private_key_path=?, panel_domain_source=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, settings.PanelDomain, settings.RouteDomain, settings.ListenPort, sqliteBool(settings.TLSEnabled), sqliteBool(settings.Configured),
		settings.ACMEEmail, settings.ACMEDNSProvider, settings.ACMETokenCiphertext, sqliteBool(settings.ACMEStaging), settings.CertificateSource, settings.CertificatePath, settings.PrivateKeyPath, settings.PanelDomainSource, panelSettingsRowID)
	return err
}

func (d *DB) setPanelCertificateSource(source, certPath, keyPath string) error {
	_, err := d.db.Exec(`UPDATE panel_settings SET certificate_source=?, certificate_path=?, private_key_path=?, tls_enabled=1, panel_domain_source='database', updated_at=CURRENT_TIMESTAMP WHERE id=1`, source, certPath, keyPath)
	return err
}
