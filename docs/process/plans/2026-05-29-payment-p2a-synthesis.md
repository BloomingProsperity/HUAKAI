# 支付 P2a 综合稿 (Claude ∥ Codex 交叉 + Owner 决策) — 2026-05-29

> CLAUDE.md #10 平行计划法综合。Claude 稿 `2026-05-29-payment-p2a-claude.md` ∥ Codex 稿 `2026-05-29-payment-p2a-codex.md` 独立成文后交叉。分支 work/quota-subsystem,实现由 Claude 直接写代码。

## 0. 三镜 shape inventory (CLAUDE.md #16 — 开写前调研成熟项目的完整功能形态)

支付「入账」的完整形态 (三默认镜子,clean-room 行为级,已 source-read):
- **sub2api** `@91da8159`:自动 webhook 入账 (`backend/internal/service/payment_fulfillment.go:70`) + 管理员手动 (`domain/constants.go` adjustment) + 退款 (`payment_refund.go`) + 幂等重放 (completed 短路 `payment_fulfillment.go:142`)。审计走专用 `payment_audit_logs` 表 + 细分动作枚举 (`payment_stats.go:155`、`payment_fulfillment.go:82-105`:PAYMENT_PROVIDER_MISMATCH / PAYMENT_AMOUNT_MISMATCH / PAYMENT_AFTER_EXPIRY)。
- **new-api** `@20d3e73`:webhook (stripe/creem/waffo `controller/topup_*.go`) + 管理员补单 (`model/topup.go:387` AdminCompleteTopUp) + 兑换码 + 返佣。审计走通用 `RecordTopupLog` + 来源/方式字段 (`model/topup.go:155、:387、:460`:admin / stripe / creem),不为每事件单建类型。
- **CLIProxyAPI** `@21fad9db`:**无等价物**——纯 relay account→API,`~/refs/CLIProxyAPI/internal/` 无 payment/order/billing/subscription 包 (grep `payment|billing|webhook|recharge` 命中全是 antigravity_credits vendor-quota + websocket relay)。

**结论形态**:入账 = {① 自动 webhook 入账 ② 管理员手动入账 ③ 退款 ④ 幂等重放 ⑤ 订阅/档位授予}。P1 已做②,P2a 做①,③=P5,⑤=P3。**没漏整条路径**(这是上一版「以为只有一套」的根因修正)。

## 1. Claude ∥ Codex 对齐情况

| 维度 | Claude 稿 | Codex 稿 | 结论 |
|---|---|---|---|
| 验签接口 | CallbackVerifier (HMAC + 常量时间) | WebhookVerifier (HMAC + 常量时间) | **一致** — 采 CallbackVerifier 命名 |
| 入账核心 | 复用 P1 ConfirmPaid+Fulfill | 复用 P1 ConfirmPaid+Fulfill | **一致** — 单一可信入账点 |
| ActorKind | confirmRecord 加 ActorKind (admin/system) | 同 | **一致** |
| 租户路由 | 签名体内含 tenant_id + out_trade_no | 同 (signed envelope 内 tenant) | **一致** — 租户来自已验签体,不来自 URL |
| 金额一致性 | 验签后比对 amount/currency/provider | 同 | **一致** |
| webhook handler | paymenthttp 公开端点 + body 限制 | paymenthttp 公开端点 + MaxBytesReader | **一致** |
| 测试 | W1-W6 真 PG 判别 | 5 PG + handler 单测 | **合并** — W1-W6 + handler 单测 |
| **审计粒度 (唯一实质分歧)** | 复用现有类型 + ActorKind 区分,**零 migration** | 新建 webhook_received/webhook_rejected 类型,**migration 0072** | **Owner 决策** ↓ |

## 2. Owner 决策 (D1 审计粒度 — AskUserQuestion 2026-05-29)

