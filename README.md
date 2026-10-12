<div align="center">

<img src="web/static/favicon.svg" width="72" height="72" alt="Meridian">

# Meridian

**把 Emby 多站点、播放路由和日常运维，放进一个清爽的面板。**

[![Release](https://img.shields.io/github/v/release/snnabb/Meridian?style=flat-square&color=578cff)](https://github.com/snnabb/Meridian/releases/latest) [![CI](https://github.com/snnabb/Meridian/actions/workflows/ci.yml/badge.svg)](https://github.com/snnabb/Meridian/actions/workflows/ci.yml) [![Docker](https://img.shields.io/badge/GHCR-Docker-2496ed?style=flat-square&logo=docker&logoColor=white)](https://github.com/snnabb/Meridian/pkgs/container/meridian) [![License](https://img.shields.io/badge/license-MIT-8190a5?style=flat-square)](LICENSE)

[在线演示](https://emby.qd.je) · [Wiki](https://github.com/snnabb/Meridian/wiki) · [文档站](https://snnabb.github.io/Meridian/index.html) · [快速安装](#快速安装) · [使用指南](docs/USAGE.md) · [部署文档](docs/DEPLOYMENT.md) · [更新记录](CHANGELOG.md)

</div>

![Meridian 仪表盘：站点概览、平滑趋势与实时状态](docs/images/dashboard.webp)

<div align="center">

**[体验只读演示站 →](https://emby.qd.je)**<br>账号 `demo` · 密码 `MeridianDemo2026`

<sub>演示站和面板截图均使用虚构站点与示例数据，不提供媒体播放；配置修改与消息发送已关闭。</sub>

</div>

## 一个面板，管好这些事

| | |
| :--- | :--- |
| **多站点入口**<br>路径、独立端口、子域名；主备线路自动切换，复制地址即可连接客户端。 | **三种播放模式**<br>自动发现、手动填写、直连播放；视频可直连，登录、海报与 API 继续代理。 |
| **流量与状态**<br>实时趋势、站点配额、速率限制和静态资源缓存；日志筛选与连接诊断。 | **Telegram 管理**<br>私聊添加、编辑、启停站点，查看地址与用量；按钮操作更新原消息。 |
| **证书与维护**<br>Cloudflare 泛域名证书及续期、证书路径或上传；加密备份和版本更新。 | **轻量且适配手机**<br>单个 Go 程序、内嵌前端与 SQLite；深色界面，本地字体，无外部数据库。 |

## 面板一览

<table>
<tr>
<td width="50%" valign="top">
<a href="docs/images/sites.webp"><img src="docs/images/sites.webp" alt="多站点管理：入口、状态与流量"></a>
<strong>站点管理</strong><br><sub>入口、线路、流量与运行状态集中查看。</sub>
</td>
<td width="50%" valign="top">
<a href="docs/images/site-editor.webp"><img src="docs/images/site-editor.webp" alt="站点编辑：协议、地址、端口与入口模式"></a>
<strong>添加与编辑</strong><br><sub>分区填写，支持粘贴链接和即时校验。</sub>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<a href="docs/images/request-logs.webp"><img src="docs/images/request-logs.webp" alt="请求日志：站点、类别、状态与日期筛选"></a>
<strong>请求日志</strong><br><sub>按站点、时间、状态与资源类型定位问题。</sub>
</td>
<td width="50%" valign="top">
<a href="docs/images/tls.webp"><img src="docs/images/tls.webp" alt="TLS 设置：自动申请、服务器路径与证书上传"></a>
<strong>TLS 证书</strong><br><sub>自动申请、读取服务器证书或上传已有证书。</sub>
</td>
</tr>
</table>

<p align="center">
<a href="docs/images/mobile.webp"><img src="docs/images/mobile.webp" width="840" alt="手机端站点、日志与 Telegram 设置"></a><br>
<sub>手机上也能完成查看、配置与维护。点击任意截图查看大图。</sub>
</p>

## Telegram 里也能管理

![Telegram Bot 操作示意：主菜单、站点管理与确认修改](docs/images/telegram-bot.webp)

<p align="center"><sub>依据当前 Bot 的真实菜单与文案绘制的操作示意；站点、数据与会话均为模拟内容。</sub></p>

在面板中填入 Bot Token、管理员数字用户 ID 和面板地址，启用后私聊发送 `/start`。

- **按钮操作**：查看日报、地址与流量，添加、编辑、启停和删除站点。
- **原消息更新**：切换菜单、返回和填写步骤不连续刷屏，修改前显示确认摘要。
- **权限分离**：只接受管理员私聊；日报接收人不会自动获得管理权限，日报可单独开关。

[查看 Bot 配置与常用命令 →](docs/USAGE.md#telegram-bot)

## 快速安装

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh)
```

1. 选择安装，设置面板端口；按提示配置域名与 HTTPS。
2. 用安装器显示的初始化令牌创建管理员，密码至少 6 个字符。
3. 添加站点：填写主地址，选择入口与播放模式，复制访问地址到 Emby 客户端。

默认面板端口为 `9090`。未配置域名时仅监听本机，需要通过 HTTPS 反向代理访问。Linux 标准 systemd 安装支持面板内更新；Docker 与自定义部署按原方式更新。

**[Docker / Windows / 源码部署](docs/DEPLOYMENT.md)** · **[证书与播放模式](docs/USAGE.md)** · **[安全说明](SECURITY.md)**

---

<div align="center">

最新发布：`v1.13.5` · [下载发行版](https://github.com/snnabb/Meridian/releases/latest) · [反馈问题](https://github.com/snnabb/Meridian/issues)<br>[MIT License](LICENSE)

</div>
