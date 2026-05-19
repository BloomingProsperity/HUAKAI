# HUAKAI 反向代理核心模块细化清单 (Claude side, parallel-draft)

Date: 2026-05-02
Lane: planner / Claude side of CLAUDE.md #10 parallel-draft pair.
Companion (must be written independently): `docs/process/plans/2026-05-02-huakai-reverse-proxy-core-codex.md` — drafted by Codex without seeing this file.

## 0. Context

之前的 plan（improvements-claude / improvements-codex / huakai-creative / huakai-fusion / accapi-spine）都聚焦在**周边**（账号绑定 / 账号资产 / capacity graph / pricing / debug agent），把 **反向代理核心模块** 当成"已 spec released"的既定事实一笔带过。

但 HUAKAI 本质上就是一个反向代理 — 它的热路径就是反代核心。如果反代核心做得粗，所有周边能力（资产估值、capacity 预测、ToS 跟踪）都建立在不可靠的根基上：客户拿到错误的响应、流式中断、错误信息不可读、credentials 注入错乱。

**Framing**（Owner 2026-05-02 directive）: HUAKAI 是开源 AI gateway 竞赛产品，目标是**产品级超越** Commercial-Pool-Ref 等竞品。任何竞品已做的产品级核心，HUAKAI 都要做；合规风险通过**操作员 opt-in 插件 + README 明示警告 + 启用 audit log** 转移给操作员，而不是通过阉割功能规避。

这份 plan 把反代核心拆成 30 个具体到算法/数据结构/状态机/边界条件级别的子模块，每个对照 (a) 9 个开源参考的代号 + (b) 5 个上游供应商的代号 + (c) HUAKAI 具体改动。

## 0.1 代号系统（参考 codename-mapping.md）

| 代号 | 主题 |
|---|---|
| Commercial-Pool-Ref | 多账号池 + 支付 + 调度算法 |
| Clean-Arch-Ref | 反代清晰架构 + per-provider executor + operator config |
| Billing-Engine-Ref | 计费 session + 价格 DSL + body storage tier |
| Obs-Ref | request explorer + body retention + wallet escrow |
| Retry-Policy-Ref | 重试 budget + Retry-After + fallback stop |
| Multi-Provider-Ref | provider 广度 + 4-tier budget |
| Declarative-Ref | CRD route + body mutation 接口 + GenAI metrics |
| Operator-Tool-Ref | telemetry profile + check-in + 反例 |
| Legacy-Ref | 老款 + 反例 anti-pattern |
| Vendor-X1..X4 / Vendor-Meta | OpenAI / Anthropic / Vertex / Bedrock / OpenRouter（不直接点名） |

---

## R1. 协议适配矩阵（5 子模块）

### R1.1 OpenAI Chat ↔ Anthropic Messages 双向 + stream event 完整覆盖  [P0]  [架构卫生]
**基线-开源**: Multi-Provider-Ref / Retry-Policy-Ref 都做过双向；Commercial-Pool-Ref 做过但 stream event 漏 1-2 个；Legacy-Ref 简化版（只覆盖最常用的）。**不足**：开源仓常漏 `message_delta` 中的 `usage` 增量、`content_block_delta` 的 `signature` 字段（thinking 模式）、`error` event 的 normalize。

**基线-官方**: Vendor-X1 Chat Completions stream 6 种 chunk 类型；Vendor-X2 Messages stream 8 种 event type（`message_start / content_block_start / content_block_delta / content_block_stop / message_delta / message_stop / ping / error`）。漏一个 = 客户端 SDK 抛错。

**HUAKAI 改动**:
- **算法**: 建 `protocol_dispatch_table[(client_proto, upstream_proto, event_type)] → handler`。每对 protocol × 每个 event type 必有 handler + 单测。
- **数据结构**: `internal/proto/dispatch_table.go` + `internal/proto/<pair>/<event>_test.go` 100+ 测试 case。
- **FSM**: stream 事件序列（如 OpenAI 的 message_start 必先于 content_block_*）作为 invariant 检查。

**信号**: AT-PROTO-002-XX 100+ 用例；客户响应头 `X-Huakai-Event-Coverage: 8/8`。

**F-***: F-PROTO-002（已 released）补 AT。

**Effort**: 8-12 小时（每对 protocol × 每个 event 单测）。

### R1.2 OpenAI Responses API 流式事件适配  [P0]  [passthrough]
**基线-开源**: Clean-Arch-Ref `internal/runtime/executor/codex_executor.go` 已支持，但其他仓滞后。

**基线-官方**: Vendor-X1 Responses API 是新生产默认：`response.created / response.in_progress / response.completed / response.output_item.added / response.output_text.delta / response.output_text.done / response.error / response.refusal.delta` 等 12+ event。**HUAKAI 不支持 = 不能 serve Codex CLI 客户**。

