# DEFERRED — OAuth 出站深层 SSRF / DNS-rebind 防御

> 创建: 2026-05-27 (Owner P1 follow-up)
> 关联: [Owner 2026-05-27 review report](../../../) (main 上 P1 报告)
> 前置: 静态 URL allowlist 已落地 in commit 39c66a3 (validateOAuthEndpointURL)

## 来源

Owner 2026-05-27 抓出 P1: OAuth token_url 缺 SSRF 防护。当前已落地的**静态层** (validateOAuthEndpointURL) 在 caller 写 endpoint 时拒绝 http:// / 127.0.0.1 / private IP / metadata IP / localhost name。但**DNS-rebind 攻击仍可绕过**: caller 配置 https://attacker.example, 该域 DNS 解到 127.0.0.1 → 静态 URL 检查全部通过 → 实际拨号命中内网 / metadata。

## 深层 guard 方案

复用 [internal/auth.newSSRFProtectedOAuthClient](../../../backend/internal/auth/antigravity_token_provider.go) pattern (已在 Antigravity refresh 路径验证):
- `transport.Proxy = nil` (防 CONNECT 转发到 proxy 后到内网)
- `transport.DialContext` 包 `ssrfGuardedDialContext` — 拨号前 resolve DNS, 拒 loopback / private / link-local / metadata / link-local-multicast / 100.64.0.0/10 (Carrier-grade NAT) / OAuth 特殊保留段
- `CheckRedirect` 返 `http.ErrUseLastResponse` 禁 3xx (防 attacker redirect 把 client_secret/authorization_code 渗到自家 endpoint)

## 阻塞原因 — 本切片未做

`internal/auth.newSSRFProtectedOAuthClient` 是 unexported, 需要 export。export 后 credentialacq.authorizationCodeOAuthExchanger 包它即生效, 但**现有 OAuth 单元 test 用 fake .test 域名** (oauth.example.test / antigravity.example.test / google.example.test) 走 `lookupOAuthIPAddrs` 真 DNS resolve, 找不到 host → SSRF guard 拒拨号 → 3 个既有 test 红:
- `TestAuthorizationCodeExchangeRejectsRefreshOnlyTokenResponse`
- `TestAntigravityOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint`
- `TestGeminiOAuthCallbackPostsAuthorizationCodeToConfiguredTokenEndpoint`

修复需要:
1. `internal/auth` 加 testing helper 让跨包 test 替换 `lookupOAuthIPAddrs` 返公网 IP (e.g., 8.8.8.8) 跳过真 DNS
2. 改造上述 3 个既有 test fixture 注入 mock lookup
3. 把 NewSSRFProtectedOAuthClient export
4. credentialacq.authorizationCodeOAuthExchanger 包 SSRF client

范围跨多 vendor adapter test + 跨包 testing infra, 非 5 分钟改动。

## 收口路径

- 选项 A (推荐): 单独切片 `P1-DEEP-SSRF`, 改 internal/auth lookup hook + 改 3 个 OAuth 集成 test + export + 接入 + mutation 自检 + 集成断言。
- 选项 B: 等 gemini OAuth fake → 真 切片落地时一并改造 (gemini/oauth 是当前主要受影响 vendor)。

## 当前层验证 (39c66a3 已落地)

静态层捕获 8 个攻击向量 (TestOperatorOAuthConfigRejectsSSRFEndpoints):
- http_scheme / loopback_host / private_net / localhost_name / metadata_ip / metadata_dns / link_local / data_url

未捕获的剩余攻击面: **DNS-rebind only** (caller 写公网 host 但 DNS 解析到内网)。
