# 配置参考

多数日常设置由面板保存到数据库。环境变量主要用于首次部署、监听绑定、信任来源和长期密钥。

## 基础环境变量

| 变量 | 默认 / 示例 | 用途 |
| --- | --- | --- |
| `PORT` | `9090` | 初始面板端口。已保存的面板监听设置优先。 |
| `DB_PATH` | `meridian.db`；Docker 为 `/app/data/meridian.db` | SQLite 文件位置。 |
| `PANEL_BIND_ADDR` | `127.0.0.1` | 面板监听地址；远程绑定需要明确配置。 |
| `PANEL_DOMAIN` | `panel.example.com` | 初始面板允许域名。 |
| `PANEL_ROUTE_DOMAIN` | `example.com` | 初始站点基础域名，可规范化通配前缀。 |
| `PANEL_TLS_ENABLED` | `false` | 初始原生 TLS 开关，需有有效证书。 |
| `TRUSTED_PROXY_CIDRS` | 逗号分隔的实际代理 CIDR | 可信代理来源，用于转发头与相关入口校验。 |
| `ALLOW_INSECURE_HTTP` | `false` | 显式允许非回环的明文管理监听；正式公网不推荐。 |

`PANEL_DOMAIN` 是管理面板的初始允许域名；`PANEL_ROUTE_DOMAIN` 是域名前缀站点使用的基础域名，例如 `media` 会生成 `media.example.com`。两者可以让面板和站点使用不同的公开地址，但 DNS、证书和反向代理仍需同时配置。面板域名、路由域名、监听端口与 TLS 设置初始化后保存在数据库。单纯修改同名启动环境变量，不一定覆盖已保存设置；在“TLS 设置”调整后按提示重启。分域名反代的完整示例见 [HTTPS 反向代理](reverse-proxy.md#separate-public-domains)。

面板的内部监听端口与网关的公开端口是两件事。站点复制地址及 Telegram 外部地址使用实际公开入口。

## 长期密钥与初始化

| 变量 | 用途 | 持久性 |
| --- | --- | --- |
| `JWT_SECRET` | 会话签名，至少 32 字节 | 轮换会使旧登录失效。 |
| `UPSTREAM_HEADER_KEY` | 固定上游 Header 加密，至少 32 字节 | 丢失后不能解密原固定 Header。 |
| `DYNAMIC_ROUTE_KEY` | 动态播放路由保护，至少 32 字节 | 轮换或重启会使已有动态播放路由不能继续复用。 |
| `MERIDIAN_SECRET_KEY` | Cloudflare / Telegram 凭据加密，至少 32 字节 | 丢失后不能正常解密相关凭据。 |
| `SETUP_TOKEN` | 首次创建管理员的凭据 | 初始化后不作为登录口令。 |

四个长期密钥必须相互独立。推荐分别生成 32 字节随机数据，再编码为十六进制字符串。不要使用示例固定值，不要让密钥随每次重启重新生成。

一键安装器与官方 Docker 入口脚本维护这些值。手工部署需要明确配置；没有持久 `JWT_SECRET` 时，进程级随机密钥会导致重启后会话失效，并限制外部凭据管理。

环境文件限制为 `0600`，数据目录限制为服务账户可访问。Windows 使用实际 ACL 保护。

## 证书、缓存与更新

| 变量 | 用途 |
| --- | --- |
| `PANEL_TLS_CERT_FILE` / `PANEL_TLS_KEY_FILE` | 高级部署的证书链与私钥路径覆盖。通常优先使用面板证书来源配置。 |
| `ASSET_CACHE_DIR` | 缓存目录，默认数据库旁的 `asset-cache`。 |
| `MERIDIAN_UPDATER_SOCKET` | 标准更新代理的本地 socket，由安装器管理。仅填写路径不等于安装了更新服务。 |
| `CLIENT_IP_REGION_ENDPOINT` | 默认 `https://api.ip.sb/geoip/{ip}`；设为 `off` 关闭外部归属地查询。 |

证书路径指实际运行环境中的路径；Docker 中使用容器内部路径与挂载。不要将 `.env` 直接上传给第三方排查。

## 命令行

```bash
# 启动，指定初始端口与数据库
./meridian --port 9090 --db /data/meridian.db

# 查看版本
./meridian --version

# 查看安装器可识别的功能标记
./meridian --version --features

# 检查本机正在运行的面板
./meridian --healthcheck
```

`-p` 是 `--port` 的别名。离线管理员重置见 [更新与维护](updates.md)。

## 哪些设置在面板中修改

| 设置 | 位置 |
| --- | --- |
| 站点回源、入口、播放、UA、缓存、配额和限速 | 站点编辑。 |
| 流量计费方向、周期、时区 | 全局设置 → 系统设置。 |
| 请求日志、保留期限、字段与显示列 | 全局设置 → 日志设置。 |
| 域名、监听端口、证书 | 全局设置 → TLS 设置。 |
| Token、管理员、日报目标与计划 | 全局设置 → Telegram Bot。 |

只修改必要项并重新验证访问。完整部署示例见 [Linux](install-linux.md)、[Docker](install-docker.md) 和 [反向代理](reverse-proxy.md)。
