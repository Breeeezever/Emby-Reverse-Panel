# HTTPS 反向代理

推荐将 Meridian 的管理端口保持在本机，通过已有 Nginx 等反向代理提供 HTTPS。路径站点可与面板共享同一公开端口。

## 两种 HTTPS 方式

| 方式 | TLS 终止位置 | 内部连接 |
| --- | --- | --- |
| 外部反向代理 | Nginx / 网关 | 通常为本机 HTTP |
| Meridian 原生 HTTPS | Meridian | 可直接访问，也可再次经过网关 |

外部 HTTPS 不代表独立站点监听器已经启用 TLS。独立端口应以站点显示的实际协议为准。原生证书管理见 [TLS 证书](tls-certificates.md)。

## Meridian 配置

同机 Nginx 的常见环境配置：

```dotenv
PORT=9090
PANEL_BIND_ADDR=127.0.0.1
PANEL_DOMAIN=panel.example.com
PANEL_ROUTE_DOMAIN=example.com
TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128
```

这些内容添加到已有环境配置，保留所有原密钥。环境管理的 `PANEL_DOMAIN` 在每次启动同步，移除或置空解除旧域名限制。在面板手动保存域名或配置原生证书后，改由数据库维护。`PANEL_ROUTE_DOMAIN`、原生 TLS 和监听端口仍以已保存的面板设置为准；需要修改时在“TLS 设置”保存并按提示重启。

`TRUSTED_PROXY_CIDRS` 必须匹配 Meridian 实际看到的代理来源。Docker、远程网关和 IPv6 连接不能直接照搬同机地址，也不要填 `0.0.0.0/0`。

### 手动 Nginx、宝塔与 1Panel：日志只显示 127.0.0.1 {#manual-proxy-client-ip}

在宝塔、1Panel 或 Nginx 中新增反代，不会自动修改 Meridian 的环境配置。`TRUSTED_PROXY_CIDRS=` 为空时，Meridian 不采用代理传来的 `X-Real-IP`，新请求日志会记录直接连接它的 Nginx 地址。同机回源通常就是 `127.0.0.1`。安装器的 `install --domain` 会同时设置可信代理；原生 HTTPS 直连可以直接看到客户端地址，因此这两种方式通常没有该现象。

