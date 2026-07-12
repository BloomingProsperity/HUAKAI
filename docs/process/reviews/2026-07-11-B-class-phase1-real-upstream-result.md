# B 类阶段 1 settlement_intents 真上游实测结果 — 2026-07-11

Owner 硬规则"闭环之后要实测"。B 类阶段 1(持久结算意图)落地后,开启灰度开关
`HUAKAI_SETTLEMENT_INTENT_ENABLED=1`,用真 ChatGPT/codex 账号打真 OpenAI Codex 上游,
验意图行生命周期在真链路上是否正确落库、金额是否落对、账目是否平衡。

## 结果:全链路真测通过,账目零漂移

真上游 E2E(`e2e_codex_live` tag,纯净迁移库 huakai_e2e_b1,真 access_token/account_id,
flag 开)全 PASS:流式文本/工具调用/图片生成/reasoning/图片输入/请求变换/非流式长输出
聚合全绿;max 档如期 SKIP(gpt-5.6 专属,本账号 gpt-5.5)。

7 个真实请求打到真 OpenAI Codex,settlement_intents 与主账本**完全对齐**:

| 核对项 | 数量 | 结论 |
|---|---|---|
| settlement_intents status=settled | 7 | 意图全部走完 pending→delivering→settled |
| billing_ledger_claims committed | 7 | 主账本全部结算 |
| usage_records | 7 | 按上游权威用量真计费 |
| 意图 actual_cost 与对应 claim 对齐 | 7 | 金额落对,无错配 |

- 意图行 actual_cost 合计 $0.00345100。
- 运行中抓到活的中间态:图片生成请求在飞时 status=delivering(首字节已交付、first_byte_at
  已写、尚未 settled),证明 delivering 态在真链路上真实出现且时机正确。
- attempt_seq 全为权威值 1(取自 billing ReserveResult.AttemptSeq)。
- first_byte_at 在首帧真实写出后记录,settled_at 在结算成功后记录。

## 观察到的非阻塞项(与既往一致)

每笔 settle 后仍打 `audit: derive receipt after settle: receipt inputs not found` warning
(cost-receipt 审计回执链未闭合)——非阻塞、结算本身成功,与之前 E2E 同款,列为待查项。

## 判断

B 类阶段 1 持久结算意图**已用真上游端到端验证通过**:flag 开时意图行按正确生命周期落库、
金额与主账本对齐、账目零漂移;flag 默认关时为惰性旁路。这是"闭环之后实测"这一关的实质通过。
