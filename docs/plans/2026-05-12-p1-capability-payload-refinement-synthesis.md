# P-1 Capability Graph IR Payload 细化 — synthesis 决议

- 日期：2026-05-12（UTC）
- 作者：Claude PM-Orchestrator
- 输入：
  - [Claude lane plan](2026-05-12-p1-capability-payload-refinement-claude.md)（19 INV，10 day，5 commit）
  - [Codex lane plan](2026-05-12-p1-capability-payload-refinement-codex.md)（34 INV，3.5-4.5 day，4 commit）
  - Fixture impact 扫描（Sonnet Explore 报告 2026-05-12T05:55Z）：35 fixture 全部正向通过 INV-1..13；`batch_minimal.input_ref` 已是 file node ID 形态；`tool_result_minimal` 已与 ToolUse 一对一匹配；6 个 enum 字段单值集中（tool_use.status / tool_result.status / thinking.redaction / audio.transport / live.transport / cache.scope）

## 1. 双 lane agree / conflict / gaps

### Agree（直接采用）

- 14 capability 字段骨架不动，本期只补 payload 内部 invariants + 跨 node ref + 条件必填
- enum 字段单一来源：把 string 常量 + 白名单 set 共同导出，validator 与 test 共用
- ValidationError 当前 `{Inv, Message}` 形态够用（Claude 提议加 `Path` 字段，Codex 没提；先沿用 `Message` 嵌路径字符串，避免 P-2 重构面）
- Hot path 仍走 `ValidateEnvelopeVersionGuard`，新 INV 全走 debug/fixture 路径
- DataRetention node 与 Policy.DataRetention 一致性是 schema 必要项（不只是建议性）
- tool_result_minimal / batch_minimal **不需要重做**（fixture impact 表明已合规）

### Conflict（PM 决议）

| 议题 | Claude lane | Codex lane | 决议 |
|---|---|---|---|
| INV 编号粒度 | 粗：INV-14..32（19 条，group 内多约束合并）| 细：INV-14..47（34 条，每个 enum/约束独立编号）| **采用 Codex 细粒度**。错误信息可定位 enum 名 vs 多义 INV |
| 切片节奏 | 10 day / 5 commit | 3.5-4.5 day / 4 commit | **折中：5 day / 5 commit**。Codex 速度过快易漏 cross-ref；Claude 太保守 |
| StreamReady enum 守门 | 未列 | INV-14 单独守门 | **采纳 Codex**。capability_graph.go 已声明 enum，validator 不查不一致 |
| PartialInput 状态约束 | 未列 | INV-17 PartialInput 只能在 status=pending/partial 时出现 | **采纳 Codex**。这是 issue-derived（sub2api#1552 tool_args_lost）的必要守门 |
| RetryPolicy.Backoff enum | 未列 | INV-29 白名单 | **采纳 Codex** |
| ProviderProjection.NodeID 反查 kind | 未列 | INV-43 强校验 | **采纳 Codex**（NodeID 非空时必须 resolve + Capability == node.Kind）|
| NodeSourceRef 索引范围 | 未列 | INV-46 message/block/event_index 必须非负且在范围内 | **采纳 Codex**（cheap O(1) check）|
| Projection 覆盖率 | 未列 | INV-47 每 node 建议有 projection | **降为 warning**，不阻塞 validate，写 capability_matrix.go 落 audit-only |
| audio.Format 白名单 | 未列 | INV-23 收紧到 wav/mp3/opus/pcm16/flac/m4a/webm | **采纳 Codex，但默认只增不删**（注释明示扩展路径）|
| StructuredOutput 三参数 ParserMode/FailureRecovery/FallbackStrategy 白名单 | 未列 | INV-38 收紧 | **延后到 P-2**。P-1 仅强守 Mode 4 vocab + Schema JSON object，三参数留自由字符串到 ClientAdapter 阶段 |
| ComputerUse Environment 白名单 | 仅非空 | INV-34 收紧到 browser/desktop/shell/mobile/other | **采纳 Codex** |
| MidStreamFallbackPolicy 改动 | 不动（D9 已锁）| 不动 | **保持** |
| 单/多 DataRetentionNode | 未列 | INV-33 P-1 先支持 0/1 个，多 node 推 P-2 | **采纳 Codex**。多 node 合并语义复杂，独立 INV |

### Gaps（双方都没覆盖，PM 补）

- **INV-48** CacheKeyHint 长度启发式：长度 > 256 byte 时记 warning（明文 prompt 嫌疑），不阻塞 validate。新增一条 ProtocolLoss severity=warning Code=cache_hint_oversized，由 INV-7 守 silent drop。
- **INV-49** TimeRange 单调性：当 StartMillis 与 EndMillis 同时非 nil 且 EndMillis > 0 时，必须 EndMillis ≥ StartMillis。Codex 在 INV-21 范围里隐含表达，提升为单独编号便于报错。
- **INV-50** 暂留位（P-2 mid-stream 行为相关，留口子）

最终编号范围：**INV-14..INV-50**（实际 37 条，含 INV-50 reserved 占位）。

## 2. 切片节奏（5 day / 5 commit）