**同机原生安装**（Nginx 与 Meridian 在同一网络命名空间，Nginx 回源为 `127.0.0.1` 或 `::1`）可在安装器菜单选择“配置本机反向代理”，或执行：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) proxy --local
```

命令会在现有 `TRUSTED_PROXY_CIDRS` 中补充 `127.0.0.1/32,::1/128`，保留其他代理范围，备份 `.env` 后重启并检查 Meridian，失败则恢复原配置。域名、监听端口、证书、数据库和自定义 Nginx 配置不由此命令改写。重启会短暂中断现有连接。

也可备份并编辑实际生效的环境文件（标准安装为 `/opt/meridian/.env`），修改已有的这一项，避免重复定义，随后运行 `sudo systemctl restart meridian`。已有其他可信代理时保留它们。

```dotenv
TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128
```

Nginx 的反代 `location` 同时需要覆盖设置以下请求头；不要把浏览器提供的 `$http_x_real_ip` 直接传给 Meridian：

```nginx
proxy_set_header Host $http_host;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $remote_addr;
proxy_set_header X-Forwarded-Proto $scheme;
```

在宝塔/1Panel 保存配置后检查并重载其实际使用的 Nginx/OpenResty，再从另一台设备访问一个站点，检查**新产生**的请求日志；历史日志不会被改写。可额外发送伪造 `X-Real-IP` 的请求，确认日志仍记录真实连接地址。

**1Panel/Docker 或远程代理**不能直接使用 `proxy --local`：容器内的回环地址只代表该容器。应根据实际回源连接来源，填写代理的精确 IP CIDR（IPv4 `/32`、IPv6 `/128`，或专用代理网络的必要范围），在 Compose 的 `environment` / `env_file` 中修改后重新创建容器；仅 `docker restart` 不会加载新的容器环境。不要把所有 Docker 网段或公网地址都加入信任。另见 [Docker 安装](install-docker.md)。

如果已信任正确的代理，仍记录回环地址，应继续检查 Nginx 的 `$remote_addr` 是否早已被前置 stream/CDN 变成了代理地址，按下一节处理。

### Nginx stream 与真实 IP {#stream-client-ip}

直接通过 HTTP 层反代时，Nginx 覆盖设置 `X-Real-IP $remote_addr`，Meridian 仅信任 `TRUSTED_PROXY_CIDRS` 中的代理来源；登录限流和请求日志即可使用客户端 IP。无端口的 `443` 反代本身不会必然让所有用户共用一个 IP。

如果前面还有 stream 层，HTTP 层的连接来源可能已经变成 `127.0.0.1`。需要从仍能看到客户端来源的 stream 层传递 PROXY protocol，在后面的 **Nginx TLS 终结监听器**启用 `listen ... ssl proxy_protocol`，仅信任该 stream 来源（`set_real_ip_from`），并设置 `real_ip_header proxy_protocol`；再用恢复后的 `$remote_addr` 覆盖 `X-Real-IP`，转给 Meridian。HTTP realip 模块必须可用。

Meridian 原生监听器不直接解析 PROXY protocol，不能把带 PROXY 头的 TLS 流直接发给 Meridian。stream 的 `proxy_protocol on` 会影响该 server 下所有分流目标，包括 Xray；必须先确认所有目标支持，或将相应入口隔离。若前面的某层已经丢失真实 IP，后层仅增加 HTTP 头无法还原。安装器不会自动改写这些自定义分流规则。

## 使用两个公开域名 {#separate-public-domains}

需要从安装开始配置 `panel.example.com → Nginx → Meridian 面板` 与 `media.example.net → Nginx → Meridian 站点代理 → Emby`，请按 [面板与站点分域名部署](nginx-separate-domains.md) 操作。该指南包含 DNS、两张证书、完整 Nginx 配置、当前界面限制、站点 API 及维护注意事项。

面板和站点反代可以使用不同的公开域名。例如面板使用 `panel.example.com`，站点使用 `media.example.com`。这不是把站点回源地址改成另一个域名，而是让网关把两个 `Host` 都送到同一个 Meridian 进程，再由站点入口配置选择代理实例。

推荐的配置关系如下：

| 项目 | 示例 | 配置位置 |
| --- | --- | --- |
| 面板域名 | `panel.example.com` | 环境管理时使用 `PANEL_DOMAIN`；面板手动保存后使用“TLS 设置”中的域名 |
| 站点域名 | `media.example.com` | 保存节点基础域名后可在表单填写前缀；也可通过站点 API 的 `public_host` 配置完整域名 |
| 节点基础域名 | `example.com` | 用于生成域名前缀；API 直接填写完整 `public_host` 时不要求配置 |
| Meridian 内部地址 | `127.0.0.1:9090` | `PORT` / 面板监听设置 |
| 对外 HTTPS 端口 | `443` | Nginx 的 `listen` 和 DNS/CDN 入口 |

Nginx 可以为两个域名使用同一个 HTTPS server，或者分别建立两个 server；关键是两个 server 都要转发到同一个上游并保留 Host：

```nginx
map $http_upgrade $meridian_connection_upgrade {
    default upgrade;
    '' close;
}

