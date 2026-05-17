# Privacy / No User Data Logs — F-PRIV-001 Spec

| 字段 | 值 |
|---|---|
| Feature ID | F-PRIV-001 privacy / no user data logs (HUAKAI 6 大差异化 2 + 5) |
| Lane | Claude PM-Orchestrator synthesis (Claude draft 在 `docs/plans/2026-05-16-f-priv-001-spec-claude.md` + Codex draft 在 `/tmp/codex-f-priv-001-spec-codex-draft.md`, 本 spec PM 合并版) |
| Base | memory `project_core_trust_chain_differentiator` 6 大要求 + F-TRUST-001 spec (commit 158c421) §2 redaction allowlist |
| Phase | PRIV-1 (5 sub-phase, 5-7 天 codex, P-AUTH/F-AUTH ready) |
| Memory ref | [[project_core_trust_chain_differentiator]] [[feedback_huakai_better_than_sub2api]] |
| Scope | F-PRIV-001 实施 6 大差异化 2 (无用户数据日志) + 5 (日志只系统报错); 跟 F-TRUST-001 (链路+模型校验+商家不能做假) + F-AUDIT-001 (消费透明) 形成闭环 |
| Out of scope | operator-facing internal audit (F-AUDIT-001) / 反代各层 audit detail (各 spec 自身) / 计费 ledger (F-BILL-001) / production-ready 集成测试 (TRUST-1 + PRIV-1 + AUDIT-1 联合 release gate) |
| UTC | 2026-05-16T11:00:00Z (synthesis) |

## 1. 问题陈述

现有所有 AI gateway (sub2api / new-api / litellm / portkey / helicone / one-api 类) **默认 log user prompt + completion** — admin 后台可查全部用户对话内容. 即便 self-hosted operator 也是单方可见 user data.

HUAKAI 6 大差异化 2 + 5:
2. **无用户数据日志** — gateway 不存 user prompt / completion text
5. **日志只系统报错** — 系统级 error log 跟 user data log 严格分离

F-PRIV-001 实施 **2 + 5** = 严格 redaction allowlist + 全链路 compile-time enforce + 三通道 logger 分离.

## 2. 范围 — 数据分类标签

每个数据字段标 1 类:

| 标签 | 含义 | 例 |
|---|---|---|
| `NEVER_PERSIST` | 严禁进任何持久化 (DB / file / external sink); 仅 in-memory transit | prompt / completion / tool input / tool output / cookie / Authorization header / raw upstream body |
| `SECRET_MATERIAL` | 加密存 (KMS / KeyProvider AES-GCM); 解密仅 critical path memory + 用后 zeroize | API key / refresh token / OAuth token / credential bytes / proxy auth |
| `SENSITIVE_PII` | 严禁默认进 log; 仅 audit-approved storage + tenant-scoped | email (full) / phone / 精确 IP geolocation / device serial |
| `SAFE_METADATA` | 允许 audit / log / metric / ledger | request_id / ledger_id / tenant_scope_ref / model name / token count / cost / error class enum / latency bucket |
| `OPT_IN_PROOF` | 默认 OFF; Owner approve 后才存; 仅作用户验证不可逆 proof | content binding hash (sha256 + keyed salt) |

## 3. 数据流分类 + Redaction 规则

| 数据流 | 描述 | 规则 |
|---|---|---|
| **transit** | inbound body → upstream forward → response stream | NEVER_PERSIST + forward-only zero-copy; gateway memory-only 不进 disk / log |
| **state** | session / channel health / fingerprint / pacing 等运行时 | SAFE_METADATA 字段 allowlist; NEVER_PERSIST 字段绝不入 state |
| **audit** | F-TRUST ledger + 各反代层 audit + F-AUDIT receipt | SAFE_METADATA only; pre-write Redactor 强制 |
| **billing** | F-BILL-001 settler + F-BILL-002 voucher + F-AUDIT receipt | SAFE_METADATA (token / cost / idempotency); NEVER_PERSIST 严断 |
| **error log** | 系统 error trace | system-internal stack trace allowed; NEVER_PERSIST 严断; user-caused error 仅 enum + redacted detail |
| **observability metrics** | latency / throughput / cache ratio / error rate | 数值聚合 per-tenant scope; NEVER_PERSIST 严断 |
| **external log sink** | 转发到 OTel / Datadog / Loki 等 | 走同 Redactor pipeline; sink 不绕过 |

