# 快速开始

这条流程使用 Linux 一键安装、HTTPS 面板和路径入口。已有 Docker 环境可从 [Docker 部署](install-docker.md) 开始。

## 1. 安装 Meridian

准备一台可访问上游 Emby 的服务器。需要 HTTPS 域名时，先把 `panel.example.com` 解析到服务器，并按安装器提示开放证书申请与访问所需的端口。

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh)
```

选择“安装”，填写面板端口，按提示配置域名。默认内部端口为 `9090`；通过安装器配置 Nginx 后，通常从 `https://panel.example.com` 访问。

未配置域名时默认仅监听本机。可用 SSH 隧道完成初始化：

```bash
ssh -L 9090:127.0.0.1:9090 user@server.example.com
```

保持连接，再在自己电脑打开 `http://127.0.0.1:9090`。正式远程访问见 [反向代理](reverse-proxy.md)。

## 2. 创建管理员

使用安装器显示的初始化令牌创建管理员。

- 管理员用户名为 1–64 个 UTF-8 字节。
- 登录密码至少 6 个字符，最多 72 个 UTF-8 字节。
- 初始化令牌仅用于首次创建管理员。已完成初始化后使用用户名与密码登录。

不要使用 Emby 账户代替 Meridian 管理员账户，也不要把初始化令牌公开。

## 3. 添加第一个站点

进入“站点管理 → 添加站点”，填写：

| 字段 | 示例 |
| --- | --- |
| 站点名称 | 星海影院 |
| 入口模式 | 路径 |
| 路径前缀 | `cinema` |
| 主地址协议 | HTTP |
| 主地址 | `emby.example.com` |
| 主地址端口 | `8096` |
| 播放回源模式 | 自动发现 |

也可把完整主地址 `http://emby.example.com:8096` 粘贴到地址栏，界面会识别协议与端口。新建表单的 HTTP 默认端口为 `8096`，HTTPS 为 `443`；粘贴已有 URL 时以 URL 的协议与端口为准。

先运行“测试连接”，再保存并启用站点。自动发现需要部署中的 `DYNAMIC_ROUTE_KEY`；一键安装器会生成并持久保存。

## 4. 连接 Emby 客户端

复制站点显示的访问地址，例如：

```text
https://panel.example.com/cinema/
```

在 Emby、Infuse 等客户端添加服务器，保留完整路径前缀，使用原 Emby 账户登录。

如果面板通过非默认端口访问，复制的站点地址也应包含它，例如 `https://panel.example.com:8443/cinema/`。

## 5. 验证完整链路

1. 能识别服务器名称并登录。
2. 媒体列表和海报正常显示。
3. 播放一段视频，并尝试暂停、拖动进度和切换字幕。
4. 在 Meridian 的“请求日志”和“流量统计”中确认实际请求。

“测试连接”通过只说明检测请求成功，不能替代真实客户端播放验证。播放失败时按 [故障排查](troubleshooting.md) 检查入口、认证与播放回源。

## 接下来

- 要使用子域名或独立端口，阅读 [入口模式](ingress-modes.md)。
- 要减少服务器媒体带宽，阅读 [直连播放](playback-modes.md)。
- 正式使用前导出一份 [加密备份](backup-and-restore.md)，并单独保存部署密钥。
