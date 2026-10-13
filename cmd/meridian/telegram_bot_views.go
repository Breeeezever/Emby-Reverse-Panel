package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func telegramBackKeyboard() telegramKeyboard {
	return telegramKeyboard{{telegramButtonFor("返回主菜单", "home")}}
}
func (b *telegramBot) home(ctx context.Context, id int64) error {
	delete(b.sessions, id)
	keys := telegramKeyboard{
		{telegramButtonFor("📊 立即日报", "report"), telegramButtonFor("🌐 站点管理", "sites:0")},
		{telegramButtonFor("🔗 站点地址", "pick:address:0"), telegramButtonFor("📈 站点流量", "pick:traffic:0")},
		{telegramButtonFor("🧾 站点账号", "pick:accounts:0"), telegramButtonFor("📋 运行概览", "overview")},
		{telegramButtonFor("➕ 添加站点", "add"), telegramButtonFor("📤 导出数据", "export")},
		{telegramButtonFor("📥 导入数据", "import")},
	}
	if b.settings.PublicURL != "" {
		keys = append(keys, []telegramButton{{Text: "打开面板", URL: b.settings.PublicURL}})
	}
	return b.send(ctx, id, "👋 Meridian 管理\n\n今天想处理点什么？选一个入口，我带你继续。\n随时发送 /cancel 可以取消当前操作。", keys)
}
func (b *telegramBot) report(ctx context.Context, id int64) error {
	b.app.pm.FlushTraffic()
	stats, err := b.app.db.buildTelegramReportStats(time.Now())
	if err != nil {
		return fmt.Errorf("读取日报数据失败")
	}
	return b.send(ctx, id, buildTelegramReportMessage(stats), telegramBackKeyboard())
}
func (b *telegramBot) overview(ctx context.Context, id int64) error {
	snap, err := b.app.pm.TrafficSnapshot()
	if err != nil {
		return fmt.Errorf("读取流量失败")
	}
	billing := "双向流量"
	if snap.BillingMode == trafficBillingModeOutbound {
		billing = "仅出站流量"
	}
	return b.send(ctx, id, fmt.Sprintf("📊 Meridian 运行概览\n\n站点：%d 个，运行 %d 个\n本周期：%s\n累计：%s\n计费方式：%s\n运行时间：%s", snap.TotalSites, snap.RunningSites, formatTelegramBytes(snap.MonthlyTraffic), formatTelegramBytes(snap.TotalTraffic), billing, (time.Duration(snap.UptimeSeconds)*time.Second).Round(time.Minute)), telegramBackKeyboard())
}
func (b *telegramBot) listSites(ctx context.Context, id int64, action string, page int) error {
	delete(b.sessions, id)
	sites, err := b.app.db.ListSites()
	if err != nil {
		return fmt.Errorf("读取站点失败")
	}
	if len(sites) == 0 {
		return b.send(ctx, id, "🌱 还没有站点，先添加第一个吧。", telegramKeyboard{{telegramButtonFor("添加站点", "add")}, {telegramButtonFor("返回主菜单", "home")}})
	}
	page = max(0, min(page, (len(sites)-1)/8))
	start := page * 8
	end := min(start+8, len(sites))
	keys := telegramKeyboard{}
	for _, site := range sites[start:end] {
		status := "⏸"
		if b.app.pm.IsRunning(site.ID) {
			status = "▶"
		}
		keys = append(keys, []telegramButton{telegramButtonFor(status+" "+truncateTelegramText(site.Name, 60), fmt.Sprintf("site:%d:%s", site.ID, action))})
	}
	nav := []telegramButton{}
	if page > 0 {
		nav = append(nav, telegramButtonFor("上一页", fmt.Sprintf("pick:%s:%d", action, page-1)))
	}
	if end < len(sites) {
		nav = append(nav, telegramButtonFor("下一页", fmt.Sprintf("pick:%s:%d", action, page+1)))
	}
	if len(nav) > 0 {
		keys = append(keys, nav)
	}
	keys = append(keys, []telegramButton{telegramButtonFor("添加站点", "add"), telegramButtonFor("主菜单", "home")})
	label := map[string]string{"menu": "管理站点", "address": "发送站点地址", "traffic": "查看站点流量", "accounts": "管理站点账号"}[action]
	return b.send(ctx, id, fmt.Sprintf("🌐 %s · 第 %d/%d 页\n\n想查看哪个站点？点下面的名字就好。", label, page+1, (len(sites)+7)/8), keys)
}

