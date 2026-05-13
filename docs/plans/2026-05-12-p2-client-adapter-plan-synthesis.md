# P-2 ClientAdapter — Synthesis（Claude lane + Codex lane）

- 日期：2026-05-13（UTC）
- 合成人：Claude PM-Orchestrator
- 输入：
  - `docs/plans/2026-05-12-p2-client-adapter-plan-claude.md`（227 行，5 切片，25 engineer-day）
  - `docs/plans/2026-05-12-p2-client-adapter-plan-codex.md`（744 行，14 切片 D0-D13，8-12 engineer-day）
- 状态：两 lane 独立起草完成，本文件落 agree / conflict / gap / Owner 决策点
- 与 CLAUDE.md #10 兼容：双 plan 独立写完 → synthesis → Owner 决策 → 进入实现

## 0. 摘要

两份 plan 在**目标、架构定位、四 hookpoint 拆分**上完全对齐：P-2 把 HCSF v0.4 envelope 从"upstream IR"推到"client boundary 可用"，落 3 协议（anthropic_messages / openai_chat / openai_responses）× 4 hookpoint（RequestToCanonical / CanonicalToClientResponse / CanonicalEventToClientChunk / FinalizeClientStream）。

**主要差异**在颗粒度、INV 扩展时机、route wire-up 时机、工程量估算。Codex lane 颗粒更细（13 工作切片+1 shared 基座），Claude lane 颗粒粗（5 切片）。Codex 估 8-12 day，Claude 估 25 day（差异主要在 Claude 把"测试 + 1 切片 1 commit + codex review"算入工期，Codex 仅算 implementer-day）。

**推荐采用 Codex lane 的切片结构**（D0-D13 显式 shared foundation + 三协议各 4 切片 + integration），**保留 Claude lane 的工程量上限 25 day 作为 buffer**（合 calendar-week ~ 5 周，含 review + rework）。

## 1. Agreements（两 lane 共识）

### A1. 协议与 hookpoint 范围

两 lane 一致：

- **3 个 client protocol**：`anthropic_messages` / `openai_chat` / `openai_responses`
- **4 个 hookpoint per protocol**：`RequestToCanonical` / `CanonicalToClientResponse` / `CanonicalEventToClientChunk` / `FinalizeClientStream`
- **gemini / bedrock_anthropic client adapter 延后**（P-2.1 或更晚）
- P-2 不动 routing / pooling / dispatcher / billing ledger / quota / auth core
- P-2 schema **不动**（HCSF v0.4 已锁，P-1 完成）

### A2. ClientAdapter 与 UpstreamAdapter 职责分离

两 lane 一致：

- ClientAdapter：client wire ↔ canonical
- UpstreamAdapter：provider wire ↔ canonical
- 在 `HCSFEnvelope` / `CanonicalEvent` 边界相遇
- 现有 `anthropic_sse.go` / `openai_sse.go` / `gemini_sse.go` 是 upstream adapter，**不复用职责**
- ClientAdapter 实现独立文件，不污染 upstream adapter package state

### A3. 内部 fixture-driven，real-smoke 限定 4 vendor

两 lane 一致：

- 主测靠 fixture + mock upstream
- 真账号 smoke 仅 4 vendor：anthropic / openai / gemini / codex（per `project_real_vendor_account_scope` memory）
- real-smoke env-gated（`HUAKAI_REAL_VENDOR_SMOKE=1` 或 `HUAKAI_REAL_SMOKE=1`，名字本 synthesis 统一为 **`HUAKAI_REAL_VENDOR_SMOKE`**）
- 真账号凭据**不**入 git，从 `~/.huakai-smoke-creds.json` 或环境变量读

### A4. ProtocolLoss preserve-by-default

两 lane 一致：

- v0.4 fields（Severity / Reason / Code / Capability / NodeID / NativePath）填齐
- 不允许 silent drop
- unknown top-level vendor fields 走 `Extensions["vendor:<protocol>"]`（per INV-12）
- typed field 与 passthrough 冲突时 **typed wins**（沿用 `MergeExtrasInto` 语义）

### A5. Anthropic Messages 当前 handler 缺陷

两 lane 一致：

