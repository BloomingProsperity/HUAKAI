# 支付子系统 Slice P2a 实施计划 — Claude 独立稿 (2026-05-29)

> CLAUDE.md #10 平行计划法 Claude 一侧,独立成文未参考 codex 稿。分支 work/quota-subsystem。实现由 Claude 直接写代码。

## 0. 信封 (Owner 已澄清)
- 入账**两套并存**:①自动(支付回调 webhook→验签→入账)②手动(管理员,P1 已做)。两套共用 P1 的入账核心。
- P2a = **自动回调路径**。真实渠道 SDK/商户密钥延后 P-RealMoney;P2a 用 test provider HMAC 模拟签名回调,全链路真 PG 可测。
- 参考确认两套都有:new-api `controller/payment_webhook_availability.go`(回调)+ `controller/topup.go:493`(管理员补单);sub2api payment 回调履约 + `domain/constants.go:51`(admin adjustment)。

## 1. 范围 (小切片闭合, 无 migration)
- **In**:Provider 验签接口扩展;test provider HMAC 验签;`ConfirmPaidByCallback` service 方法;webhook handler(paymenthttp);复用 P1 `store.ConfirmPaid`(加 ActorKind)+ `Fulfill`;审计。
- **Out**:真渠道 SDK(P-RealMoney);多 provider DB 实例 + LB + 限流(P2b);真 provider 的全局 callback_token 路由(需 migration,延后)。
- **不碰**:冻结 gatewayhttp/gateway/proto;internal/billing;migration(P2a 零 schema 改动)。

## 2. 设计

### Provider 验签接口 (provider.go 扩展)
```go
type CallbackResult struct {
    TenantID        int64
    OutTradeNo      string
    PaidAmountCents int64
    ProviderRef     string
}
// 仅回调型 provider 实现; manual provider 不实现 (手动路径不走回调)。
type CallbackVerifier interface {
    Provider
    VerifyCallback(ctx context.Context, rawBody []byte, headers http.Header) (CallbackResult, error)
}
```
- test provider 实现 `VerifyCallback`:回调体 JSON `{tenant_id, out_trade_no, paid_amount_cents}`,签名 = HMAC-SHA256(body, testSecret) 放 header `X-Payment-Signature`;重算 HMAC + **常量时间比较**(`hmac.Equal`);通过才解析字段。
- manual provider 不实现 → webhook 对 manual 返回 not-supported。

### service.ConfirmPaidByCallback
```go
func (s *Service) ConfirmPaidByCallback(ctx, providerKind ProviderKind, rawBody []byte, headers http.Header) (FulfillResult, error)
```
1. resolve provider;断言实现 `CallbackVerifier`,否则 ErrProviderUnknown。
2. `VerifyCallback` 验签;失败 → ErrCallbackUnverified(**零入账**)。
3. resolve 订单 by (TenantID, OutTradeNo);无 → ErrOrderNotFound。
4. **金额一致性**:PaidAmountCents != order.AmountCents → ErrCallbackAmountMismatch(零入账,审计 fulfillment_failed)。
5. `store.ConfirmPaid`(ActorKind=system,CAS pending→paid;已 paid/recharging/completed 幂等)。
6. `Fulfill`(复用 P1 两段式,幂等)。
7. 审计:paid_confirmed(actor=system)+ credited(Fulfill 内)。

### 与 P1 复用点 (扩展非重写)
- `confirmRecord` 加 `ActorKind string` 字段;`store.ConfirmPaid` 审计用 `rec.ActorKind`(P1 `AdminConfirmPaid` 传 admin,回调传 system)。**这是对 P1 已提交文件的小扩展**(internal/payment 非冻结)。
- `Fulfill` / `BeginFulfill` / `CompleteFulfill` / billing_events seam / 幂等 / 审计表 **完全复用,零改动**。

### webhook handler (paymenthttp, 公开端点)
- `POST /v1/payments/webhook/{provider}`:读 raw body(MaxBytesReader 限制)→ `ConfirmPaidByCallback` → 200 ok / 验签失败 401(通用错误不泄露哪步失败)/ 金额不符 400 / 订单不存在 404。
- **公开,无 admin/session auth**(provider 调用);安全靠验签。routes.go 挂载(串行协调点)。
- 伪造签名(无有效订单)→ 拒 + app-log;不写 payment_audit_events(无 order FK)。

