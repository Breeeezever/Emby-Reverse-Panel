# 面板与站点分域名部署

目标：

```text
panel.example.com → Nginx HTTPS → Meridian 管理面板
media.example.net → Nginx HTTPS → Meridian 站点代理 → Emby
```

以下适用于同机 Linux 一键安装的外部 Nginx 模式：Meridian 在 `127.0.0.1:9090` 提供 HTTP。示例域名、邮箱、端口和 Emby 地址需替换成自己的实际值。已有原生 HTTPS 实例不能只把变量改成 false 就套用这份 HTTP 回源配置；数据库保存的 TLS 和监听设置需要单独迁移。

## 1. 配置 DNS

将 `panel.example.com` 和 `media.example.net` 的 A 记录指向服务器公网 IPv4；Cloudflare 先使用“仅 DNS”。如果配置 AAAA，也要正确指向同一服务的 IPv6。服务器应允许公网访问 80 和 443，HTTP-01 证书验证需要 80。

## 2. 建立面板的 Nginx HTTPS 入口

新安装可使用项目一键安装命令：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) install \
  --port 9090 --domain panel.example.com --email admin@example.com -y
```

该流程安装 Nginx / Certbot，申请面板证书并设置面板的代理。已经按此方式安装且面板 HTTPS 正常的部署，直接继续下一步。

内部配置应为：

```dotenv
PORT=9090
PANEL_BIND_ADDR=127.0.0.1
PANEL_DOMAIN=panel.example.com
TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128
```

保留原来的持久密钥。此流程不需要在 Meridian 中申请原生证书，也不要求设置或修改 `PANEL_ROUTE_DOMAIN`。下面直接指定完整站点域名。

## 3. 新增站点的 Nginx 配置

在服务器创建 `/etc/nginx/conf.d/meridian-media.conf`，写入以下内容，并将域名和内部端口换成实际值：

```nginx
map $http_upgrade $meridian_media_connection {
    default upgrade;
    '' close;
}

server {
    listen 80;
    listen [::]:80;
    server_name media.example.net;
    client_max_body_size 300m;
    access_log off;

    location / {
        proxy_pass http://127.0.0.1:9090;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $meridian_media_connection;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

这是新增媒体站点配置，面板继续使用安装器创建的 `/etc/nginx/conf.d/meridian-panel.conf`。两个入口都回源同一个 Meridian HTTP 端口，保留客户端请求的 Host。

检查并加载：

```bash
sudo nginx -t && sudo systemctl reload nginx
```

申请媒体域名证书并让 Certbot 配置 HTTPS：

```bash
sudo certbot --nginx \
  -d media.example.net --cert-name media.example.net \
  --email admin@example.com --agree-tos --redirect

sudo nginx -t && sudo systemctl reload nginx
```

确保 Certbot 的定时续期任务保持启用。可以验证该证书的续期流程：

```bash
sudo certbot renew --cert-name media.example.net --dry-run
```

这里申请具体域名 `media.example.net` 即可。未来要覆盖任意一层子域名时，再通过 acme.sh 或 Certbot 的 DNS-01 流程申请 `*.example.net`，将 Nginx 的证书及 `server_name` 对应调整；各站点仍需在 Meridian 中注册。

## 4. 在 Meridian 创建域名站点

配置可信代理和节点基础域名后，也可在表单创建域名前缀站点。本页的完整域名无需属于面板中保存的节点基础域名，因此仍使用已有的站点 API；后端在 v1.13.4 和 v1.13.5 均支持该方式。

登录 `https://panel.example.com/`，打开浏览器开发者工具（F12）的“控制台 / Console”。将下面的站点域名和 Emby 回源地址替换为真实值后执行一次：

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

`target_url` 是 Meridian 服务器能访问的原 Emby 地址。如果 Emby 在别的机器上，填写那台机器的地址；如果上游使用 `/emby` 基础路径，保留该路径。浏览器提交使用当前面板登录状态，不需要复制登录 Token。

看到“站点已创建”后刷新面板。通过完整 `public_host` 建立的站点不需要节点基础域名；但当前域名前缀编辑表单不能完整表达这种配置，后续修改入口域名仍应使用站点 API。

## 5. 验证

- `https://panel.example.com/`：Meridian 登录 / 管理界面。
- `https://media.example.net/`：Emby 界面，使用原 Emby 账户登录。
- 在 Meridian 的站点列表确认站点正在运行，实际播放并检查统计。

若媒体域名显示 Meridian 面板，应核对 API 中 `public_host` 是否正确，以及 Nginx 是否保持 `Host $http_host`。若为 502，检查 Meridian 实际监听协议与端口；若为 421，检查完整站点域名是否已经成功注册。

## 注意事项

- **表单与完整域名**：可信外部 HTTPS 代理可使用域名前缀表单；本页通过 API 注册独立的完整站点 Host。使用现有网关时无需在 Meridian 中申请或导入另一套原生证书。
- **证书覆盖**：两个域名需要各自匹配的证书。只使用 `media.example.net` 时不需要泛域名证书；`*.example.net` 也不能覆盖 `panel.example.com`。当前 Meridian 原生 HTTPS 共用一套证书，不支持面板和站点分别管理两张证书。
- **域名来源**：完整 `public_host` 不依赖 `PANEL_ROUTE_DOMAIN`。已有数据库中的节点基础域名不会随该环境变量变化；手动保存或配置过原生 HTTPS 的面板域名也以数据库为准，见 [配置参考](configuration.md)。
- **保留 Host**：两个 Nginx 入口都转发到 Meridian，由请求的 Host 区分管理面板和媒体站点。不能把媒体请求的 Host 改成面板域名，也不能把媒体入口直接代理到 Emby 后仍期待 Meridian 统计该流量。
- **内部协议与端口**：此示例回源为 HTTP。已有原生 HTTPS 部署需按实际协议、端口和证书验证配置回源，或先完成部署方式迁移；单改环境变量不会覆盖已保存的 TLS 状态。
- **修改端口与升级**：安装器只维护其面板 Nginx 文件；本页新增的 `meridian-media.conf` 由管理员维护。未来切换 Meridian 内部端口时也要同步修改媒体配置中的 `proxy_pass`，检查并重载 Nginx，否则媒体入口会出现 502。
- **客户端地址与 Docker**：本页仅适用于 Nginx 和非容器 Meridian 同机部署。Docker 的容器监听、端口映射及可信代理来源不同，见 [Docker 部署](install-docker.md)。不要直接把容器内 `127.0.0.1` 当成宿主机，也不要把所有来源设为可信代理。
- **使用与维护**：保留数据库和持久密钥，确认 Certbot 自动续期任务运行。Meridian 服务重启会中断现有连接；站点实际播放仍取决于 Emby、回源网络及客户端支持。

此方式已通过隔离环境中的真实 Nginx HTTPS、面板登录、站点创建 API、Host 分流和上游转发验证。公共证书签发与实际播放仍需按自己的 DNS、服务器和媒体源完成验证。
