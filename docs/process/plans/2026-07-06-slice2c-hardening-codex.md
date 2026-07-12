# 2026-07-06 片2c 加固补测试与流式 loss 修

| Owner directive | “片2c 加固(续)——H3 裁定已定+继续 H1/H2/H4” |
| Scope | 仅补 H1-H4 测试、`chat_completions_billing.go` 的非流式 `ReasoningTokens` 审计字段、以及 `chat_completions_stream.go` 的流式 protocol loss 重快照；不改 P1-P4 接线逻辑，不做 schema/auth/billing ledger/quota 改动，不提交、不推送。 |
| Success criteria | H1 聚合失败路径能判别 abort 不 settle；H2 强制流式 settle 断言 `AccountID` 与 `AcquisitionToken`；H3 非流式与流式 settle draft 均写入 canonical `ReasoningTokens` 审计明细且不改变成本口径；H4 流式 chat→codex 的 `codex_max_output_tokens_stripped` 进入 settle 记录；指定 Go 门禁可真实运行并报告尾部。 |
| Time estimate | 约 1-2 小时墙钟时间，主要消耗在本地测试与质量门。 |
| Blast radius | 测试文件变更影响 `internal/gatewayhttp` 测试判别性；一行生产变更只影响流式 HCSF 翻译路径的结算 loss 证据快照。 |
| Failure modes | 构造的损坏 SSE 未走预期 abort 分支；现有生产实际未填 `AccountID`/token；流式 usage 若无 `ReasoningTokens` 源则不能硬塞；H4 落点导致流式事件 loss 被覆盖。缓解：先读现有实现与 fixture，再写判别性断言；发现真实生产缺陷则停止并标注，不用测试迁就。 |
| Decision points | 若发现 H2 槽归还字段真实缺失，需 Owner 判断是否扩大到生产修复；若流式 usage 实际没有 `ReasoningTokens` 源，则停止并报告。当前只允许 H3/H4 指定生产补丁。 |
| Pre-execution checklist | 1. 确认 cwd 是 `backend`；2. 读取本地目标文件，不用 GitHub/web；3. 确认 `recordingSettler` 支持 settle/abort 分离；4. 确认 `StreamForwarder.finishDraft` 已从 `acc.Usage.ReasoningTokens` 写流式 draft；5. 确认 H4 重快照不会覆盖 `draft.StreamProtocolLoss`，而是在 settle 时合并；6. 修改测试与 H3/H4 生产补丁；7. 运行 gofmt 与指定门禁。 |
| Concrete execution order | 先补 H3 非流式 draft 与 H4 流式重快照，再扩展 Codex forced-streaming helper 断言 H2/H3/H4，新增 H1 损坏 SSE abort 用例，最后跑指定检查并报告真实输出。 |