## 4. Redaction Allowlist (compile-time enforce)

每个 write path 必须经 `Redactor` interface:

```go
type Redactor interface {
    SanitizePayload(ctx, payload) ([]byte, error)
    SanitizeError(ctx, err) (string, error)
}
```

实现要求:
- 接受 typed struct (allowlist 字段); 拒绝 freeform `map[string]any` 除非 sub-key 全 allowlist
- panic on `string` 字段 > 256 chars (防 prompt 误入, debug build only; release build log warn + truncate + hash)
- 自动 truncate 长 string + 替换为 hash + structured warn
- `SanitizeError`: 仅 error class enum; raw upstream body 全 strip; HUAKAI 内部 stack 保留

**Allowlist 字段** (跟 F-TRUST-001 §2 一致):
- Identity: `request_id`, `trace_id`, `tenant_id` (DB-internal), `tenant_scope_ref` (user-facing receipt), `actor_type`, `actor_id_ref`
- Route/model: requested/route_decided/upstream_reported model, model verdict enum, provider family, protocol family
- Account: account public fingerprint (sha256[:8]), credential version, route policy version
- Token/Cost: input/output/cache_read/cache_write token count, cost_total_microcents, cost_rate_version
- Error: redacted error class enum (e.g. `network_timeout`, `upstream_rate_limit`), status class (2xx/4xx/5xx)
- Timing: latency bucket (10ms-100ms / 100ms-1s / etc, 不精确 ms 防 timing 推断)
- Hash: canonical payload hash (sha256[:8], opt-in only)
- System: HUAKAI internal stack trace, component, panic class, request_id ref

## 5. 错误日志规范 (Structured Logging, 3 channel 严分)

**结构化 schema**:
```
schema_version = "privacy.log.v1"
severity = debug | info | warn | error | critical
channel = system | user_action | security
tenant_scope = <tenant_id internal> | <tenant_scope_ref external>
```

**3 channel 严分** (compile-time interface 强制):

| Channel | 用途 | 允许 | 写入 |
|---|---|---|---|
| `SystemLogger` | HUAKAI 内部 bug / panic / config / DB connection fail | HUAKAI stack trace / 内部 error class / request_id | stdout structured JSON + OTel (operator-only) |
| `UserActionLogger` | user-caused error (auth fail / quota / model not found) | enum error class + request_id + redacted reason | F-TRUST ledger or per-tenant audit (tenant-scoped) |
| `SecurityLogger` | auth / 反代 / 跨 tenant / privacy guard hit | security event class + request_id + actor (tenant-scoped) | dedicated security sink (retention policy 独立) |

**严禁混用**:
- `SystemLogger` 不接受 user-controlled string (除 request_id)
- `UserActionLogger` 不接受 raw error.Error() (必须 SanitizeError)
- `SecurityLogger` 不接受非 security event class

**严禁 freeform interpolation**:
- ✓ `slog.Info("auth.failed", "request_id", id, "reason", "invalid_password")`
- ✗ `log.Printf("auth failed for %s: %s", email, err.Error())`

## 6. 实施机制

### 6.1 Pre-Write Redaction Check
- 所有 audit / ledger write 经 `Redactor` (compile-time interface enforce)
- 单测覆盖每 write path 验证 raw prompt 拒绝
- runtime panic if `unsafe_payload` 字段误入 (debug build); release log warn