**HUAKAI 改动**:
- **算法**: Responses → Chat 反向降级表（当本地池子里没 Responses 兼容账号时）；Responses → Anthropic Messages 跨协议 mapping（`response.output_text.delta` ↔ `content_block_delta.text`）。
- **数据结构**: `responses_event_handlers/` 目录；`stream_continuation_state` 表（previous_response_id 跨账号映射）。
- **FSM**: `created → in_progress → output_*_delta+ → completed | error | refusal`。

**信号**: 客户端 Codex CLI 能正常工作；admin trace `response_id` ↔ `previous_response_id` 链可见。

**F-***: F-PROTO-002 + 新 AT-PROTO-002-RESPONSES。

**Effort**: 10-14 小时（含 previous_response_id 跨账号映射）。

### R1.3 Gemini event shape + safetyRatings/citations 字段保留  [P1]  [passthrough]
**基线-开源**: Multi-Provider-Ref / Commercial-Pool-Ref 部分覆盖；Legacy-Ref 简化（safetyRatings 直接丢）。**不足**：safetyRatings 丢失 = 客户端 SDK 报"unsafe content not detected" 误报；citations 丢失 = grounding 客户端不工作。

**基线-官方**: Vendor-X3 stream `candidates[].content.parts[].text` 增量 + `candidates[].safetyRatings[]` + `candidates[].citationMetadata.citationSources[]` + `usageMetadata`。每条都要保留。

**HUAKAI 改动**:
- **算法**: Gemini → Chat 转换时把 safetyRatings / citations 用 `response.metadata.gemini_safety` / `response.metadata.gemini_citations` 字段透传（自定义命名空间）。
- **数据结构**: 客户协议响应增加 `metadata` 顶级字段约定。
- **FSM**: 无新状态。

**信号**: 客户响应包含 `metadata.gemini_safety`；admin "Gemini 流量保留 100% 字段"。

**F-***: F-PROTO-002 + 新 AT-PROTO-002-GEMINI-FIELDS。

**Effort**: 6-8 小时。

### R1.4 Vendor-X4 binary stream → SSE normalize  [P1]  [passthrough]
**基线-开源**: Multi-Provider-Ref 有 `bedrock_request.go / bedrock_signer.go / bedrock_stream.go` 套件；Commercial-Pool-Ref 没做 X4。**不足**：binary event-stream 解析依赖 AWS 私有 framing（IEEE 754 prelude + headers + payload）。

**基线-官方**: Vendor-X4 ConverseStream 是 binary event-stream（`messageStart / contentBlockStart / contentBlockDelta / contentBlockStop / messageStop / metadata`），不是 JSON SSE。**必须**正确解析才能 normalize 到 HUAKAI SSE 输出。

**HUAKAI 改动**:
- **算法**: binary event-stream parser（先解 prelude 12 byte → header section → payload）→ JSON 化 → 转 HUAKAI SSE frame。
- **数据结构**: `internal/proto/bedrock/eventstream_decoder.go`。
- **FSM**: prelude → headers → payload → next-event。

**信号**: AT-PROTO-002-BEDROCK-STREAM 测试 binary 帧。

**F-***: F-PROTO-002。

**Effort**: 12-16 小时（binary 协议没法投机）。

### R1.5 Tool call schema 跨协议 normalize  [P0]  [架构卫生]
**基线-开源**: Multi-Provider-Ref 部分；Retry-Policy-Ref 有 hooks；都不够细。**不足**：OpenAI `tool_calls[].function.arguments` 是字符串（JSON encoded），Anthropic `tool_use.input` 是对象（JSON 解析后）。直接 forward = 客户端解析 fail。

**基线-官方**: Vendor-X1 `tools[].function` + `tool_calls[]`；Vendor-X2 `tools[].input_schema` + `content[].tool_use`；Vendor-X3 `tools[].functionDeclarations` + `functionCall`；Vendor-X4 `tools[].toolSpec` + `toolUse`。

**HUAKAI 改动**:
- **算法**: 跨协议 tool schema mapping table（4 vendor × 双向）+ args 类型 normalize（统一对象，按目标 provider serialize）。
- **数据结构**: `tool_schema_normalize/` 目录 + 4×4 mapping。
- **FSM**: 无。

**信号**: AT-PROTO-002-TOOL-CALLS 跨 provider 工具调用单测。

**F-***: F-PROTO-002。

**Effort**: 10-12 小时（tool schema corner cases 多）。

---

## R2. WebSocket 全双工反代（3 子模块）

### R2.1 WS 连接生命周期 + 心跳  [P1]  [创新（HUAKAI 之前未做）]
**基线-开源**: Clean-Arch-Ref `internal/wsrelay/session.go` + `codex_websockets_executor.go` 是唯一参考。**不足**：心跳间隔硬编码（30s）；客户连断检测靠 ping/pong。