## 3. mutation-discriminating 真 PG 测试
| 测试 | 守的缺陷 | 判别 fixture | mutation 变红 |
|---|---|---|---|
| W1 ValidSignedCallbackCreditsOnce | 合法回调不入账 | test provider 签合法回调 → 订单 completed,余额=金额,1 credit/1 event | 验签/入账漏 → 无 credit → 红 |
| W2 ForgedSignatureRejectedNoCredit | **伪造回调入账** | 篡改签名(或错 secret)→ ErrCallbackUnverified,**零 credit** | 跳过验签 → 伪造入账 → 红 |
| W3 ReplayedCallbackIdempotent | 重放双账 | 同一合法回调发两次 → 仅 1 credit,余额增一次 | 漏幂等 → 双账 → 红 |
| W4 AmountMismatchRejected | 金额篡改 | 回调 paid_amount != 订单金额 → 拒,零 credit | 跳过金额校验 → 错额入账 → 红 |
| W5 TenantIsolation | 串租户 | tenant A 回调不影响 B 余额/订单 | 漏 tenant 谓词 → 串账 → 红 |
| W6 UnknownOrderRejected | 幽灵订单 | 不存在 out_trade_no 的合法签名回调 → not found,零 credit | 误建/误入账 → 红 |
- 自证:W1 vs W2 同测对比合法 vs 伪造的余额必须不同(合法增、伪造零)。
- W2 用 `hmac.Equal` 常量时间比较的 mutation:换成 `==` 字符串比较仍会红于"伪造拒绝"(签名不匹配)— 但常量时间是防时序侧信道,额外加注释说明。

## 4. blast radius / 安全
- 钱:伪造回调入账 → HMAC 验签 + 常量时间比较(W2);金额篡改 → 一致性校验(W4);重放双账 → 复用 P1 三重幂等(W3)。
- 公开端点 → body 大小限制;限流留 P2b。
- 串租户 → 所有 query 带 tenant_id(W5)。
- 验签失败响应通用化,不泄露失败原因给攻击者。

## 5. fusion-upgrade delta (三维)
- **架构**:入账双路径(手动 admin + 自动 webhook)收敛到**同一 store.ConfirmPaid+Fulfill + 同一 billing_events seam + 同一审计表**;webhook 是薄验签适配层,不重复钱逻辑。参考项目(new-api webhook 与 admin 补单各写额度逻辑、各自分离)→ HUAKAI 升级 = 两路径单一可信入账点。
- **算法**:HMAC 验签 + 金额一致性 + 复用 P1 三重幂等(重放回调天然幂等);区别 new-api 每 provider 各自 notify + 内存锁。
- **生态**:公开 webhook 端点 + 验签失败/金额篡改审计可见;test provider 模拟签名回调使全链路真 PG 可测,真渠道 SDK Owner-gated 隔离。

## 6. 需 Owner 拍板的开放点
- **D1 webhook 路由前缀**:`/v1/payments/webhook/{provider}`(公开无 auth,验签保护)。建议采用。
- **D2 多租户路由**:真 provider 不知 HUAKAI tenant_id。P2a 用 test provider 把 (tenant_id, out_trade_no) 签进回调体(我们控制格式);真 provider 的 opaque 全局 callback_token 路由 → 需 migration,延后 P2b/P-RealMoney。建议延后。
- **D3 验签失败响应**:返回通用错误码,不告知 forger 哪步失败。建议采用。

## 7. Source files read
HUAKAI(SPECIFIER lane,自有):`internal/payment/{provider,service,store,store_postgres,store_memory,types,audit}.go`(P1 已提交)、`internal/paymenthttp/handler.go`、`cmd/gateway/routes.go`。
参考(行为级,#12;已 source-read 确认两套路径):new-api(`controller/payment_webhook_availability.go`、`controller/topup.go:493 AdminCompleteTopUp`、`model/topup.go:317`)、sub2api(payment 回调履约、`domain/constants.go:51` admin adjustment)。被引标识符未在散文 verbatim 复用。

Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
