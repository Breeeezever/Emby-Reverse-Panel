package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (b *telegramBot) siteAccounts(ctx context.Context, chatID, siteID int64) error {
	site, err := b.app.db.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("站点不存在")
	}
	accounts, err := b.app.db.listSiteAccounts(siteID)
	if err != nil {
		return fmt.Errorf("读取账号失败")
	}
	keys := telegramKeyboard{}
	reminders := make([]string, 0)
	for _, account := range accounts {
		label := account.AccountName
		if label == "" {
			label = account.Username
		}
		keys = append(keys, []telegramButton{telegramButtonFor("编辑 "+truncateTelegramText(label, 42), fmt.Sprintf("account:%d:%d:edit", siteID, account.ID)), telegramButtonFor("删除", fmt.Sprintf("account:%d:%d:delete", siteID, account.ID))})
		if account.ExpiresAt != "" {
			if expiry, expiryErr := parseSiteAccountExpiry(account.ExpiresAt); expiryErr == nil {
				days := int(math.Ceil(expiry.Sub(time.Now()).Hours() / 24))
				if expiry.Before(time.Now()) {
					reminders = append(reminders, label+"：已到期")
				} else {
					reminders = append(reminders, fmt.Sprintf("%s：剩余 %d 天", label, days))
				}
			}
		}
	}
	keys = append(keys, []telegramButton{telegramButtonFor("添加账号", fmt.Sprintf("account:%d:new", siteID)), telegramButtonFor("导出账号", fmt.Sprintf("account:%d:export", siteID))})
	keys = append(keys, []telegramButton{telegramButtonFor("返回站点", fmt.Sprintf("site:%d:menu", siteID)), telegramButtonFor("主菜单", "home")})
	message := fmt.Sprintf("%s · 账号管理\n当前账号：%d 个", site.Name, len(accounts))
	if len(reminders) > 0 {
		message += "\n到期提醒：\n" + strings.Join(reminders, "\n")
	}
	return b.send(ctx, chatID, message, keys)
}

func (b *telegramBot) startAccountCreate(ctx context.Context, id, siteID int64) error {
	if _, err := b.app.db.GetSite(siteID); err != nil {
		return fmt.Errorf("站点不存在")
	}
	s := b.newSession(id, "account-create", nil)
	s.SiteID = siteID
	return b.prompt(ctx, id, s, "account_name", "请输入账号名称：")
}

func (b *telegramBot) startAccountEdit(ctx context.Context, id, siteID, accountID int64) error {
	if _, err := b.app.db.getSiteAccount(siteID, accountID); err != nil {
		return fmt.Errorf("账号不存在")
	}
	s := b.newSession(id, "account-update", nil)
	s.SiteID, s.AccountID = siteID, accountID
	return b.prompt(ctx, id, s, "account_field", "请选择要修改的账号字段：", telegramButtonFor("账号名称", "name"), telegramButtonFor("用户名", "username"), telegramButtonFor("登录密码", "password"), telegramButtonFor("安全密码", "security"), telegramButtonFor("到期时间", "expires"), telegramButtonFor("备注", "note"))
}

func (b *telegramBot) startAccountDelete(ctx context.Context, id, siteID, accountID int64) error {
	account, err := b.app.db.getSiteAccount(siteID, accountID)
	if err != nil {
		return fmt.Errorf("账号不存在")
	}
	s := b.newSession(id, "account-delete", nil)
	s.SiteID, s.AccountID = siteID, accountID
	return b.confirm(ctx, id, s, "删除账号："+account.AccountName+"\n此操作无法撤销。")
}

func (b *telegramBot) accountExportMenu(ctx context.Context, id, siteID int64) error {
	delete(b.sessions, id)
	data := "export:client"
	if siteID > 0 {
		data = fmt.Sprintf("account:%d:export", siteID)
	}
	return b.send(ctx, id, "选择客户端线路格式：", telegramKeyboard{
		{telegramButtonFor("多线路", data+":multi")},
		{telegramButtonFor("原线路", data+":original")},
		{telegramButtonFor("反代线路", data+":proxy")},
		{telegramButtonFor("返回", "home")},
	})
}