**基线-官方**: Vendor-X1 Realtime API（音频流）；Codex CLI 在 path /v1/responses 上对 WS 也有交互。客户连接 ≠ 上游连接，HUAKAI 必须在中间维护两条连接。

**HUAKAI 改动**:
- **算法**: 客户 WS connect → HUAKAI 建上游 WS（携带 credential）→ 双向桥接 frame；客户/上游任一断 → 另一边 close + audit reason。
- **数据结构**: `wsrelay_sessions(id, tenant_id, api_key_id, binding_id, account_id, client_conn_id, upstream_conn_id, opened_at, closed_at, close_reason)` 表。
- **FSM**: `opened → forwarding → client_disconnected | upstream_disconnected | both_active → closed_*`。

**信号**: admin "活跃 WS 会话: X 个 / 平均时长 Y 分钟"。

**F-***: F-RT-001 提到 L2。

**Effort**: 16-20 小时（含连接管理 + 测试）。

### R2.2 WS 双向 message tap（billing 计量点）  [P1]  [创新]
**基线-开源**: Clean-Arch-Ref `wsrelay/session.go` 有 deadline 但没 billing tap；Obs-Ref 不做 WS。

**基线-官方**: Vendor-X1 Realtime API 按 audio 秒数 + token 计费；不 tap 每条 message = 不知道客户用了多少。

**HUAKAI 改动**:
- **算法**: 双向 message 流过 → 提取 `event.type / event.usage / audio_duration_ms` → 写 `usage_records` 增量 row（per WS event）。
- **数据结构**: `usage_records` 加 `ws_event_type text` + `audio_duration_ms int`。
- **FSM**: 无。

**信号**: 客户响应可查 per-event usage；admin "WS 实时流 / 入站 token / 出站 token / 音频秒数"。

**F-***: F-RT-001 + F-OBS-001。

**Effort**: 10-12 小时。

### R2.3 WS 重连 + session 状态保留  [P2]  [创新]
**基线-开源**: 0 reference。

**基线-官方**: Vendor-X1 Realtime 没有官方 resume 协议（一断即重）；HUAKAI 自做 resume 提供差异化。

**HUAKAI 改动**:
- **算法**: 客户 WS 断 → 保留 `wsrelay_sessions` 行 + cache 上游 session state（最近 N 条 frame）30s。客户重连带 `resume_token` → 找到 row → 重发 cached frame → 接续上游。
- **数据结构**: `ws_resume_tokens(token_hmac, session_id, expires_at)` 表 + Redis 缓存。
- **FSM**: `closed → resumable_grace (30s) → resumed | abandoned`。

**信号**: 客户网络抖动期间 0 中断感知。

**F-***: 新增 F-WS-RESUME-001。

**Effort**: 12-16 小时。

---

## R3. 流式 forwarder（4 子模块）

### R3.1 Scanner buffer 自适应  [P0]  [架构卫生]
**基线-开源**: Commercial-Pool-Ref / 自家 streaming-forwarder spec 默认 1MB scanner buffer。**不足**：reasoning + vision 一个 ContentBlockDelta 可能 1.5MB → scanner panic。

**基线-官方**: Vendor-X2 启用 thinking budget 后单 delta 可达数 MB；Vendor-X1 reasoning_effort=high 同。

**HUAKAI 改动**:
- **算法**: bufio.Scanner 起 64KB → 触发 ErrTooLong → 自动 grow 2x → 上限 16MB（per-tenant 配置）。grow 触发 audit + metric。
- **数据结构**: `scanner_buffer_grow_events(tenant_id, account_id, model, peak_size_bytes, occurred_at)`。
- **FSM**: 无。

**信号**: admin "scanner grow 频率 / 峰值大小分布 / 是否触发上限"。

**F-***: F-GW-002（streaming-forwarder spec）补 AT。

**Effort**: 4-6 小时。

### R3.2 客户端断线后 drain 上游到 settlement  [P0]  [架构卫生]
**基线-开源**: Commercial-Pool-Ref 已做（"Billing-preserving stream drain after client disconnect" 是 sub2api 的核心）。**不足**：drain 没有 timeout 上限（客户走了上游还在烧 token）。

**基线-官方**: Vendor-X1/X2/X3 都按上游 stream 完整长度计费（不管客户在不在）。

**HUAKAI 改动**:
- **算法**: 客户 disconnect → continue read 上游 → 当 (a) 上游正常 message_stop 或 (b) drain timeout 60s（per-tenant 配）或 (c) 上游错误。所有 token 计入 usage_records.drain_outcome。
- **数据结构**: `usage_records.drain_outcome enum(clean, truncated, timeout, error)`。
- **FSM**: stream `forwarding → client_gone_drain → drain_complete | drain_timeout | drain_error`。

