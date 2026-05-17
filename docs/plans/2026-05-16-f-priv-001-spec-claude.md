# F-PRIV-001 Privacy / No User Data Logs Spec — Claude Lane Draft

| 字段 | 值 |
|---|---|
| Feature ID | F-PRIV-001 privacy / no user data logs (6 大差异化 2 + 5) |
| Lane | Claude PM-Orchestrator parallel-draft (跟 codex specifier lane parallel per CLAUDE.md #10; final spec 等 synthesis) |
| Plan path | `docs/plans/2026-05-16-f-priv-001-spec-claude.md` (本文件) |
| Intended final path after synthesis | `docs/specs/privacy-no-user-data-logs.md` |
| Closure partner | F-TRUST-001 链路+模型校验+商家不能做假 [[trust-chain-user-verifiable-ledger]] / F-AUDIT-001 用户消费透明 (并行 lane) |
| Memory ref | [[project_core_trust_chain_differentiator]] [[feedback_huakai_better_than_sub2api]] |
| UTC | 2026-05-16T10:30:00Z |

## 1. 问题陈述

现有所有 AI gateway (sub2api / new-api / litellm / portkey / helicone / one-api 类) 全 **默认 log user prompt + completion text** — admin 后台可查全部用户对话内容。即便 self-hosted operator 也是单方可见 user data。

HUAKAI 核心差异化 (memory `project_core_trust_chain_differentiator` 6 要求中的 2 + 5):
2. **无用户数据日志** — gateway 不存 user prompt / completion text
5. **日志只系统报错** — 系统级 error log 跟 user data log 严格分离 (不允许 error log 含 user content)

F-PRIV-001 实施 **2 + 5** = HUAKAI 严格 redaction allowlist + 全链路 no-user-data 保证.

## 2. 范围定义

**严禁记录 (任何 audit/log/ledger/metrics 不允许出现)**:
- user prompt 任意 message content
- assistant completion 任意 text/tool call/tool result
- system prompt (用户自定义)
- tool input/output (用户提供的 schema 内容)
- user-provided context (file content / 自定义 metadata.user_content)
- cookie (Authorization / Cookie header)
- token (API key / refresh token / session token / OAuth token)
- credential bytes (encrypted credential blob 解密后明文)
- raw upstream response body / error body 文本
- proxy credential (F-NET-001 outbound IP pool proxy auth)
- 可逆 PII (email / phone / IP geolocation 精确度 / 用户 device serial)

**允许记录 (audit/log/ledger 白名单)**:
- request_id / ledger_id / tenant_scope_ref (不暴露裸 tenant_id)
- HTTP method + path + status code class (2xx/4xx/5xx)
- model name / provider family / route policy version / account public fingerprint (sha256[:8])
- token counts (input/output/cache hit) + cost refs (跟 F-AUDIT-001 cross-ref)
- redacted error class enum (e.g. `network_timeout` / `upstream_rate_limit`, **不含 raw upstream error body**)
- status class (cool/warm/healthy)
- latency bucket (10ms-100ms / 100ms-1s / etc, 不精确 ms 防 timing 推断)
- canonical payload hash (sha256[:8], opt-in only by F-PRIV-001 §5)
- 系统级 error trace (HUAKAI 内部 stack trace, 不含 user data)

## 3. 数据流分类

| 数据流 | 描述 | F-PRIV redaction 规则 |
|---|---|---|
| **transit** | inbound request body → upstream forward → response stream | 不存储 (memory-only), gateway forward 后立即丢弃; 流转期间 zero-copy 不进 disk / log |
| **state** | session / channel health / device fingerprint / PASR cache 等运行时状态 | 状态字段允许 (allowlist 白名单), **不含 prompt/response content** |
| **audit** | 0013 user-facing ledger + 各反代层 audit + F-AUDIT-001 receipt | 严格 redaction allowlist 内, pre-write check 强制; raw user content 全断 |
| **billing** | F-BILL-001 settler + F-BILL-002 voucher | 仅 token counts + cost rate + idempotency key, 不含 content |
| **error log** | 系统 error trace (HUAKAI 自身 bug / panic / structured logger) | 仅含 HUAKAI 内部 stack trace + request_id + structured fields enum, **不含 user data**; user-caused error (validation / auth fail) 仅记 error class 不含 content |
| **observability metrics** | latency / throughput / cache hit ratio / error rate | 仅 数值聚合 (per-tenant scope), 不含 per-request content |

## 4. Redaction Allowlist (强制 pre-write check)

每个 write path (audit / log / metric / ledger) 必须经过 `Redactor` 中间件:

```
type Redactor interface {
    SanitizePayload(ctx, payload) ([]byte, error) // 仅 allowlist 字段; 拒绝 raw content
    SanitizeError(ctx, err) (string, error)        // 仅 error class; 不暴露 raw message
}
```

`SanitizePayload` 实现:
- 接受 typed struct (whitelist 内字段); 拒绝 freeform `map[string]any` (除非 sub-key 全在 allowlist)
- panic on `string` 字段 > N (默认 N=256) — 防 prompt 误入
- 自动 truncate 长 string + 替换为 hash + warn

`SanitizeError`:
- 仅取 error class enum (e.g. `network_timeout`)
- raw upstream body / response text 全 strip
- 系统 stack trace (HUAKAI 内部) 保留 (帮 debug HUAKAI bug, 不含 user data)

## 5. 错误日志规范 (Structured Logging)

**两套 logger 严格分离**:

| Logger | 用途 | 允许内容 | 写入位置 |
|---|---|---|---|
| `SystemLogger` | HUAKAI 内部错误 (panic / bug / config error / DB connection fail) | HUAKAI stack trace / 内部 error class / request_id | stdout structured JSON; 系统 monitoring (operator-only) |
| `UserActionLogger` | user-caused error (auth fail / quota exceeded / model not found) | enum error class + request_id + redacted detail (e.g. "model not allowed for tenant"), **不含 user prompt** | audit 表 (F-TRUST-001 ledger) + per-tenant operator-visible log |

**严禁混用**:
- SystemLogger 不允许接受 user-controlled string 字段 (除 request_id)
- UserActionLogger 不允许 raw error.Error() string (必须先经 Redactor.SanitizeError)

**结构化字段** (避免 string interpolation 误入 user data):
- ✓ `slog.Info("auth.failed", "request_id", id, "reason", "invalid_password")`
- ✗ `log.Printf("auth failed for %s: %s", email, err.Error())`

## 6. 实施机制

### 6.1 Pre-Write Redaction Check
- 所有 audit / ledger write 必须经 `Redactor` 中间件 (compile-time 强制 via interface signature)
- 单测覆盖每个 write path 验证 raw prompt/response 拒绝
- runtime panic if `unsafe_payload` 字段误入 (debug build only)

### 6.2 Logger Configuration
- `SystemLogger` 默认 stdout JSON (structured); 配置 sink 可选 OTel
- `UserActionLogger` 默认 audit DB (F-TRUST-001 ledger or per-tenant audit)
- 严禁同进程内同时 import 两个 logger 而不 explicit 选择

### 6.3 HTTP Middleware
- Request middleware: 解析后立即丢 raw body (仅保留 metadata: model / token_count / tenant); 防 raw body 进 log/audit
- Response middleware: streaming forward 时 zero-copy, 不缓存 raw content; F-TRUST-001 ledger 写入仅 metadata

### 6.4 Storage Hardening
- DB column policy: prompt/completion 字段不存在任何表; schema review 拒绝任何 `prompt TEXT` / `completion TEXT` / `body BYTEA` 字段 (审查 PR 时强制)
- Encrypted credential blob 解密只在 critical path (forward) memory, decrypted bytes never logged
- Memory zeroize: 解密后 buffer 用完 `crypto/subtle.ConstantTimeCopy + zeroize` 清除

## 7. Cross-Chain Reference

| Family | 关系 |
|---|---|
| F-TRUST-001 | 共享 §2 redaction allowlist; F-TRUST-001 ledger.hop_chain.decision_ref 必须经 F-PRIV-001 `Redactor.SanitizePayload`; F-TRUST-001 §5 opt-in content binding hash 默认 OFF, 启用必须 F-PRIV-001 单独 approve |
| F-AUDIT-001 | F-AUDIT user-facing cost receipt 必须经 `Redactor`; 仅 token count + cost rate + provider family + rate version, 不含 content |
| F-CH-002 channel_health_audit_events | `evidence_redacted JSONB` 必须经 `Redactor.SanitizePayload`; 严禁 raw upstream body |
| F-FP-001 device_fingerprint_bindings | profile_id 是 sha256 fingerprint, 不暴露 raw browser/OS detail to audit |
| F-PACE-001 pacing_session_traces | pacing 数据是 metadata (session_uuid + count + drift), 严禁 raw request content |
| F-NET-001 outbound_ip_burn_events | proxy_endpoint encrypted at rest; burn evidence 仅 enum + 计数 + ID |
| F-ADV-001 active_detection_events | detection_evidence JSONB 严禁 raw cookie / raw body |
| F-BILL-001 + F-BILL-002 | billing_events 仅 token counts + cost + idempotency, 不含 content |
| user-facing error response | client 收到的 error message 仅 enum class (e.g. `INVALID_MODEL`); 不暴露 internal HUAKAI stack |

## 8. Storage

**不引新表**. F-PRIV-001 是 cross-cutting policy + middleware, 不需要专属表.

约束已存在表 (writer pipeline + DB schema review):
- audit_ledger_entries (F-TRUST-001): redaction enforced via Redactor
- channel_health_audit_events (F-CH-002): 同
- active_detection_events (F-ADV-001): 同
- pacing_session_traces (F-PACE-001): 同
- outbound_ip_burn_events (F-NET-001): 同
- billing_events (F-BILL-001): 同
- usage_record (F-BILL-001): 同
- admin_audit_events (F-AUDIT-001 等 spec 引入): 同

## 9. 实施 Phase (Phase PRIV-1)

- **Phase PRIV-1-A** (1-2 天 codex): Redactor interface + impl + 单测; 所有 audit write path 接入 (compile-time enforce)
- **Phase PRIV-1-B** (1-2 天): SystemLogger + UserActionLogger 分离; 全代码 audit 替换 (grep `log.Print*` 全部走 structured logger)
- **Phase PRIV-1-C** (1 天): HTTP middleware (request raw body discard + response zero-copy verify)
- **Phase PRIV-1-D** (1 天): Memory zeroize for decrypted credential + DB schema review tool (CI check 拒绝 prompt/completion 字段)
- **Phase PRIV-1-E** (1-2 天): AT-PRIV-001 测试集 + cross-spec audit 联动验证 + admin transparency dashboard (operator 可见自家 audit, 不可见 cross-tenant)

## 10. 跟其它项目对比 (HUAKAI 强差异化)

| 项目类别 | 隐私处理 | HUAKAI 升级 |
|---|---|---|
| operator-only audit gateway (sub2api / new-api 类) | 默认 log prompt + completion, admin 可见 | HUAKAI 默认 strip prompt/completion, 严格 redaction allowlist, compile-time enforce |
| observability tracing (litellm / portkey / helicone) | OTel trace 含 prompt sample, operator 可见 | HUAKAI 双 logger 严分; metadata-only observability, content 永不进 trace |
| 云厂 gateway (Bedrock / Azure OpenAI / Vertex) | 上游 audit 含 prompt, caller 必须信任云厂 | HUAKAI 不存 prompt, 上游 audit 不可见到本端 |
| AI privacy proxy (e.g. Cloak / Anon-LLM) | 用 PII detection + redact, 但仍 log redacted prompt | HUAKAI 不 log 任何 prompt-derived content, 包括 redacted version (除 opt-in F-TRUST-001 content binding hash) |

**HUAKAI F-PRIV-001 独有**:
- compile-time Redactor interface 强制 (不仅 runtime check)
- 双 logger 严分 (SystemLogger vs UserActionLogger) + import 互斥
- DB schema review CI check (拒绝 prompt/completion 字段)
- memory zeroize for credential
- cross-spec redaction enforce (F-TRUST + L3/L4/L5/L6 audit 全共享 Redactor)
- content binding hash 默认 OFF (即便用户 opt-in 也仅 hash 不存 content)

## 11. Owner 后续 OCAW

- (D-PRIV-1) DB schema review CI check 严格度 — 拒绝任何 TEXT 字段 vs 仅拒绝 prompt/completion 命名?
- (D-PRIV-2) Memory zeroize 性能预算 — 每 request zeroize 加 latency 多少接受?
- (D-PRIV-3) opt-in content binding hash 默认状态 — OFF (推) vs OFF-with-button vs full-disable?
- (D-PRIV-4) UserActionLogger sink — 默认 audit DB vs operator-only file vs both?
- (D-PRIV-5) Redactor panic vs warn — debug build panic, release build warn 还是统一?
- (D-PRIV-6) Operator-debugging 例外 — 是否允许 admin 临时启用 prompt log for debug (with audit + Owner approval)? 影响用户信任承诺.

## 12. Acceptance test outline (AT-PRIV-001-001..010, 加进 docs/11_ACCEPTANCE_TEST_MATRIX.md)

- AT-PRIV-001-001: 发 request → 0013 ledger entry hop_chain.decision_ref / model_chain 无 user prompt / completion text
- AT-PRIV-001-002: 发 request → channel_health_audit_events / active_detection_events 无 raw upstream body
- AT-PRIV-001-003: SystemLogger 输出 (stdout JSON) 无 user prompt / completion
- AT-PRIV-001-004: UserActionLogger (auth fail / quota exceeded) 无 user prompt
- AT-PRIV-001-005: 故意构造 freeform map[string]any 写 audit → Redactor 拒绝 (panic in debug, error in release)
- AT-PRIV-001-006: > N (256) chars string 字段 → Redactor truncate + hash
- AT-PRIV-001-007: DB schema review CI check — PR 加 prompt TEXT 字段 → CI fail
- AT-PRIV-001-008: decrypted credential 内存 zeroize 验证 (post-use buffer 是 zero)
- AT-PRIV-001-009: cross-tenant log — tenant A 操作 → tenant B operator 不可见 (RLS + UserActionLogger tenant scope)
- AT-PRIV-001-010: streaming response zero-copy 验证 (response body 不进 disk / log / memory cache)

## 13. 风险表

| 风险 | Severity | 缓解 |
|---|---|---|
| 实施漏点 (某个新 audit write path 没接 Redactor) | HIGH | compile-time interface enforce + CI review checklist + 单测覆盖所有 write path |
| Redactor 误判 (合法字段被 strip 致 audit 缺) | MED | 单测 / fuzz; allowlist 字段定义清晰 / 文档化 |
| Memory zeroize 性能影响 | MED | benchmark + OCAW D-PRIV-2 决定阈值; 仅 critical path zeroize |
| DB schema review CI 误判 (合法 TEXT 字段被拒) | LOW | 命名 convention (prompt/completion/body 关键字 trigger; 其他 TEXT OK); review 可 override with reason |
| operator-debugging 需求 (debug 用户问题需要 prompt) | MED | OCAW D-PRIV-6 决定; 默认拒绝, 启用必须 audit + Owner approve + time-limited |
| SystemLogger 误含 user data | HIGH | interface signature 不接受 user-controlled string + 单测 + lint |
| UserActionLogger 跨 tenant leak | HIGH | tenant scope enforce + RLS + AT-PRIV-001-009 |
| content binding hash 启用后 reverse-engineer | MED | opt-in only; hash 用 keyed sha256 防 dictionary; 默认 OFF |
| Stack trace 含 user-controlled string (e.g. panic with user input) | HIGH | Redactor 在 panic recover 时 strip; structured panic handler |

## 14. Source files read + 中文摘要

### Source files read (Claude lane plan-then-draft)
- docs/specs/trust-chain-user-verifiable-ledger.md (F-TRUST-001 closure partner, §2 redaction allowlist 锚定)
- docs/specs/active-anti-detection.md §6 (L6 redaction guard 模板)
- docs/specs/device-fingerprint-binding.md (L3 redaction 模板)
- docs/specs/request-pacing-mimicry.md §6 (L4 redaction)
- docs/specs/outbound-ip-pool.md §6 (L5 redaction + proxy_endpoint encrypted)
- docs/specs/channel-health-auto-disable.md (F-CH-002 audit redaction)
- backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql
- memory: `project_core_trust_chain_differentiator` (HUAKAI 6 大差异化, F-PRIV 实施 2 + 5)
- 不读任何上游项目源码 (clean-room)

### OWNER 中文摘要

F-PRIV-001 隐私 / 无用户数据日志 spec Claude lane draft 落档. HUAKAI 6 大差异化 2 (无用户数据日志) + 5 (日志只系统报错) 实施 spec. 关键设计 = 严格 redaction allowlist (compile-time `Redactor` interface 强制) + 双 logger 严分 (`SystemLogger` HUAKAI bug only / `UserActionLogger` user-caused error 仅 enum) + DB schema review CI check (拒绝 prompt/completion 字段) + memory zeroize (decrypted credential) + cross-spec enforce (F-TRUST + L3/L4/L5/L6 audit 全用同 Redactor). 不引新表 (cross-cutting policy + middleware). Phase PRIV-1 (5 sub-phase 5-7 天 codex). 6 Owner OCAW (CI 严格度 / zeroize 性能 / content hash 默认 / sink / panic vs warn / operator-debug 例外). AT-PRIV-001-001..010. 风险表 8 项含 HIGH (实施漏 / SystemLogger 含 user data / UserActionLogger cross-tenant leak / Stack trace user input / 实施漏点). 跟所有现有 gateway 差异 = HUAKAI 不存 prompt + 全链路 compile-time enforce + 双 logger 严分. Phase 6 商业基础 + Trust family 一部分.
