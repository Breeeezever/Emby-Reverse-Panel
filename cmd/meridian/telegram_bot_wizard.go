package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type telegramSession struct {
	Action         string
	Original       *Site
	Draft          map[string]any
	Accounts       []SiteAccountInput
	SiteID         int64
	AccountID      int64
	Step           string
	Nonce          string
	Expires        time.Time
	Scheme         string
	Target         *url.URL
	Summary        string
	EditingDraft   bool
	AccountIndex   int
	AccountField   string
	ImportRequest  *siteImportRequest
	ImportPreview  *siteImportPreview
	ImportPage     int
	PromptMessage  string
	PromptKeyboard telegramKeyboard
}

func (b *telegramBot) newSession(id int64, action string, site *Site) *telegramSession {
	s := &telegramSession{Action: action, Original: site, Draft: map[string]any{}, Expires: time.Now().Add(10 * time.Minute)}
	if site != nil {
		s.Draft["name"] = site.Name
		s.Draft["target_url"] = site.TargetURL
		s.Draft["listen_port"] = site.ListenPort
	}
	b.sessions[id] = s
	return s
}
func (b *telegramBot) prompt(ctx context.Context, id int64, s *telegramSession, step, message string, choices ...telegramButton) error {
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("无法生成操作凭据")
	}
	s.Nonce = hex.EncodeToString(nonce)
	s.Step = step
	s.Expires = time.Now().Add(10 * time.Minute)
	keys := telegramKeyboard{}
	pairable := false
	for _, choice := range choices {
		wide := choice.Data == "yes" || choice.Data == "done" || utf8.RuneCountInString(choice.Text) > 16
		choice.Data = "w:" + s.Nonce + ":" + choice.Data
		if !wide && pairable {
			keys[len(keys)-1] = append(keys[len(keys)-1], choice)
			pairable = false
		} else {
			keys = append(keys, []telegramButton{choice})
			pairable = !wide
		}
	}
	keys = append(keys, []telegramButton{telegramButtonFor("↩️ 取消，回到菜单", "cancel")})
	s.PromptMessage, s.PromptKeyboard = message, keys
	return b.send(ctx, id, message, keys)
}
func (b *telegramBot) confirm(ctx context.Context, id int64, s *telegramSession, message string) error {
	s.Summary = message
	choices := []telegramButton{telegramButtonFor("✅ 确认执行", "yes")}
	if s.Action == "create" {
		choices[0] = telegramButtonFor("💾 保存并启用", "yes")
		if len(s.Accounts) > 0 {
			choices = append(choices, telegramButtonFor("🧾 修改账号 / 到期提醒", "draft_accounts"))
		}
		choices = append(choices, telegramButtonFor("修改名称", "draft_name"), telegramButtonFor("修改入口模式", "draft_ingress"), telegramButtonFor("修改路径 / 域名 / 端口", "draft_address"), telegramButtonFor("修改播放模式", "draft_playback"))
	}
	return b.prompt(ctx, id, s, "confirm", message+"\n\n💡 还未生效。检查好了再点确认，草稿保留 10 分钟。", choices...)
}
func (b *telegramBot) handleText(ctx context.Context, id int64, text string) error {
	rawText := text
	text = strings.TrimSpace(text)
	if text == "" {
		return b.send(ctx, id, "请输入文字或点击菜单按钮。", nil)
	}
	command := strings.ToLower(strings.SplitN(strings.Fields(text + " ")[0], "@", 2)[0])
	switch command {
	case "/start":
		delete(b.panels, id)
		return b.home(ctx, id)
	case "/menu", "/help":
		return b.home(ctx, id)
	case "/cancel":
		delete(b.sessions, id)
		return b.home(ctx, id)
	case "/report":
		delete(b.sessions, id)
		return b.report(ctx, id)
	case "/sites":
		return b.listSites(ctx, id, "menu", 0)
	case "/accounts":
		return b.listSites(ctx, id, "accounts", 0)
	case "/export":
		delete(b.sessions, id)
		return b.exportMenu(ctx, id)
	case "/import":
		delete(b.sessions, id)
		return b.importMenu(ctx, id)
	case "/address":
		return b.listSites(ctx, id, "address", 0)
	case "/traffic":
		return b.listSites(ctx, id, "traffic", 0)
	case "/status":
		delete(b.sessions, id)
		return b.overview(ctx, id)
	case "/add":
		return b.startAdd(ctx, id)
	case "/id":
		return b.send(ctx, id, fmt.Sprintf("你的 Telegram 用户 ID：%d", id), telegramBackKeyboard())
	}
	if s := b.sessions[id]; s != nil {
		field, _ := s.Draft["field"].(string)
		secretInput := s.Step == "account_password" || s.Step == "account_security" || (s.Step == "account_value" && (field == "password" || field == "security")) || (s.Step == "draft_account_value" && (s.AccountField == "password" || s.AccountField == "security"))
		if s.Step == "path" || secretInput {
			return b.acceptInput(ctx, id, s, rawText)
		}
	}
	if strings.HasPrefix(text, "/") {
		return b.send(ctx, id, "可用命令：/menu /report /sites /accounts /address /traffic /status /add /export /import /cancel", telegramBackKeyboard())
	}
	s := b.sessions[id]
	if s == nil {
		return b.home(ctx, id)
	}
	return b.acceptInput(ctx, id, s, rawText)
}
func (b *telegramBot) handleCallback(ctx context.Context, id int64, data string) error {
	switch data {
	case "home", "cancel":
		return b.home(ctx, id)
	case "report":
		delete(b.sessions, id)
		return b.report(ctx, id)
	case "overview":
		delete(b.sessions, id)
		return b.overview(ctx, id)
	case "add":
		return b.startAdd(ctx, id)
	}
	parts := strings.Split(data, ":")
	if len(parts) == 3 && parts[0] == "accountspage" {
		siteID, err := strconv.ParseInt(parts[1], 10, 64)
		page, pageErr := strconv.Atoi(parts[2])
		if err != nil || pageErr != nil {
			return fmt.Errorf("页码无效")
		}
		return b.siteAccounts(ctx, id, siteID, page)
	}
	if len(parts) == 3 && parts[0] == "reminder" {
		accountID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || accountID <= 0 {
			return fmt.Errorf("提醒账号无效")
		}
		return b.handleReminderAction(ctx, id, accountID, parts[2])
	}
	if len(parts) == 2 && parts[0] == "sites" {
		page, _ := strconv.Atoi(parts[1])
		return b.listSites(ctx, id, "menu", page)
	}
	if len(parts) == 3 && parts[0] == "pick" {
		page, _ := strconv.Atoi(parts[2])
		if parts[1] == "menu" || parts[1] == "address" || parts[1] == "traffic" || parts[1] == "accounts" {
			return b.listSites(ctx, id, parts[1], page)
		}
	}
	if data == "export" {
		delete(b.sessions, id)
		return b.exportMenu(ctx, id)
	}
	if data == "import" {
		delete(b.sessions, id)
		return b.importMenu(ctx, id)
	}
	if len(parts) == 2 && parts[0] == "export" {
		return b.startExport(ctx, id, parts[1], 0)
	}
	if len(parts) == 3 && parts[0] == "export" && parts[1] == "client" {
		return b.startExport(ctx, id, "client", 0, parts[2])
	}
	if len(parts) == 2 && parts[0] == "import" {
		return b.startImport(ctx, id, parts[1])
	}
	if len(parts) == 3 && parts[0] == "site" {
		siteID, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil {
			return fmt.Errorf("站点编号无效")
		}
		delete(b.sessions, id)
		return b.siteView(ctx, id, siteID, parts[2])
	}
	if len(parts) == 3 && parts[0] == "account" {
		siteID, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil {
			return fmt.Errorf("站点编号无效")
		}
		switch parts[2] {
		case "new":
			return b.startAccountCreate(ctx, id, siteID)
		case "export":
			return b.accountExportMenu(ctx, id, siteID)
		}
	}
	if len(parts) == 4 && parts[0] == "account" && parts[2] == "export" {
		siteID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return fmt.Errorf("站点编号无效")
		}
		return b.startExport(ctx, id, "client", siteID, parts[3])
	}
	if len(parts) == 4 && parts[0] == "account" {
		siteID, siteErr := strconv.ParseInt(parts[1], 10, 64)
		accountID, accountErr := strconv.ParseInt(parts[2], 10, 64)
		if siteErr != nil || accountErr != nil {
			return fmt.Errorf("账号编号无效")
		}
		switch parts[3] {
		case "edit":
			return b.startAccountEdit(ctx, id, siteID, accountID)
		case "delete":
			return b.startAccountDelete(ctx, id, siteID, accountID)
		case "export":
			return b.startExport(ctx, id, "client", siteID, "multi")
		case "renew":
			return b.startAccountRenew(ctx, id, siteID, accountID)
		}
	}
	if len(parts) == 3 && parts[0] == "edit" {
		siteID, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil {
			return fmt.Errorf("站点编号无效")
		}
		return b.startEdit(ctx, id, siteID, parts[2])
	}
	if len(parts) == 3 && parts[0] == "w" {
		s := b.sessions[id]
		if s == nil || s.Nonce != parts[1] || time.Now().After(s.Expires) {
			return b.send(ctx, id, "⏳ 这个按钮已经过期啦。请用最新消息里的按钮，或发送 /menu 重新开始。", telegramBackKeyboard())
		}
		if s.Step == "confirm" && parts[2] == "yes" {
			return b.applySession(ctx, id, s)
		}
		return b.acceptInput(ctx, id, s, parts[2], true)
	}
	return b.send(ctx, id, "⏳ 这一步已失效，发 /menu 重新开始就好。", telegramBackKeyboard())
}
func (b *telegramBot) startAdd(ctx context.Context, id int64) error {
	s := b.newSession(id, "create", nil)
	s.Draft["ua_mode"] = "passthrough"
	s.Draft["client_ip_mode"] = "both"
	s.Draft["playback_mode"] = "direct"
	s.Draft["asset_cache_ttl_sec"] = 86400
	s.Draft["asset_cache_max_bytes"] = 512 * 1048576
	return b.prompt(ctx, id, s, "name", "🌱 一起添加一个站点吧\n\n先发我站点名称；有开号消息的话，也可以直接点下面的自动识别。", telegramButtonFor("粘贴开号信息", "launch"))
}
func (b *telegramBot) protocolPrompt(ctx context.Context, id int64, s *telegramSession) error {
	return b.prompt(ctx, id, s, "protocol", "🌐 主地址用哪种协议？按服务商提供的地址选就好。", telegramButtonFor("HTTP · 默认 8096", "http"), telegramButtonFor("HTTPS · 默认 443", "https"))
}
func (b *telegramBot) ingressPrompt(ctx context.Context, id int64, s *telegramSession) error {
	choices := []telegramButton{telegramButtonFor("路径模式（默认）", "path"), telegramButtonFor("独立端口", "port")}
	if b.app.pm.HostOnlyIngressSafe() {
		choices = append(choices, telegramButtonFor("域名模式", "host"))
	}
	return b.prompt(ctx, id, s, "ingress", "🚪 想通过哪种方式访问？\n路径模式最省事，例如 /movies。", choices...)
}
func (b *telegramBot) playbackPrompt(ctx context.Context, id int64, s *telegramSession) error {
	choices := []telegramButton{}
	if len(b.app.dynamicRouteKey) == 32 {
		choices = append(choices, telegramButtonFor("自动发现", "auto"))
	}
	choices = append(choices, telegramButtonFor("手动填写回源", "manual"), telegramButtonFor("直连播放", "direct"))
	return b.prompt(ctx, id, s, "playback", "🎬 播放方式选哪一种？\n\n自动发现：自动识别视频线路并代理播放。\n手动回源：自己指定视频地址。\n直连播放：视频直连，海报和接口继续代理。", choices...)
}
func telegramInteger(text string, minValue, maxValue int64) (int64, error) {
	if text == "" {
		return 0, fmt.Errorf("请输入整数")
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("请输入 %d–%d 之间的整数", minValue, maxValue)
		}
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < minValue || n > maxValue {
		return 0, fmt.Errorf("请输入 %d–%d 之间的整数", minValue, maxValue)
	}
	return n, nil
}
func telegramTarget(raw, scheme string) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = scheme + "://" + raw
	}
	u, err := normalizeTargetURL(raw)
	if err != nil {
		return nil, fmt.Errorf("主地址无效，请填写域名或 IP，可包含端口和路径")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("地址不能包含账号、密码、查询参数或片段")
	}
	return u, nil
}
func (b *telegramBot) acceptInput(ctx context.Context, id int64, s *telegramSession, text string, fromButton ...bool) error {
	button := len(fromButton) > 0 && fromButton[0]
	if s.Step == "draft_account" || s.Step == "draft_account_value" {
		return b.acceptDraftAccount(ctx, id, s, text, button)
	}
	field, _ := s.Draft["field"].(string)
	secretInput := s.Step == "account_password" || s.Step == "account_security" || (s.Step == "account_value" && (field == "password" || field == "security"))
	if !secretInput || button {
		text = strings.TrimSpace(text)
	}
	switch s.Step {
	case "export_accounts":
		if text != "include" && text != "exclude" {
			return fmt.Errorf("请从按钮选择")
		}
		s.Draft["include_accounts"] = text == "include"
		if text == "exclude" {
			return b.sendSiteExport(ctx, id, s)
		}
		return b.prompt(ctx, id, s, "export_passwords", "🔑 密码要一起导出吗？\n选择不包含，文件里就只保留账号和其他资料。", telegramButtonFor("不包含密码", "exclude"), telegramButtonFor("包含密码", "include"))
	case "export_passwords":
		if text != "include" && text != "exclude" {
			return fmt.Errorf("请从按钮选择")
		}
		s.Draft["include_passwords"] = text == "include"
		return b.sendSiteExport(ctx, id, s)
	case "launch":
		record, err := parseSiteMessageRecord(text)
		if err != nil {
			return err
		}
		s.Draft["name"] = record.Name
		s.Draft["target_url"] = record.TargetURL
		s.Draft["ingress_mode"] = ingressModePath
		s.Draft["path_prefix"] = record.PathPrefix
		s.Draft["playback_mode"] = "direct"
		s.Draft["main_video_stream_mode"] = "proxy"
		s.Draft["dynamic_discovery_enabled"] = len(b.app.dynamicRouteKey) == 32
		s.Draft["dynamic_profile"] = "compatible"
		s.Draft["playback_target_url"] = ""
		s.Draft["failover_targets"] = record.FailoverTargets
		s.Draft["stream_hosts"] = record.StreamHosts
		s.Draft["ua_mode"] = passthroughUAMode
		s.Accounts = append([]SiteAccountInput(nil), record.Accounts...)
		s.AccountIndex = 0
		if len(s.Accounts) > 0 {
			return b.draftAccountPrompt(ctx, id, s)
		}
		return b.finishDraft(ctx, id, s, "已解析开号信息")
	case "account_name":
		if s.Action != "account-create" || text == "" || len(text) > 128 {
			return fmt.Errorf("账号名称不能为空且不能超过 128 个字符")
		}
		s.Draft["account_name"] = text
		return b.prompt(ctx, id, s, "account_username", "👤 接下来是登录用户名，也可以和账号名称一样。", telegramButtonFor("与账号名称相同", "same"))
	case "account_username":
		if button && text == "same" {
			text, _ = s.Draft["account_name"].(string)
		}
		if text == "" || len(text) > 128 {
			return fmt.Errorf("用户名不能为空且不能超过 128 个字符")
		}
		s.Draft["username"] = text
		return b.prompt(ctx, id, s, "account_password", "🔑 把登录密码发给我吧。暂时不填可以跳过。", telegramButtonFor("跳过", "skip"))
	case "account_password":
		if !button || text != "skip" {
			s.Draft["password"] = text
		}
		return b.prompt(ctx, id, s, "account_security", "🔐 有安全密码吗？有的话发给我，没有就跳过。", telegramButtonFor("跳过", "skip"))
	case "account_security":
		if !button || text != "skip" {
			s.Draft["security_password"] = text
		}
		return b.prompt(ctx, id, s, "account_expires", "📅 账号什么时候到期？\n例如 2030-11-09，没有到期时间也可以跳过。", telegramButtonFor("无到期时间", "skip"))
	case "account_expires":
		if !button || text != "skip" {
			if _, err := parseSiteAccountExpiry(text); err != nil {
				return fmt.Errorf("到期日期格式无效")
			}
			s.Draft["expires_at"] = text
		}
		return b.prompt(ctx, id, s, "account_reminder", "🔔 想提前几天提醒？\n发我天数，0 表示到期当天；同一次到期只提醒一次。", telegramButtonFor("关闭提醒", "skip"))
	case "account_reminder":
		if !button || text != "skip" {
			days, err := telegramInteger(text, 0, 3650)
			if err != nil {
				return err
			}
			s.Draft["reminder_days"] = strconv.FormatInt(days, 10)
		}
		return b.prompt(ctx, id, s, "account_note", "📝 还有什么想记下来的？发我备注，或直接跳过。", telegramButtonFor("跳过", "skip"))
	case "account_note":
		if !button || text != "skip" {
			s.Draft["note"] = text
		}
		return b.finishAccountDraft(ctx, id, s)
	case "renew_days":
		if button && text == "custom" {
			return b.prompt(ctx, id, s, "renew_custom_days", "🔄 输入自定义续期天数（1–3650）。", telegramButtonFor("返回常用时长", "back"))
		}
		days, err := telegramInteger(text, 1, 3650)
		if err != nil {
			return err
		}
		s.Draft["renew_days"] = strconv.FormatInt(days, 10)
		return b.confirm(ctx, id, s, fmt.Sprintf("🔄 将账号续期 %d 天。确认后会立即更新到期时间并保留续期记录。", days))
	case "renew_custom_days":
		if button && text == "back" {
			return b.startAccountRenew(ctx, id, s.SiteID, s.AccountID)
		}
		days, err := telegramInteger(text, 1, 3650)
		if err != nil {
			return err
		}
		s.Draft["renew_days"] = strconv.FormatInt(days, 10)
		return b.confirm(ctx, id, s, fmt.Sprintf("🔄 将账号续期 %d 天。确认后会立即更新到期时间并保留续期记录。", days))
	case "account_field":
		if s.Action != "account-update" {
			return fmt.Errorf("账号操作已失效")
		}
		return b.accountFieldPrompt(ctx, id, s, text)
	case "account_value":
		field, _ := s.Draft["field"].(string)
		if button && text == "keep" {
			return b.finishAccountDraft(ctx, id, s)
		}
		if button && text == "clear" {
			if field == "password" {
				s.Draft["clear_password"] = true
			} else if field == "security" {
				s.Draft["clear_security"] = true
			} else if field == "reminder" {
				s.Draft["clear_reminder"] = true
			} else if field == "expires" {
				s.Draft["expires_at"] = ""
				s.Draft["clear_expires"] = true
			}
			return b.finishAccountDraft(ctx, id, s)
		}
		if text == "" {
			return fmt.Errorf("内容不能为空")
		}
		key := map[string]string{"name": "account_name", "username": "username", "password": "password", "security": "security_password", "expires": "expires_at", "reminder": "reminder_days", "note": "note"}[field]
		if key == "reminder_days" {
			if _, err := telegramInteger(text, 0, 3650); err != nil {
				return err
			}
		}
		if key == "expires_at" {
			if _, err := parseSiteAccountExpiry(text); err != nil {
				return fmt.Errorf("到期日期格式无效")
			}
		}
		s.Draft[key] = text
		return b.finishAccountDraft(ctx, id, s)
	case "site-import-data":
		if s.ImportRequest == nil {
			return fmt.Errorf("请重新选择导入格式")
		}
		s.ImportRequest.Data = text
		return b.previewImport(ctx, id, s)
	case "import_preview":
		if s.ImportRequest == nil {
			return fmt.Errorf("导入已失效")
		}
		if text == "import_yes" {
			return b.applySession(ctx, id, s)
		}
		if text == "import_prev" {
			s.ImportPage--
		} else if text == "import_next" {
			s.ImportPage++
		} else if strings.HasPrefix(text, "duplicates_") {
			s.ImportRequest.DuplicateAction = strings.TrimPrefix(text, "duplicates_")
			s.ImportRequest.Actions = map[int]string{}
		} else if strings.HasPrefix(text, "import_action_") {
			parts := strings.Split(text, "_")
			if len(parts) != 4 {
				return fmt.Errorf("导入操作无效")
			}
			index, err := strconv.Atoi(parts[2])
			if err != nil {
				return fmt.Errorf("导入操作无效")
			}
			s.ImportRequest.Actions[index] = parts[3]
		} else {
			return fmt.Errorf("请使用导入预览按钮")
		}
		return b.previewImport(ctx, id, s)
	case "name":
		if text == "launch" {
			return b.prompt(ctx, id, s, "launch", "📋 把完整开号消息粘贴过来吧。\n我会先整理成草稿，你可以修改账号和提醒，确认后再保存。")
		}
		if len(text) == 0 || len(text) > 100 || strings.ContainsAny(text, "\r\n") {
			return fmt.Errorf("名称请填写 1–100 字节的单行文字")
		}
		s.Draft["name"] = text
		if s.Action == "create" && !s.EditingDraft {
			return b.protocolPrompt(ctx, id, s)
		}
		return b.finishDraft(ctx, id, s, "名称改为："+text)
	case "protocol":
		if text != "http" && text != "https" {
			return fmt.Errorf("请点击 HTTP 或 HTTPS")
		}
		s.Scheme = text
		return b.prompt(ctx, id, s, "address", "🔗 发我主地址吧，域名、IP 或完整链接都可以。")
	case "address":
		target, err := telegramTarget(text, s.Scheme)
		if err != nil {
			return err
		}
		s.Target = target
		port := target.Port()
		if port == "" {
			port = "8096"
			if target.Scheme == "https" {
				port = "443"
			}
		}
		return b.prompt(ctx, id, s, "port", "🔌 主地址使用哪个端口？\n可以直接选下面的推荐值，或发送 1–65535 之间的整数。", telegramButtonFor("使用 "+port, port))
	case "port":
		port, err := telegramInteger(text, 1, 65535)
		if err != nil {
			return err
		}
		if s.Target == nil {
			return fmt.Errorf("请重新添加主地址")
		}
		s.Target.Host = net.JoinHostPort(s.Target.Hostname(), strconv.FormatInt(port, 10))
		s.Draft["target_url"] = s.Target.String()
		if s.Action == "create" {
			return b.ingressPrompt(ctx, id, s)
		}
		note := "主地址改为：" + s.Target.String()
		if s.Original != nil && s.Original.StoredUpstreamHeaders != "[]" && s.Original.StoredUpstreamHeaders != "" {
			note += "\n如协议、域名或端口变化，原有固定请求头会被清除；请在面板重新配置。"
		}
		return b.finishDraft(ctx, id, s, note)
	case "ingress":
		if text != "path" && text != "port" && text != "host" {
			return fmt.Errorf("请从按钮选择入口模式")
		}
		if text == "host" && !b.app.pm.HostOnlyIngressSafe() {
			return fmt.Errorf("当前部署不支持域名模式")
		}
		s.Draft["ingress_mode"] = text
		s.Draft["public_host"] = ""
		s.Draft["path_prefix"] = ""
		switch text {
		case "path":
			return b.prompt(ctx, id, s, "path", "🔗 给访问路径起个名字吧，例如 /movies。")
		case "port":
			return b.prompt(ctx, id, s, "listen_port", "请输入访问站点的独立端口（1–65535）。该端口需要已开放。")
		}
		return b.prompt(ctx, id, s, "public_host", "请输入完整访问域名，例如 movies.example.com：")
	case "path":
		prefix, err := normalizePathPrefix(text)
		if err != nil || prefix == "" {
			return fmt.Errorf("路径无效或被面板占用，请换一个路径前缀")
		}
		s.Draft["path_prefix"] = prefix
		return b.afterIngress(ctx, id, s)
	case "listen_port":
		port, err := telegramInteger(text, 1, 65535)
		if err != nil {
			return err
		}
		s.Draft["listen_port"] = int(port)
		return b.afterIngress(ctx, id, s)
	case "public_host":
		host, err := normalizePublicHost(text)
		if err != nil || host == "" {
			return fmt.Errorf("请填写有效的完整域名，不含协议、端口或路径")
		}
		s.Draft["public_host"] = host
		return b.afterIngress(ctx, id, s)
	case "playback":
		if text != "auto" && text != "manual" && text != "direct" {
			return fmt.Errorf("请从按钮选择播放模式")
		}
		if text == "auto" && len(b.app.dynamicRouteKey) != 32 {
			return fmt.Errorf("当前服务器未配置自动发现密钥")
		}
		s.Draft["playback_mode"] = "direct"
		s.Draft["main_video_stream_mode"] = "proxy"
		s.Draft["dynamic_discovery_enabled"] = text == "auto"
		s.Draft["playback_target_url"] = ""
		s.Draft["stream_hosts"] = []string{}
		if text == "auto" {
			s.Draft["dynamic_profile"] = "compatible"
		}
		if text == "direct" {
			s.Draft["main_video_stream_mode"] = "direct"
		}
		if text == "manual" {
			return b.prompt(ctx, id, s, "playback_target", "🎬 发我视频回源地址吧，也可以沿用站点主地址。", telegramButtonFor("沿用主地址", "same"))
		}
		return b.finishDraft(ctx, id, s, "播放模式："+map[string]string{"auto": "自动发现", "direct": "直连播放"}[text])
	case "playback_target":
		if text != "same" {
			u, err := telegramTarget(text, "https")
			if err != nil {
				return err
			}
			s.Draft["playback_target_url"] = u.String()
		}
		return b.finishDraft(ctx, id, s, "播放模式：手动回源\n播放地址："+fmt.Sprint(s.Draft["playback_target_url"]))
	case "quota":
		n, err := telegramInteger(text, 0, 1<<30)
		if err != nil {
			return err
		}
		s.Draft["traffic_quota"] = n * 1073741824
		return b.finishDraft(ctx, id, s, fmt.Sprintf("流量配额：%d GB（0 为不限）", n))
	case "speed":
		n, err := telegramInteger(text, 0, int64(maxSpeedLimitMbps))
		if err != nil {
			return err
		}
		s.Draft["speed_limit"] = n
		return b.finishDraft(ctx, id, s, "速率上限："+telegramSpeedLabel(int(n)))
	case "cache":
		if text != "on" && text != "off" {
			return fmt.Errorf("请选择开启或关闭")
		}
		s.Draft["asset_cache_enabled"] = text == "on"
		return b.finishDraft(ctx, id, s, "静态资源缓存："+map[string]string{"on": "开启", "off": "关闭"}[text])
	case "failovers":
		lines := []FailoverLine{}
		if text != "clear" {
			for i, raw := range strings.Split(text, "\n") {
				if strings.TrimSpace(raw) == "" {
					continue
				}
				u, err := telegramTarget(strings.TrimSpace(raw), "https")
				if err != nil {
					return err
				}
				lines = append(lines, FailoverLine{Name: fmt.Sprintf("线路%d", i+2), URL: u.String(), Enabled: true})
			}
		}
		if len(lines) > maxFailoverTargets-1 {
			return fmt.Errorf("一次最多填写 %d 条备用线路", maxFailoverTargets-1)
		}
		s.Draft["failover_lines"] = lines
		summary := fmt.Sprintf("备用线路：%d 条（替换现有备用线路）", len(lines))
		for _, line := range lines {
			summary += "\n" + line.URL
		}
		return b.finishDraft(ctx, id, s, summary)
	case "ua":
		if text != "passthrough" && text != "infuse" && text != "client" && text != "web" {
			return fmt.Errorf("请从按钮选择客户端标识")
		}
		s.Draft["ua_mode"] = text
		return b.finishDraft(ctx, id, s, "客户端标识："+text)
	case "confirm":
		if s.Action == "create" && strings.HasPrefix(text, "draft_") {
			s.EditingDraft = true
			switch text {
			case "draft_accounts":
				return b.draftAccountPrompt(ctx, id, s)
			case "draft_name":
				return b.prompt(ctx, id, s, "name", "✏️ 这个站点想叫什么名字？发给我就好。")
			case "draft_ingress":
				return b.ingressPrompt(ctx, id, s)
			case "draft_playback":
				return b.playbackPrompt(ctx, id, s)
			case "draft_address":
				switch s.Draft["ingress_mode"] {
				case ingressModePath:
					return b.prompt(ctx, id, s, "path", "🔗 给访问路径起个名字吧，例如 /movies。")
				case ingressModePort:
					return b.prompt(ctx, id, s, "listen_port", "🔌 想用哪个访问端口？发送 1–65535 之间的整数就好。")
				default:
					return b.prompt(ctx, id, s, "public_host", "🌐 发我完整的访问域名吧，例如 emby.example.com。")
				}
			}
		}
		return b.send(ctx, id, "💾 检查好了就点上方的保存按钮；还想改一改，也可以继续点编辑。", nil)
	}
	return fmt.Errorf("当前步骤已失效，请发送 /menu 重新开始")
}
func (b *telegramBot) afterIngress(ctx context.Context, id int64, s *telegramSession) error {
	if s.Action == "create" && !s.EditingDraft {
		return b.playbackPrompt(ctx, id, s)
	}
	return b.finishDraft(ctx, id, s, fmt.Sprintf("入口模式：%v\n路径：%v\n域名：%v\n端口：%v", s.Draft["ingress_mode"], s.Draft["path_prefix"], s.Draft["public_host"], s.Draft["listen_port"]))
}
func (b *telegramBot) finishDraft(ctx context.Context, id int64, s *telegramSession, summary string) error {
	s.EditingDraft = false
	if strings.HasPrefix(s.Action, "account-") {
		return b.finishAccountDraft(ctx, id, s)
	}
	if s.Action == "create" {
		body, _ := json.Marshal(s.Draft)
		var site Site
		_ = json.Unmarshal(body, &site)
		summary = fmt.Sprintf("📝 再看一眼，准备好就保存吧\n\n🌐 %s\n主地址：%s\n入口：%s", site.Name, site.TargetURL, telegramIngressLabel(site.IngressMode))
		if site.IngressMode == ingressModePort && site.ListenPort == 0 {
			return fmt.Errorf("独立端口不能为空")
		}
		addr, err := b.app.telegramSiteAddress(site, b.settings.PublicURL)
		if err == nil {
			summary += "\n\n🔗 访问地址\n" + addr
		}
		summary += "\n\n🎬 播放：" + telegramPlaybackLabel(site)
		if len(b.app.dynamicRouteKey) != 32 {
			summary += "\n当前服务器尚未开启自动发现，可在播放设置中调整。"
		}
		if len(s.Accounts) > 0 {
			summary += fmt.Sprintf("\n\n🧾 待保存账号：%d 个", len(s.Accounts))
			for _, account := range s.Accounts[:min(3, len(s.Accounts))] {
				summary += "\n👤 " + account.Username + "\n📅 " + func() string {
					if account.ExpiresAt == "" {
						return "未设置到期时间"
					}
					return formatSiteAccountExpiryDate(account.ExpiresAt)
				}() + " · " + telegramReminderLabel(account)
			}
			if len(s.Accounts) > 3 {
				summary += "\n其余账号可在“修改账号 / 到期提醒”中查看。"
			}
		}
	} else {
		summary = "编辑站点：" + s.Original.Name + "\n" + summary
	}
	return b.confirm(ctx, id, s, summary)
}