**信号**: 客户响应头 `X-Huakai-Drain-Outcome: clean`；admin "客户断线 drain 占比"。

**F-***: F-GW-002 已 spec released。

**Effort**: 6-8 小时。

### R3.3 SSE 帧 normalize（去 vendor 私有头/字段）  [P1]  [架构卫生]
**基线-开源**: Multi-Provider-Ref / Retry-Policy-Ref 部分；Legacy-Ref 直接 forward（含 vendor account-id / trace headers — 数据泄露反例）。

**基线-官方**: Vendor-X1 SSE 中 chunks 有 `system_fingerprint`（OpenAI 私有 model identifier）；Vendor-X2 有 `model`（包含 vendor-specific suffix）；Vendor-X3 有 `responseId`（GCP project 暴露）。**全部要 strip 或 rewrite**。

**HUAKAI 改动**:
- **算法**: 出站 SSE frame 走 `frame_normalizer` → 去/改 vendor 私有字段 → 替换为 HUAKAI 命名空间。
- **数据结构**: `frame_normalize_rules.json` per vendor。
- **FSM**: 无。

**信号**: 客户响应不含 vendor-specific identifier；admin diagnostic "现在暴露哪些字段"。

**F-***: F-PROTO-002 + F-SEC-005。

**Effort**: 6-8 小时。

### R3.4 JSON-stream / chunked transfer normalize  [P1]  [架构卫生]
**基线-开源**: 0 reference 完整覆盖（多数仓只支持 SSE）。

**基线-官方**: Vendor-X3 streaming 用 chunked JSON；Vendor-X4 用 binary（已 R1.4 处理）。

**HUAKAI 改动**:
- **算法**: 客户协议要 SSE → 上游 chunked JSON → 中间转 SSE-style frame；客户协议要 chunked → 反向。
- **数据结构**: 流形态 enum `sse / chunked_json / binary_event_stream / websocket`。
- **FSM**: 无。

**信号**: AT-PROTO-002-STREAM-FORMATS 多形态测试。

**F-***: F-PROTO-002。

**Effort**: 8-10 小时。

---

## R4. 上游 transport pool（5 子模块）

### R4.1 Idle / per-host / total cap 三层 budget  [P0]  [架构卫生]
**基线-开源**: Commercial-Pool-Ref 是唯一全做过的（idle / per-host / total + active-stream protection）；Legacy-Ref 默认 Go HTTP client。

**基线-官方**: Vendor-X1/X2/X3 都默默关闭 idle keep-alive；Vendor-X4 有 IAM 重新签名间隔。

**HUAKAI 改动**:
- **算法**: `http.Transport` 配置：`MaxIdleConns: total_cap, MaxIdleConnsPerHost: per_host_cap, IdleConnTimeout: 90s, MaxConnsPerHost: 0 (无限新建)`。
- **数据结构**: `transport_pool_config(tenant_id, total_cap, per_host_cap, idle_timeout_s)` 配置；metric `transport_pool_active / idle / new_dial_rate`。
- **FSM**: 无。

**信号**: admin "transport pool: 124/200 active, 23 idle, 上次 idle GC 5min 前"。

**F-***: 新增 F-NET-003。

**Effort**: 4-6 小时。

### R4.2 Active-stream protection（不被 idle GC kill）  [P0]  [架构卫生]
**基线-开源**: Commercial-Pool-Ref 唯一做的。

**基线-官方**: Go 默认 idle GC 不区分 active stream — 长 stream 可能被关。

**HUAKAI 改动**:
- **算法**: 自定义 RoundTripper wrap → 跟踪每个 conn 的 active stream count → conn 有 active stream 时跳过 idle GC。
- **数据结构**: `conn_state map[*Conn]struct{active_streams int, last_idle_at time}`。
- **FSM**: conn 状态 `active → idle → idle_grace → close_eligible`。

**信号**: admin "受保护连接数 / GC 跳过次数"。

**F-***: F-NET-003。

**Effort**: 6-8 小时。

### R4.3 Dial-time DNS resolve（防 DNS rebinding）  [P0]  [架构卫生]
**基线-开源**: Commercial-Pool-Ref `service/channel_monitor_ssrf.go` 实现 DialContext 重新 resolve；其他仓不做。

**基线-官方**: Vendor-X1/X2 DNS 偶尔变；客户配置 custom upstream URL = SSRF 风险（如指向 169.254.169.254 metadata）。

**HUAKAI 改动**:
- **算法**: 自定义 DialContext → 在 dial 时 resolve hostname → 检查 resolved IP 不是 private/metadata/loopback → 才 dial。
- **数据结构**: `ssrf_blocklist`（私有/loopback/metadata IP ranges）+ resolve cache TTL 60s。
- **FSM**: 无。

**信号**: AT-NET-001 测试 localhost / metadata IP / 0.0.0.0 / IPv6 ::1 全 reject。

