# 入口模式

入口决定客户端如何访问站点，主地址决定 Meridian 如何连接 Emby。两者互不替代。

## 如何选择

| 模式 | 示例地址 | 适用情况 | 额外要求 |
| --- | --- | --- | --- |
| 路径 | `https://panel.example.com/cinema/` | 新建默认，已有 HTTPS 面板时最省事 | 客户端保留路径，网关不剥离前缀。 |
| 独立端口 | `http://panel.example.com:8001` | 需要独立监听或兼容不支持基础路径的客户端 | 端口可绑定、防火墙放行，Docker 映射，CDN 支持。 |
| 域名前缀 | `https://cinema.example.com:9090` | 每个站点使用自己的子域名 | 路由域名、DNS、匹配证书与原生 TLS 就绪。 |

示例端口只用于说明。实际地址以界面显示和网关配置为准。

## 路径入口

例如路径前缀填写 `cinema`，从共享面板入口访问：

```text
https://panel.example.com/cinema/
https://panel.example.com:8443/cinema/
```

不会为每个站点额外开放监听端口。反向代理必须把完整请求路径传给 Meridian，由 Meridian 去掉站点前缀并回源。

前缀规则：

- 单个路径段，最长 64 个字符。
- 使用英文字母、数字、连字符 `-` 或下划线 `_`，保存时规范为小写。
- 同一个部署中必须唯一。
- 不能占用 `api`、`css`、`js`、`fonts`、`_meridian` 等面板保留路径。

客户端填写完整地址，不要只保留域名。上游本身使用 `/emby` 时，按实际主地址填写其基础路径。

## 独立端口

为站点指定一个 `1–65535` 的有效整数端口。它是 Meridian 的监听端口，**不是 Emby 回源端口**。

检查以下条件：

1. 没有与面板、其他站点或其他程序冲突。
2. 服务账户有权限绑定该端口；低于 `1024` 的端口可能需要额外能力。
3. 防火墙与服务器安全组允许访问。
4. Docker 已添加对应映射。
5. 前置 CDN / 网关支持该端口和媒体请求。

协议由 Meridian 的站点监听器决定。若只是外部 Nginx 将面板包装为 HTTPS，独立端口仍可能是 HTTP；不能把面板的 `https://` 自动套到它上面。

## 域名前缀

先在 [TLS 设置](tls-certificates.md) 配置面板域名、节点泛域名和证书。满足能力条件后，新增站点即可选择域名前缀。

例如：

```text
面板域名：panel.example.com
节点泛域名：*.example.com
站点前缀：cinema
站点域名：cinema.example.com
```

证书必须覆盖 `panel.example.com` 与 `*.example.com`，DNS 应将实际站点子域名或通配记录解析到入口服务器。通配符只覆盖一层子域名，不覆盖 `deep.cinema.example.com`。

域名前缀使用共享监听入口，不需要新增站点独立端口。外部反向代理须识别站点 Host、提供覆盖该域名的证书，并将原 Host 转发给 Meridian。

只配置外部网关 HTTPS，不会自动开启新建表单的原生 TLS 能力。暂时不能选择域名前缀时，可先用路径入口。

## 面板域名与站点域名分开 {#separate-panel-and-site-domains}

面板域名和站点反代域名可以是两个地址。面板域名只负责登录和管理 API；站点的 `public_host` 负责按 Host 将请求交给对应的 Emby 代理。两者共用 Meridian 的面板监听器，但必须在 DNS 和反向代理中同时指向这台 Meridian。

例如：

```text
面板：panel.example.com
反代：media.example.com
节点泛域名：*.example.com
```

配置步骤：

1. 将 `panel.example.com` 和 `media.example.com` 都解析到入口服务器。
2. 在“全局设置 → TLS 设置”保存面板域名 `panel.example.com`、节点泛域名 `*.example.com` 和 Meridian 的**内部监听端口**。
3. 在 Nginx 等网关中为两个域名配置 HTTPS，并把请求转发到同一个 Meridian 监听端口，保留原始 `Host`。证书必须覆盖两个域名；使用 `*.example.com` 时，它会覆盖这两个一层子域名。
4. 添加站点时选择“域名前缀”，填写 `media`。Meridian 会生成 `media.example.com`，客户端使用该地址访问站点。

如果站点域名不在 `PANEL_ROUTE_DOMAIN` 对应的基础域名下，也可以通过 API 直接填写完整 `public_host`，但仍需自行准备 DNS、证书和网关路由；面板表单的“域名前缀”只生成 `前缀.PANEL_ROUTE_DOMAIN`。

`PANEL_DOMAIN` 和 `PANEL_ROUTE_DOMAIN` 只用于首次启动时初始化数据库。数据库已经有面板设置时，单独修改 `.env` 不会可靠覆盖已保存值；请在 TLS 设置中保存新值，并按页面提示重启。修改基础域名后，Meridian 会同步迁移由旧基础域名生成的站点域名，并检查冲突。

面板和站点域名共用入口时，外部端口可以相同或不同。客户端填写公开网关地址；`PORT`、面板设置中的监听端口和 Nginx 的 `listen` 端口分别属于内部监听、应用配置和公开入口，不能混为一谈。

## 旧版本配置

旧数据库或 API 可能保留 `both`（域名与端口同时提供）或未配置状态。当前新增表单提供上表三种模式。编辑旧站点时，应核对最终访问地址并明确选择所需入口。