### 6.2 Logger Configuration
- 3 channel 各 sink 配 (stdout / OTel / audit DB / security sink)
- 同进程 import 互斥 (compile-time tag)
- 外部 sink 必走 Redactor (sink adapter test 验)

### 6.3 HTTP Middleware
- Request middleware: 解析后 raw body discard (仅保 metadata: model / token_count / tenant)
- Response middleware: streaming forward zero-copy, 不缓存 raw content
- F-TRUST ledger 写入仅 metadata

### 6.4 Storage Hardening
- DB schema review CI: 拒绝 `prompt TEXT` / `completion TEXT` / `body BYTEA` / `*_content TEXT` 字段 (命名 trigger)
- Memory zeroize for decrypted credential (用 `crypto/subtle.ConstantTimeCopy + zeroize`)
- panic recover scrubber: stack trace 经 Redactor 后才输出

### 6.5 External Sink Adapter
- 所有 OTel / Datadog / Loki / Sentry 等 sink 走 same Redactor pipeline
- sink contract test 验证 redacted payload schema 跟 internal 一致

## 7. Cross-Chain Reference

| Family | 关系 |
|---|---|
| F-TRUST-001 | 共享 §2 redaction allowlist; F-TRUST `hop_chain.decision_ref` 必经 Redactor; F-TRUST `tenant_scope_ref` 用于 user-facing receipt; F-TRUST `OPT_IN_PROOF` content binding hash 默认 OFF, 启用必须 F-PRIV 单独 approve |
| F-AUDIT-001 | F-AUDIT receipt 字段必经 Redactor; 仅 token count + cost rate + provider family + rate version, 不含 content |
| F-CH-002 | `channel_health_audit_events.evidence_redacted` 必经 Redactor; 严禁 raw upstream body |
| F-FP-001 | profile_id 是 sha256 fingerprint, 不暴露 raw browser/OS detail |
| F-PACE-001 | pacing metadata only; 严禁 raw request content |
| F-NET-001 | proxy_endpoint encrypted at rest; burn evidence enum + 计数 + ID |
| F-ADV-001 | `active_detection_events.detection_evidence` 严禁 raw cookie/body |
| F-BILL-001 / F-BILL-002 | billing_events token/cost/idempotency only |
| user-facing error response | client 仅收 enum error class; 不暴露 HUAKAI internal stack |

## 8. Storage

**不引新表**. F-PRIV-001 是 cross-cutting policy + middleware. 约束已存表 writer pipeline (audit_ledger_entries / channel_health_audit_events / active_detection_events / pacing_session_traces / outbound_ip_burn_events / billing_events / usage_record / admin_audit_events).

## 9. 实施 Phase (Phase PRIV-1)

- **Phase PRIV-1-A** (1-2 天): inventory 所有 audit/log write path + `Redactor` interface + impl + compile-time enforce + 单测全 write path
- **Phase PRIV-1-B** (1-2 天): 3 channel logger (System/UserAction/Security) 分离 + 全代码 audit (grep `log.Print*` 替换 structured logger)
- **Phase PRIV-1-C** (1 天): HTTP middleware (request raw body discard + response zero-copy) + sink adapter (外部 sink 走 Redactor)
- **Phase PRIV-1-D** (1 天): Memory zeroize + DB schema review CI check + panic recover scrubber
- **Phase PRIV-1-E** (1-2 天): AT-PRIV-001 测试集 + cross-spec audit 联动 + admin transparency dashboard

## 10. 跟其它项目对比 (HUAKAI 强差异化)

