# Meridian Wiki

Meridian 是面向 Emby 的多站点管理与播放代理工具。本 Wiki 对应 **v1.13.1**，涵盖部署、日常使用与维护。

[GitHub Wiki](https://github.com/snnabb/Meridian/wiki) · [文档站](https://snnabb.github.io/Meridian/index.html)

![Meridian 仪表盘，虚构站点与示例数据](https://raw.githubusercontent.com/snnabb/Meridian/v1.13.0/docs/images/dashboard.webp)

## 从这里开始

| 你的目标 | 推荐阅读 |
| --- | --- |
| 第一次安装并接入 Emby | [快速开始](quick-start.md) |
| 用服务器或容器部署 | [Linux 安装](install-linux.md) · [Docker 部署](install-docker.md) |
| 用 Windows、macOS 或源码运行 | [Windows 与源码](install-source.md) |
| 使用自己的域名和 HTTPS | [反向代理](reverse-proxy.md) · [TLS 证书](tls-certificates.md) |
| 添加站点并连接客户端 | [站点管理](site-management.md) · [入口模式](ingress-modes.md) |
| 选择视频如何回源 | [播放模式](playback-modes.md) · [自动发现](auto-discovery.md) |
| 在 Telegram 中管理 | [Telegram Bot](telegram-bot.md) |
| 查找登录、连接或播放故障 | [故障排查](troubleshooting.md) |

## 日常管理

| 功能 | 文档 |
| --- | --- |
| 主备线路、UA、自定义请求头与缓存 | [高级策略](advanced-settings.md) |
| 配额、限速、计费方向与重置周期 | [流量与配额](traffic-and-quota.md) |
| 请求记录、筛选和连接检测 | [日志与诊断](logs-and-diagnostics.md) |
| 导出加密备份与迁移 | [备份与恢复](backup-and-restore.md) |
| 升级、检查状态与回滚 | [更新与维护](updates.md) |
| 环境变量和启动参数 | [配置参考](configuration.md) |
| 账户、凭据和网络边界 | [安全与隐私](security.md) |
| 构建、测试与参与开发 | [开发指南](development.md) |

## 体验与下载

- [只读演示站](https://emby.qd.je)：账号 `demo`，密码 `MeridianDemo2026`。
- [下载正式版本](https://github.com/snnabb/Meridian/releases/latest)。
- [查看更新记录](https://github.com/snnabb/Meridian/blob/master/CHANGELOG.md)。
- [反馈问题](https://github.com/snnabb/Meridian/issues)。

演示站、面板截图与 Telegram 操作示意使用虚构站点和示例数据。演示站不提供媒体播放，修改配置和发送消息已关闭。

## 三个常用概念

| 概念 | 含义 |
| --- | --- |
| 主地址 | 原 Emby 服务器地址，用于登录、媒体信息和普通 API。 |
| 站点入口 | Meridian 提供给客户端的地址，由入口模式决定。 |
| 播放回源 | 实际提供视频、清单或分片的主服务器、播放节点或自动发现的后端。 |

Meridian 管理员与 Emby 用户是两套账户。客户端连接站点入口后，仍使用原来的 Emby 用户名和密码。
