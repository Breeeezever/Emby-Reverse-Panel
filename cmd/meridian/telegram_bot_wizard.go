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
)

type telegramSession struct {
	Action   string
	Original *Site
	Draft    map[string]any
	Step     string
	Nonce    string
	Expires  time.Time
	Scheme   string
	Target   *url.URL
	Summary  string
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
	for _, choice := range choices {
		choice.Data = "w:" + s.Nonce + ":" + choice.Data
		keys = append(keys, []telegramButton{choice})
	}
	keys = append(keys, []telegramButton{telegramButtonFor("取消操作", "cancel")})
	return b.send(ctx, id, message, keys)
}
func (b *telegramBot) confirm(ctx context.Context, id int64, s *telegramSession, message string) error {
	s.Summary = message
	return b.prompt(ctx, id, s, "confirm", message+"\n\n确认后生效（10 分钟内有效）。", telegramButtonFor("确认执行", "yes"))
}
func (b *telegramBot) handleText(ctx context.Context, id int64, text string) error {
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
	if s := b.sessions[id]; s != nil && s.Step == "path" {
		return b.acceptInput(ctx, id, s, text)
	}
	if strings.HasPrefix(text, "/") {
		return b.send(ctx, id, "可用命令：/menu /report /sites /address /traffic /status /add /cancel", telegramBackKeyboard())
	}
	s := b.sessions[id]
	if s == nil {
		return b.home(ctx, id)
	}
	return b.acceptInput(ctx, id, s, text)
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
	if len(parts) == 2 && parts[0] == "sites" {
		page, _ := strconv.Atoi(parts[1])
		return b.listSites(ctx, id, "menu", page)
	}
	if len(parts) == 3 && parts[0] == "pick" {
		page, _ := strconv.Atoi(parts[2])
		if parts[1] == "menu" || parts[1] == "address" || parts[1] == "traffic" {
			return b.listSites(ctx, id, parts[1], page)
		}
	}
	if len(parts) == 3 && parts[0] == "site" {
		siteID, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil {
			return fmt.Errorf("站点编号无效")
		}
		delete(b.sessions, id)
		return b.siteView(ctx, id, siteID, parts[2])
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
			return b.send(ctx, id, "这个按钮已失效，请使用最新消息中的按钮或重新打开菜单。", telegramBackKeyboard())
		}
		if s.Step == "confirm" && parts[2] == "yes" {
			return b.applySession(ctx, id, s)
		}
		return b.acceptInput(ctx, id, s, parts[2])
	}
	return b.send(ctx, id, "操作已失效，请重新打开菜单。", telegramBackKeyboard())
}
func (b *telegramBot) startAdd(ctx context.Context, id int64) error {
	s := b.newSession(id, "create", nil)
	s.Draft["ua_mode"] = "passthrough"
	s.Draft["client_ip_mode"] = "both"
	s.Draft["playback_mode"] = "direct"
	s.Draft["asset_cache_ttl_sec"] = 86400
	s.Draft["asset_cache_max_bytes"] = 512 * 1048576
	return b.prompt(ctx, id, s, "name", "添加站点 · 请输入站点名称：")
}
func (b *telegramBot) protocolPrompt(ctx context.Context, id int64, s *telegramSession) error {
	return b.prompt(ctx, id, s, "protocol", "请选择主地址协议：", telegramButtonFor("HTTP · 默认 8096", "http"), telegramButtonFor("HTTPS · 默认 443", "https"))
}
func (b *telegramBot) ingressPrompt(ctx context.Context, id int64, s *telegramSession) error {
	choices := []telegramButton{telegramButtonFor("路径模式（默认）", "path"), telegramButtonFor("独立端口", "port")}
	if b.app.pm.HostOnlyIngressSafe() {
		choices = append(choices, telegramButtonFor("域名模式", "host"))
	}
	return b.prompt(ctx, id, s, "ingress", "选择入口模式：", choices...)
}
func (b *telegramBot) playbackPrompt(ctx context.Context, id int64, s *telegramSession) error {
	choices := []telegramButton{}
	if len(b.app.dynamicRouteKey) == 32 {
		choices = append(choices, telegramButtonFor("自动发现", "auto"))
	}
	choices = append(choices, telegramButtonFor("手动填写回源", "manual"), telegramButtonFor("直连播放", "direct"))
	return b.prompt(ctx, id, s, "playback", "选择播放回源模式。直连播放仍代理海报和接口，只让视频直连。", choices...)
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
func (b *telegramBot) acceptInput(ctx context.Context, id int64, s *telegramSession, text string) error {
	text = strings.TrimSpace(text)
	switch s.Step {
	case "name":
		if len(text) == 0 || len(text) > 100 || strings.ContainsAny(text, "\r\n") {
			return fmt.Errorf("名称请填写 1–100 字节的单行文字")
		}
		s.Draft["name"] = text
		if s.Action == "create" {
			return b.protocolPrompt(ctx, id, s)
		}
		return b.finishDraft(ctx, id, s, "名称改为："+text)
	case "protocol":
		if text != "http" && text != "https" {
			return fmt.Errorf("请点击 HTTP 或 HTTPS")
		}
		s.Scheme = text
		return b.prompt(ctx, id, s, "address", "请输入主地址（域名或 IP，也可以粘贴完整地址）：")
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
		return b.prompt(ctx, id, s, "port", "请输入主地址端口（1–65535）：", telegramButtonFor("使用 "+port, port))
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
			return b.prompt(ctx, id, s, "path", "请输入路径前缀，例如 /movies：")
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
			return b.prompt(ctx, id, s, "playback_target", "请输入播放回源地址，或沿用主地址：", telegramButtonFor("沿用主地址", "same"))
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
		return b.send(ctx, id, "请点击上方“确认执行”，或发送 /cancel 取消。", nil)
	}
	return fmt.Errorf("当前步骤已失效，请发送 /menu 重新开始")
}
func (b *telegramBot) afterIngress(ctx context.Context, id int64, s *telegramSession) error {
	if s.Action == "create" {
		return b.playbackPrompt(ctx, id, s)
	}
	return b.finishDraft(ctx, id, s, fmt.Sprintf("入口模式：%v\n路径：%v\n域名：%v\n端口：%v", s.Draft["ingress_mode"], s.Draft["path_prefix"], s.Draft["public_host"], s.Draft["listen_port"]))
}
func (b *telegramBot) finishDraft(ctx context.Context, id int64, s *telegramSession, summary string) error {
	if s.Action == "create" {
		body, _ := json.Marshal(s.Draft)
		var site Site
		_ = json.Unmarshal(body, &site)
		summary = fmt.Sprintf("添加并启用站点\n名称：%s\n主地址：%s\n入口：%s", site.Name, site.TargetURL, site.IngressMode)
		if site.IngressMode == ingressModePort && site.ListenPort == 0 {
			return fmt.Errorf("独立端口不能为空")
		}
		addr, err := b.app.telegramSiteAddress(site, b.settings.PublicURL)
		if err == nil {
			summary += "\n访问地址：" + addr
		}
		summary += "\n播放：" + telegramPlaybackLabel(site)
	} else {
		summary = "编辑站点：" + s.Original.Name + "\n" + summary
	}
	return b.confirm(ctx, id, s, summary)
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
	return b.send(ctx, id, "编辑 "+site.Name+"\n请选择要修改的项目：", keys)
}
func (b *telegramBot) startEdit(ctx context.Context, id, siteID int64, field string) error {
	site, err := b.app.db.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("站点不存在")
	}
	s := b.newSession(id, "update", site)
	switch field {
	case "name":
		return b.prompt(ctx, id, s, "name", "请输入新的站点名称：")
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
		return b.prompt(ctx, id, s, "cache", "设置静态资源缓存：", telegramButtonFor("开启", "on"), telegramButtonFor("关闭", "off"))
	case "failovers":
		return b.prompt(ctx, id, s, "failovers", "请发送备用地址，每行一个（将替换现有备用线路）：", telegramButtonFor("清空备用线路", "clear"))
	case "ua":
		return b.prompt(ctx, id, s, "ua", "选择客户端标识：", telegramButtonFor("透传客户端", "passthrough"), telegramButtonFor("Infuse", "infuse"), telegramButtonFor("Emby 客户端", "client"), telegramButtonFor("Emby Web", "web"))
	}
	delete(b.sessions, id)
	return fmt.Errorf("不支持的编辑项目")
}
func (b *telegramBot) applySession(ctx context.Context, id int64, s *telegramSession) error {
	// Consume before mutation, including failures: retry always needs a fresh view
	// and confirmation, avoiding ambiguous repeated creates after network failures.
	delete(b.sessions, id)
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
		return b.send(ctx, id, "站点已删除。", telegramBackKeyboard())
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
	return b.send(ctx, id, "操作已完成。", telegramSiteKeys(siteID))
}
func telegramDiagnosticText(name string, result DiagResult) string {
	state := map[string]string{"online": "正常", "reachable": "可连接", "offline": "无法连接", "error": "检测失败"}[result.Health.Status]
	if state == "" {
		state = "未知"
	}
	return fmt.Sprintf("%s · 连接检测\n结果：%s\nHTTP 状态：%d\n延迟：%d ms\n此检测不代表影片一定能播放。", name, state, result.Health.Probe.HTTPStatus, result.Health.LatencyMs)
}
