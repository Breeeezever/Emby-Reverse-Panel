package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const siteAccountCipherPrefix = "v1:"

type SiteAccount struct {
	ID           int64  `json:"id"`
	SiteID       int64  `json:"site_id"`
	SiteName     string `json:"site_name,omitempty"`
	AccountName  string `json:"account_name"`
	Username     string `json:"username"`
	ExpiresAt    string `json:"expires_at"`
	Note         string `json:"note"`
	ReminderDays *int   `json:"reminder_days"`
	PasswordSet  bool   `json:"password_configured"`
	SecuritySet  bool   `json:"security_password_configured"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
}

type storedSiteAccount struct {
	SiteAccount
	PasswordCiphertext         string
	SecurityPasswordCiphertext string
}

type SiteAccountInput struct {
	AccountName      string `json:"account_name"`
	Username         string `json:"username"`
	Password         string `json:"password,omitempty"`          // #nosec G117 -- explicitly authorized account memo/export.
	SecurityPassword string `json:"security_password,omitempty"` // #nosec G117 -- explicitly authorized account memo/export.
	ExpiresAt        string `json:"expires_at"`
	Note             string `json:"note"`
	ReminderDays     *int   `json:"reminder_days,omitempty"`
	ClearReminder    bool   `json:"clear_reminder,omitempty"`
	ClearNote        bool   `json:"clear_note,omitempty"`
	ClearPassword    bool   `json:"clear_password,omitempty"`
	ClearSecurity    bool   `json:"clear_security_password,omitempty"`
	ClearExpiry      bool   `json:"clear_expires_at,omitempty"`
}

func siteAccountKey(secret []byte) []byte {
	sum := sha256.Sum256(secret)
	return sum[:]
}

func encryptSiteAccountSecret(value string) (string, error) {
	return encryptSiteAccountSecretWithSecret(value, activeStoredCredentialSecret())
}

func encryptSiteAccountSecretWithSecret(value string, secret []byte) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	block, err := aes.NewCipher(siteAccountKey(secret))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate account secret nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(value), []byte("meridian-site-account:v1"))
	return siteAccountCipherPrefix + base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func decryptSiteAccountSecret(ciphertext string) (string, error) {
	return decryptSiteAccountSecretWithSecrets(ciphertext, storedCredentialSecrets())
}

func decryptSiteAccountSecretWithSecrets(ciphertext string, secrets [][]byte) (string, error) {
	if strings.TrimSpace(ciphertext) == "" {
		return "", nil
	}
	if !strings.HasPrefix(ciphertext, siteAccountCipherPrefix) {
		return "", errors.New("invalid account secret ciphertext")
	}
	encoded := strings.TrimPrefix(ciphertext, siteAccountCipherPrefix)
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return "", err
	}
	if base64.RawURLEncoding.EncodeToString(payload) != encoded {
		return "", errors.New("invalid account secret ciphertext")
	}
	for _, secret := range secrets {
		block, blockErr := aes.NewCipher(siteAccountKey(secret))
		if blockErr != nil {
			continue
		}
		gcm, gcmErr := cipher.NewGCM(block)
		if gcmErr != nil || len(payload) < gcm.NonceSize()+gcm.Overhead() {
			continue
		}
		plain, openErr := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], []byte("meridian-site-account:v1"))
		if openErr == nil {
			return string(plain), nil
		}
	}
	return "", errors.New("unable to decrypt account secret")
}

func normalizeSiteAccountInput(input SiteAccountInput, existing *storedSiteAccount) (SiteAccountInput, error) {
	input.AccountName = strings.TrimSpace(input.AccountName)
	input.Username = strings.TrimSpace(input.Username)
	// Passwords are opaque values. Preserve intentional leading/trailing spaces.
	input.ExpiresAt = strings.TrimSpace(input.ExpiresAt)
	input.Note = strings.TrimSpace(input.Note)
	if input.ReminderDays != nil && (*input.ReminderDays < 0 || *input.ReminderDays > 3650) {
		return SiteAccountInput{}, errors.New("提前提醒天数应为 0–3650，0 表示到期时提醒")
	}
	if input.ExpiresAt != "" {
		if len(input.ExpiresAt) > 64 {
			return SiteAccountInput{}, errors.New("到期时间过长")
		}
		if _, err := parseSiteAccountExpiry(input.ExpiresAt); err != nil {
			return SiteAccountInput{}, errors.New("到期时间格式应为日期、ISO 时间或常见日期时间")
		}
	}
	if existing != nil {
		if input.AccountName == "" {
			input.AccountName = existing.AccountName
		}
		if input.Username == "" {
			input.Username = existing.Username
		}
		if input.ExpiresAt == "" && !input.ClearExpiry {
			input.ExpiresAt = existing.ExpiresAt
		}
		if input.Note == "" && !input.ClearNote {
			input.Note = existing.Note
		}
		if input.ReminderDays == nil && !input.ClearReminder {
			input.ReminderDays = existing.ReminderDays
		}
		if input.Password == "" && !input.ClearPassword {
			input.Password = "\x00__KEEP__\x00"
		}
		if input.SecurityPassword == "" && !input.ClearSecurity {
			input.SecurityPassword = "\x00__KEEP__\x00"
		}
	}
	if input.AccountName == "" && input.Username != "" {
		input.AccountName = input.Username
	}
	if input.Username == "" && input.AccountName != "" {
		input.Username = input.AccountName
	}
	if input.AccountName == "" || input.Username == "" {
		return SiteAccountInput{}, errors.New("填写账号信息时，账号名称和用户名至少需要一个")
	}
	if len(input.AccountName) > 128 || len(input.Username) > 128 || len(input.Note) > 2000 {
		return SiteAccountInput{}, errors.New("账号名称、用户名或备注过长")
	}
	return input, nil
}

func parseSiteAccountExpiry(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{
		"2006-01-02",
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	} {
		parsed, err := time.ParseInLocation(layout, value, time.Local)
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("invalid account expiry")
}

func (d *DB) listSiteAccounts(siteID int64) ([]storedSiteAccount, error) {
	rows, err := d.db.Query(`SELECT id, site_id, account_name, username, password_ciphertext, security_password_ciphertext, expires_at, note, reminder_days, created_at, updated_at FROM site_accounts WHERE site_id=? ORDER BY id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []storedSiteAccount
	for rows.Next() {
		var item storedSiteAccount
		if err := rows.Scan(&item.ID, &item.SiteID, &item.AccountName, &item.Username, &item.PasswordCiphertext, &item.SecurityPasswordCiphertext, &item.ExpiresAt, &item.Note, &item.ReminderDays, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.PasswordSet = item.PasswordCiphertext != ""
		item.SecuritySet = item.SecurityPasswordCiphertext != ""
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if result == nil {
		result = []storedSiteAccount{}
	}
	return result, nil
}

func publicSiteAccount(item storedSiteAccount, siteName string) SiteAccount {
	item.SiteAccount.SiteName = siteName
	item.SiteAccount.PasswordSet = item.PasswordCiphertext != ""
	item.SiteAccount.SecuritySet = item.SecurityPasswordCiphertext != ""
	return item.SiteAccount
}

func (d *DB) getSiteAccount(siteID, accountID int64) (*storedSiteAccount, error) {
	var item storedSiteAccount
	err := d.db.QueryRow(`SELECT id, site_id, account_name, username, password_ciphertext, security_password_ciphertext, expires_at, note, reminder_days, created_at, updated_at FROM site_accounts WHERE site_id=? AND id=?`, siteID, accountID).Scan(&item.ID, &item.SiteID, &item.AccountName, &item.Username, &item.PasswordCiphertext, &item.SecurityPasswordCiphertext, &item.ExpiresAt, &item.Note, &item.ReminderDays, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, err
	}
	item.PasswordSet = item.PasswordCiphertext != ""
	item.SecuritySet = item.SecurityPasswordCiphertext != ""
	return &item, nil
}

func (d *DB) saveSiteAccount(siteID int64, accountID int64, input SiteAccountInput) (*storedSiteAccount, error) {
	var existing *storedSiteAccount
	var err error
	if accountID > 0 {
		existing, err = d.getSiteAccount(siteID, accountID)
		if err != nil {
			return nil, err
		}
	}
	input, err = normalizeSiteAccountInput(input, existing)
	if err != nil {
		return nil, err
	}
	passwordCipher := ""
	securityCipher := ""
	if existing != nil {
		passwordCipher, securityCipher = existing.PasswordCiphertext, existing.SecurityPasswordCiphertext
	}
	if input.Password != "\x00__KEEP__\x00" {
		passwordCipher, err = encryptSiteAccountSecret(input.Password)
		if err != nil {
			return nil, err
		}
	}
	if input.SecurityPassword != "\x00__KEEP__\x00" {
		securityCipher, err = encryptSiteAccountSecret(input.SecurityPassword)
		if err != nil {
			return nil, err
		}
	}
	if accountID == 0 {
		result, err := d.db.Exec(`INSERT INTO site_accounts (site_id, account_name, username, password_ciphertext, security_password_ciphertext, expires_at, note, reminder_days) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, siteID, input.AccountName, input.Username, passwordCipher, securityCipher, input.ExpiresAt, input.Note, input.ReminderDays)
		if err != nil {
			return nil, err
		}
		accountID, err = result.LastInsertId()
		if err != nil {
			return nil, err
		}
	} else {
		_, err = d.db.Exec(`UPDATE site_accounts SET account_name=?, username=?, password_ciphertext=?, security_password_ciphertext=?, expires_at=?, note=?, reminder_days=?, updated_at=CURRENT_TIMESTAMP WHERE site_id=? AND id=?`, input.AccountName, input.Username, passwordCipher, securityCipher, input.ExpiresAt, input.Note, input.ReminderDays, siteID, accountID)
		if err != nil {
			return nil, err
		}
	}
	return d.getSiteAccount(siteID, accountID)
}

func accountPlaintext(item storedSiteAccount) (SiteAccountInput, error) {
	password, err := decryptSiteAccountSecret(item.PasswordCiphertext)
	if err != nil {
		return SiteAccountInput{}, err
	}
	security, err := decryptSiteAccountSecret(item.SecurityPasswordCiphertext)
	if err != nil {
		return SiteAccountInput{}, err
	}
	return SiteAccountInput{AccountName: item.AccountName, Username: item.Username, Password: password, SecurityPassword: security, ExpiresAt: item.ExpiresAt, Note: item.Note, ReminderDays: item.ReminderDays}, nil
}

// Only the authenticated account workspace returns cleartext memo fields.
// Reminder/status responses continue to use publicSiteAccount metadata.
type siteAccountMemo struct {
	SiteAccount
	Password         string `json:"password"`          // #nosec G117 -- authorized memo response.
	SecurityPassword string `json:"security_password"` // #nosec G117 -- authorized memo response.
}

func accountMemo(item storedSiteAccount, name string) (siteAccountMemo, error) {
	plain, err := accountPlaintext(item)
	if err != nil {
		return siteAccountMemo{}, err
	}
	return siteAccountMemo{publicSiteAccount(item, name), plain.Password, plain.SecurityPassword}, nil
}

func (a *App) handleSiteAccounts(w http.ResponseWriter, r *http.Request, siteID int64, tail string) {
	w.Header().Set("Cache-Control", "no-store")
	site, err := a.db.GetSite(siteID)
	if err != nil {
		a.jsonErr(w, http.StatusNotFound, "site not found")
		return
	}
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			items, err := a.db.listSiteAccounts(siteID)
			if err != nil {
				a.jsonErr(w, 500, "账号读取失败")
				return
			}
			result := make([]siteAccountMemo, 0, len(items))
			for _, item := range items {
				memo, err := accountMemo(item, site.Name)
				if err != nil {
					a.jsonErr(w, 500, "账号信息读取失败")
					return
				}
				result = append(result, memo)
			}
			a.jsonOK(w, result)
		case http.MethodPost:
			var input SiteAccountInput
			if err := decodeJSONBody(w, r, &input); err != nil {
				a.jsonErr(w, 400, "invalid request")
				return
			}
			item, err := a.db.saveSiteAccount(siteID, 0, input)
			if err != nil {
				a.jsonErr(w, 400, err.Error())
				return
			}
			memo, err := accountMemo(*item, site.Name)
			if err != nil {
				a.jsonErr(w, 500, "账号信息读取失败")
				return
			}
			a.jsonResponse(w, http.StatusCreated, memo)
		default:
			a.jsonErr(w, 405, "method not allowed")
		}
		return
	}
	accountID, err := strconv.ParseInt(strings.Trim(tail, "/"), 10, 64)
	if err != nil || accountID <= 0 {
		a.jsonErr(w, 400, "invalid account id")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var input SiteAccountInput
		if err := decodeJSONBody(w, r, &input); err != nil {
			a.jsonErr(w, 400, "invalid request")
			return
		}
		item, err := a.db.saveSiteAccount(siteID, accountID, input)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				a.jsonErr(w, 404, "account not found")
			} else {
				a.jsonErr(w, 400, err.Error())
			}
			return
		}
		memo, err := accountMemo(*item, site.Name)
		if err != nil {
			a.jsonErr(w, 500, "账号信息读取失败")
			return
		}
		a.jsonOK(w, memo)
	case http.MethodDelete:
		if _, err := a.db.getSiteAccount(siteID, accountID); err != nil {
			a.jsonErr(w, 404, "account not found")
			return
		}
		if _, err := a.db.db.Exec("DELETE FROM site_accounts WHERE site_id=? AND id=?", siteID, accountID); err != nil {
			a.jsonErr(w, 500, "account delete failed")
			return
		}
		a.jsonOK(w, map[string]bool{"deleted": true})
	case http.MethodGet:
		item, err := a.db.getSiteAccount(siteID, accountID)
		if err != nil {
			a.jsonErr(w, 404, "account not found")
			return
		}
		mode := strings.TrimSpace(r.URL.Query().Get("line_mode"))
		if mode == "" {
			mode = "multi"
		}
		if mode != "original" && mode != "proxy" && mode != "multi" {
			a.jsonErr(w, http.StatusBadRequest, "line_mode must be original, proxy or multi")
			return
		}
		text, err := a.accountClientText(*site, *item, mode)
		if err != nil {
			a.jsonErr(w, 500, "账号信息读取失败")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		a.jsonOK(w, map[string]string{"text": text})
	default:
		a.jsonErr(w, 405, "method not allowed")
	}
}

func (a *App) accountClientText(site Site, item storedSiteAccount, mode string) (string, error) {
	plain, err := accountPlaintext(item)
	if err != nil {
		return "", err
	}
	return a.renderAccountClientText(site, plain, mode, siteExportOptions{IncludeAccounts: true, IncludePasswords: true}), nil
}

func (a *App) siteProxyLines(site Site) []string {
	if settings, err := a.db.telegramBotSettings(); err == nil && settings.PublicURL != "" {
		if address, err := a.telegramSiteAddress(site, settings.PublicURL); err == nil {
			return []string{address}
		}
	}
	host := a.panelHost
	if site.PublicHost != "" {
		host = site.PublicHost
	}
	if host == "" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if a.panelTLSEnabled {
		scheme = "https"
	}
	if site.IngressMode == ingressModePath && site.PathPrefix != "" {
		return []string{scheme + "://" + host + "/" + strings.Trim(site.PathPrefix, "/")}
	}
	if site.IngressMode == ingressModeHost || site.IngressMode == ingressModeBoth {
		return []string{scheme + "://" + host}
	}
	port := strconv.Itoa(site.ListenPort)
	if (scheme == "http" && site.ListenPort == 80) || (scheme == "https" && site.ListenPort == 443) {
		port = ""
	}
	return []string{scheme + "://" + host + func() string {
		if port == "" {
			return ""
		}
		return ":" + port
	}()}
}

type siteImportRecord struct {
	Fields                     map[string]bool     `json:"-"`
	ID                         int64               `json:"-"`
	Name                       string              `json:"name"`
	TargetURL                  string              `json:"target_url"`
	ListenPort                 int                 `json:"listen_port"`
	PublicHost                 string              `json:"public_host"`
	PathPrefix                 string              `json:"path_prefix"`
	IngressMode                string              `json:"ingress_mode"`
	PrimaryLineName            string              `json:"primary_line_name"`
	PlaybackTargetURL          string              `json:"playback_target_url"`
	PlaybackMode               string              `json:"playback_mode"`
	MainVideoStreamMode        string              `json:"main_video_stream_mode"`
	FailoverTargets            []string            `json:"failover_targets"`
	FailoverLines              []FailoverLine      `json:"failover_lines"`
	StreamHosts                []string            `json:"stream_hosts"`
	UAMode                     string              `json:"ua_mode"`
	CustomUserAgent            string              `json:"custom_user_agent"`
	CustomClient               string              `json:"custom_client"`
	CustomVersion              string              `json:"custom_version"`
	ClientIPMode               string              `json:"client_ip_mode"`
	DynamicDiscoveryEnabled    bool                `json:"dynamic_discovery_enabled"`
	DynamicProfile             string              `json:"dynamic_profile"`
	DynamicDiscoverySources    []string            `json:"dynamic_discovery_sources"`
	DynamicDomainRules         []DynamicDomainRule `json:"dynamic_domain_rules"`
	DynamicAllowHTTPSDowngrade bool                `json:"dynamic_allow_https_downgrade"`
	PingCacheEnabled           bool                `json:"ping_cache_enabled"`
	ImageCacheEnabled          bool                `json:"image_cache_enabled"`
	ProgressCoalescingEnabled  bool                `json:"progress_coalescing_enabled"`
	AssetCacheEnabled          bool                `json:"asset_cache_enabled"`
	AssetCacheTTLSec           int                 `json:"asset_cache_ttl_sec"`
	AssetCacheMaxBytes         int64               `json:"asset_cache_max_bytes"`
	AssetCacheRules            string              `json:"asset_cache_rules"`
	TrafficQuota               int64               `json:"traffic_quota"`
	SpeedLimit                 int                 `json:"speed_limit"`
	Enabled                    bool                `json:"enabled"`
	Accounts                   []SiteAccountInput  `json:"accounts"`
}
type siteDataEnvelope struct {
	Version int                `json:"version"`
	Sites   []siteImportRecord `json:"sites"`
}

func (a *App) siteDataRecords(options ...siteExportOptions) ([]siteImportRecord, error) {
	opts := siteExportOptions{IncludeAccounts: true}
	if len(options) > 0 {
		opts = options[0]
	}
	sites, err := a.db.ListSites()
	if err != nil {
		return nil, err
	}
	result := make([]siteImportRecord, 0, len(sites))
	for _, site := range sites {
		plain := []SiteAccountInput{}
		if opts.IncludeAccounts {
			accounts, err := a.db.listSiteAccounts(site.ID)
			if err != nil {
				return nil, err
			}
			for _, item := range accounts {
				v := SiteAccountInput{AccountName: item.AccountName, Username: item.Username, ExpiresAt: item.ExpiresAt, Note: item.Note, ReminderDays: item.ReminderDays}
				if opts.IncludePasswords {
					v, err = accountPlaintext(item)
					if err != nil {
						return nil, err
					}
				}
				plain = append(plain, v)
			}
		}
		result = append(result, siteImportRecord{
			ID:   site.ID,
			Name: site.Name, TargetURL: site.TargetURL, ListenPort: site.ListenPort, PublicHost: site.PublicHost,
			PathPrefix: site.PathPrefix, IngressMode: site.IngressMode, PrimaryLineName: site.PrimaryLineName,
			PlaybackTargetURL: site.PlaybackTargetURL, PlaybackMode: site.PlaybackMode, MainVideoStreamMode: site.MainVideoStreamMode,
			FailoverTargets: site.FailoverTargetList, FailoverLines: site.FailoverLines, StreamHosts: site.StreamHostList, UAMode: site.UAMode,
			CustomUserAgent: site.CustomUserAgent, CustomClient: site.CustomClient, CustomVersion: site.CustomVersion,
			ClientIPMode: site.ClientIPMode, DynamicDiscoveryEnabled: site.DynamicDiscoveryEnabled, DynamicProfile: site.DynamicProfile,
			DynamicDiscoverySources: site.DynamicDiscoverySources, DynamicDomainRules: site.DynamicDomainRules,
			DynamicAllowHTTPSDowngrade: site.DynamicAllowHTTPSDowngrade, PingCacheEnabled: site.PingCacheEnabled,
			ImageCacheEnabled: site.ImageCacheEnabled, ProgressCoalescingEnabled: site.ProgressCoalescingEnabled,
			AssetCacheEnabled: site.AssetCacheEnabled, AssetCacheTTLSec: site.AssetCacheTTLSec,
			AssetCacheMaxBytes: site.AssetCacheMaxBytes, AssetCacheRules: site.AssetCacheRules,
			TrafficQuota: site.TrafficQuota, SpeedLimit: site.SpeedLimit,
			Enabled: site.Enabled, Accounts: plain,
		})
	}
	return result, nil
}

func importSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "site"
	}
	return value[:minInt(len(value), 24)]
}

var launchMessageField = regexp.MustCompile(`(?mi)(?:用户名称|账号名称|用户名)\s*[|：:]\s*` + "`?" + `([^` + "`" + `\r\n]+)` + "`?")
var launchPasswordField = regexp.MustCompile(`(?mi)用户密码\s*[|：:]\s*` + "`?" + `([^` + "`" + `\r\n]+)` + "`?")
var launchSecurityField = regexp.MustCompile(`(?mi)(?:安全密码|二次密码)\s*[|：:]\s*` + "`?" + `([^` + "`" + `\r\n]+)` + "`?")
var launchExpiryField = regexp.MustCompile(`(?mi)到期时间\s*[|：:]\s*` + "`?" + `([^` + "`" + `\r\n]+)` + "`?")
var launchURL = regexp.MustCompile(`https?://[A-Za-z0-9._~:/?#\[\]@!$&'*+,;=%-]+`)
var launchBoldLine = regexp.MustCompile(`(?mi)^\s*\*\*([^*\r\n]+)\*\*`)

func parseSiteMessageRecord(text string) (siteImportRecord, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return siteImportRecord{}, errors.New("请粘贴开服消息")
	}
	urls := launchURL.FindAllString(text, -1)
	if len(urls) == 0 {
		return siteImportRecord{}, errors.New("没有找到有效的回源地址")
	}
	name := "导入站点"
	if m := launchBoldLine.FindStringSubmatch(text); len(m) > 1 {
		name = strings.TrimSpace(m[1])
	}
	account := SiteAccountInput{}
	if m := launchMessageField.FindStringSubmatch(text); len(m) > 1 {
		account.AccountName = strings.TrimSpace(m[1])
		account.Username = account.AccountName
	}
	if m := launchPasswordField.FindStringSubmatch(text); len(m) > 1 {
		account.Password = strings.TrimSpace(m[1])
	}
	if m := launchSecurityField.FindStringSubmatch(text); len(m) > 1 {
		account.SecurityPassword = strings.TrimSpace(m[1])
	}
	if m := launchExpiryField.FindStringSubmatch(text); len(m) > 1 {
		account.ExpiresAt = strings.TrimSpace(m[1])
	}
	if account.AccountName == "" {
		if m := launchBoldLine.FindStringSubmatch(text); len(m) > 1 {
			words := strings.Fields(strings.ReplaceAll(m[1], "&#x20;", " "))
			if len(words) >= 3 {
				name, account.AccountName, account.Username, account.Password = words[0], words[1], words[1], words[2]
			} else if len(words) == 2 {
				name, account.AccountName, account.Username = words[0], words[1], words[1]
			}
		}
	}
	for index, rawURL := range urls {
		urls[index] = normalizeLaunchURL(rawURL)
	}
	validURLs := urls[:0]
	seenURLs := make(map[string]struct{}, len(urls))
	for _, value := range urls {
		if value != "" {
			if _, seen := seenURLs[value]; seen {
				continue
			}
			seenURLs[value] = struct{}{}
			validURLs = append(validURLs, value)
		}
	}
	urls = validURLs
	if len(urls) == 0 {
		return siteImportRecord{}, errors.New("没有找到有效的回源地址")
	}
	if name == "导入站点" {
		if u, err := url.Parse(urls[0]); err == nil {
			name = u.Hostname()
		}
	}
	record := siteImportRecord{Name: name, TargetURL: strings.TrimRight(urls[0], "，。)】"), IngressMode: ingressModePath, PathPrefix: importSlug(name), FailoverTargets: append([]string(nil), urls[1:]...), Accounts: []SiteAccountInput{account}}
	if account.AccountName == "" && account.Username == "" {
		record.Accounts = nil
	}
	return record, nil
}

func (a *App) handleSiteMessageParse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		a.jsonErr(w, 405, "method not allowed")
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		a.jsonErr(w, 400, "invalid request")
		return
	}
	record, err := parseSiteMessageRecord(req.Text)
	if err != nil {
		a.jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonOK(w, record)
}

func normalizeLaunchURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if cut := strings.IndexAny(raw, "（("); cut >= 0 {
		raw = raw[:cut]
	}
	raw = strings.TrimRight(raw, "，。；;）)]】>")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.String()
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *App) handleAccountReminders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		a.jsonErr(w, 405, "method not allowed")
		return
	}
	sites, err := a.db.ListSites()
	if err != nil {
		a.jsonErr(w, 500, "读取到期提醒失败")
		return
	}
	now := time.Now()
	result := make([]map[string]interface{}, 0)
	for _, site := range sites {
		items, _ := a.db.listSiteAccounts(site.ID)
		for _, item := range items {
			if item.ExpiresAt == "" {
				continue
			}
			parsed, err := parseSiteAccountExpiry(item.ExpiresAt)
			if err != nil {
				continue
			}
			duration := parsed.Sub(now)
			days := int(math.Ceil(duration.Hours() / 24))
			if duration < 0 {
				days = int(math.Floor(duration.Hours() / 24))
			}
			result = append(result, map[string]interface{}{"site_id": site.ID, "site_name": site.Name, "account": publicSiteAccount(item, site.Name), "days_left": days, "status": map[bool]string{true: "expired", false: "upcoming"}[parsed.Before(now)]})
		}
	}
	a.jsonOK(w, result)
}
