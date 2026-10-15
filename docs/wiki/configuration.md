# 配置参考

多数日常设置由面板保存到数据库。环境变量主要用于首次部署、监听绑定、信任来源和长期密钥。

## 基础环境变量

| 变量 | 默认 / 示例 | 用途 |
| --- | --- | --- |
| `PORT` | `9090` | 初始面板端口。已保存的面板监听设置优先。 |
| `DB_PATH` | `meridian.db`；Docker 为 `/app/data/meridian.db` | SQLite 文件位置。 |
| `PANEL_BIND_ADDR` | `127.0.0.1` | 面板监听地址；远程绑定需要明确配置。 |
| `PANEL_DOMAIN` | `panel.example.com` | 环境管理的面板允许域名；每次启动同步，移除或置空解除限制。面板手动保存或启用原生证书后，以数据库为准。 |
| `PANEL_ROUTE_DOMAIN` | `example.com` | 初始站点基础域名，可规范化通配前缀。 |
| `PANEL_TLS_ENABLED` | `false` | 初始原生 TLS 开关，需有有效证书。 |
| `TRUSTED_PROXY_CIDRS` | 逗号分隔的实际代理 CIDR | 可信代理来源，用于转发头与相关入口校验。 |
| `ALLOW_INSECURE_HTTP` | `false` | 显式允许非回环的明文管理监听；正式公网不推荐。 |

`PANEL_DOMAIN` 是管理面板的允许域名；`PANEL_ROUTE_DOMAIN` 是域名前缀站点使用的基础域名，例如 `media` 会生成 `media.example.com`。两者可以让面板和站点使用不同的公开地址，但 DNS、证书和反向代理仍需同时配置。从 `v1.13.5` 起，环境管理的面板域名随 `PANEL_DOMAIN` 在每次启动时同步；修改后使用新域名，移除或置空后解除旧 Host 限制。在“TLS 设置”手动保存域名或配置原生证书后，该域名改由数据库管理，后续环境变更不会覆盖它。`PANEL_ROUTE_DOMAIN`、监听端口与原生 TLS 开关仍为初始化默认值，已保存的数据库设置优先。分域名反代的完整示例见 [HTTPS 反向代理](reverse-proxy.md#separate-public-domains)。

旧版本未记录域名来源。升级时，仅配置面板域名、未配置节点基础域名或原生 TLS / 证书的 HTTP 部署会按环境管理迁移；其他旧配置保留数据库管理。这类旧 HTTP 部署如需保留域名限制，请保留 `PANEL_DOMAIN`；升级后可在面板保存一次，改为数据库管理。

已有数据目录时，域名变更按以下规则处理。Docker Compose 修改环境后需要重建容器，例如 `docker compose up -d --force-recreate`；仅执行 `docker compose restart` 不会更新容器的环境变量。

| 配置状态 | 替换、置空或删除 `PANEL_DOMAIN` 后重建 / 启动 | 修改位置 |
| --- | --- | --- |
| v1.13.5 首次由环境初始化的 HTTP 部署，包括面板和站点基础域名不同 | 同步数据库中的面板域名；置空或删除解除旧 Host 限制，保留站点配置 | 环境变量 |
| 已在“TLS 设置”手动保存，或配置过原生证书 / HTTPS | 保留数据库中的面板域名 | TLS 设置 |
| 从旧版升级，已有节点基础域名或原生 TLS / 证书配置 | 保留数据库中的面板域名，因为旧版没有记录来源 | TLS 设置 |
| 任意已初始化部署的 `PANEL_ROUTE_DOMAIN` | 替换、置空或删除均不覆盖已保存的节点基础域名 | TLS 设置；保存时会检查并迁移相关站点域名 |

因此，旧版分域名部署升级后只修改环境变量，仍可能看到旧的数据库值。这类部署应通过界面更新，不能把所有环境变量都视为每次启动时覆盖数据库的配置。

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
