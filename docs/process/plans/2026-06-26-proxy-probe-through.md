# 代理质检 probe-through(rank3)— 计划

状态:实现中
日期:2026-06-26
作者:Claude(Owner 全权 + 按 scoping 排期推进)

## 范围
admin 在 UI 点"测试"→ 服务端**通过该代理建隧道到一个固定 canary 目标**,测真实出站连通性 + 延迟,
区别于现有 proxyhealth 的被动裸 TCP 存活探测。这是**最高 SSRF 风险**切片(凭据解密 + 服务端主动出站)。

## 真码复用点(已核,避免另造敏感逻辑)
- 凭据解密:proxyadmin.Service **已持有** `keys credentialstore.KeyProvider`(service.go:29),
  复用 `proxysecret.Decode(ctx, keys, tenantID, stored)`(secret.go:41)解密 auth_secret。
- URL 构造:`url.UserPassword()` 自动 percent-encode(同 provider/postgres_proxy_resolver.go:234 范式),escape-safe。
- 隧道 dialer:transport/mimicry 已有 `proxyDialerFromURL`(proxy_dialer.go:28,处理 http CONNECT + socks5 握手),
  仅未导出 → 加薄导出包装 `DialerFromURL`。
- host 安全:proxyadmin 已有 `proxyHostSafe`(service.go:213,挡 loopback/内网/link-local/metadata),probe 前复校。
- 鉴权/租户门:proxyadminhttp 已有 `resolveTenant`(tenant_operator 自 scope / platform_admin via ?tenant_id+CanIssueForTenant)。

## SSRF 守卫(两条向量,均 fail-closed)
1. **代理 host 本身**(服务端第一跳):probe 前复跑 `proxyHostSafe(host)`,内网/metadata → 拒(ErrUnsafeHost)。
   绝不接受请求体里的临时 host;只按 stored proxy id 查。
2. **探测目标 canary**(CONNECT 第二跳):**服务端硬编码常量**(`api.anthropic.com:443`),admin **绝不能**指定;
   再经 ssrfpolicy.AllowsHost 复校。杜绝"代理隧道 + 任意目标 = 打内网/metadata 的双跳 SSRF"。

## 凭据/泄露守卫
- 解密后的 *url.URL(含凭据)**只用于内部拨号,绝不入日志、绝不进响应**。
- 响应只回 {ok, latency_ms, error_class, probed_at};error 归类成枚举(dial_timeout/tunnel_refused/tls_fail/
  unsafe_proxy_host/bad_proxy_url),**不回传原始错误**(防泄露内网拓扑/凭据片段)。

## 实现(blast radius:4 后端包,均非 §6 冻结)
- `proxyadmin`:加 `DialTarget(ctx, tenantID, id) (*url.URL, error)`(GetProxy → proxyHostSafe 复校 → 解密 → 建 URL)。
- `transport/mimicry`:加导出 `DialerFromURL(*url.URL) (ProxyDialerFunc, error)`(薄包装现有未导出实现)。
- `proxyhealth`:加 `ProbeThrough(ctx, policy, proxyURL, canary) ProbeResult`(建 dialer→隧道到 canary→可选 TLS 握手→
  测延迟→归类),凭据不外露。
- `proxyadminhttp`:加 `POST /{id}/test`,新 `Prober` dep,复用 resolveTenant。cmd/gateway 实现 Prober(组合
  DialTarget + ProbeThrough + 常量 canary + ssrfpolicy)。
- **零 schema 迁移、零 money、零写入(不落库,首版只返即时结果)**。

## 测试(变异须能红)
- proxyadmin DialTarget:host 不安全 → ErrUnsafeHost(变异:删 proxyHostSafe 复校 → 内网 host 应被放行 → 红)。
- proxyhealth ProbeThrough:canary 非白名单 → 拒(变异:删 canary 校验 → 红);拨号失败 → 正确 error_class。
- 端点:鉴权/租户隔离(越权 → 403);成功 → {ok,latency_ms};凭据/原始错误不在响应。
- 凭据不入日志:用 fake dialer 断言 proxyURL 不出现在任何回传字段。

## 后续(本切片不做)
- 质量字段持久化(success_rate 历史)= 第二切片(需迁移)。
- 自定义健康 URL 探测目标 = default-deny + 显式 allowlist,Owner-gated。
- 前端 proxies 列表页 + "测试"按钮:本切片若 backend 先行,前端作紧接切片。
