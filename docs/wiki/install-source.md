# Windows、macOS 与源码

Meridian 是单个程序，内嵌前端并使用 SQLite。运行预编译版本不需要 Go、Node 或外部数据库。

## 下载程序

在 [Releases](https://github.com/snnabb/Meridian/releases/latest) 下载对应平台：

| 平台 | 文件 |
| --- | --- |
| Linux x86-64 | `meridian-linux-amd64` |
| Linux ARM64 | `meridian-linux-arm64` |
| Windows x86-64 | `meridian-windows-amd64.exe` |
| macOS Intel | `meridian-darwin-amd64` |
| macOS Apple Silicon | `meridian-darwin-arm64` |

Release 同时附带 `SHA256SUMS` 和 Sigstore 签名。Linux / macOS 下载后先校验对应文件，再赋予可执行权限。

## Windows

使用受当前用户保护的数据目录，例如 `%LOCALAPPDATA%\Meridian`。检查该目录的 Windows ACL，避免将数据库放在所有用户都能读取的公共目录。

在 PowerShell 中，**仅首次部署**生成独立密钥并保存到当前用户环境：

```powershell
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$keys = 'JWT_SECRET', 'UPSTREAM_HEADER_KEY', 'DYNAMIC_ROUTE_KEY', 'MERIDIAN_SECRET_KEY', 'SETUP_TOKEN'
foreach ($key in $keys) {
    $bytes = New-Object byte[] 32
    $rng.GetBytes($bytes)
    $value = [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
    [Environment]::SetEnvironmentVariable($key, $value, 'User')
    Set-Item -Path "Env:$key" -Value $value
}
$rng.Dispose()
$dataDir = Join-Path $env:LOCALAPPDATA 'Meridian'
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
```

已有部署不要重跑密钥生成步骤。启动：

```powershell
.\meridian-windows-amd64.exe --port 9090 --db "$dataDir\meridian.db"
```

本机打开 `http://127.0.0.1:9090`，使用 `SETUP_TOKEN` 创建管理员。查看令牌时注意终端隐私，不要将它发布到 Issue。

Windows 不提供与 Unix `0600` 等价的自动 ACL 收紧；数据目录与环境配置的访问权限由部署者管理。远程使用应配置 HTTPS。

## Linux / macOS 手工运行

以下示例在受保护的工作目录中创建首次环境文件。已有部署使用原文件。

```bash
umask 077
for key in JWT_SECRET UPSTREAM_HEADER_KEY DYNAMIC_ROUTE_KEY MERIDIAN_SECRET_KEY SETUP_TOKEN; do
  printf '%s=%s\n' "$key" "$(openssl rand -hex 32)"
done > .env
chmod 600 .env
set -a
. ./.env
set +a
chmod 755 ./meridian
./meridian --port 9090 --db ./meridian.db
```

这里的 `meridian` 是下载后重命名的对应平台程序。默认绑定回环地址；通过 [反向代理](reverse-proxy.md) 或 [原生 TLS](tls-certificates.md) 提供远程访问。

如需后台启动，使用系统的服务管理器维护运行目录、环境、数据权限和重启策略。自定义服务不自动获得标准安装器的面板内更新能力。

## 从源码构建

需要 **Go 1.26.10 或更高版本**，无需前端打包。

```bash
git clone https://github.com/snnabb/Meridian.git
cd Meridian
go build -trimpath -buildvcs=false -o meridian ./cmd/meridian
```

Windows 可将输出改为 `meridian.exe`。交叉编译示例：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false \
  -o meridian-linux-arm64 ./cmd/meridian
```

构建产物仍需配置独立持久密钥、初始化令牌和数据目录。开发与验证流程见 [开发指南](development.md)。

## 测试版 beta

`beta` 测试分支按需从最新 `master` 创建，用于集中验证未发布改动；整合完成并正式发布后可清理。分支不存在时，使用正式 Release；下一轮测试开始后再按下面的方式获取构建。此前整合的泛域名 HTTPS、同机 Nginx 默认真实 IP、Go 1.26.10 与构建依赖更新已随 v1.14.2 发布。

存在活动测试分支时，Linux x86-64 可直接获取已构建的测试程序：

1. 登录 GitHub，打开 [beta 分支的 CI 运行](https://github.com/snnabb/Meridian/actions/workflows/ci.yml?query=branch%3Abeta)，选择最新成功的运行。
2. 在运行页面底部的 Artifacts 下载 `meridian-beta-linux-amd64-提交号`，解压得到 `meridian-linux-amd64`、`BETA_VERSION` 和 `SHA256SUMS`。产物保留 14 天。
3. 在解压目录运行 `sha256sum -c SHA256SUMS`，然后执行 `chmod 755 meridian-linux-amd64` 和 `./meridian-linux-amd64 --version`，确认与 `BETA_VERSION` 一致；再按上方手工运行方法配置测试实例。

测试版本号格式为 `当前版本-beta.提交号`，例如 `v1.14.2-beta.abcdef0`。使用独立测试数据目录；已有部署替换前备份程序、环境和数据库，并保留原密钥。

**下载 beta 分支的安装脚本不会自动安装 beta 程序。** 常规安装器和面板内更新仍下载正式 Release；测试分支产物不创建正式 Tag、Release 或 `latest` 镜像。ARM64、Windows 和 macOS 可从 `beta` 检出源码，再按上面的方式构建。

测试分支的 Wiki 会构建并检查链接，正式文档站仍由 `master` 发布。公开演示站显示其实际部署版本，不能仅凭分支推送就视为已更新演示程序。
