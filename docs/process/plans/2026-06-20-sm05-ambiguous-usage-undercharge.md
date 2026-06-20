# SM-05 修复:歧义用量(AmbiguousUsage)已交付内容永久漏收

- 日期:2026-06-20
- 分支:`fix/sm05-ambiguous-usage-undercharge`(off `feat/frontend-portal` @ 25678e7e)
- 决策来源:Owner 拍板「修啊」= 选项 A(估算正收交付内容);billing 修复全权授权
- 关联记忆:[[core-chain-bughunt-deferred-2026-06-20]](SM-05 深查精化)、[[product-core-is-relay-not-payment]](relay 核心钱路)

## 一、Bug(真,已码证可达)

上游 usage 帧畸形/缺失被判 `AmbiguousUsage`,但**内容已交付给用户**(`EstimatedOutputTokens > 0`)时,
该请求被**永久零收**。触发门:`forwarder.go:541` 把 `EstimatedOutputTokens` 写进 draft,而 `acc.Empty()`
只查 token 桶不含可见估算,故「已交付 estimable 内容」与 `AmbiguousUsage` 可同时成立。

漏收是**两道闸叠加**(均在钱路必经):

| 闸 | 位置 | 现状 |
|---|---|---|
| 闸1 | `gatewayhttp/chat_completions_stream.go` `streamingCompletionEvent` 守卫 | `draft.UsageSource != UsageSourceAmbiguous` 把现成 `estimatedStreamingCost` 估算计费对 Ambiguous **跳过** → `draft.ActualCost = 0` |
| 闸2 | `billing/state.go` `AttemptFromGatewayDraft` | `EndClass == AmbiguousUsage` **无条件** `StreamStateFailed` → `CostForAttempt` 对非 Chargeable 返回 `decimal.Zero` |

结算 `billing/settler.go:131` = `CostForAttempt(req.ActualCost, AttemptFromSettleRequest(req))`;
`StreamAttempt` 由 `stream.go:266 AttemptFromGatewayDraft(true, draft)` 预建并塞进 `SettleRequest.StreamAttempt`(非 nil)
→ 结算直接采用,不重算。两闸缺一钱都落不了 → 故两处都要改。

reconciliation 是 **refund-only / zero-finalize**:`reconciliation_worker.go` 候选要 `usage_source='inferred'`
且**全零记录**,Ambiguous 行(`usage_source='ambiguous'` + 非零交付)永不入选 → 留歧义态 = 永久零收。

## 二、三家方向(#16,clean-room,只引生产行为不引测试)

- 一家:畸形 usage 判 forward 失败整单零收(宁漏勿过收)。
- 另一家:按已交付文本本地 tokenizer 估算正收(宁不漏)。
- HUAKAI 现状:**自己建了估算机器**(`estimatedStreamingCost`,基数 = `EstimatedOutputTokens + EstimatedReasoningTokens`,
  输入走 `tokencheck.EstimateRequestInputTokens` 内容感知 + base64 封顶)**且对非 Ambiguous 的 no-usage 流已正收**,
  唯独对 Ambiguous 留了「保留歧义待对账」却又没建对账补收通道 → 半成品取舍而非干净设计。

**决策**:对齐「已交付就估算正收」方向,复用现成估算机器,**不引入 schema、不改 refund-only 架构**。

## 三、修复(两闸同一判据,最小面)

判据 = `draft.EstimatedOutputTokens + draft.EstimatedReasoningTokens > 0`(「有可估交付内容」),两闸完全一致:

1. **闸1** `chat_completions_stream.go`:新增 unexported `ambiguousDeliveredEstimable(draft)`;守卫改为
   `reportedUsageMissing(usage) && (UsageSource != Ambiguous || ambiguousDeliveredEstimable(draft))`。
   → Ambiguous 且有可估内容时走 `estimatedStreamingCost` 估出正成本、`UsageSource` 升 `inferred`、清 pending、
   挂 `usage_basis=estimated` 标记(审计链);无可估内容的 Ambiguous **仍跳过 → 保留歧义态**(不动)。
2. **闸2** `billing/state.go` `AttemptFromGatewayDraft`:Ambiguous 分支改为「有可估交付内容 → `Partial`(Chargeable),
   否则 `Failed`」,与闸1 同口径 → `CostForAttempt` 放行闸1 估出的正成本落账。

## 四、money-safety(硬约束)

- **宁少勿多收**:计费基数用 `EstimatedOutputTokens`(可见输出估算)**非** `DeliveredTokenCount`(chunk 帧数,会 > 真 token);
  输入基数走 `tokencheck` 内容感知 + base64 封顶(非 body 字节/4)。
- **幂等不双扣**:claim_id 一次性状态机(reserving→committed),已有保证,本改动不触。
- **保守兜底**:费率表不可用 → 闸1 估算 `ok=false` 维持零结算;此时闸2 仍判 Partial 但成本 0 → `CostForAttempt(0)=0`,
  零收(与现有非 Ambiguous 路径一致),money 中性。
- **零 schema**:复用 `usage_source=inferred` + `usage_basis=estimated_from_delivered_content` 标记区分估算行。

## 五、测试(变异证 RED→GREEN,`-count=1` 强制真跑)

- 改 `chat_completions_pricing_test.go:715` `AmbiguousUsageNeverEstimated` → 反转为「Ambiguous 且 estimable>0 按估算计费」:
  断言 `ActualCost == (wantInput*1000 + 200*2000)/1e6` 正成本、`UsageSource=inferred`、`pending=false`、快照含 `usage_basis=estimated`。
- **保留** `chat_completions_pricing_test.go:465` `AmbiguousUsagePreservedNotInferred`(无 `EstimatedOutputTokens`)作**判别对照**:
  无可估内容的 Ambiguous 仍零收 + 保留歧义 + pending,证「只在有可估交付时才收」。
- 改 `chat_completions_stream_test.go:1224` `...SettlesAmbiguousUsageWithDeliveredContent`(端到端):
  `StreamAttempt.State.Chargeable()` 反转为 **true** + `SettleRequest.ActualCost.IsPositive()`,
  真 forwarder→streamingCompletionEvent→AttemptFromGatewayDraft 端到端铁证。
- **新增** `billing/state_test.go` `TestAttemptFromGatewayDraftAmbiguousDeliveredChargeable`(闸2 单元):
  `{EndClass:Ambiguous, EstimatedOutputTokens:200}` → `Partial` 且 `CostForAttempt(0.01)=0.01`;
  判别对照 `{EndClass:Ambiguous, EstimatedOutputTokens:0, DeliveredTokenCount:40}` → `Failed` 且 `CostForAttempt(0.01)=0`。

## 六、碰撞与协调

- `billing/state.go` + `_test.go`:非碰撞包。
- `gatewayhttp/chat_completions_stream.go`:碰撞包 `gateway*`,但该**文件无任何活跃分支在动**(协调板核过);Owner 已授权修复。
- `gatewayhttp/chat_completions_pricing_test.go`:仅改两条具名测试函数,与 parked wave2 分支的合并冲突可在 rebase 解。
- 不动 `chat_completions_pricing.go` 生产逻辑(只读 `estimatedStreamingCost`),收敛碰撞面。