**F-***: F-NET-003 + F-SEC-005。

**Effort**: 6-8 小时。

### R4.4 Per-account 隔离 transport（cookie / session jar）  [P1]  [架构卫生]
**基线-开源**: Commercial-Pool-Ref `provider account transport pool with isolation modes` 已做。

**基线-官方**: Vendor-X1/X2 OAuth 流程中 session cookie 不能跨 account 共享；Vendor-X3 Service Account JWT 同样。

**HUAKAI 改动**:
- **算法**: 每 account 一个独立 `http.Transport` + 独立 `cookiejar.Jar`；transport pool key = `(provider, account_id)`。
- **数据结构**: `transport_pool map[string]*http.Transport` 索引 by `(provider, account_id)`。
- **FSM**: 无。

**信号**: admin "每账号 transport conn 数"。

**F-***: F-NET-003。

**Effort**: 4-6 小时。

### R4.5 HTTP/2 ping + connection lifetime  [P1]  [passthrough]
**基线-开源**: 0 reference 显式管理。

**基线-官方**: Vendor-X1/X2 HTTP/2 idle ping 间隔不确定；上游可能 GOAWAY。

**HUAKAI 改动**:
- **算法**: `http2.Transport` 配置 `ReadIdleTimeout: 30s, PingTimeout: 15s`；GOAWAY 收到 → 优雅迁移 active stream 到新 conn。
- **数据结构**: 无新表；metric 计 `goaway_events_count`。
- **FSM**: conn 状态加 `goaway_received → drain → close`。

**信号**: admin "GOAWAY 事件 / 平均连接生存时间"。

**F-***: F-NET-003。

**Effort**: 6-8 小时。

---

## R5. TLS 指纹 / impersonation（3 子模块，全部 plugin）

### R5.1 Cipher suite + curves + ALPN profile per provider  [P3 plugin]  [创新（合规风险）]
**基线-开源**: Commercial-Pool-Ref `tls_fingerprint_profile` 是唯一做的（10 字段 JA3）。

**基线-官方**: 所有 vendor 都有 bot detection；默认 Go TLS 易被识别。HUAKAI **不能默认开** — 操作员合规义务（README §upstream-provider-terms 已明示）。

**HUAKAI 改动**:
- **算法**: 用 `utls` 库 → custom ClientHelloID per provider profile。Plugin 形态：`internal/plugin/tls_fingerprint/`（默认未编译进 binary）。
- **数据结构**: `tls_fingerprint_profiles(provider, name, cipher_suites[], curves[], alpn[], extensions[], grease, enabled bool)`。
- **FSM**: 无。

**信号**: 操作员 opt-in；audit "TLS fingerprint 启用日期 / 操作员 ID / 选择的 profile"。

**F-***: F-NET-001 plugin。

**Effort**: 12-16 小时（utls integration + 测试）。

### R5.2 Extensions order + GREASE 模拟  [P3 plugin]  [创新]
**基线-开源**: Commercial-Pool-Ref schema 含；执行细节未读。

**基线-官方**: Chrome 124+ TLS extensions order 是 fingerprintable；GREASE values 必须随机。

**HUAKAI 改动**:
- **算法**: extensions 按 profile 配置 order；GREASE 用 `crypto/rand` 生成；模拟 Chrome 124+ behavior。
- **数据结构**: `tls_fingerprint_profiles.extensions_order text[]`, `grease_strategy enum(disabled, random, fixed)`。
- **FSM**: 无。

**信号**: admin profile 编辑器 + audit。

**F-***: F-NET-001。

**Effort**: 8-10 小时。

### R5.3 Plugin 控制（默认 off + 操作员 opt-in + ToS warning）  [P0]  [架构卫生（合规）]
**基线-开源**: 0 reference 做合规控制（Commercial-Pool-Ref 直接默认开）。

**基线-官方**: Vendor-X1/X2/X3 ToS 普遍禁止"绕过 detection"行为。HUAKAI 必须显式合规。

**HUAKAI 改动**:
- **算法**: TLS fingerprint plugin 编译 flag default off；运行时 enable 触发 admin confirm + ToS-warning splash + 写入 `compliance_audit_log`。
- **数据结构**: `compliance_audit_log(actor_id, action, target, reason, occurred_at)`。
- **FSM**: 无。

**信号**: admin 看到 "您启用了 TLS impersonation —— 您已确认这不违反任何上游 ToS"。

**F-***: F-NET-001 + F-SEC-007。

**Effort**: 4-6 小时（splash UI + audit）。

---

## R6. 错误归一化（4 子模块）

### R6.1 Vendor-X1 错误 shape map  [P0]  [架构卫生]
**基线-开源**: Multi-Provider-Ref / Retry-Policy-Ref 部分；不全。