func (b *telegramBot) finishAccountDraft(ctx context.Context, id int64, s *telegramSession) error {
	value := func(key string) string {
		v, _ := s.Draft[key].(string)
		if v == "" {
			return "未填写"
		}
		return v
	}
	if s.Action == "account-create" {
		reminder := "提醒未开启"
		if days, ok := s.Draft["reminder_days"].(string); ok {
			reminder = "提前 " + days + " 天提醒一次"
			if days == "0" {
				reminder = "到期时提醒一次"
			}
		}
		return b.confirm(ctx, id, s, fmt.Sprintf("🧾 账号信息整理好了\n\n👤 %s\n用户名：%s\n登录密码：%s\n安全密码：%s\n\n📅 到期日：%s\n🔔 %s\n备注：%s", value("account_name"), value("username"), value("password"), value("security_password"), formatSiteAccountExpiryDate(value("expires_at")), reminder, value("note")))
	}
	if s.Action == "account-update" {
		field, _ := s.Draft["field"].(string)
		key := map[string]string{"name": "account_name", "username": "username", "password": "password", "security": "security_password", "expires": "expires_at", "reminder": "reminder_days", "note": "note"}[field]
		label := map[string]string{"name": "账号名称", "username": "用户名", "password": "登录密码", "security": "安全密码", "expires": "到期时间", "reminder": "提前提醒天数", "note": "备注"}[field]
		display := "保持不变"
		if text, ok := s.Draft[key].(string); ok {
			display = text
		}
		if key == "expires_at" {
			display = formatSiteAccountExpiryDate(display)
		}
		for _, clear := range []string{"clear_password", "clear_security", "clear_expires", "clear_reminder"} {
			if flag, _ := s.Draft[clear].(bool); flag {
				display = "清空"
				if field == "reminder" {
					display = "关闭提醒"
				}
			}
		}
		return b.confirm(ctx, id, s, "📝 这次准备修改\n\n"+label+"："+display+"\n\n检查一下，确认后我再保存。")
	}
	return b.confirm(ctx, id, s, "确认账号操作")
}
func (b *telegramBot) editMenu(ctx context.Context, id int64, site Site) error {
	delete(b.sessions, id)
	keys := telegramKeyboard{}
	fields := [][2]string{{"站点名称", "name"}, {"主地址", "target"}, {"入口模式", "ingress"}, {"播放模式", "playback"}, {"流量配额", "quota"}, {"速率上限", "speed"}, {"静态资源缓存", "cache"}, {"备用线路", "failovers"}, {"客户端标识", "ua"}}
	for _, f := range fields {
		keys = append(keys, []telegramButton{telegramButtonFor(f[0], fmt.Sprintf("edit:%d:%s", site.ID, f[1]))})
	}
	keys = append(keys, []telegramButton{{Text: "更多配置 · 打开面板", URL: b.settings.PublicURL + "/#sites"}})
	keys = append(keys, []telegramButton{telegramButtonFor("返回站点", fmt.Sprintf("site:%d:menu", site.ID))})
	return b.send(ctx, id, "✏️ 编辑 "+site.Name+"\n请选择要修改的项目：", keys)
}
func (b *telegramBot) startEdit(ctx context.Context, id, siteID int64, field string) error {
	site, err := b.app.db.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("站点不存在")
	}
	s := b.newSession(id, "update", site)
	switch field {
	case "name":
		return b.prompt(ctx, id, s, "name", "✏️ 发我新的站点名称吧。")
	case "target":
		return b.protocolPrompt(ctx, id, s)
	case "ingress":
		return b.ingressPrompt(ctx, id, s)
	case "playback":
		return b.playbackPrompt(ctx, id, s)
	case "quota":
		return b.prompt(ctx, id, s, "quota", "当前配额："+formatTelegramBytes(site.TrafficQuota)+"\n请输入新的配额（整数 GB，0 为不限）：")
	case "speed":
		return b.prompt(ctx, id, s, "speed", "当前上限："+telegramSpeedLabel(site.SpeedLimit)+"\n请输入新的上限（整数 Mbps，0 为不限）：")
	case "cache":
		return b.prompt(ctx, id, s, "cache", "🖼️ 要开启静态资源缓存吗？", telegramButtonFor("开启", "on"), telegramButtonFor("关闭", "off"))
	case "failovers":
		return b.prompt(ctx, id, s, "failovers", "🔗 把备用地址发给我吧，每行一个。\n保存后会替换现有备用线路。", telegramButtonFor("清空备用线路", "clear"))
	case "ua":
		return b.prompt(ctx, id, s, "ua", "🎭 想使用哪个客户端标识？", telegramButtonFor("透传客户端", "passthrough"), telegramButtonFor("Infuse", "infuse"), telegramButtonFor("Emby 客户端", "client"), telegramButtonFor("Emby Web", "web"))
	}
	delete(b.sessions, id)
	return fmt.Errorf("不支持的编辑项目")
}
func (b *telegramBot) applySession(ctx context.Context, id int64, s *telegramSession) error {
	if s.Action == "create" {
		if err := validateNewSiteAccounts(s.Accounts); err != nil {
			return err
		}
		s.Draft["accounts"] = s.Accounts
	}
	// Consume before mutation, including failures: retry always needs a fresh view
	// and confirmation, avoiding ambiguous repeated creates after network failures.
	delete(b.sessions, id)
	if strings.HasPrefix(s.Action, "account-") {
		return b.applyAccountSession(ctx, id, s)
	}
	if s.Action == "site-import" {
		return b.applyImportSession(ctx, id, s)
	}
	siteID := int64(0)
	expected := ""
	if s.Original != nil {
		siteID = s.Original.ID
		expected = siteConfigurationFingerprint(*s.Original)
	}
	result, err := b.app.executeTelegramSiteCommand(ctx, s.Action, siteID, s.Draft, expected)
	if err != nil {
		return err
	}
	if s.Action == "delete" {
		return b.send(ctx, id, "🗑️ 站点已删除。需要的话，可以再添加一个。", telegramBackKeyboard())
	}
	if s.Action == "create" {
		var site Site
		if json.Unmarshal(result, &site) == nil {
			siteID = site.ID
		}
	}
	var warning struct {
		Warning string `json:"warning"`
	}
	_ = json.Unmarshal(result, &warning)
	if warning.Warning != "" {
		return b.send(ctx, id, "入口已停用，资源清理尚未完成，请到面板检查。", telegramSiteKeys(siteID))
	}
	return b.send(ctx, id, "✅ 好了，设置已经生效。接下来可以查看地址或继续管理账号。", telegramSiteKeys(siteID))
}
func telegramDiagnosticText(name string, result DiagResult) string {
	state := map[string]string{"online": "正常", "reachable": "可连接", "offline": "无法连接", "error": "检测失败"}[result.Health.Status]
	if state == "" {
		state = "未知"
	}
	return fmt.Sprintf("🩺 %s · 连接检测\n\n结果：%s\nHTTP 状态：%d\n延迟：%d ms\n此检测不代表影片一定能播放。", name, state, result.Health.Probe.HTTPStatus, result.Health.LatencyMs)
}
