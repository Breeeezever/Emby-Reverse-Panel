# 更新与维护

“全局设置 → 版本更新”检查 GitHub 最新正式版本。更新方式取决于实际部署方式。

## 更新方式

| 部署 | 更新方式 |
| --- | --- |
| 标准 Linux systemd 安装 | 面板内更新或一键安装器 `update`。 |
| Docker / Compose | 拉取镜像并重建容器，保持数据卷与密钥。 |
| Windows、macOS、手工或自定义目录 | 停止服务，替换对应平台程序后重新启动。 |

“检查新版本”可用，不代表当前部署支持面板内替换程序。

## 面板内更新

标准 Linux 安装器配置独立更新服务。面板向固定的本机更新代理发起任务，由更新服务执行受保护的安装流程。

1. 打开版本更新，检查最新正式版本与发布说明。
2. 导出 [加密备份](backup-and-restore.md)，确认环境配置与密钥已保存。
3. 确认更新，查看状态。
4. 等待服务重启与面板恢复，核对版本及站点功能。

下载执行 SHA-256 校验，有签名清单时验证 Sigstore；安装器创建备份、执行健康检查，失败时尝试回滚。

### v1.13.4 升级到 v1.13.5

已启用更新服务的标准 Linux 安装，使用 Nginx 提供 HTTPS、Meridian 内部提供 HTTP 时，可以直接在面板更新。

**原生 HTTPS 的旧安装需要先修复一次更新组件。** v1.13.4 随附安装器的健康检查固定使用 HTTP，会把正常运行的原生 HTTPS 判定为失败并触发回滚。面板显示“支持更新”只说明更新服务可连接，无法识别这个旧脚本问题。

先在服务器执行下面的命令。已有安装的 `install -y` 保留当前程序版本、域名、证书和数据，只补齐安装配置并更新组件；不要附加 `--domain`、`--port` 或 `--no-domain`：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) install -y
```

完成后回到“全局设置 → 版本更新”，检查更新并点击“更新并重启”。修复后的安装器使用程序自带的本机健康检查，读取实际端口和数据库中的域名，并校验证书，兼容 HTTP 与原生 HTTPS。

这项修复于 v1.13.5 发布后同步到主分支安装器；已下载到服务器的旧更新脚本不会因 GitHub 发布新版而自动刷新。因此原生 HTTPS 用户不能省略首次组件修复，也不需要重装面板或重新申请证书。

新版安装器在首次安装、升级和检查到已是最新版时，都会配置或修复面板更新服务。旧标准安装没有更新代理时，在服务器运行一次：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) update -y
```

这次操作会保留配置并补装更新服务，之后可直接在面板更新。仅修复更新组件时也可使用 `install -y`。自定义服务名、账户、目录和 Docker 不应强行启用标准更新代理。

## 安装器更新

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) update
```

不要在同一部署上同时运行多个更新任务。更新时正在播放的连接可能中断，选择空闲时间执行。

## Docker 更新

固定版本部署先调整 `image` 标签，然后执行：

```bash
docker compose pull
docker compose up -d
docker compose ps
```

保留 `.env` 和数据卷，不使用 `down -v`。完整示例见 [Docker 部署](install-docker.md)。

## 手工更新

1. 备份数据、密钥和服务配置。
2. 停止运行中的 Meridian。
3. 下载与当前平台匹配的正式版本，校验文件。
4. 替换程序，保留原工作目录、数据库和环境。
5. 启动服务，检查版本、认证、站点和真实播放。

可以确认程序版本：

```bash
./meridian --version
```

## 回滚

安装器的失败回滚用于更新事务内的恢复。更新成功后，手动降级需要核对数据库结构与版本兼容性。

跨结构版本降级时，恢复升级前对应版本的数据库与环境备份，再使用旧程序。不要仅替换二进制并继续使用新版本修改过的数据库。

## 忘记管理员密码

标准 Linux 部署使用：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh) password
```

其他部署先停止服务，再通过标准输入执行离线命令：

```bash
read -r -s -p '新密码: ' MERIDIAN_ADMIN_PASSWORD
printf '\n'
printf '%s\n' "$MERIDIAN_ADMIN_PASSWORD" | ./meridian admin reset-password \
  --db /data/meridian.db --password-stdin
unset MERIDIAN_ADMIN_PASSWORD
```

数据库必须已经存在且只有一个管理员。标准安装器同时轮换会话密钥，使旧登录失效；手工部署还应按实际环境管理会话密钥并重启服务。