**基线-官方**: Vendor-X1 错误结构: `{error: {message, type, param, code}}`，type 有 `invalid_request_error / authentication_error / permission_error / not_found_error / rate_limit_error / server_error` 等 10+ 种 + headers 有 `x-ratelimit-reset-requests / -tokens`。

**HUAKAI 改动**:
- **算法**: `vendor_x1_error_classifier(http_status, body, headers) → ErrorClassification{class, retry_policy, transition, retry_after_ms, human_msg}`。
- **数据结构**: `error_class_table` 用 jsonb 配置。
- **FSM**: 输出 `AccountStateTransition`（如 401 → needs_refresh / 429 → cooling_down / 402 → quota_exhausted）。

**信号**: admin "Vendor-X1 错误 7 日分类分布"。

**F-***: F-ACCAPI-ERR-CLASSIFY-001（spine plan）。

**Effort**: 6-8 小时。

### R6.2 Vendor-X2 错误 shape map  [P0]  [架构卫生]
**基线-官方**: Vendor-X2 错误: `{type: "error", error: {type, message}}`，error.type 6 种 (`invalid_request_error / authentication_error / permission_error / not_found_error / rate_limit_error / api_error / overloaded_error`) + headers `anthropic-ratelimit-requests-reset` / `-tokens-reset` 等。

**HUAKAI 改动**: 同 R6.1 但针对 Vendor-X2 shape。

**F-***: F-ACCAPI-ERR-CLASSIFY-001。

**Effort**: 4-6 小时。

### R6.3 Vendor-X3 + Vendor-X4 错误 shape map  [P1]  [架构卫生]
**基线-官方**: Vendor-X3 错误: `{error: {code, message, status, details[]}}`；Vendor-X4 错误: `{__type: "...Exception", message}` 6 种 ServiceException。

**HUAKAI 改动**: 同 R6.1 但针对 X3/X4 shape；X4 还要解 binary event 中的 error event。

**F-***: F-ACCAPI-ERR-CLASSIFY-001。

**Effort**: 6-8 小时。

### R6.4 12 类标准错误 + retry policy 表 + state transition  [P0]  [架构卫生]
**基线-开源**: Retry-Policy-Ref retry 类别中等，未跨 vendor 统一。

**基线-官方**: 不同 vendor 的 429 含义不同（X1 organization-level vs X2 workspace-level vs X4 region-level）— retry 行为不能一刀切。

**HUAKAI 改动**:
- **算法**: 12 类标准错误 enum：`upstream_5xx / upstream_4xx_auth / upstream_4xx_quota / upstream_429_org / upstream_429_workspace / upstream_429_region / upstream_overloaded / upstream_invalid_request / local_timeout / local_validate / network_unreachable / unknown`。每类 → `(retry_now / retry_after_ms / fallback / fail_final)` 决策表。
- **数据结构**: `error_normalize_decisions.json` per-tenant 可覆盖。
- **FSM**: 决策驱动 `request_attempts.state_transition_emitted`。

**信号**: 客户响应头 `X-Huakai-Error-Class: upstream_429_workspace` + `X-Huakai-Retry-After-Ms: 30000`；admin "12 类错误 7 日分布"。

**F-***: F-ACCAPI-ERR-CLASSIFY-001。

**Effort**: 8-10 小时（含 12 × 4 决策表 + 跨 vendor 统一）。

---

## R7. 请求 body 改写（3 子模块）

### R7.1 JSONPath set / remove / conditional 规则注册表  [P1]  [架构卫生]
**基线-开源**: Declarative-Ref `body_mutator.go` 成熟（field set / remove / raw JSON）。

**基线-官方**: Vendor-X1 system 是 string；Vendor-X2 system 是 array of content blocks；模型间互转需 mutation。

**HUAKAI 改动**:
- **算法**: 规则 = `(target_path JSONPath, action enum(set/remove/conditional), value, condition_expr)`；按规则集对入站 body 执行 → 出 mutated body + audit diff。
- **数据结构**: `body_mutation_rules(id, scope_kind, scope_id, target_path, action, value, condition, enabled, priority)`。scope = global / tenant / pool / binding。
- **FSM**: 无。

**信号**: admin trace 显示每条 mutation 的 before/after diff。

**F-***: 新增 F-BODY-MUT-001。

**Effort**: 8-10 小时（含 JSONPath engine + 测试）。

### R7.2 Strict mode（拒所有 mutation, audit）  [P1]  [架构卫生]
**基线-开源**: 0 reference 显式提供 "no-mutation guarantee" 模式。

**基线-官方**: 客户合同可能要求 "我的 prompt 不能被改"；HUAKAI 必须能保证。

**HUAKAI 改动**:
- **算法**: per-binding flag `strict_no_mutation: true` → mutation engine 在该 binding 上 short-circuit；任何匹配的 mutation rule 触发 → 写 audit + 拒绝请求 4xx。
- **数据结构**: `api_key_bindings.strict_no_mutation bool`。
- **FSM**: 无。