server {
    listen 443 ssl;
    server_name panel.example.com media.example.com;

    ssl_certificate /etc/letsencrypt/live/example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:9090;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $meridian_connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

将站点的 `public_host` 设置为 `media.example.com` 后，访问地址是 `https://media.example.com/`；面板仍从 `https://panel.example.com/` 打开。若公开端口不是 `443`，两个地址都要带实际公开端口。Telegram Bot 的“面板外部地址”填写面板地址，不要填写站点域名。

网关必须把 `Host` 原样传给 Meridian。若所有请求都被改写成面板域名，Meridian 只能看到管理入口，站点请求会返回未识别 Host。若前置代理不是本机回环地址，还要把它的实际来源加入 `TRUSTED_PROXY_CIDRS`；面板监听在回环地址时通常无需额外放开站点入口。

`PANEL_DOMAIN` 在环境管理模式下可通过修改、移除或置空后重启来同步。面板手动保存或配置原生证书后，域名由数据库维护；`PANEL_ROUTE_DOMAIN` 和 `PORT` 仍是首次初始化默认值。数据库管理的配置在 TLS 设置修改；使用外部 HTTPS 代理时，内部端口切换应由安装器完成，并同步 Nginx。旧数据库迁移规则见 [配置与环境变量](configuration.md)。

### 面板和站点使用不同的基础域名

例如面板使用 `panel.example.com`，站点使用 `media.example.net`。新部署可在首次启动前填写：

```dotenv
PANEL_DOMAIN=panel.example.com
PANEL_ROUTE_DOMAIN=example.net
```

`PANEL_ROUTE_DOMAIN` 用于把 `media` 这样的前缀生成完整站点域名。配置了可信代理和节点基础域名后，外部 HTTPS 部署也可在表单选择域名前缀；网关仍须配置泛域名证书和节点转发。通过下方 API 填写完整 `public_host` 时，可以省略 `PANEL_ROUTE_DOMAIN`。

需要管理节点基础域名的已有部署，可在“TLS 设置”填写完整面板域名 `panel.example.com` 和节点泛域名 `*.example.net`，检查、保存后按提示重启；只改环境中的 `PANEL_ROUTE_DOMAIN` 不会改动已保存的基础域名。改变基础域名会迁移使用旧基础域名的站点入口，应先准备新站点域名的 DNS、证书与网关配置。

外部 Nginx 可以在同一 `443` 端口使用两个 HTTPS server，分别管理证书：

| Nginx `server_name` | 证书覆盖范围 | Meridian 上游 |
| --- | --- | --- |
| `panel.example.com` | `panel.example.com` | `http://127.0.0.1:9090` |
| `*.example.net` | `*.example.net` | `http://127.0.0.1:9090` |

两个 server 分别设置自己的 `ssl_certificate` / `ssl_certificate_key`，并使用上文保留 `Host` 的 `location` 配置。DNS 需将两组域名指向相应网关。`*.example.net` 的证书不能用于 `panel.example.com`。

Meridian 当前的原生 HTTPS 共用一套证书，内置 Cloudflare 申请流程要求面板属于节点基础域名。不同基础域名使用原生 HTTPS 时，需导入**同一张同时覆盖** `panel.example.com` 和 `*.example.net` 的证书；目前不能分别导入两张证书并按域名自动选择。需要独立申请、续期两张证书时，使用外部网关管理。

仅更换环境管理的面板域名不会迁移独立站点域名或改写站点配置；`https://面板域名/watch/` 这类路径入口则需要使用新的面板域名。移除面板域名会解除面板 Host 限制，但已配置的站点 Host 仍优先进入站点代理。重建容器或重启服务期间会短暂中断连接；测试通过不代表公共 DNS、防火墙或你的网关配置已自动完成。

### 外部 HTTPS 模式创建域名站点 {#external-https-host-site}

配置节点基础域名和可信代理后，可直接在新增站点表单选择“域名前缀”，无需在 Meridian 中启用原生证书。下面的 API 方式也适用于完整域名不属于已配置节点基础域名的情况。

在已登录的面板页面打开浏览器开发者工具的控制台，替换域名与 Emby 回源地址后执行一次：

```javascript
await (async () => {
  const response = await fetch('/api/sites', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      name: 'Emby',
      ingress_mode: 'host',
      public_host: 'media.example.net',
      target_url: 'http://127.0.0.1:8096'
    })
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || `HTTP ${response.status}`);
  console.log('站点已创建', result.id, result.public_host);
})();
```

浏览器会使用当前登录会话提交同源请求。`target_url` 是 Meridian 能访问的实际 Emby 地址；上例只适用于 Emby 位于同一服务器且端口为 `8096`。完整 `public_host` 不必属于已配置的节点基础域名，也不要求设置 `PANEL_ROUTE_DOMAIN`。刷新面板后可以查看站点和流量；当前域名前缀编辑表单不能表达所有这类完整域名配置，后续改入口时仍应使用 API。

成功后分别访问面板域名和站点域名，确认前者打开 Meridian、后者进入 Emby。只使用 `media.example.net` 时，网关证书覆盖该具体域名即可；配置 `*.example.net` 泛域名证书需要通过证书工具的 DNS 验证流程。

## Nginx 示例

以下内容放在 Nginx 的 `http` 上下文中，通常可使用被 `http` 引入的 `conf.d` 文件。证书文件须事先存在，域名和路径替换为你的实际配置。

```nginx
map $http_upgrade $meridian_connection_upgrade {
    default upgrade;
    '' close;
}

map $uri $meridian_log_path {
    default $uri;
    ~/_meridian/d/ /_meridian/d/[REDACTED];
}

log_format meridian_redacted '$remote_addr [$time_local] '
    '"$request_method $meridian_log_path $server_protocol" '
    '$status $body_bytes_sent';

server {
    listen 80;
    server_name panel.example.com;
    access_log /var/log/nginx/meridian_access.log meridian_redacted;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name panel.example.com;
    ssl_certificate /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;

    client_max_body_size 300m;
    access_log /var/log/nginx/meridian_access.log meridian_redacted;

    location / {
        proxy_pass http://127.0.0.1:9090;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $meridian_connection_upgrade;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

关键点：

- `proxy_pass` 保留完整请求路径，`/cinema/` 前缀交给 Meridian 处理。
- 保留外部 Host 和协议，支持同源管理请求。
- 关闭代理缓冲以支持实时事件与媒体传输。
- 转发 Upgrade 以支持 WebSocket。
- 提高请求体上限以允许上传加密备份；应用仍执行自己的大小校验。
- access log 不包含查询参数，并对动态播放 capability 路径脱敏。

加载前检查配置：

```bash
sudo nginx -t
sudo systemctl reload nginx
```

## 非默认公开端口

例如外部监听 `8443`、内部仍为 `9090`：

```text
面板：https://panel.example.com:8443
路径站点：https://panel.example.com:8443/cinema/
```

应保留 Nginx 的实际公开端口，并同步修正 HTTP 重定向。Telegram 的“面板外部地址”也填写带 `8443` 的真实地址，不填内部 `9090`。

## CDN 与客户端 IP

使用 CDN 时还需确认端口、WebSocket、长连接和媒体传输策略。管理 API、动态播放地址及认证响应不应被 CDN 强制缓存。

若要在 Nginx 中恢复 CDN 前的真实客户端 IP，只信任供应商公布的实际出口网段，并在 Nginx 先正确配置 real IP。Meridian 再信任 Nginx 的来源地址，不直接信任任意浏览器提供的转发头。

## 常见错误

| 现象 | 原因与处理 |
| --- | --- |
| 站点请求落到管理面板 | 路径前缀被网关剥离，检查 `proxy_pass` 的 URI 规则。 |
| 日志 IP 全是 127.0.0.1 或容器网关地址 | 按[手动反代真实 IP 配置](#manual-proxy-client-ip)核对生效的可信代理 CIDR、X-Real-IP 覆盖和前置转发层。 |
| 保存提示同源校验失败 | 检查 Host、外部协议、可信代理和浏览器实际访问地址。 |
| 备份上传返回 413 | 调整网关请求体上限，再检查备份文件大小。 |
| 面板能打开但实时状态不更新 | 检查 SSE 缓冲、连接超时与 CDN 行为。 |
| HTTPS 面板可用但独立端口握手失败 | 面板 HTTPS 在网关终止，独立端口可能仍是 HTTP。 |