func (b *telegramBot) exportMenu(ctx context.Context, id int64) error {
	delete(b.sessions, id)
	return b.send(ctx, id, "选择导出格式：", telegramKeyboard{
		{telegramButtonFor("JSON 配置", "export:json"), telegramButtonFor("CSV 表格", "export:csv")},
		{telegramButtonFor("客户端格式", "export:client")},
		{telegramButtonFor("返回主菜单", "home")},
	})
}

func (b *telegramBot) startExport(ctx context.Context, id int64, format string, siteID int64, modes ...string) error {
	mode := ""
	if len(modes) > 0 {
		mode = modes[0]
	}
	if format == "client" && mode == "" {
		return b.accountExportMenu(ctx, id, siteID)
	}
	if format != "json" && format != "csv" && format != "client" {
		return fmt.Errorf("不支持的导出格式")
	}
	records, err := b.app.siteDataRecords()
	if err != nil {
		return fmt.Errorf("读取导出数据失败")
	}
	if siteID > 0 {
		filtered := records[:0]
		for _, record := range records {
			if record.ID == siteID {
				filtered = append(filtered, record)
			}
		}
		records = filtered
	}
	var text string
	switch format {
	case "json":
		data, _ := json.MarshalIndent(siteDataEnvelope{Version: 1, Sites: records}, "", "  ")
		text = string(data)
	case "csv":
		var buffer bytes.Buffer
		writer := csv.NewWriter(&buffer)
		_ = writer.Write([]string{"站点名称", "回源地址", "入口模式", "路径前缀", "监听端口", "账号名称", "用户名", "密码", "安全密码", "到期时间", "备注"})
		for _, site := range records {
			for _, account := range site.Accounts {
				_ = writer.Write([]string{site.Name, site.TargetURL, site.IngressMode, site.PathPrefix, strconv.Itoa(site.ListenPort), account.AccountName, account.Username, account.Password, account.SecurityPassword, account.ExpiresAt, account.Note})
			}
		}
		writer.Flush()
		text = buffer.String()
	case "client":
		var out strings.Builder
		for _, site := range records {
			if siteID > 0 { /* site-specific filtering is handled by the caller's menu */
			}
			for _, account := range site.Accounts {
				item := storedSiteAccount{SiteAccount: SiteAccount{AccountName: account.AccountName, Username: account.Username, ExpiresAt: account.ExpiresAt, Note: account.Note}}
				item.PasswordCiphertext, err = encryptSiteAccountSecret(account.Password)
				if err != nil {
					return fmt.Errorf("账号信息读取失败")
				}
				item.SecurityPasswordCiphertext, err = encryptSiteAccountSecret(account.SecurityPassword)
				if err != nil {
					return fmt.Errorf("账号信息读取失败")
				}
				value, e := b.app.accountClientText(Site{Name: site.Name, TargetURL: site.TargetURL, IngressMode: site.IngressMode, PathPrefix: site.PathPrefix, PublicHost: site.PublicHost, ListenPort: site.ListenPort}, item, mode)
				if e != nil {
					return fmt.Errorf("账号信息读取失败")
				}
				fmt.Fprintf(&out, "## %s\n%s\n", site.Name, value)
			}
		}
		text = out.String()
	}
	if text == "" {
		return b.send(ctx, id, "没有可导出的数据。", telegramBackKeyboard())
	}
	return b.sendTelegramChunks(ctx, id, text)
}

func (b *telegramBot) sendTelegramChunks(ctx context.Context, id int64, text string) error {
	text = strings.TrimSpace(text)
	for len(text) > 0 {
		cut := min(len(text), 3500)
		if cut < len(text) {
			if index := strings.LastIndexByte(text[:cut], '\n'); index > 200 {
				cut = index
			}
			for cut > 0 && !utf8.ValidString(text[:cut]) {
				cut--
			}
		}
		part := text[:cut]
		text = strings.TrimLeft(text[cut:], "\n")
		if err := telegramAPICall(ctx, b.client, b.token, "sendMessage", map[string]any{"chat_id": id, "text": part, "disable_web_page_preview": true}, nil); err != nil {
			return &telegramPanelError{err: err}
		}
	}
	return nil
}