**信号**: 客户响应头 `X-Huakai-Mutation-Mode: strict`；audit "strict mode 触发的 mutation reject 次数"。

**F-***: F-BODY-MUT-001 + F-A2A-BIND-001 (extend)。

**Effort**: 4-6 小时。

### R7.3 Mutation audit + before/after diff  [P1]  [架构卫生]
**基线-开源**: Declarative-Ref 有 audit；Multi-Provider-Ref 简化。

**基线-官方**: 调试 / 客户支持需要"我的请求被改了什么"可见。

**HUAKAI 改动**:
- **算法**: 每次 mutation → 写 `request_attempts.body_mutations jsonb[]`，每条 = `{rule_id, before_path_value, after_path_value}`。
- **数据结构**: `request_attempts.body_mutations jsonb`。
- **FSM**: 无。

**信号**: admin trace 一个请求看完整 mutation 历史。

**F-***: F-BODY-MUT-001 + F-ACCAPI-ATTEMPT-001。

**Effort**: 4-6 小时。

---

## R8. Header/Cookie 防火墙（3 子模块）

### R8.1 双向 allowlist + 默认严格  [P0]  [架构卫生]
**基线-开源**: Commercial-Pool-Ref / Retry-Policy-Ref 都有；默认严格度参差。

**基线-官方**: 上游响应 headers 含敏感（rate-limit reset、provider trace、account ID）— 不能直接 forward。

**HUAKAI 改动**:
- **算法**: 两张 allowlist `inbound_allow[]`（客户→上游）+ `outbound_allow[]`（上游→客户）；默认 deny + 每条 forward 必有 audit。
- **数据结构**: `header_firewall_rules(direction enum, header_name, action enum(allow/strip/rewrite), rewrite_to)`。
- **FSM**: 无。

**信号**: admin "本周通过 firewall forward 的 headers 名单" + 异常告警。

**F-***: F-SEC-005 已 spec released；补 AT。

**Effort**: 6-8 小时。

### R8.2 敏感信息识别（headers + body）  [P1]  [架构卫生]
**基线-开源**: 0 reference 系统做。

**基线-官方**: Vendor-X1 `x-request-id` 含 OpenAI internal ID；X2 含 workspace；X3 含 GCP project；X4 含 AWS account。**全部要识别 + strip/rewrite**。

**HUAKAI 改动**:
- **算法**: 出站前扫描 headers + body → 匹配 sensitive patterns（regex + per-vendor signatures）→ strip 或替换为 HUAKAI ID。
- **数据结构**: `sensitive_pattern_registry.json` per-vendor。
- **FSM**: 无。

**信号**: admin "本周拦截的敏感信息次数 / 类型分布"。

**F-***: F-SEC-005 + F-LOG-SAFE-001（Track 2）。

**Effort**: 8-10 小时。

### R8.3 Operator "现在暴露哪些 headers" 诊断面板  [P2]  [架构卫生]
**基线-开源**: 0 reference。

**基线-官方**: 操作员配置 firewall 规则后没有反馈 → 容易配置错误暴露敏感信息。

**HUAKAI 改动**:
- **算法**: cron 每小时聚合最近 1h 出站 headers 名单 → admin "您最近暴露给客户的 headers: [x-request-id, retry-after, ...]"。
- **数据结构**: `header_exposure_snapshots(taken_at, direction, header_name, count)`。
- **FSM**: 无。

**信号**: admin "暴露 header 列表 + 推荐 strip 哪些"。

**F-***: F-SEC-005 + F-OPS-001。

**Effort**: 6-8 小时。

---

## R9. 解压保护（2 子模块）

### R9.1 Gzip + br 流式解压 + 字节上限  [P0]  [架构卫生]
**基线-开源**: Billing-Engine-Ref `middleware/gzip.go` 有 MaxBytesReader 是良例；Legacy-Ref `middleware/gzip.go` 反例（无保护）。

**基线-官方**: 客户 SDK 默认 gzip 压缩；HUAKAI 必须解压才能转发。

**HUAKAI 改动**:
- **算法**: 流式解压（不全 buffer）→ 解压后 byte 计数 → 超 per-tenant 上限（default 64MB）→ 413 + audit。
- **数据结构**: `tenant_config.max_decompressed_bytes`。
- **FSM**: 无。

**信号**: 客户 413 + 错误 `decompressed_size_exceeded`。

**F-***: F-REQ-BODY-001（Track 2）。

**Effort**: 4-6 小时。

### R9.2 解压比上限 10x（防 zip bomb）  [P0]  [架构卫生]
**基线-开源**: 0 reference 显式做 ratio guard（多数仓只有 byte 上限）。