- `/v1/messages` 当前复用 `chat_completions_handler.go` 仅解析 `model/messages/stream` 三字段（[chat_completions_handler.go:59](backend/internal/gatewayhttp/chat_completions_handler.go#L59) / [chat_completions_handler.go:389](backend/internal/gatewayhttp/chat_completions_handler.go#L389)）
- non-streaming 直接返回 `non_streaming_unsupported`
- P-2 必须拆出 client protocol selector，否则 `/v1/messages` 不算 Anthropic Messages compatible

### A6. Cross-Cutting 行为

两 lane 一致：

- 每 adapter buffered + streaming 都要有 positive + negative 测试
- 每 hookpoint 至少 4 positive + 4 negative
- `FinalizeClientStream` 必须 idempotent（双调用不双发 terminal）
- Cache mutate / billing settle 不在 client adapter 内做，只暴露 callback / interface

## 2. Conflicts（两 lane 分歧 — 需选定）

### C1. 切片颗粒度（核心冲突）

| 维度 | Claude lane | Codex lane | 推荐 |
|---|---|---|---|
| 总切片数 | 5 (D1-D5) | 14 (D0-D13) | **Codex** |
| 每协议 | 1 day req + 1 day 三 hookpoint 合并 | 1 day each × 4 hookpoint | **Codex** |
| Shared foundation | 隐式（散落各切片） | D0 显式 | **Codex** |
| Integration | D5 集中 mock harness | D13 集中 wire-up | **Codex** |
| Wire-up（forwarder 调四 hookpoint） | 含在 D5 | D13 后置 | **Codex** |

**结论**：采用 Codex 切片表（D0 + 3 × 4 + D13 = 14 切片），允许实现期合并相邻小切片（如 D2+D4、D6+D8 合 commit），但 review 颗粒按 14 切片走。

### C2. 工程量估算

| 维度 | Claude | Codex |
|---|---|---|
| Implementer-day | 25 | 8-12 |
| Calendar 估算 | ~5 周 | ~2-3 周 |
| 含 review cycle | 是 | 否 |
| 含 fixture + golden update | 是（D5 +1500 test LoC） | 部分（D0 + per-D 内联） |

**Synthesis 推荐**：

- 内核 implementer-day = **12 day**（Codex 上限）
- 加 codex per-commit review + fixture + golden + rework = **+8 day**
- 加 real-vendor smoke + docs/specs update + release notes = **+3 day**
- 总 calendar 估算 = **~23 engineer-day ≈ 4-5 周**（含 review + buffer）

Claude lane 的 25 day buffer ≈ 该 synthesis 估算上限。**采用 4-5 周作为 calendar plan，但 implementer 切片按 Codex 的 14 切片走**。

### C3. INV 扩展时机

| 提议 | Claude lane | Codex lane | 推荐 |
|---|---|---|---|
| P-2 新增 INV-44（projection severity 必填） | 不增 | 建议增 | **增**（合 Codex） |
| P-2 新增 INV-48（cache hint 最短长度） | 不增 | adapter-level only，不进 global validator | **adapter-level only**（合 Codex） |
| INV-50 保留 / 用掉 | 不动 | 草案 StreamPlan.EventClasses terminal class 覆盖 | **保留**（不急用编号） |
| HCSFVersion wire bump | 不提 | 选项 A profile bump（wire 仍 `"0.4"`） | **选项 A**（合 Codex） |

**结论**：

- INV-44（projection severity 必填）**纳入** P-2，与 Codex D1/D5/D9 验收一起落
- INV-48（cache hint 长度）**仅 adapter-level negative test**，不进 global validator（避免 P-2 期间动 validator 风险）
- INV-50 保留
- wire `HCSFVersion="0.4"` 不动；docs 记 "validator profile v0.4.1"

### C4. RequestMeta 注入方式

| 方式 | Claude lane | Codex lane | 推荐 |
|---|---|---|---|
| Context value 注入 RequestMetaSeed | 不提 | 建议（避免改接口） | **采用** |
| 改 ClientAdapter 接口签名加 metadata 参数 | 不提 | 不推荐（影响面大） | **不采用** |

**结论**：D0 落 `RequestMetaSeed` context helper，三 adapter 从 context 读 `RequestID / IngressPath / ClientProtocol / ProtocolFamily / Model` 等。接口签名不动。

### C5. Real-vendor smoke 是否 P-2 exit blocker

| 立场 | Claude lane | Codex lane | 推荐 |
|---|---|---|---|
| P-2 exit 必须 4 vendor smoke 全过 | 是（D5 验收第 6 条） | 否（默认不阻塞 release，除非 Owner 指定 RC） | **折中** |

**Synthesis 折中**：

- `npm test` / `go test` CI 不跑真账号（默认 mock）
- P-2 exit gate 包含 **manual smoke 4 vendor 各一次 minimal text request**，Owner / PM 触发
- smoke 输出仅记 `request_id / protocol / status / parse_ok`，**不**记 prompt / secret
- 失败不自动 block release，但记入 release notes 并 Owner sign-off

### C6. Non-streaming HTTP route 是否在 P-2 内打开

| 立场 | Claude lane | Codex lane | 推荐 |
|---|---|---|---|
| 打开 `/v1/responses` non-streaming HTTP route | 含在 D5（wire-up） | 决策 1 / 决策 2，建议**proto + forwarder tests 先**，HTTP route 后 | **后置** |

**结论**：

- P-2 D1-D12 完成 proto adapter + forwarder unit test → **proto level merged**
- D13 接 HTTP route：先 `/v1/chat/completions` non-streaming（最稳）→ `/v1/responses` non-streaming → `/v1/messages` non-streaming
- 每 route 单独 commit、单独 review，可拆 P-2 → P-2a / P-2b

### C7. Tool call ID 客户端 format

| 立场 | Claude lane | Codex lane | 推荐 |
|---|---|---|---|
| 双向重新生成（每 vendor 用自己 format） | 不提 | 决策 8 | **preserve canonical `call_`** |
| Preserve canonical `call_<sha1prefix>` | 不提 | 同上 | **preserve canonical**（合 Codex 建议 5.4 风险四） |

**结论**：

- HCSF 内部 canonical id = `call_<uuid>` 或 `call_<sha1prefix>` 锁定
- Anthropic client serializer 直接 emit canonical id（Anthropic 不强制 format）
- OpenAI client serializer 直接 emit canonical id（OpenAI tool_call.id 接受任意 string）
- 仅当 client SDK 强解析时再做 vendor-specific 投影

### C8. Test count

| 维度 | Claude lane | Codex lane | 推荐 |
|---|---|---|---|
| ~80 client TestINV + TestClientAdapter | 是 | 96 adapter + 6 shared + 8 integration = **110-130** | **Codex** |

**结论**：以 per-hookpoint × positive/negative 覆盖矩阵为准（每 hookpoint 至少 4+4=8，共 3 协议 × 4 hookpoint × 8 = 96，加 D0 6 + D13 8 ≈ **110+**）。

## 3. Gaps（两 lane 都未覆盖）

### G1. v0.3 兼容路径下沉时机

Claude lane 5.4 提到"P-3 阶段把现有 UpstreamAdapter 也下沉 v0.3 path"，Codex lane 未提。

**Gap**：当前 `anthropic_sse.go` / `openai_sse.go` 内 `ProtocolLossEntry` 仍含 v0.3 字段（`Feature/Direction/Verdict/Note`）。P-2 client adapter 默认填 v0.4，但 upstream adapter 残留 v0.3 字段未清理。

**Synthesis 决策**：

- P-2 client adapter **只填 v0.4**
- upstream adapter v0.3 字段下沉 **延后到 P-3**（不阻塞 P-2 merge）
- D13 验收时 grep `proto.go` / `protocol_loss.go` 确保 v0.4 字段优先

### G2. 性能 microbench

两 lane 都说"先 correctness，later perf"。

**Gap**：没具体 microbench / baseline metric。

**Synthesis 决策**：

- P-2 D13 加 `BenchmarkClientAdapter_<Protocol>_<Hook>` 8 个（每协议 buffered + stream 各一）
- 基线测 alloc / ns per op，**不**设硬 p99
- Phase 8 hardening 再 promote 到 release gate

### G3. tool_choice 强约束（"required" / 特定 tool name）

两 lane 都没明确"tool_choice required / 特定 tool name" 在 HCSF graph 如何表达。

**Gap**：OpenAI Chat `tool_choice: {"type": "function", "function": {"name": "foo"}}` / Anthropic `tool_choice: {"type": "tool", "name": "foo"}` 在 HCSF 落 `RequestControls.ToolChoice` 还是 graph node。

**Synthesis 决策**：

- 当前 `RequestControls.ToolChoice` 是 string（per `request_meta.go`）—— **P-2 实施前先扩为 tagged union**：`{"mode": "auto" | "any" | "required" | "none" | "specific", "tool_name": "..."}`
- 这是 schema patch，归到 P-2 D0 作为前置 schema patch（不破坏 HCSF v0.4 wire，因为只在 RequestControls 内部）
- D1 / D5 / D9 测 tool_choice 覆盖全 mode

### G4. Streaming usage cumulative semantics

Codex lane 提到 "Anthropic streaming usage 在 message_delta 中是累计 token count"，Claude lane 没提。OpenAI usage chunk 是 final-only。

**Gap**：HCSF `Accounting.Usage` 是 cumulative 还是 delta？per-event 还是 final-only？

**Synthesis 决策**：

- `Accounting.Usage` 锁为 **final cumulative** at terminal
- per-event delta usage 走 `CanonicalEvent.Usage`（已有字段）
- D3 / D7 / D11 测试覆盖：累计 vs delta 一致性

### G5. Mock upstream test harness 设计文档

Claude lane 5.2 提了 mock upstream 设计要点，Codex lane 5.42-5.46 提了搭法但没具体 directory layout / fixture naming convention。

**Gap**：mock upstream 落地路径、fixture naming、httptest server 起停 fixture。

**Synthesis 决策**：

- 落 `backend/internal/test/mockupstream/`（不在 proto / gateway 内）
- fixtures：`backend/internal/test/mockupstream/fixtures/<vendor>_<scenario>.json` 或 `.sse`
- httptest server helper：`NewMockOpenAIServer(t, fixture)` / `NewMockAnthropicServer(t, fixture)`
- D0 落基础 harness（每 vendor 一个 baseline fixture）
- D13 接 forwarder + gatewayhttp 集成时再扩

## 4. Owner / PM 决策点（10 项 — 需 Owner 选）

合并两 lane 的决策项，去重 + 重新编号：

### Q1. P-2 calendar 估算

- A. 12 day（Codex 上限，只算 implementer，review/fixture/smoke 包到 implementer 工期内）
- B. 25 day（Claude 上限，含完整 review cycle + buffer）
- **C. 22-23 day（synthesis 推荐折中，实测 4-5 周 calendar 含 codex review + rework）**

### Q2. 切片颗粒度

- A. Claude 5 切片（D1-D5）
- **B. Codex 14 切片（D0-D13）— synthesis 推荐**
- C. 折中：D0 + 三协议 D1-D3 合并（含三 hookpoint）+ D4 integration = 5 切片但显式 shared

### Q3. INV-44（projection severity 必填）是否纳入 P-2

- **A. 纳入，docs profile v0.4.1，wire HCSFVersion 不动 — synthesis 推荐**
- B. 不纳入，留 P-3
- C. 纳入并 wire bump 到 `"0.4.1"`（fixture churn 大）

### Q4. Non-streaming HTTP route 是否在 P-2 内打开

- A. 全打开（`/v1/chat/completions` + `/v1/responses` + `/v1/messages`）
- **B. proto + forwarder unit-test merge 后，D13 仅打开 `/v1/chat/completions` non-streaming，其它 P-2.1 — synthesis 推荐**
- C. 全延后（P-2 仅 proto / forwarder 层，HTTP route P-3）

### Q5. RequestMeta 注入方式

- **A. Context value 注入 `RequestMetaSeed`（不改接口）— synthesis 推荐**
- B. 改 `ClientAdapter` 接口签名加 metadata 参数

### Q6. Real-vendor smoke 是否 P-2 exit blocker

- A. 强制 exit blocker（4 vendor 必过）
- **B. 折中：manual smoke required by Owner sign-off，失败不自动 block release 但记 release notes — synthesis 推荐**
- C. 不要求 smoke，只 mock

### Q7. Tool call ID format

- **A. Preserve canonical `call_<...>`，client serializer 直 emit — synthesis 推荐**
- B. 每 vendor 重生成 ID（双向映射）

### Q8. OpenAI Responses stream 末尾是否追加 `[DONE]`

- A. 追加（兼容 OpenAI Chat 习惯）
- **B. 不追加（per OpenAI Responses 官方 docs；Codex 建议）— synthesis 推荐**
- C. 配置项控制

### Q9. Responses built-in tools（web_search / code_interpreter / etc.）

- A. native_required + ProtocolLoss
- B. Plugin shell（roadmap 实现）
- C. First-class partial implementation
- **D. native_required + roadmap entry for plugin shell — synthesis 推荐**

### Q10. Cache mutate / billing settle callback 归属

- A. Client adapter 内调用
- **B. forwarder 拥有，client adapter 仅返回 finalize metadata — synthesis 推荐**

## 5. 推荐执行计划

### 5.1 切片表（合 Codex D0-D13）

| Slice | 内容 | engineer-day | 依赖 |
|---|---|---:|---|
| **D0** | shared foundation（RequestMetaSeed / ClientStreamState / SSE emit / loss helper / registry / tool_choice schema patch / mock upstream harness baseline） | 1.5 | P-1 完成 |
| **D1** | anthropic_messages.RequestToCanonical | 1.0 | D0 |
| **D2** | anthropic_messages.CanonicalToClientResponse | 0.75 | D0, D1 |
| **D3** | anthropic_messages.CanonicalEventToClientChunk | 1.0 | D0 |
| **D4** | anthropic_messages.FinalizeClientStream | 0.5 | D3 |
| **D5** | openai_chat.RequestToCanonical | 1.0 | D0 |
| **D6** | openai_chat.CanonicalToClientResponse | 0.75 | D0 |
| **D7** | openai_chat.CanonicalEventToClientChunk | 1.0 | D0 |
| **D8** | openai_chat.FinalizeClientStream | 0.5 | D7 |
| **D9** | openai_responses.RequestToCanonical | 1.5 | D0 |
| **D10** | openai_responses.CanonicalToClientResponse | 1.0 | D0 |
| **D11** | openai_responses.CanonicalEventToClientChunk | 1.5 | D0 |
| **D12** | openai_responses.FinalizeClientStream | 0.5 | D11 |
| **D13** | integration / forwarder wire-up / `/v1/chat/completions` non-streaming route / docs / release gate | 1.5 | D1-D12 |
| **总计 implementer** | | **13.5 day** | |
| + per-commit codex review × 14 + rework | | **+6 day** | |
| + manual real-vendor smoke 4 vendor | | **+1 day** | |
| + docs/specs/protocol-translation update + release notes | | **+1.5 day** | |
| + INV-44 落 global validator + fixture update | | **+0.5 day** | |
| **总 calendar 估** | | **~22.5 day ≈ 4-5 周** | |

### 5.2 验收标准（Exit）

P-2 complete 当且仅当：

1. D0-D13 全 commit，每 commit 0 HIGH codex finding
2. `go build ./... && go vet ./... && go build -tags debug ./...` 0 error
3. `go test ./backend/internal/proto/... ./backend/internal/gateway/... ./backend/internal/gatewayhttp/...` 全绿（含 `-race`）
4. `go test -tags debug ...` 全绿
5. 新增 ~110 个 TestClientAdapter / TestINV 子case 全过
6. P-1 35+2 fixture 不回归（`TestFixtures_AllValidate`）
7. INV-44 落 global validator + golden fixture 覆盖
8. forwarder wire-up：四 hookpoint 全部被 forwarder 调用一次（grep + unit test 守门）
9. `/v1/chat/completions` non-streaming route 走 client adapter，不走 raw passthrough fallback（grep `// TODO: passthrough fallback` 找不到）
10. real-vendor smoke 4 vendor 手动 sign-off（per Q6 决策 B）
11. docs/specs/protocol-translation 更新 Released spec or Implementer Notes
12. `docs/16_PHASED_DELIVERY_PLAN.md` Phase 5 / Phase 4.5 ClientAdapter 行勾 ✅
13. Owner Chinese summary 包含：实现完成度、clean-room status、real-smoke status、剩余 P-2.1 gap（`/v1/messages` + `/v1/responses` route / gemini client adapter / bedrock_anthropic client adapter）

### 5.3 Risk surface

- **Schema patch in D0**（tool_choice 扩 tagged union）—— 影响 fixture，须先跑 P-1 35 fixture 全过再 merge D0
- **INV-44 落 global validator** —— 现有 fixture 可能不带 severity，须批量 update 35 fixture 加 severity（约 +30 min 工作量）
- **D13 wire-up** —— 第一次让 client adapter 跑过 dispatch path，可能暴露 forwarder state lifecycle bug；预留 0.5 day rework

## 6. 与 Round 9 / Round 10 frontend 的关系

P-2 是 backend P-2，与 frontend Round 9/10 独立 lane。frontend P-1 Dashboard 完成（Round 10 落地）后才能可视化 P-2 client adapter 的 dispatch trace。两者**互不阻塞**：

- P-2 D0 可立即开始（P-1 已完成）
- frontend Round 10 完成不阻塞 P-2 启动
- Phase 7 (Admin Lite) 才需要 frontend 展示 client adapter projection / loss 列表

## 7. Owner 决策（2026-05-13 一次性锁定 — 全采纳 synthesis 推荐）

Owner 指令 "一次性写完"，10 项决策**全部采用本 synthesis 推荐答案**：

| Q | 项 | 决策 | Letter |
|---|---|---|---|
| Q1 | P-2 calendar 估算 | 22-23 day（4-5 周 含 review + buffer + smoke + docs） | C |
| Q2 | 切片颗粒度 | Codex 14 切片 D0-D13（D0 shared + 3×4 + D13 integration） | B |
| Q3 | INV-44 projection severity 必填 | 纳入 P-2，docs profile v0.4.1，wire `HCSFVersion="0.4"` 不动 | A |
| Q4 | Non-streaming HTTP route 打开时机 | D13 仅打开 `/v1/chat/completions` non-streaming；`/v1/responses` + `/v1/messages` non-streaming 推迟 P-2.1 | B |
| Q5 | RequestMeta 注入方式 | Context value `RequestMetaSeed`，接口签名不动 | A |
| Q6 | Real-vendor smoke 是否 P-2 exit blocker | 折中：manual smoke 4 vendor required by Owner sign-off；失败不自动 block release 但记 release notes | B |
| Q7 | Tool call ID format | Preserve canonical `call_<...>`，三 client serializer 直接 emit | A |
| Q8 | OpenAI Responses stream 末尾是否追加 `[DONE]` | 不追加（per OpenAI Responses 官方 docs） | B |
| Q9 | Responses built-in tools（web_search / code_interpreter） | native_required + Mandatory Roadmap entry for plugin shell | D |
| Q10 | Cache mutate / billing settle callback 归属 | forwarder 拥有，client adapter 仅返回 finalize metadata | B |

## 8. 锁定后的执行序列

1. **本 synthesis 即生效**（不再 reopen Q1-Q10 除非 Owner 显式提）
2. **D0 schema patch**（`RequestControls.ToolChoice` 扩 tagged union） + **D0 shared foundation**（RequestMetaSeed / ClientStreamState / SSE emit / loss helper / registry / mock upstream harness baseline） — 1.5 day
3. **D0 commit** → codex per-commit review (`codex exec review --uncommitted --full-auto`) → 0 HIGH → merge
4. **D1 anthropic_messages.RequestToCanonical** — 1.0 day → commit → review
5. 按 D2 → D3 → D4 → D5 → ... → D13 顺序，每切片 1 commit + 1 codex review
6. INV-44（projection severity 必填）单独一个 commit 落 global validator + 35 fixture batch update
7. D13 接 `/v1/chat/completions` non-streaming HTTP route，单独 commit
8. P-2 exit：上述全过 + Owner sign-off real-vendor smoke 4 vendor + docs/specs/protocol-translation 更新 + Owner Chinese summary

## 9. P-2 启动前置（在开 D0 前完成）

- [ ] Owner 显式 ack 本 synthesis（"按 synthesis 推荐 / 一次性写完" 已记为 ack — 2026-05-13）
- [ ] P-1 D1-D5 全部 commit 落库（当前 backend D1-D5 文件在 git 工作区未 commit；等 Owner 明请 commit）
- [ ] frontend Round 10 不阻塞，但建议 Round 10 落地后再开 D0（避免 commit history 混叠）
- [ ] `go test ./backend/internal/proto/...` baseline 截图保存（D0 之前的 291 TestINV pass 状态）

## 10. 与 frontend Round 10 / 其它 lane 的协同

- frontend Round 10：独立 lane，不阻塞 P-2 D0
- PASR-lite M3 / M4：已在主线推进，与 P-2 client adapter 互不依赖（PASR 在 router 层，P-2 在 proto 层）
- Phase 4.5 异步任务骨架（F-OBS-003/004/005）：建议 P-2 D0-D6 期间 codex 并行起草 Phase 4.5 plan（双 lane parallel-draft per CLAUDE.md #10）

---

Claude PM-Orchestrator synthesis 完成 + Owner 决策锁定时间：2026-05-13T06:28:00Z  
session ID：04d37436-9b8b-4a8e-b2c4-24538cfd6f23
