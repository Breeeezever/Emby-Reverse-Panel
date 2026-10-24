# Linux 安装

推荐使用带 systemd 的 Linux 服务器。安装器创建独立的非 root 服务账户，管理程序、环境文件和服务。

## 一键安装

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh)
```

交互菜单提供安装、更新、修改管理员密码、卸载、泛域名证书和本机反向代理配置。安装过程会检查平台、依赖与端口，并下载对应正式版本。

安装时可直接选择“泛域名 + Meridian 原生 HTTPS”（推荐）。输入 `example.com` 或 `*.example.com`，新安装默认使用 `panel.example.com` 作为面板域名，再填写 ACME 邮箱与 Cloudflare DNS Token。一次申请泛域名证书，面板与域名前缀站点共用，无需先给面板单独申请证书，也无需安装后再去网页配置泛域名。

也可直接执行，Token 会在提示时隐藏输入：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) install \
  --wildcard '*.example.com' --email admin@example.com --port 9090
```

提前将 `panel.example.com` 和 `*.example.com` 的 A / AAAA 记录指向服务器；DNS-01 仅写入临时验证 TXT，不会代建访问记录。放行本机防火墙和云安全组的 TCP `9090`，使用该端口时将面板及节点记录设为“仅 DNS”。成功后访问 `https://panel.example.com:9090`，添加前缀 `cinema` 的站点即可使用 `https://cinema.example.com:9090`，共用原生监听端口。

需要无端口地址时，可在 `443` 空闲并已放行的情况下指定 `--port 443`；端口被 Nginx、Xray 等服务占用时安装器会拒绝切换。需要其他面板名时加 `--domain console.example.com`，它必须受同一泛域名证书覆盖。

非交互安装可从受保护的文件读取 Token：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) install \
  --wildcard '*.example.com' --email admin@example.com --port 9090 \
  --dns-token-file /root/cloudflare-token -y
```

Token 文件只供管理员读取，例如权限设为 `600`；Token 需有目标 Zone 的 Zone Read 与 DNS Edit 权限。泛域名原生 HTTPS 需要 systemd 和有效的证书邮箱，签发后由 Meridian 自动续期。`--wildcard` 不能与 `--no-domain` 同用。

已有安装不指定域名操作时保留配置；使用 `--wildcard` 重新配置时沿用安装器保存的面板域名。若需更换，显式传入 `--domain`；原域名不在新泛域名覆盖范围内时会拒绝配置。已有 Nginx 入口的处理见下方[泛域名与原生 HTTPS](#wildcard-https)。

只需要单独面板域名、由 Nginx 管理 HTTPS 时：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) install \
  --port 9090 --domain panel.example.com --email admin@example.com -y
```

域名应解析到当前服务器，申请证书所需的端口须可达。单独使用 `--domain` 配置安装器管理的 Nginx 面板代理，证书由 Certbot 维护；与 `--wildcard` 同用才是 Meridian 原生 HTTPS。

面板和站点需要使用不同基础域名时，可继续按 [面板与站点分域名部署](nginx-separate-domains.md) 增加媒体域名的 Nginx HTTPS 入口；当前外部 HTTPS 模式通过 API 注册完整站点域名。

没有域名或已有外部反向代理时：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) install \
  --port 9090 -y