**基线-官方**: 攻击向量 — 1KB 压缩 → 1GB 解压。

**HUAKAI 改动**:
- **算法**: 流式解压时同时跟踪 `compressed_in_bytes` + `decompressed_out_bytes`；ratio > 10x → 中断 + 413 + audit + 触发 abuse detection。
- **数据结构**: `compression_bomb_alerts(tenant_id, api_key_id, ratio, occurred_at)`。
- **FSM**: 无。

**信号**: admin "压缩炸弹尝试 7 日分布"。

**F-***: F-REQ-BODY-001 + F-SEC-007。

**Effort**: 4-6 小时。

---

## R10. 响应 body cache / retry 重放（2 子模块）

### R10.1 Memory → disk → reject 三档 tier  [P1]  [架构卫生]
**基线-开源**: Billing-Engine-Ref `body_storage.go`（memory 阈值→disk）；Obs-Ref S3 持久。

**基线-官方**: 客户 retry 需要相同 body；流式响应 retry 时 HUAKAI 需要重新 read 客户 body。

**HUAKAI 改动**:
- **算法**: 客户请求 body 入站 → ≤64KB 留 memory → 64KB-16MB 落 disk tmp → >16MB reject 413。retry 时从 buffer 重读。
- **数据结构**: `request_body_buffer`（memory map 或 tmp file path）。
- **FSM**: 无。

**信号**: admin "tier 分布: 80% memory, 19% disk, 1% reject"。

**F-***: F-REQ-BODY-001（Track 2 extend）。

**Effort**: 6-8 小时。

### R10.2 TTL 5min（仅 retry 期）+ cleanup  [P1]  [架构卫生]
**基线-开源**: Billing-Engine-Ref 有 cleanup；Obs-Ref 有 TTL 配置。

**基线-官方**: 不能持久化客户 body（隐私 + 合规）— 仅 retry 期短暂存。

**HUAKAI 改动**:
- **算法**: buffer TTL = 5min（请求结束 + 5min）；后台 cron 每分钟扫 + 删过期。
- **数据结构**: `request_body_buffer_ttl(buffer_id, expires_at)`。
- **FSM**: buffer `active → ttl_grace → expired → deleted`。

**信号**: admin "buffer 占用 / cleanup 频率"。

**F-***: F-REQ-BODY-001。

**Effort**: 4-6 小时。

---

## Priority Rollup

| Priority | 子模块编号 | 类型 | 不做的代价 |
|---|---|---|---|
| **P0**（10 个）| R1.1 / R1.2 / R1.5 / R3.1 / R3.2 / R4.1 / R4.2 / R4.3 / R5.3 / R6.1 / R6.2 / R6.4 / R8.1 / R9.1 / R9.2 | passthrough + 架构卫生 | 协议事件漏 → 客户卡死 / 反代核心崩 / 合规失守 / zip bomb 漏洞 |
| **P1**（13 个）| R1.3 / R1.4 / R2.1 / R2.2 / R3.3 / R3.4 / R4.4 / R4.5 / R6.3 / R7.1 / R7.2 / R7.3 / R8.2 / R10.1 / R10.2 | passthrough + 架构卫生 + 创新 | provider 覆盖不全 / WS 客户报错 / 敏感信息泄露 / retry 失败 |
| **P2**（1 个）| R2.3 / R8.3 | 创新 | WS 体验差 / 操作员配置盲区 |
| **P3 plugin**（2 个）| R5.1 / R5.2 | 创新（合规风险） | 永远 plugin opt-in，不进默认 binary |

总 Effort 预估（P0+P1）: ~180-220 小时（即 4-6 周单 agent 工时）。

---

## Open Questions

1. R1.4 binary event-stream parser 是否值得 12-16 小时投入？或者推到 L2（只支持 X1+X2 先）？
2. R2 WebSocket 整体（R2.1+R2.2+R2.3 = ~40 小时）是否在 Personal Edition L1 范围内？
3. ~~R5 TLS plugin 是否进 backlog 还是直接拒绝~~ **已确认做**（Owner 2026-05-02：HUAKAI 竞赛产品定位，合规靠操作员 opt-in + README 警告而非阉割）。Open question 收窄为：**R5.1+R5.2 进 P2 plugin 还是 P3 plugin** — 即在 Personal Edition launch 前要不要做？
4. R7 mutation rule scope（global/tenant/pool/binding）4 层是否过度？L1 是否只 binding 级足够？
5. R8.3 暴露面板是否需要实时（流式聚合）还是 1h 延迟（cron）？
6. R10 disk tier 16MB 上限会不会太小（Vision 请求 base64 图片可能更大）？
7. R6.4 12 类错误 enum 是否够用？还是该再细分（如 X1 429 又分 RPM/TPM/billing）？
8. R3.1 scanner buffer 16MB 上限：per-tenant 配可改？还是 hardcoded？