| Day | 切片 | 文件 | 新 INV | 新 fixture | 风险 |
|---|---|---|---|---|---|
| D1 | helper + enum set 抽取 + 6 个 payload enum 守门（StreamReady / ToolUse.Status / ToolResult.Status / Thinking.Redaction / Audio.Transport / Live.Transport）+ 对应 negative test | envelope_validate.go + 新增 enum_sets.go + envelope_test.go | 14, 16(部分), 18(部分), 23(transport only), 39(redaction only), 41(transport only) | 6 enum negative table | 低 — 现有 fixture 全过 |
| D2 | 复合 payload validator（TextNode role + block.type / ToolUseNode 必填 / ToolResultNode 必填 + IsError 一致 / Cache.Scope + LocalityHint / Structured.Mode / ComputerUse.Environment + Approval / Thinking 数值非负 / Batch enum + RetryPolicy.Backoff）| 同上 | 15, 16(收口), 17, 18(收口), 25, 27, 29, 34, 36, 37(部分), 39(收口) | 12 negative | 中 — payload helper 拆分需独立 commit 控审 |
| D3 | 跨 node ref 完整性（INV-19 tool_call_id 匹配 + INV-26 cache breakpoint + INV-28 batch input_ref + INV-35 screenshot_ref + INV-41 live tool_node_ids + INV-42 mcp invocation/result + INV-43 projection node + INV-46 source_ref 索引范围）| envelope_validate.go + 新增 cross_ref.go | 19, 26, 28, 35, 41, 42, 43, 46 | 8 negative | 中高 — 最复杂切片，独立 commit 独立 review |
| D4 | 条件必填 + 一致性（INV-30/31/32 DataRetention 条件 / INV-33 node↔policy 一致 / INV-37 native_required 关联 / INV-40 thinking redaction 排序 / INV-44/45 ProtocolLoss v0.4 严格化 / INV-48 cacheHint 启发式 / INV-49 TimeRange 单调）| envelope_validate.go | 30, 31, 32, 33, 37, 40, 44, 45, 48, 49 | 8 negative + 3 positive (zdr_verified / provider_contract_required / hidden thinking) | 中 |
| D5 | fixture 扫尾 + audio.Format 白名单 + go build/vet/test/-tags debug 全绿 + 修文档 / matrix audit-only INV-47 | fixtures/ + envelope_validate.go + envelope_test.go + capability_matrix.go + docs | 23(format), 47(audit-only) | 4-6 audio variant fixture | 低 |

总产出：
- LoC delta：envelope_validate.go +500、新增 enum_sets.go ~80、新增 cross_ref.go ~150、envelope_test.go +800、fixtures/ ~25 个新文件 + 0 修改
- 与 P-0c-A 的 +430 同量级

## 3. Fixture 策略（Owner 决策表）

| 议题 | 决议 |
|---|---|
| 现有 35 fixture 是否需修改？ | **否**。impact scan 表明全部已合规 |
| Negative fixture 命名约定 | `_invalid_inv<NN>_<reason>.json`（如 `_invalid_inv14_stream_ready_missing.json`）。下划线前缀 walker 已支持 |
| 新增 positive fixture | 至少 6 条：tool_use.status=pending/partial/error 各 1（共用 1 个 chained fixture），thinking.redaction=redacted/hidden/provider_only 各 1，audio.transport=file/url/stream 各 1，data_retention.value=zdr_verified/provider_contract_required/unknown 各 1，live.transport=sse 1 个，cache.scope=message/session/vendor 各 1 |
| Audio.Format 白名单 7 vocab 是否合理？ | **是**（wav/mp3/opus/pcm16/flac/m4a/webm）。注释明示"只增不删"，新 vocab 走 patch bump |

## 4. Codex review 节奏

按 CLAUDE.md #8：

- D1 commit → `codex exec review --uncommitted --full-auto`，0 HIGH 才推
- D2 同上
- D3 单独 review（最复杂）
- D4 单独 review
- D5 是收尾 commit，过 review 后即 P-1 完成

每个 review 命令模板：

```bash
codex exec review --uncommitted --full-auto \
  -c model_reasoning_effort=xhigh --enable fast_mode \
  --output-last-message /tmp/codex-p1-d{N}-review.txt \
  < /dev/null
```

## 5. 验证标准（P-1 exit criteria）

1. `go build ./... && go vet ./... && go build -tags debug ./...` 0 error
2. `go test ./backend/internal/proto/...` 全绿
3. `go test -tags debug ./backend/internal/proto/...` 全绿
4. 新增 ~40 个 negative `TestINV1[4-9]_*` / `TestINV[234][0-9]_*` 子测试全 PASS
5. ~6 个 positive fixture（共 ~25 个新 fixture）validate + round-trip 全 PASS
6. 35 条原 fixture 全部仍 PASS（无回归）
7. Hot path 不动：`grep ValidateEnvelopeVersionGuard backend/internal/proto/openai_sse.go backend/internal/proto/gemini_sse.go` 仍只调 guard
8. Codex review：每个 D{1..5} commit 0 HIGH
9. envelope_validate.go godoc 从 "INV-1..13" 改 "INV-1..50"
10. docs/16 Phase 4.5 P-1 行勾 ✅
11. TestINV1_FullCapabilityRoundTrip 加 ≥4 新 vocab 后继续 PASS

## 6. 与 P-2/P-3 contract 冻结

P-1 完成后 14 capability schema **schema-locked** → v0.4.x patch only。
- StructuredOutput 三参数白名单延后 P-2 ClientAdapter 落地
- 多 DataRetentionNode 合并语义延后 P-2
- Mid-stream fallback INV-50 占位、P-8 解锁

## 7. 启动条件

- Owner 已隐式确认（"他做前端的时候，你继续完善后端"，2026-05-12）
- Codex lane plan 已起草（独立未读 Claude lane）
- Fixture impact 已扫
- **可立即开 D1**
