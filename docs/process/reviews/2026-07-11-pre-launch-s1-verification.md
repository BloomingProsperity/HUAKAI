# 上线前 S1 现状核实 — 2026-07-11

主线闭环、准备上线前,对 2026-06-26 一轮对抗 bug-hunt 曾 surface 给 Owner 的
money/security/schema S1 逐项核当前分支真码(feat/fe-wire-users-mod),确认无未修 blocker。
Explore agent 核实 + Claude 亲核三个真 money/security 关键点(不尽信 agent 报告)。

## 结论:上线前无未修 money / security / schema S1 blocker

| # | 项 | 判定 | 当前真码证据 | blocker |
|---|---|---|---|:---:|
| 1 | 凭证 KEK/密钥轮换 | 部分(单版本+补偿) | crypto.go:145 Decrypt 按密文 `env.KeyID` 取 key(架构支持多版本);生产 StaticKeyProvider 单 key(crypto.go:36-54 + config.go:238-252);key_selfcheck.go:33-79 启动 fail-closed 自检 | 否 |
| 2 | media 任务计费白吃 | 已修 | store_money.go:48-51 `billedCents<=0` 锚定 EstimatedCents(不归零)+ 上限钳制不超扣;:81 billing.Capture 真扣 | 否 |
| 3 | 配额退款不冲减 | 已修+接线 | service_reverse.go:38-98 ReverseCost 负向冲减按窗钳制;refund_worker_quota_reverse.go:47 `refund.Idempotent` 守卫防双冲减;wiring.go:1126 QuotaEnforce 注入 | 否 |
| 4 | request_id schema | 已修 | 服务端 uuid 生成(chat_completions_handler.go:450),billing_events 独立 audit_request_id 列(migration 0029),commit fa1ad194 已是 HEAD 祖先 | 否 |
| 5 | 配额 calendar_month | 已修 | rate_window.go:35-40 当月1号→下月1号 UTC 自然月边界;migration 0072 CHECK | 否 |
| 6 | tls-sidecar marker(#11) | 部分(默认关不影响) | marker/proxy 接口已补(factory.go:451/483);残留 deadline/短写(sidecar_client.go:331-378)属 sidecar S2,SidecarFallbackEnabled 默认 false、默认出口走 Go uTLS | 否 |

**三个真 money 项(#2/#3/#4)已修复并接线到生产装配,亲核确认;#5 语义正确;#1/#6 属"设计接受+有补偿/默认关"而非缺陷仍在。**

## 上线运营约束(非代码 blocker,运营须知)

1. **多版本密钥环落地前,禁止轮换 `HUAKAI_CREDENTIAL_KEY_ID` / `_KEY_B64`**:单版本 KEK 下轮换会
   使旧密文解不开(凭证全瘫)。已有启动期 fail-closed 自检把这从运行时静默灾难前移为响亮的启动
   失败,但正确做法是等多版本密钥环切片落地再轮换。不轮换即安全。
2. **工具按次附加费尾部暴露(少收,非 blocker)**:OpenAI Responses / Gemini 服务端工具(web_search 等)
   按次附加费当前计零(TODO NAPI-BILLING-01,`chat_completions_pricing.go`),方向是**保守少收**非
   多扣/漏账;主流量 Anthropic 已全覆盖计费。稳定上游 usage 信号可用后接入,财务暴露有限。

## 方法

Explore agent 读 DEFERRED-bughunt-tail-2026-06-24.md 等线索文档 + 核当前真码给 file:line;Claude
亲核 KEK 多版本架构(crypto.go:145)、media 计费锚定(store_money.go:48-51)、配额退款幂等守卫
(refund_worker_quota_reverse.go:47)三点确认 agent 报告准确。旧文档仅作线索,以当前码为准。
