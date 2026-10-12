package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func (b *telegramBot) siteAccounts(ctx context.Context, chatID, siteID int64, pages ...int) error {
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
	page := 0
	if len(pages) > 0 {
		page = max(0, pages[0])
	}
	if page > max(0, (len(accounts)-1)/3) {
		page = 0
	}
	start, end := page*3, min(page*3+3, len(accounts))
	for _, account := range accounts[start:end] {
		plain, err := accountPlaintext(account)
		if err != nil {
			return fmt.Errorf("账号信息读取失败")
		}
		reminders = append(reminders, fmt.Sprintf("\n账号：%s\n用户名：%s\n登录密码：%s\n安全密码：%s", plain.AccountName, plain.Username, plain.Password, plain.SecurityPassword))
		if plain.Note != "" {
			reminders = append(reminders, "备注："+plain.Note)
		}
		if plain.ReminderDays != nil {
			reminders = append(reminders, fmt.Sprintf("提前提醒：%d 天（每次到期一次）", *plain.ReminderDays))
		}

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
	navigation := []telegramButton{}
	if page > 0 {
		navigation = append(navigation, telegramButtonFor("上一页", fmt.Sprintf("accountspage:%d:%d", siteID, page-1)))
	}
	if end < len(accounts) {
		navigation = append(navigation, telegramButtonFor("下一页", fmt.Sprintf("accountspage:%d:%d", siteID, page+1)))
	}
	if len(navigation) > 0 {
		keys = append(keys, navigation)
	}
	keys = append(keys, []telegramButton{telegramButtonFor("添加账号", fmt.Sprintf("account:%d:new", siteID)), telegramButtonFor("导出账号", fmt.Sprintf("account:%d:export", siteID))})
	keys = append(keys, []telegramButton{telegramButtonFor("返回站点", fmt.Sprintf("site:%d:menu", siteID)), telegramButtonFor("主菜单", "home")})
	message := fmt.Sprintf("%s · 账号管理\n当前账号：%d 个", site.Name, len(accounts))
	if len(reminders) > 0 {
		message += "\n" + strings.Join(reminders, "\n")
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
	return b.prompt(ctx, id, s, "account_field", "请选择要修改的账号字段：", telegramButtonFor("账号名称", "name"), telegramButtonFor("用户名", "username"), telegramButtonFor("登录密码", "password"), telegramButtonFor("安全密码", "security"), telegramButtonFor("到期时间", "expires"), telegramButtonFor("提前提醒", "reminder"), telegramButtonFor("备注", "note"))
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
	s := b.newSession(id, "site-export", nil)
	s.SiteID = siteID
	s.Draft["format"], s.Draft["line_mode"] = format, mode
	return b.prompt(ctx, id, s, "export_accounts", "导出是否包含账号信息？", telegramButtonFor("包含账号", "include"), telegramButtonFor("仅站点和线路", "exclude"))
}

func (b *telegramBot) sendSiteExport(ctx context.Context, id int64, s *telegramSession) error {
	format, _ := s.Draft["format"].(string)
	mode, _ := s.Draft["line_mode"].(string)
	accounts, _ := s.Draft["include_accounts"].(bool)
	passwords, _ := s.Draft["include_passwords"].(bool)
	data, name, _, err := b.app.exportSiteData(format, mode, s.SiteID, siteExportOptions{accounts, passwords})
	if err != nil {
		return err
	}
	if err := telegramSendDocument(ctx, b.client, b.token, id, name, data); err != nil {
		return &telegramPanelError{err: err}
	}
	delete(b.sessions, id)
	return b.send(ctx, id, "导出文件已发送。", telegramBackKeyboard())
}

func (b *telegramBot) importMenu(ctx context.Context, id int64) error {
	delete(b.sessions, id)
	return b.send(ctx, id, "发送 JSON/CSV 文件，或选择格式后粘贴内容，先预览再确认：", telegramKeyboard{
		{telegramButtonFor("JSON 配置", "import:json"), telegramButtonFor("CSV 表格", "import:csv")},
		{telegramButtonFor("粘贴开号信息", "import:launch")},
		{telegramButtonFor("返回主菜单", "home")},
	})
}

func (b *telegramBot) startImport(ctx context.Context, id int64, format string) error {
	if format == "launch" {
		if err := b.startAdd(ctx, id); err != nil {
			return err
		}
		return b.prompt(ctx, id, b.sessions[id], "launch", "请粘贴完整开号信息，解析后可以修改名称、入口模式和路径：")
	}
	if format != "json" && format != "csv" {
		return fmt.Errorf("不支持的导入格式")
	}
	s := b.newSession(id, "site-import", nil)
	s.Draft["format"] = format
	s.ImportRequest = &siteImportRequest{Format: format, Actions: map[int]string{}}
	return b.prompt(ctx, id, s, "site-import-data", "请粘贴完整的 "+strings.ToUpper(format)+" 内容（导入的站点会默认停用）：")
}

func (b *telegramBot) previewImport(ctx context.Context, id int64, s *telegramSession) error {
	preview, err := b.app.previewSiteImport(ctx, *s.ImportRequest)
	if err != nil {
		return err
	}
	s.ImportPreview = &preview
	s.ImportRequest.PreviewToken = preview.Token
	pages := (len(preview.Items) + 3) / 4
	s.ImportPage = max(0, min(s.ImportPage, pages-1))
	var out strings.Builder
	fmt.Fprintf(&out, "导入预览 · %d/%d 页\n新增站点默认停用；更新已有站点保留启停状态。\n未提供密码时保留已有密码。\n", s.ImportPage+1, pages)
	choices := []telegramButton{}
	for _, item := range preview.Items[s.ImportPage*4 : min((s.ImportPage+1)*4, len(preview.Items))] {
		fmt.Fprintf(&out, "\n%d. %s · %s · 账号 %d 个\n", item.Index+1, truncateTelegramText(item.Name, 70), map[string]string{"new": "新增", "update": "更新", "skip": "跳过"}[item.Action], item.AccountCount)
		if len(item.DuplicateNames) > 0 {
			fmt.Fprintf(&out, "匹配：%s\n", truncateTelegramText(strings.Join(item.DuplicateNames, "、"), 90))
		}
		if item.Action != "skip" {
			fmt.Fprintf(&out, "入口：%s %s%s（端口 %d）\n", item.IngressMode, item.PathPrefix, item.PublicHost, item.ListenPort)
		}
		if item.Error != "" {
			fmt.Fprintf(&out, "错误：%s\n", truncateTelegramText(item.Error, 140))
		}
		for _, action := range []string{"new", "update", "skip"} {
			if action == "update" && len(item.DuplicateNames) != 1 {
				continue
			}
			choices = append(choices, telegramButtonFor(fmt.Sprintf("%d · %s", item.Index+1, map[string]string{"new": "新增", "update": "更新", "skip": "跳过"}[action]), fmt.Sprintf("import_action_%d_%s", item.Index, action)))
		}
	}
	if s.ImportPage > 0 {
		choices = append(choices, telegramButtonFor("上一页", "import_prev"))
	}
	if s.ImportPage+1 < pages {
		choices = append(choices, telegramButtonFor("下一页", "import_next"))
	}
	choices = append(choices, telegramButtonFor("重复项全部跳过", "duplicates_skip"), telegramButtonFor("重复项全部更新", "duplicates_update"), telegramButtonFor("重复项全部新增", "duplicates_new"))
	if preview.Valid {
		choices = append(choices, telegramButtonFor("确认导入", "import_yes"))
	} else {
		out.WriteString("\n请修正或跳过错误记录后再确认。")
	}
	return b.prompt(ctx, id, s, "import_preview", out.String(), choices...)
}

func (b *telegramBot) applyImportSession(ctx context.Context, id int64, s *telegramSession) error {
	if s.ImportRequest == nil {
		return fmt.Errorf("请重新导入并预览")
	}
	preview, err := b.app.previewSiteImport(ctx, *s.ImportRequest)
	if err != nil {
		return err
	}
	if preview.Token != s.ImportRequest.PreviewToken || !preview.Valid {
		return fmt.Errorf("数据或选项已变化，请重新导入并预览")
	}
	result := b.app.applySiteImport(ctx, preview)
	message := fmt.Sprintf("导入完成：新增 %d、更新 %d、跳过 %d、失败 %d。\n新增站点默认停用。", result.Created, result.Updated, result.Skipped, result.Failed)
	for _, item := range result.Items {
		if item.Error != "" {
			message += fmt.Sprintf("\n%s：%s", item.Name, item.Error)
		}
	}
	return b.send(ctx, id, message, telegramBackKeyboard())
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
			case "reminder_days":
				days, err := strconv.Atoi(stringValue)
				if err != nil {
					return fmt.Errorf("提醒天数无效")
				}
				input.ReminderDays = &days
			case "note":
				input.Note = stringValue
			}
		}
	}
	if clear, _ := s.Draft["clear_reminder"].(bool); clear {
		input.ClearReminder = true
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
	if _, ok := map[string]string{"name": "账号名称", "username": "用户名", "password": "登录密码", "security": "安全密码", "expires": "到期时间", "reminder": "提前提醒天数（0–3650，0 为到期时）", "note": "备注"}[field]; !ok {
		return fmt.Errorf("账号字段无效")
	}
	s.Draft["field"] = field
	buttons := []telegramButton{}
	switch field {
	case "password", "security":
		buttons = []telegramButton{telegramButtonFor("清除", "clear"), telegramButtonFor("保持不变", "keep")}
	case "reminder":
		buttons = []telegramButton{telegramButtonFor("关闭提醒", "clear")}
	case "expires":
		buttons = []telegramButton{telegramButtonFor("无到期时间", "clear")}
	case "name", "username", "note":
	}
	return b.prompt(ctx, id, s, "account_value", "请输入新的"+map[string]string{"name": "账号名称", "username": "用户名", "password": "登录密码", "security": "安全密码", "expires": "到期时间", "reminder": "提前提醒天数（0–3650，0 为到期时）", "note": "备注"}[field]+"：", buttons...)
}
