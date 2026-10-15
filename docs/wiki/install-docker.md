# Docker 部署

官方镜像为 `ghcr.io/snnabb/meridian`，提供 Linux amd64 和 arm64。`latest` 跟随最新正式版本；固定版本可使用 `v1.14.0`。

## 1. 准备目录与密钥

以下操作只在首次部署时执行。已有部署继续使用原来的 `.env`，不要重新生成密钥。

```bash
mkdir meridian
cd meridian
umask 077
for key in JWT_SECRET UPSTREAM_HEADER_KEY DYNAMIC_ROUTE_KEY MERIDIAN_SECRET_KEY SETUP_TOKEN; do
  printf '%s=%s\n' "$key" "$(openssl rand -hex 32)"
done > .env
chmod 600 .env
```

四个长期密钥必须独立且持久保存。它们分别保护会话、自定义 Header、动态播放路由和 Cloudflare / Telegram 凭据。

## 2. 创建 Compose 配置

保存为 `compose.yaml`：

```yaml
services:
  meridian:
    image: ghcr.io/snnabb/meridian:v1.14.0
    user: "10001:10001"
    restart: unless-stopped
    read_only: true
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE
    security_opt:
      - no-new-privileges:true
    tmpfs:
      - /tmp:rw,noexec,nosuid,size=16m
    ulimits:
      nofile:
        soft: 65536
        hard: 65536
    ports:
      - "127.0.0.1:9090:9090"
    volumes:
      - meridian-data:/app/data
    env_file:
      - .env
    environment:
      PANEL_BIND_ADDR: 0.0.0.0
      ALLOW_INSECURE_HTTP: "true"

volumes:
  meridian-data:
```

这个示例让应用以 UID/GID `10001` 运行，使用官方镜像初始化的命名卷。根文件系统只读，数据卷可写。官方程序带有 `NET_BIND_SERVICE` 文件能力，容器必须保留对应能力边界，否则执行程序可能返回 `Operation not permitted`。

容器内 HTTP 仅用于这一跳，宿主机端口限制在 `127.0.0.1`。远程访问时接入 [HTTPS 反向代理](reverse-proxy.md)，不要把该端口直接暴露到公网。

使用宿主机目录代替命名卷时，先确保该目录可由 `10001:10001` 读写，权限限制为 `0700`。不要以递归修改整个宿主机目录的方式解决权限问题。

## 3. 启动与初始化

```bash
docker compose up -d
docker compose ps
docker compose logs --tail 100 meridian
```

从受保护的 `.env` 中获取 `SETUP_TOKEN`，打开面板创建管理员。如果未显式传入密钥，官方入口脚本会将生成的密钥保存到数据卷中的 `.meridian-secrets`；新生成的初始化令牌会显示在容器日志中。

设置 `PANEL_DOMAIN` 为实际面板域名。要采纳反向代理提供的客户端地址和协议，还需将 `TRUSTED_PROXY_CIDRS` 设置为**容器实际看到的代理来源地址**。宿主机 Nginx 连接 Docker 映射端口时，来源可能是桥接网关，而非容器内的 `127.0.0.1`。

从 `v1.13.5` 起，由环境变量设置的 `PANEL_DOMAIN` 会随容器启动同步。改成新域名即可切换；移除该变量或设置 `PANEL_DOMAIN: ""` 后，旧域名限制会解除，无需删除数据库或数据卷。若使用了 `env_file`，也要检查其中是否仍定义同名变量。在面板手动保存域名或配置原生证书后，域名改由数据库管理；完整优先级与旧版迁移规则见 [配置与环境变量](configuration.md)。

修改 Compose 环境配置后重新创建容器（仅执行 `docker compose restart` 不会应用新环境）：

```bash
docker compose up -d
```

## 独立站点端口

路径入口不需要新增端口映射。独立端口入口则必须同时满足容器映射、防火墙和上层 CDN 的要求。

例如站点监听 `8001`，在 `ports` 中增加：

```yaml
      - "8001:8001"
```

低端口的实际可用性还取决于容器与宿主机的权限策略。不要把“主地址端口”当成“站点监听端口”。

## 更新与备份

1. 导出 [加密备份](backup-and-restore.md)，单独保存 `.env` 和数据卷。
2. 固定版本部署先修改 `image` 的版本标签；跟随正式版时可改为 `latest`。
3. 拉取并重建容器：

```bash
docker compose pull
docker compose up -d
docker compose ps
```

不要使用 `docker compose down -v` 更新，它会删除命名数据卷。Docker 部署不通过面板内更新替换镜像。

## 常见问题

| 现象 | 检查 |
| --- | --- |
| 数据目录不可写 | 命名卷是否来自官方镜像，或宿主目录是否属于 `10001:10001`。 |
| 创建后配置丢失 | `/app/data` 是否挂载持久卷。 |
| 重建后凭据不可解密 | `.env` 或卷中的长期密钥是否被重新生成。 |
| 路径入口可用，独立端口不通 | 新端口是否已映射并开放。 |
| 更换或移除域名后仍只认旧域名 | 确认镜像已更新到 `v1.13.5` 或更新版本并重建容器，检查 `env_file`；面板手动保存的域名在 TLS 设置中修改。 |
| 面板提示不能一键更新 | 属于预期行为，请通过 Compose 更新镜像。 |
