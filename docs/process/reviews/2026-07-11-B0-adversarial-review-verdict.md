# B0 结算失败补偿——codex 对抗审裁定 + Claude 亲核 — 2026-07-11

对暂存的 B0 四缺口改动(42 文件,先交付后结算/已交付永不 Abort/sweeper 排除/图片同构)跑 money-path 对抗审(reviewer lane,codex GPT-5,只读)。**裁定:S0=0,S1=7,S2=2,S3=0。** Claude 已逐条亲核真伪与归属(未盲信)。

**结论:B0 关闭了四缺口的常见路径(变异证过 gap2/gap3/接线),但恢复/失败边缘路径的 7 个 S1 使其未达 money-path 落地门(零 S1)。故不提交,surface Owner。** 这印证「闭环必须真实测验」——Claude 早前逐行亲验漏掉了这些边缘路径,对抗审是有效安全网。

## 7 个 S1 分三类(按可修性 + 归属)

### A 类:B0 引入 / 可在现有 schema 内自主修(无需 Owner schema 门)
- **S1-1 流式"已交付"判定过宽**:`businessDelivered = BusinessFrameDelivered || DeliveredTokenCount>0`。raw 直通在真写之前累计 `delivered`([forwarder.go:395](../../backend/internal/gateway/forwarder.go)),且用上游生成 token 数当客户端交付证据([chatpipe.go:164](../../backend/internal/gatewayhttp/chatpipe/chatpipe.go))。首帧零/短写 + 上游有 usage → 客户端零字节仍结算。修法:仅整帧 `n==len && err==nil` 才置交付;严禁用上游 token 反推客户端交付。(亲核属实)
- **S1-2 恢复 worker 不重验审计**:定稿要"审计+结算 bundle recovery",实现里 worker 直接 `Settler.Settle`([handler.go:61](../../backend/internal/settlementrecovery/handler.go)),不重建/复验 audit ledger。ledger+audit-DLQ 双失败但 settlement-DLQ 成功时,worker 扣费却无有效审计证据,绕过 fail-closed。修法:恢复 payload 带可重建审计 bundle;worker 先确认审计证据再 Settle。(亲核属实)
- **S2-1 gauge 注册成 counter**:`delivered_unsettled_count/age` 是快照可降的 gauge,却经 [expvarbridge.go:25](../../backend/internal/otelbridge/expvarbridge.go) 建成单调 counter,外部 Prometheus 误判 reset。改 `Int64ObservableGauge`。
- **S2-2 图片缺真 worker 消费测试**:接线正确(SourceImagesDelivered 进统一 worker),但只测 spy 收 payload,没跑 Claim→Handler→Settler→MarkDelivered 全链。补真 worker 测试。

### B 类:pre-existing 架构缺陷,B0 恢复行流入放大;彻底修需"交付前落持久结算意图"= schema 迁移 = **Owner 门**
- **S1-3 sweeper 竞态未彻底闭窗**:B0 在 Abort 事务内复查恢复行(缩窗),但正常链路是"交付后 Settle 失败才 enqueue"。lease 过期时 sweeper 先锁 claim 复查看不到恢复行 → Abort,请求随后才插恢复行 → aborted claim + pending recovery,worker 无法补扣。彻底修=首字节前建 durable settlement intent。(claim_gate/proof B0 未改,亲核=pre-existing)
- **S1-4 claim 复活串账 + 证据不绑 attempt**:`ReReserveAbortedClaim`([claim_gate.go:121](../../backend/internal/billing/claim_gate.go),**B0 未改**)对任何 aborted claim 直接复活同 ID;恢复 proof 只验 `(tenant,claim)` 任意 committed([postgres_proof.go:33](../../backend/internal/settlementrecovery/postgres_proof.go),**B0 未改**),不绑 attempt_seq/token/金额。旧恢复看到新 attempt 的三证误判"已结算"→旧交付白吃。修=有未决恢复行禁复活 + proof 绑 attempt/token/fingerprint。
- **S1-5 crash 后陈旧重放**:非流式结算前记幂等重放([chat_completions_billing.go:174]);crash+sweep+复活后 `ON CONFLICT DO NOTHING` 保留旧响应 A,收费却属新 attempt B。修=复活时同 Tx 删旧 replay 或 replay 绑 attempt_seq。

### C 类:Owner 已明确接受的 D4 残余
- **S1-6 settle+enqueue 双失败永久丢账**:定稿明确终极双失败只 ERROR 告警(维持 D4)。属 Owner 已接受 residual,但仍是 S1——最低应触发付费流量 readiness/P0 外部告警。
- **S1-7 结构性坏恢复行永久冻结无运维出口**:ErrUnretryable/ErrNoHandler 也强制连续 pending([service.go:165] B0 改过),Abort 又拒未决恢复行,Admin DLQ 只有 List/Replay 无 force-settle/裁决闭合 → claim/hold/配额/槽永冻,只能改库。修=加带审计强授权的运维修复终局(force-settle/裁决关闭 + proof 绑 attempt)。

## Owner 决策点
彻底闭合 B 类需"首字节前落 durable settlement intent"(新列/表)= schema 迁移。两条路:
- **路 A(推荐):A 类当前自主修 + B0 作为对当前 broken 态的严格改进先落 + B 类 durable-intent 作为你批准的 schema 后续切片**。理由:当前 broken 态在常见路径就误扣/白吃/冻钱,B0 修掉这些;B 类是 crash+sweep+复活的边缘竞态,概率低,可后续硬化。
- **路 B(保守):B0 整体挂起,先设计 durable settlement intent(schema)你批准后连同 A/B 类一次落**。理由:money-path 零妥协,不留任何 S1 边缘白吃。

Claude 建议路 A(strict improvement 不留常见路径钱洞,边缘竞态排期硬化),但 schema 与风险容忍是 Owner 门,故 surface。
