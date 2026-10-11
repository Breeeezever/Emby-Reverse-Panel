package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Reporting destinations and administrators are deliberately independent: a
// group receiving a report never gains control over the panel.
type TelegramBotSettings struct {
	Enabled         bool    `json:"enabled"`
	AdminIDs        []int64 `json:"admin_ids"`
	PublicURL       string  `json:"public_url"`
	Status          string  `json:"status,omitempty"`
	StatusMessage   string  `json:"status_message,omitempty"`
	Revision        int64   `json:"-"`
	Offset          int64   `json:"-"`
	AuthorizedSince int64   `json:"-"`
}

func normalizeTelegramBotSettings(s TelegramBotSettings) (TelegramBotSettings, error) {
	if len(s.AdminIDs) > 10 {
		return s, fmt.Errorf("最多配置 10 个 Telegram 管理员")
	}
	s.AdminIDs = append([]int64{}, s.AdminIDs...)
	for _, id := range s.AdminIDs {
		if id <= 0 || id > 4503599627370495 {
			return s, fmt.Errorf("管理员 ID 必须是正整数 Telegram 用户 ID，不能填写群组 ID")
		}
	}
	slices.Sort(s.AdminIDs)
	s.AdminIDs = slices.Compact(s.AdminIDs)
	s.PublicURL = strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	if s.PublicURL != "" {
		u, err := url.Parse(s.PublicURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || strings.ContainsAny(s.PublicURL, "\r\n\t ") {
			return s, fmt.Errorf("面板外部地址请填写完整的 HTTP/HTTPS 地址和实际端口，不包含路径、账号或参数")
		}
		if u.Port() != "" {
			p, e := strconv.Atoi(u.Port())
			if e != nil || p < 1 || p > 65535 {
				return s, fmt.Errorf("面板外部地址端口必须在 1–65535 之间")
			}
		}
		if len(s.PublicURL) > 512 {
			return s, fmt.Errorf("面板外部地址过长")
		}
	}
	if s.Enabled && (len(s.AdminIDs) == 0 || s.PublicURL == "") {
		return s, fmt.Errorf("启用 Bot 管理前，请填写管理员 ID 和面板外部地址")
	}
	return s, nil
}

func (d *DB) telegramBotSettings() (TelegramBotSettings, error) {
	var s TelegramBotSettings
	var admins string
	err := d.db.QueryRow(`SELECT enabled,admin_ids,public_url,revision,update_offset,authorized_since FROM telegram_bot_settings WHERE id=1`).Scan(&s.Enabled, &admins, &s.PublicURL, &s.Revision, &s.Offset, &s.AuthorizedSince)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal([]byte(admins), &s.AdminIDs); err != nil {
		return s, err
	}
	return s, nil
}

func saveTelegramBotSettings(tx *sql.Tx, s *TelegramBotSettings, tokenChanged bool) error {
	if s == nil && !tokenChanged {
		return nil
	}
	if s != nil {
		normalized, err := normalizeTelegramBotSettings(*s)
		if err != nil {
			return err
		}
		admins, _ := json.Marshal(normalized.AdminIDs)
		_, err = tx.Exec(`UPDATE telegram_bot_settings SET revision=revision+CASE WHEN enabled<>? OR admin_ids<>? OR public_url<>? OR ? THEN 1 ELSE 0 END,
   update_offset=CASE WHEN enabled<>? OR admin_ids<>? OR public_url<>? OR ? THEN -1 ELSE update_offset END,
   authorized_since=CASE WHEN enabled<>? OR admin_ids<>? OR public_url<>? OR ? THEN ? ELSE authorized_since END,
   enabled=?,admin_ids=?,public_url=? WHERE id=1`, normalized.Enabled, string(admins), normalized.PublicURL, tokenChanged,
			normalized.Enabled, string(admins), normalized.PublicURL, tokenChanged,
			normalized.Enabled, string(admins), normalized.PublicURL, tokenChanged, time.Now().Unix(),
			normalized.Enabled, string(admins), normalized.PublicURL)
		return err
	}
	_, err := tx.Exec(`UPDATE telegram_bot_settings SET revision=revision+1,update_offset=-1,authorized_since=? WHERE id=1`, time.Now().Unix())
	return err
}