**选 Option A:复用现有审计类型 · 不改表**。
- webhook 入账复用 P1 现有 `payment_audit_events` 类型,`ActorKind=system` 区分自动 vs `admin` 手动;无 migration、小切片闭合。
- 参考对照 (#15):new-api 即此做法 (通用日志 + 来源字段,`model/topup.go:155/:387/:460`);sub2api 走专用细分类型 (`payment_fulfillment.go:82-105`) = 被否决的 Option B;CLIProxyAPI 无支付模块,无此问题。
- 后果:金额不符/验签失败的「拒绝」事件不单独留 payment_audit_events 行 (无对应现有类型),靠「订单停在 pending + 无 paid_confirmed 行 + app-log」体现。成功路径仍有 paid_confirmed(actor=system) + credited 全轨迹。granularity 取舍已被 Owner 接受。

## 3. 最终设计 (锁定)

- **零 migration**。复用 P1 表 + 入账 seam + 三重幂等。
- `provider.go`:加 `CallbackResult` + `CallbackVerifier` 接口;testProvider 加 secret + `VerifyCallback` (HMAC-SHA256 over raw body, `hmac.Equal` 常量时间比较, 验签通过才解析字段);`SignTestCallback` 导出供测试签名;manual provider 不实现 → webhook 返 provider-no-callback。
- `webhook.go` (新):`Service.ConfirmPaidByCallback(ctx, providerKind, rawBody, signature)` = resolve provider → 断言 CallbackVerifier → VerifyCallback(失败零入账) → GetOrderByOutTradeNo(已验签 tenant) → provider/amount/currency 一致性(不符零入账) → ConfirmPaid(system) → Fulfill(system)。
- `store.go`:confirmRecord 加 `ActorKind`;`service.AdminConfirmPaid` 显式传 admin(保 P1 行为);postgres+memory ConfirmPaid 的 paid_confirmed 审计改 `actorKindOrDefault(rec.ActorKind)`。
- `paymenthttp/webhook.go` (新):公开 `POST /v1/payments/webhooks/{provider}`,MaxBytesReader 1MiB,读 raw body + `X-Payment-Signature` 头 → service;错误映射(验签失败 401 通用化不泄露哪步、业务拒 409、订单不存在 404、provider 不支持回调 400、未知 provider 404);**无 session/admin 中间件**,安全靠验签。
- `cmd/gateway/routes.go`:顶层公开挂载(串行协调点,与新机 merge 时对齐;复用 d.paymentService,无新 wiring 字段)。生产未注册真 provider → 端点 fail-closed 拒一切,直到 P-RealMoney。

## 4. mutation-discriminating 真 PG 测试 (W1-W6 + handler 单测)

| 测试 | 守的缺陷 | 判别 fixture | mutation 变红 |
|---|---|---|---|
| W1 合法回调入账一次 | 验签/入账漏 | 签合法回调 → completed,余额=金额,1 credit/1 event/paid_confirmed(system) | 跳 Fulfill → 无 credit |
| W2 伪造签名零入账 | 跳过验签 | 篡改签名 → ErrCallbackUnverified,**零 credit**,订单仍 pending,DB 零交互 | 跳 hmac.Equal → 伪造入账 |
| W3 重放幂等 | 重放双账 | 同合法回调发两次 → 仅 1 credit,余额增一次,第二次 Idempotent | 漏 P1 幂等 → 双账 |
| W4 金额篡改拒 | 跳过金额校验 | 验签合法但金额≠单 → ErrCallbackRejected,零 credit | 删金额比对 → 错额入账 |
| W5 跨租户隔离 | 漏 tenant 谓词 | A/B 同 out_trade_no,签 B → 仅 B 入账,A 仍 pending/余额 0 | 漏 tenant → 串账 |
| W6 幽灵订单 | 误入账 | 合法签名但 out_trade_no 不存在 → ErrOrderNotFound,零 credit | 误建/误入账 |
| handler 单测 | body 不限/状态错映射 | fake service + raw body 透传 + MaxBytesReader cap + 错误→状态 | 改映射 → 状态错 |
- 自证:W1 vs W2 同测对比合法(增)vs 伪造(零)余额必须不同。

## 5. fusion-upgrade delta (三维, #12)
- **架构**:两入账路径(手动 admin + 自动 webhook)收敛到**同一 ConfirmPaid+Fulfill + 同一 billing_events seam + 同一审计表**;webhook 是薄验签适配层。参考(new-api/sub2api webhook 与 admin 各写额度)→ HUAKAI 升级 = 单一可信入账点。
- **算法**:HMAC 验签 + 金额/币种/provider 一致性 + 复用 P1 三重幂等(重放天然幂等);租户来自已验签体而非 URL(防越权路由)。
- **生态**:公开 webhook 端点 fail-closed(无真 provider 时拒一切);test provider 模拟签名使全链路真 PG 可测,真渠道 SDK Owner-gated 隔离。

## 6. Source files read
HUAKAI(SPECIFIER,自有):`internal/payment/{provider,service,store,store_postgres,store_memory,types,audit}.go`、`internal/paymenthttp/handler.go`、`cmd/gateway/{routes,wiring}.go`、`internal/payment/{store_postgres_integration_test,test_helpers_test}.go`。
参考(行为级 #12):sub2api@91da8159(`payment_fulfillment.go`、`payment_stats.go`、`payment_order_lifecycle.go`)、new-api@20d3e73(`model/topup.go`、`controller/topup_*.go`)、CLIProxyAPI@21fad9db(grep 全仓确认无 payment 模块 = no equivalent)。被引标识符未在散文 verbatim 复用。

Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
