# completionshttp 流式交付后全额退款修复(wave-2 / 审计 wy94u3tn9 两个 S1)

## 背景 / 来源
全后端审计 wy94u3tn9 对抗确认了 completionshttp 流式路径上的两个 S1 平台亏钱缺陷
(详见记忆 audit-blockers-2026-06-19-wy94u3tn9)。本切片修这两个,同包同文件,一个 PR。

## 真码缺陷(均在 backend/internal/completionshttp/attempt.go 的 finishStreamingResponse)
- **缺陷 1(attempt.go:160-163)**:WriteHeader(200)+已 flush 部分 SSE 后,`streamAndCapture`
  中途出错(上游 Read 失败或客户端 Write 失败)就无条件 `ex.abort(w, CodeUpstreamReadError, 0)`,
  释放整笔 Tx1 预留。结果:客户端拿到部分补全、上游已生成并向平台计费的 token,用户却 0 扣费。
- **缺陷 2(attempt.go:168-171)**:清流全量交付后 `ex.actualCost(usage)` 取价失败就
  `ex.abort(w, "pricing_unavailable", ...)`,同样释放整笔预留 → 已全量交付的流计 $0;且 abort 用
  可取消的 `ex.ctx`,客户端在流末断连会取消 abort 本身 → 预留滞留到 90s lease 清扫。

两者都是"已发生交付却走 abort 退款"。紧邻的 usage-missing 分支(attempt.go:173-189)正是该走的纪律:
PendingReconciliation + 脱钩 `WithoutCancel` ctx + `settleStreamWithRecovery`(DLQ)。

## 内部先例(要镜像的)
HUAKAI 自有 chat 流式路径 gatewayhttp/chat_completions_stream.go:289-293 的结算判定:
`settle := Chargeable() || DeliveredTokenCount > 0 || EndClass==AmbiguousUsage`——**仅"真零交付"才 abort**;
其交付后取价失败(streamingCompletionEvent)也是 `PendingReconciliation=true` + 零成本 + recovery,绝不 abort。
completions 缺等价保护,本切片补齐。completions 里"已交付"的等价信号 = `copied.Len() > 0`。

## #16 三镜像对照(流式中断/缺 usage 时的计费做法)
- **new-api**(`relay/channel/openai/relay-openai.go:178`):流式 usage 缺失时
  `usage = service.ResponseText2Usage(...)`——按已交付文本**估算计费**,不退款。
- **sub2api**:基于 usage_log 按已交付用量计费(backend/migrations 077/075 等 usage_log 列),无"交付后整额退款"路径。
- **CLIProxyAPI**:纯 relay,**无 billing 模块**(记忆已证),无对应概念。
- **HUAKAI delta(生态升级)**:不只是估算计费,而是 PendingReconciliation + settlementrecovery DLQ 让 worker
  后续按权威 usage / 真实价表**重结算**,靠既有三证 proof(claim/usage_records/billing_events)幂等防重扣。

## 修复(纯逻辑,无 schema,无默认翻转,money 安全)
重写 finishStreamingResponse 的 capture 之后段:
1. `streamErr := streamAndCapture(...)`;**仅当 `streamErr != nil && copied.Len()==0`(真零交付)才 abort**
   (释放预留正确,无交付无计费)。
2. 否则(干净结束 或 中途出错但有部分交付)进入"尽力计费 + 不足待对账"路径,**任何分支都不再 abort**:
   - `streamErr != nil` 时强制把 usage 视为缺失(中途中断的 usage 不可信),用 `inputEstimate` 兜底。
   - usage 缺失或取价失败 → `PendingReconciliation=true`,取价失败时用零成本占位
     (`completionCostBreakdown{Total: Zero, PendingReconciliation: true}`),CostSnapshot 标因由。
   - 一律经脱钩 `WithoutCancel` ctx + `settleStreamWithRecovery` 落账,worker 后续重算。

## 成功标准 / 测试(变异可证)
- 中途出错 + 有部分交付 → **不 abort**、走 settleStreamWithRecovery 且 PendingReconciliation=true
  (变异:把该分支改回 abort → 断言捕获 abort 调用 → RED)。
- 中途出错 + 零交付 → abort(保留正确行为)。
- 清流交付 + 取价失败 → **不 abort**、pending 零成本 settle(变异:改回 abort → RED)。
- money 安全控制:断言交付场景下 Settler.Abort **从未**被调用(防回归再退款)。
- 干净基线 `-count=1`:completionshttp 全绿。

## blast radius
仅 completionshttp(off proxies-collision 面)。不动 schema / 默认值 / 其它包。对抗审查零 S0/S1 后合并。

## 对抗审查结论 + S2 修正(2026-06-19)
三路并行对抗审查(money-safety / 回归边界 / 测试质量-cleanroom):**零 S0/S1**;两个原始 S1 确认闭合、
无双扣、零交付边界正确、ctx 脱钩正确、happy path 无回归;3 条测试变异实测均判别(RED);
clean-room/secret-mask/codebudget/全中文均合规。
- **S2(已就地修)**:原注释/commit 夸大"DLQ worker 后续按权威 usage/真实价表重算计费"。真码核实:
  `settlementrecovery/handler.go:62-63` 只重放原 SettleRequest(不重算金额),且仅在原 settle 失败时入队;
  `billing.PendingReconciliationWorker` 是 no-usage finalizer(按 grace 定稿,非按权威 usage 重算)。
  故 completions 这些 `reported + pending + Total=0` 行**当前无自动重算消费者**。已把注释/commit 改为
  准确表述:"留待对账审计行供运维/后续对账消费者补价;DLQ 只保证待对账行最终落库"。净钱效果不劣于
  原 abort(且额外留审计行),故 S2 不阻塞合并。
- **Follow-up(排后续切片)**:接一个消费 `usage_source='reported' AND pending_reconciliation` 行的补价/
  对账 worker 或运维报表,真正闭合"交付后未能定价"的金额回收;届时再考虑保留中途中断的部分 usage 进
  CostSnapshot 供重算(当前 S3:中断场景丢部分 usage、用 inputEstimate 兜底,保守偏向用户、money-safe)。