| 项目类别 | 隐私处理 | HUAKAI 升级 |
|---|---|---|
| operator-only audit gateway (sub2api / new-api 类) | 默认 log prompt + completion, admin 可见 | HUAKAI 默认 strip; 严格 allowlist; compile-time enforce |
| observability tracing (litellm / portkey / helicone) | OTel trace 含 prompt sample, operator 可见 | HUAKAI 3 channel 严分; metadata-only observability; content 永不进 trace |
| 云厂 gateway (Bedrock / Azure OpenAI / Vertex) | 上游 audit 含 prompt | HUAKAI 不存 prompt, 上游 audit 不可见到本端 |
| AI privacy proxy (Cloak / Anon-LLM 类) | PII detection + redact, 仍 log redacted prompt | HUAKAI 不 log 任何 prompt-derived content; 包括 redacted version (除 OPT_IN_PROOF) |

**HUAKAI 独有**:
- compile-time Redactor interface 强制
- 3 channel 严分 (System / UserAction / Security) + import 互斥
- DB schema review CI check (拒绝 prompt/completion 字段)
- memory zeroize for credential
- cross-spec Redactor 共享 (F-TRUST + L3/L4/L5/L6 + F-BILL audit)
- forward-only data flow boundary (transit zero-copy)
- external sink 必走 Redactor pipeline
- content binding hash 默认 OFF (即便 opt-in 也仅 keyed hash + 不可作 search key)

## 11. Owner 后续 OCAW

- (D-PRIV-1) DB schema review CI 严格度 — 仅拒绝 prompt/completion 命名 vs 任何 TEXT 字段?
- (D-PRIV-2) Memory zeroize 性能预算 — 每 request 加 latency 多少?
- (D-PRIV-3) opt-in content binding hash 默认状态 — 永久 disable / off-with-button / opt-in available?
- (D-PRIV-4) Logger sink — 默认 stdout vs OTel vs file?
- (D-PRIV-5) Redactor panic vs warn — debug panic + release warn, 还是统一?
- (D-PRIV-6) Operator debugging 例外 — 允许 admin 临时启 prompt log (audit + Owner approval + time-limited) 还是绝对禁?
- (D-PRIV-7) Stack trace 含 file:line 是否 leak — 仅 component+function vs full file:line?
- (D-PRIV-8) redaction guard 发现 leak → admin critical alert + automatic sink shutdown?

## 12. Acceptance test outline (AT-PRIV-001-001..014)

| Test ID | Scenario | Expected |
|---|---|---|
| AT-PRIV-001-001 | prompt/completion sentinel 全 sink 搜索 | 仅 metadata; sentinel 不存在 |
| AT-PRIV-001-002 | streaming failure partial chunk | usage record terminal class + token estimate; log 无 chunk text |
| AT-PRIV-001-003 | tool I/O redaction (call + result 含 sentinel) | audit 仅 class/status/latency/count; 无 args/result |
| AT-PRIV-001-004 | cookie/token/API key 不进日志 (auth/upstream/retry fail) | system log 仅 fingerprint/ref; 无 secret value/prefix/suffix |
| AT-PRIV-001-005 | raw upstream error body normalized | 仅 normalized error_class; 无 raw body 持久化 |
| AT-PRIV-001-006 | system log freeform interpolation 拒绝 | pre-write guard block; fallback event `redaction_result=blocked` |
| AT-PRIV-001-007 | F-TRUST hop/model chain 无 content | hop_chain/model_chain 仅 safe metadata |
| AT-PRIV-001-008 | usage/billing 仅消费 metadata | 无 prompt preview |
| AT-PRIV-001-009 | cross-tenant log isolation | 404 or empty; 不暴露存在性 |
| AT-PRIV-001-010 | metadata/details/evidence JSON pre-write guard | writer fail-closed or drop; alert emitted |
| AT-PRIV-001-011 | panic recovery 不 dump request | system log 含 panic class/component/request_id; 无 body/header/locals |
| AT-PRIV-001-012 | external sink 仅 redacted event | sink payload 跟 internal schema 一致; 无 sentinel |
| AT-PRIV-001-013 | content binding hash default OFF | receipt 不含 content hash; no server-side content digest |
| AT-PRIV-001-014 | opt-in content binding hash 仅不可逆 proof | 仅 proof field + keyed hash; hash 不可作 search/debug key |

