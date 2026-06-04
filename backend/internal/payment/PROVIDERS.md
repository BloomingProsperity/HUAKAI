# 支付 Provider 状态备注（Payment Providers Status）

> **Owner 决策 2026-06-04**：真实支付 provider（支付宝 / 微信 wxpay / 易支付 EasyPay / Stripe / Airwallex）
> **暂不实现** —— Owner 当前不具备 商户号 / SDK 接入 / 沙箱密钥 / 资质实名 条件。
> **只保留框架。** 等 Owner 取得某家商户号 + 沙箱密钥后，再按本文档接入该 provider。

## 框架现状（已就绪，无需重做）

HUAKAI 支付框架已完整，**比 sub2api/new-api 更全**（含幂等/审计/隐私/三层防伪造）：

| 组件 | 文件 | 作用 |
|---|---|---|
| `Provider` 接口 | `provider.go` | 抽象支付渠道，**不耦合任何真实 SDK 类型** |
| `CallbackVerifier` 接口 | `provider.go` | webhook 回调验签（密钥重算 + 常量时间比较，通过才解析） |
| manual provider | `provider.go` | 管理员手动确认，**生产可用**，不碰任何商户密钥 |
| hmac / test provider | `provider.go` | HTTP HMAC 验签桥 / 测试 |
| 订单状态机 | `order.go`, `service.go`, `callback.go` | 下单 → 待付 → 已付 → 入账 |
| 入账 / 幂等 / 审计 / 隐私 | `fulfillment.go`, `idempotency.go`, `audit.go`, `privacy.go` | 防重复入账、可观测、PII 脱敏 |
| 三层防伪造 | `paymenthttp` | tenant 来自**已验签回调体**（非 URL/query），防越权路由 |

## 将来加一个真实 provider 的步骤（Owner 有商户号 + 密钥后）

参考 sub2api 成熟模式（**clean-room：学方法，不抄码**）：

1. 在非冻结包实现 `Provider` 接口（`Kind()` / `CreateIntent()`）+ `CallbackVerifier`（验签）。
2. 该 provider 内部：
   - **CreatePayment / CreateIntent**：调该平台 API（或官方 Go SDK，由 PM 用直连 ssh `go get` 引入——codex 沙箱无网络），返回支付 URL / 二维码 / 跳转参数。
   - **VerifyNotification**：从 webhook 头取签名，用商户密钥重算并**常量时间比较**，通过才归一化出可信字段。
   - QueryOrder（查状态对账）、Refund、可选 Cancel。
3. **配置加密存**（商户号 / 私钥）—— 复用 `credentialstore` AES-GCM envelope，绝不明文。
4. 注册到 provider 选择（factory/registry 模式）；webhook 路由按 provider 名分发验签。
5. 安全闸：密钥加密、验签常量时间、tenant 来自已验签体、幂等（已有 `idempotency.go`）、CMB-5 不 log 密钥/原始 payload。

## 为何 Owner-gated（高风险，不自主落）

真实密钥 · webhook 验签正确性（验签 bug = 伪造入账）· 退款/撤销语义 · SDK 供应链风险 · 国内支付需企业商户号实名/资质。**每个真实 provider 都 money 高风险 → 设计 → codex 盲实现 → PM 深审（验签安全重点）→ park 给 Owner 最终批，PM 绝不自合。**
