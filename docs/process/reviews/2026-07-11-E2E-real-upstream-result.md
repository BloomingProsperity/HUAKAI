# 端到端全链路真上游 E2E 结果 — 2026-07-11

Owner 硬要求:闭环必须真实测验。用真 ChatGPT/codex 账号(~/.codex/auth.json,gpt-5.5 档)打真 OpenAI Codex 上游,验账号转 API relay 全链(转发→计费→配额→并发槽→结算)。

## 结果:全链路真测通过

真上游 E2E(`e2e_codex_live` tag,纯净迁移库,真 access_token/account_id)全绿:流式文本/工具调用/图片生成/reasoning/图片输入/请求变换/非流式长输出聚合/keepalive 等用例全 PASS;max 档用例正确跳过(max 为 gpt-5.6 专属,本账号 gpt-5.5 无此档,已核 gpt-5.6 全 400 确认账号档位)。

## 计费链落账证据(真 token、真钱、真上游)

7 个真实请求打到真 OpenAI Codex,数据库终态**完全平衡**:

| 环节 | 终态 | 结论 |
|---|---|---|
| billing_ledger_claims | 7 committed | 全部结算,无卡 reserving |
| balance_holds | 7 captured | 全部按实扣费,无残留 active |
| quota_reservations | 7 settled | 配额全部结清,无卡 reserved |
| pool_slot_acquisitions | 7 released_success | 并发槽全释放,无泄漏 |
| usage_records | 7 行,input 2514 + output 383 真 token,$0.003280,7 笔计费 | 按上游权威用量真计费 |
| dlq_events | 0 | 无失败补偿、无钱丢、无卡死 |

**7 请求 = 7 committed = 7 captured = 7 settled = 7 slot-freed**,链路各环节完全对齐。

## B0 在真失败上的验证(附带收获)

首轮用 gpt-5.5 时,一个 gpt-5.6-only 的 max 用例触发真实上游 400。观察到 B0 行为正确:该请求 claim 转 aborted、hold released、quota released、slot freed、$0 计费、DLQ 空——**未交付→不扣钱→全链释放**,正是 B0"照官方、已交付才收、未交付释放"的设计在真实失败上生效。

## 观察到的非阻塞项(记录待办)
1. **cost receipt 派生告警**:每笔 settle 后 `middleware.go:394` 打 `audit: derive receipt after settle: receipt inputs not found` warning。结算本身成功(200、已扣费),但审计成本回执未生成。非阻塞(warning 级),但审计成本回执链在此 E2E 配置下未闭合,列为待查(与 dlq worker 测试里同款 "receipt inputs not found" 疑同源)。
2. **max/gpt-5.6 档**:本 ChatGPT 账号只有 gpt-5.5,无 gpt-5.6/max。要验 max 字节直通不折叠需 gpt-5.6 账号。

## 判断
账号转 API relay 的核心链路(账号池→网关转发→真上游→按权威用量计费→配额/并发/结算)**已用真上游端到端验证通过,账目平衡,失败路径正确释放**。这是上线前"真实测验"这一关的实质通过(gpt-5.5 档);gpt-5.6/max 与 cost-receipt 审计回执为后续项。
