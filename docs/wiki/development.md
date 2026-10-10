# 开发指南

Meridian 使用 Go 后端、SQLite 和内嵌的原生 JavaScript / CSS 前端。修改前先检查相关测试与现有模块，不需要单独的前端打包框架。

## 环境与构建

- Go 1.26.9 或更高版本。
- Node.js 用于前端测试与语法检查，运行应用不需要 Node。
- Bash、ShellCheck 用于安装器验证。
- Docker 用于容器入口测试。

```bash
git clone https://github.com/snnabb/Meridian.git
cd Meridian
go build -trimpath -buildvcs=false -o meridian ./cmd/meridian
```

本地运行仍需要持久密钥与初始化令牌，参考 [手工运行](install-source.md)。使用独立开发数据库和虚构站点，不把现有生产数据库直接作为测试数据。

## 主要目录

| 位置 | 内容 |
| --- | --- |
| `cmd/meridian/main.go` | 启动、组件配置与 API 路由。 |
| `cmd/meridian/app_*.go` | 面板、账户、站点和统计接口。 |
| `cmd/meridian/database*.go`、`*_repository.go` | SQLite、迁移与数据访问。 |
| `cmd/meridian/proxy_*.go`、`dynamic_*.go` | 代理生命周期与动态播放路由。 |
| `cmd/meridian/hls_*.go`、`dash_*.go` | 有界清单解析与改写。 |
| `cmd/meridian/telegram_*.go` | Bot API、菜单、填写流程与日报。 |
| `web/static` | 内嵌页面、字体、图标、JavaScript 与 CSS。 |
| `tests` | 前端及安装器回归测试。 |
| `install.sh`、`Dockerfile`、`docker-entrypoint.sh` | 安装、更新与容器运行。 |
| `docs/wiki` | 此 Wiki 的 Markdown 源文件。 |

## 验证改动

根据影响范围运行相关测试。共享后端行为或发布改动应执行完整竞态与静态检查：

```bash
go test -race ./...
go vet ./...
node --test tests/*.test.js
bash -n install.sh
shellcheck install.sh docker-entrypoint.sh
```

涉及安装器或 Docker 时，再运行对应脚本测试：

```bash
bash tests/install_test.sh
bash tests/install_tls_test.sh
bash tests/docker_entrypoint_test.sh
bash tests/dynamic_route_key_test.sh
bash tests/upstream_header_key_test.sh
```

CI 还检查非 Unix 构建目标、依赖漏洞、代码安全和发布产物。协议改动应覆盖有效播放与失败关闭行为，不只检查源码字符串。

## 修改前端

静态文件嵌入程序。交付构建需要重新编译 Go 程序；不要把浏览器里的旧缓存误认为新代码。

核对桌面与手机布局、表单数字边界、加载 / 错误状态、跨页导航，以及依赖后端能力的选项。用真实浏览器验证关键交互。

## 维护 Wiki

文档以 `docs/wiki` 为源，站点配置在 `mkdocs.yml`。在隔离环境中安装文档依赖并构建：

```bash
python3 -m venv .venv-docs
. .venv-docs/bin/activate
pip install -r tools/wiki/requirements.txt
mkdocs build --strict
mkdocs serve
```

修改页面后同步更新导航，验证相对链接、截图与中文搜索。示例只使用保留示例域名、虚构用户与站点，不记录真实凭据或私人部署数据。

合并到 `master` 的文档改动由 Wiki 工作流自动构建与发布。公开地址为 `https://emby.qd.je/wiki/`，通过独立的只读文档路由访问 GitHub Pages 构建产物。发布应用新版本时，核对文档功能边界与截图版本，不把测试中功能当成已发布行为。

## 参与项目

先阅读 [贡献说明](https://github.com/snnabb/Meridian/blob/master/CONTRIBUTING.md)。普通问题使用 Issue，安全问题私下报告。PR 描述应包含具体问题、行为变化与验证结果。