func (b *telegramBot) importMenu(ctx context.Context, id int64) error {
	delete(b.sessions, id)
	return b.send(ctx, id, "选择导入格式，然后粘贴完整内容：", telegramKeyboard{
		{telegramButtonFor("JSON 配置", "import:json"), telegramButtonFor("CSV 表格", "import:csv")},
		{telegramButtonFor("返回主菜单", "home")},
	})
}

func (b *telegramBot) startImport(ctx context.Context, id int64, format string) error {
	if format != "json" && format != "csv" {
		return fmt.Errorf("不支持的导入格式")
	}
	s := b.newSession(id, "site-import", nil)
	s.Draft["format"] = format
	return b.prompt(ctx, id, s, "site-import-data", "请粘贴完整的 "+strings.ToUpper(format)+" 内容（导入的站点会默认停用）：")
}

func (b *telegramBot) applyImportSession(ctx context.Context, id int64, s *telegramSession) error {
	format, _ := s.Draft["format"].(string)
	data, _ := s.Draft["data"].(string)
	body, _ := json.Marshal(map[string]string{"format": format, "data": data})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/api/site-data/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	result := &siteCommandResponse{header: make(http.Header)}
	b.app.handleSiteDataImport(result, req)
	if result.status < 200 || result.status >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(result.body.Bytes(), &failure)
		return fmt.Errorf("%s", failure.Error)
	}
	return b.send(ctx, id, "导入完成，站点默认已停用，请在面板检查后启用。", telegramBackKeyboard())
}

func (b *telegramBot) applyAccountSession(ctx context.Context, id int64, s *telegramSession) error {
	if s.Action == "account-delete" {
		if _, err := b.app.db.getSiteAccount(s.SiteID, s.AccountID); err != nil {
			return fmt.Errorf("账号不存在或已删除")
		}
		if _, err := b.app.db.db.Exec("DELETE FROM site_accounts WHERE site_id=? AND id=?", s.SiteID, s.AccountID); err != nil {
			return fmt.Errorf("删除账号失败")
		}
		return b.siteAccounts(ctx, id, s.SiteID)
	}
	input := SiteAccountInput{}
	for key, value := range s.Draft {
		if stringValue, ok := value.(string); ok {
			switch key {
			case "account_name":
				input.AccountName = stringValue
			case "username":
				input.Username = stringValue
			case "password":
				input.Password = stringValue
			case "security_password":
				input.SecurityPassword = stringValue
			case "expires_at":
				input.ExpiresAt = stringValue
			case "note":
				input.Note = stringValue
			}
		}
	}
	if clear, _ := s.Draft["clear_password"].(bool); clear {
		input.ClearPassword = true
	}
	if clear, _ := s.Draft["clear_security"].(bool); clear {
		input.ClearSecurity = true
	}
	if clear, _ := s.Draft["clear_expires"].(bool); clear {
		input.ClearExpiry = true
	}
	if _, err := b.app.db.saveSiteAccount(s.SiteID, func() int64 {
		if s.Action == "account-update" {
			return s.AccountID
		}
		return 0
	}(), input); err != nil {
		return fmt.Errorf("保存账号失败：%w", err)
	}
	return b.siteAccounts(ctx, id, s.SiteID)
}

func (b *telegramBot) accountFieldPrompt(ctx context.Context, id int64, s *telegramSession, field string) error {
	if _, ok := map[string]string{"name": "账号名称", "username": "用户名", "password": "登录密码", "security": "安全密码", "expires": "到期时间", "note": "备注"}[field]; !ok {
		return fmt.Errorf("账号字段无效")
	}
	s.Draft["field"] = field
	buttons := []telegramButton{}
	switch field {
	case "password", "security":
		buttons = []telegramButton{telegramButtonFor("清除", "clear"), telegramButtonFor("保持不变", "keep")}
	case "expires":
		buttons = []telegramButton{telegramButtonFor("无到期时间", "clear")}
	case "name", "username", "note":
	}
	return b.prompt(ctx, id, s, "account_value", "请输入新的"+map[string]string{"name": "账号名称", "username": "用户名", "password": "登录密码", "security": "安全密码", "expires": "到期时间", "note": "备注"}[field]+"：", buttons...)
}