## 13. 风险表

| 风险 | Severity | 缓解 |
|---|---|---|
| legacy logger 旁路 (某 audit 写没接 Redactor) | HIGH | PRIV-1-A inventory + sink adapter + grep/static review + AT-006/012 |
| panic / recover dump local variables / request body | HIGH | panic scrubber + stack policy OCAW + AT-011 |
| upstream error body 当 error message 持久化 | HIGH | error normalizer + forbidden raw body rule + AT-005 |
| `metadata`/`details`/`evidence` 成 raw dump | HIGH | JSON allowlist + pre-write guard + schema review |
| F-TRUST / F-AUDIT 为透明误存 content | HIGH | F-PRIV cross-chain contract; trust/usage AT 必须 reference 隐私测试 |
| cross-tenant log query 暴露另一 tenant 存在性 | HIGH | tenant scope query + 404/empty semantics + AT-009 |
| content binding hash 成可关联指纹 | MED | 默认 OFF; opt-in salt/nonce; 禁 search/debug key |
| operator debugging 压力要 body capture | MED | OCAW 高风险确认; 默认无配置开关; incident path 另开 spec |
| external sink 配置错拿未脱敏 | MED | 所有 sink 走 redacted writer; contract test |
| full client IP / PII 混入 system log | MED | security-specific storage 与 general log 分离 |
| redaction 过严致排障难 | LOW | reason class + trace id + safe counters + dependency class 替代 raw |
| false positive block 丢 audit event | LOW | minimal fallback event + alert + test fixture 调整 |

## 14. Source files read + 中文摘要

### Source files read (synthesis lane)
- docs/specs/trust-chain-user-verifiable-ledger.md (F-TRUST-001 closure partner)
- docs/specs/active-anti-detection.md / device-fingerprint-binding.md / request-pacing-mimicry.md / outbound-ip-pool.md / channel-health-auto-disable.md (cross-chain audit ref)
- docs/plans/2026-05-16-f-priv-001-spec-claude.md (Claude lane parallel-draft, 16KB)
- /tmp/codex-f-priv-001-spec-codex-draft.md (Codex lane parallel-draft, 357KB)
- memory: `project_core_trust_chain_differentiator`

### Synthesis decisions (Claude + Codex diff)
- 取 Codex: 5 标签数据分类 (`NEVER_PERSIST` / `SECRET_MATERIAL` / `SENSITIVE_PII` / `SAFE_METADATA` / `OPT_IN_PROOF`); `schema_version` privacy.log.v1; forward-only transit boundary; external sink 必走 Redactor; 14 AT (vs Claude 10); 3 channel logger 加 `SecurityLogger` (Claude 只 System + UserAction)
- 取 Claude: cross-chain 9 行表; Phase TRUST-1-A 已完成标注; 6 OCAW (codex 加 OCAW-7 stack file:line + OCAW-8 critical alert → 合并 8 OCAW)
- 合并: 14 章 + Codex 加分类标签 + 3 channel logger; Claude cross-chain detail

### OWNER 中文摘要
F-PRIV-001 隐私 / 无用户数据日志 synthesis spec 落档. 6 大差异化 2 + 5 实施. 关键设计 = 5 标签数据分类 (NEVER_PERSIST 等) + 严格 Redactor allowlist (compile-time interface 强制) + 3 channel logger 严分 (System / UserAction / Security) + forward-only transit boundary + DB schema review CI check + memory zeroize + cross-spec Redactor 共享 + external sink 必走 Redactor pipeline. 不引新表. Phase PRIV-1 (5 sub-phase 5-7 天 codex). 8 Owner OCAW. AT-PRIV-001-001..014. 风险表 12 项 (HIGH 6). 跟所有现有 gateway 差异 = HUAKAI 不存 prompt + compile-time enforce + 3 channel + external sink 必脱敏. Phase 6 商业基础 + Trust family 闭环.
