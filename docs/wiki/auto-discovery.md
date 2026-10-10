# 自动发现

自动发现从受支持的上游响应中识别媒体后端，再通过 Meridian 的受限路由代理。它适合回源地址动态变化的站点。

## 使用条件

1. 部署有独立、持久的 `DYNAMIC_ROUTE_KEY`。
2. 站点选择“自动发现”。
3. 主地址与普通 Emby 登录正常。
4. 实际媒体目标满足所选策略的协议、端口和网络要求。

一键安装与官方 Docker 入口脚本会维护密钥；手工部署见 [配置参考](configuration.md)。

## 兼容级别

| 级别 | 常用来源 | 动态目标 | 用法 |
| --- | --- | --- | --- |
| Safe | HTTP 30x、PlaybackInfo | 公网 DNS 域名，HTTPS `443` | 上游结构标准，希望收窄自动发现范围。 |
| Compatible | HTTP 30x、PlaybackInfo、HLS、DASH | 公网 HTTP(S)，有效端口 | 默认，适合常见 Emby 播放链路。 |
| Extreme | 上述来源及扩展的结构兼容 | 仍仅公网 HTTP(S) | 明确需要扩展兼容时使用，界面要求额外确认。 |

提高兼容级别不会允许私网、回环、链路本地、自身目标或跳过 TLS 校验，也不会让客户端随意指定目标 URL。

## Safe 域名规则

Safe 的空规则表示允许符合网络校验的公网 DNS 域名，且仍只允许 HTTPS `443`；并非“允许所有地址”，也不接受 IP literal。

添加规则可以进一步收窄目标：

| 规则 | 例子 | 含义 |
| --- | --- | --- |
| 精确域名 | `media.example.com` | 只匹配该域名。 |
| 域名后缀 | `example.com` | 按 DNS 标签边界匹配该域及其子域，不匹配 `badexample.com`。 |

使用界面提供的规则类型填写，不把完整视频 URL 或令牌放入域名规则。Safe 不允许 HTTPS 降级。

## 播放路由与有效期

受支持的目标可改写为 Meridian 的 `/_meridian/d/<capability>` 地址。capability 绑定站点、策略、目标与有效期，只用于受限的播放请求。

- 不把这种地址当作永久分享链接。
- 站点重载、策略变化、重启或有效期结束后，旧地址可能失效。
- 出现路由失效时，让客户端重新获取播放信息并开始播放。
- 网关日志应脱敏 capability 路径，参见 [反向代理](reverse-proxy.md)。

## 观察记录

编辑站点，打开“播放设置 → 自动发现选项 → 安全规则与观察记录”。记录按媒体 authority、来源、处理阶段和结果聚合，不保存完整媒体 URL、查询参数、请求头或正文。

| 原因 | 含义 | 优先检查 |
| --- | --- | --- |
| `scheme_denied` / `port_denied` | 协议或端口不符合策略 | 实际媒体协议、端口与兼容级别。 |
| `domain_denied` | 未匹配域名规则 | Safe 规则是否覆盖真正的媒体域名。 |
| `address_denied` / `self_target` | 网络地址被拒绝 | DNS 中是否混入私网，或回指本机。 |
| `dns_failure` | DNS 解析失败 | 服务器 DNS 与媒体域名。 |
| `dial_failure` / `tls_failure` | 连接或证书验证失败 | 出站网络、证书链、域名和有效期。 |
| `redirect_loop` / `hop_limit` | 跳转循环或超出跳数 | 上游跳转配置。 |
| `parse_failure` / `hls_feature_denied` / `dash_feature_denied` | 结构或清单不受支持 | 上游格式；确有需要时再评估兼容级别。 |
| `capacity_limit` / `rate_limit` | 命中运行预算 | 并发量、反复生成的节点与服务资源。 |
| `capability_invalid` / `capability_expired` | 播放路由失效 | 重新获取 PlaybackInfo，不重复使用旧链接。 |

## 使用边界

自动发现会验证全部 DNS 回答并固定校验后的连接目标，要求有效的 HTTPS 证书，不转发管理会话或把固定上游 Header 带到任意跨域目标。

复杂的清单、DRM、第三方 Header 或特殊客户端仍可能不兼容。没有受支持的安全回退时，请求会失败，而不是直接放行原始目标。完整协议与容量边界见项目的 [安全策略](https://github.com/snnabb/Meridian/blob/master/SECURITY.md)。

已知且受信任的回源可考虑 [手动填写](playback-modes.md)，而不是通过提高兼容级别绕过网络校验。