func (a *App) telegramSiteAddress(site Site, origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("请先在面板设置 Bot 使用的外部地址")
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	switch site.IngressMode {
	case ingressModePath:
		u.Path = strings.TrimRight(site.PathPrefix, "/") + "/"
	case ingressModeHost, ingressModeBoth:
		if site.PublicHost == "" {
			return "", fmt.Errorf("站点未配置访问域名")
		}
		u.Host = site.PublicHost
		// Host ingress uses the externally exposed panel listener, including its
		// nonstandard port. Dedicated listeners use their own actual TLS state.
		if source, _ := url.Parse(origin); source.Port() != "" {
			u.Host = net.JoinHostPort(site.PublicHost, source.Port())
		}
	case ingressModePort:
		u.Scheme = "http"
		if a.pm.siteTLSConfigSnapshot() != nil {
			u.Scheme = "https"
		}
		u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(site.ListenPort))
	default:
		return "", fmt.Errorf("站点尚未配置入口")
	}
	return u.String(), nil
}
func telegramSiteKeys(id int64) telegramKeyboard {
	prefix := fmt.Sprintf("site:%d:", id)
	return telegramKeyboard{
		{telegramButtonFor("发送地址", prefix+"address"), telegramButtonFor("查看流量", prefix+"traffic")},
		{telegramButtonFor("账号管理", prefix+"accounts"), telegramButtonFor("导出账号", prefix+"export")},
		{telegramButtonFor("编辑配置", prefix+"edit"), telegramButtonFor("启用 / 停用", prefix+"toggle")},
		{telegramButtonFor("检测连接", prefix+"diag"), telegramButtonFor("删除站点", prefix+"delete")},
		{telegramButtonFor("返回站点列表", "sites:0"), telegramButtonFor("主菜单", "home")},
	}
}
func (b *telegramBot) siteView(ctx context.Context, chatID, siteID int64, action string) error {
	site, err := b.app.db.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("站点不存在，请重新选择")
	}
	switch action {
	case "address":
		addr, e := b.app.telegramSiteAddress(*site, b.settings.PublicURL)
		if e != nil {
			return e
		}
		note := ""
		if !site.Enabled {
			note = "\n站点当前已停用。"
		}
		return b.send(ctx, chatID, "🔗 "+site.Name+"\n\n访问地址\n"+addr+note, telegramSiteKeys(site.ID))
	case "traffic":
		live := b.app.pm.LiveSiteTraffic([]Site{*site})[site.ID]
		quota := "不限额"
		if site.TrafficQuota > 0 {
			quota = formatTelegramBytes(site.TrafficQuota)
		}
		return b.send(ctx, chatID, fmt.Sprintf("📈 %s · 流量\n\n本周期：%s\n累计：%s\n配额：%s\n速率上限：%s\n统计口径与面板的全局计费方式一致。", site.Name, formatTelegramBytes(live.MonthlyTraffic), formatTelegramBytes(live.TrafficUsed), quota, telegramSpeedLabel(site.SpeedLimit)), telegramSiteKeys(site.ID))
	case "accounts":
		return b.siteAccounts(ctx, chatID, site.ID)
	case "export":
		return b.accountExportMenu(ctx, chatID, site.ID)
	case "edit":
		return b.editMenu(ctx, chatID, *site)
	case "toggle", "delete":
		session := b.newSession(chatID, action, site)
		summary := "▶️ 准备启用站点：" + site.Name
		if site.Enabled {
			summary = "⏸ 准备停用站点：" + site.Name
		}
		if action == "delete" {
			summary = "🗑️ 准备删除站点：" + site.Name + "\n将停止代理并删除该站点及相关统计，无法撤销。"
		}
		return b.confirm(ctx, chatID, session, summary)
	case "diag":
		result := diagnoseSite(site, b.app.pm)
		// Present the established diagnostics response without exposing raw request
		// headers or upstream query credentials.
		return b.send(ctx, chatID, telegramDiagnosticText(site.Name, result), telegramSiteKeys(site.ID))
	default:
		status := "已停用"
		if site.Enabled {
			status = "已启用"
		}
		if b.app.pm.IsRunning(site.ID) {
			status = "运行中"
		}
		address, _ := b.app.telegramSiteAddress(*site, b.settings.PublicURL)
		return b.send(ctx, chatID, fmt.Sprintf("🌐 %s\n\n状态：%s\n🎬 播放：%s\n\n🔗 访问地址\n%s", site.Name, status, telegramPlaybackLabel(*site), address), telegramSiteKeys(site.ID))
	}
}
func telegramSpeedLabel(speed int) string {
	if speed == 0 {
		return "不限速"
	}
	return fmt.Sprintf("%d Mbps", speed)
}
func telegramPlaybackLabel(site Site) string {
	if site.MainVideoStreamMode == "direct" {
		return "直连播放"
	}
	if site.DynamicDiscoveryEnabled {
		return "自动发现"
	}
	return "手动回源"
}