```

新安装默认仅监听 `127.0.0.1`，已有安装保留域名和入口配置。通过 SSH 隧道初始化，或按 [反向代理](reverse-proxy.md) 接入 HTTPS。`--no-domain` 是取消安装器域名并启用公开明文兼容入口，不用于保留已有的外部 HTTPS 反代。

手动 Nginx/宝塔同机反代无需额外运行命令：`TRUSTED_PROXY_CIDRS` 未设置或留空时，Meridian 默认信任 `127.0.0.1` 和 `::1`。Nginx 覆盖设置 `X-Real-IP $remote_addr` 后，新请求日志自动显示真实 IP；已有空配置升级后同样生效。旧版程序仍可通过 `bash install.sh proxy --local`（菜单选项 6）显式配置。1Panel 的 OpenResty 或 Meridian 如果在不同容器，必须按实际代理来源配置。完整步骤见[手动反代真实 IP](reverse-proxy.md#manual-proxy-client-ip)。

## 标准安装位置

| 内容 | 默认位置 |
| --- | --- |
| 程序 | `/usr/local/bin/meridian` |
| 数据库 | `/opt/meridian/meridian.db` |
| 环境与持久密钥 | `/opt/meridian/.env` |
| systemd 服务 | `/etc/systemd/system/meridian.service` |
| 安装器备份目录 | `/opt/meridian-backups` |
| 安装器管理的 Nginx 配置 | `/etc/nginx/conf.d/meridian-panel.conf` |

自定义路径以实际安装配置为准。不要仅复制数据库而遗漏 `.env` 中的密钥。

每次标准安装器更新前会在备份目录生成 `meridian-pre-<version>-<timestamp>.tar.gz`，内容是更新前的整个 `/opt/meridian` 数据目录。它包含 SQLite 主文件及 WAL/SHM 旁车文件，但不包含 systemd、Nginx 或数据目录之外的数据库路径；跨结构降级前请阅读[更新回滚](updates.md#rollback)。

## 首次初始化

安装器新生成 `SETUP_TOKEN` 时会在终端显示一次，并写入受保护的环境文件。打开面板后使用令牌创建管理员。

管理员登录密码至少 6 个字符、最多 72 个 UTF-8 字节。首次初始化完成后，不再使用令牌登录。

已有安装再次运行脚本不会自动回显原令牌。尚未初始化时，由服务器管理员在本机查看受保护的配置文件；不要把整个环境文件贴到聊天或 Issue 中。

## 管理服务

```bash
sudo systemctl status meridian --no-pager
sudo systemctl restart meridian
sudo journalctl -u meridian -n 100 --no-pager
```

运行日志用于启动、监听、证书和上游错误排查。客户端请求详情在面板的“请求日志”中查看。

## 泛域名与原生 HTTPS {#wildcard-https}

首次安装可按上方步骤直接申请。已有 Meridian 时，菜单的“配置泛域名证书”或 `tls` 命令同样使用 Cloudflare DNS-01：

```bash
curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh -o install.sh
bash install.sh tls --domain panel.example.com --wildcard '*.example.com' \
  --email admin@example.com --port 9090 --dns-token-file /root/cloudflare-token -y
```

Token 文件只能由管理员读取。授予目标 Zone 的 Zone Read 与 DNS Edit 权限，不要把 Token 作为命令行参数。

`tls` 省略 `--domain` 时沿用安装器保存的面板域名；未设置过面板域名时默认使用 `panel.基础域名`。

成功后启用 Meridian 原生 HTTPS。已有安装器管理的 Nginx 面板入口会保留，将回源改为本机 HTTPS 并发送与 Host 一致的 TLS SNI；原来的 `https://panel.example.com` 仍可使用，其证书继续由 Certbot 维护。节点使用实际原生监听端口，例如 `https://cinema.example.com:9090`。失败时恢复数据库、环境、证书和原 Nginx 配置；不修改其他 Nginx 站点。非安装器管理或结构不明确的代理配置会在停服务前拒绝修改。

签发前检查所选 TCP 端口的监听进程，避免占用 Nginx、Xray 等其他服务的端口；缺少 `ss` 时会明确提示未能预检查。配置后分别报告本机健康与域名入口探测结果：入口探测按服务器的 DNS 解析访问 `/api/auth/check`，验证证书链、域名与面板响应，不跳过证书校验，也不自动跟随跳转。首次配置 Nginx 域名 HTTPS 时同样执行该探测。证书已安装但入口检查失败时，会显示待处理原因，不能视为公网部署完成；外部网络故障不会触发本机证书回滚。

使用原生 `9090` 等端口时，还需在本机防火墙、云安全组和端口映射中放行对应 TCP。检测到 UFW 启用时会提示检查规则，安装器不会自动修改防火墙。服务器侧探测通过仍需从浏览器或另一网络复测。没有受管理 Nginx 入口时，`https://panel.example.com` 与 `https://panel.example.com:9090` 是不同入口；前者可能仍被已有 stream/SNI 规则转到其他程序，后者的证书不会自动替换前者。

安装器设置 `PANEL_ROUTE_DIRECT=true`，网页与 Telegram 据此生成节点的原生端口地址。若后续将节点统一放到网关后，或使用不同的 Docker 公网映射端口，应去掉这个设置，使用对应的公开入口。

保留网关后需要更换原生监听端口时，使用上面的 `tls --port 新端口` 命令，让数据库与代理回源同步；只改环境变量或网页端口会被阻止，以免原面板入口断开。

面板也可直接使用节点基础域名，例如 `--domain media.example.com --wildcard '*.media.example.com'`；证书会同时包含 `media.example.com` 和 `*.media.example.com`。

配置完成后检查防火墙和 CDN 是否支持该端口。完整说明见 [TLS 证书](tls-certificates.md)。

## 更新、改密与卸载

```bash
# 更新正式版本，包含下载校验、备份和健康检查
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) update

# 离线重置管理员密码
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) password

# 卸载服务并保留数据
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) uninstall
```

只有显式使用 `--purge` 才删除数据。重置密码会轮换会话密钥，使旧登录失效。

标准 Linux 安装支持 [面板内更新](updates.md)。新版安装器会在安装、升级和已是最新版时自动配置更新服务；旧标准安装运行一次新版安装器的 `update -y` 即可补装。自定义服务名、目录或运行账户不能直接照搬这一流程。
