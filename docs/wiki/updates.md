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

旧标准安装没有更新代理时，可运行新版安装器的 `install -y` 补装。自定义服务名、账户、目录和 Docker 不应强行启用标准更新代理。

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
