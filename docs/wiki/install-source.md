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

需要 **Go 1.26.9 或更高版本**，无需前端打包。

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
