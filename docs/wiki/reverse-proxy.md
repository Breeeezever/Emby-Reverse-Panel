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
TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128
```

这些内容添加到已有环境配置，保留所有原密钥。`PANEL_DOMAIN`、面板 TLS 和监听端口在已初始化的数据库中由面板设置维护；需要修改时在“TLS 设置”保存并按提示重启。

`TRUSTED_PROXY_CIDRS` 必须匹配 Meridian 实际看到的代理来源。Docker、远程网关和 IPv6 连接不能直接照搬同机地址，也不要填 `0.0.0.0/0`。

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
| 保存提示同源校验失败 | 检查 Host、外部协议、可信代理和浏览器实际访问地址。 |
| 备份上传返回 413 | 调整网关请求体上限，再检查备份文件大小。 |
| 面板能打开但实时状态不更新 | 检查 SSE 缓冲、连接超时与 CDN 行为。 |
| HTTPS 面板可用但独立端口握手失败 | 面板 HTTPS 在网关终止，独立端口可能仍是 HTTP。 |
