=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: new-api / sub2api by HUAKAI parity-matrix reputation only; no reference source read in this session.

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors -
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

# 2026-05-15 F-OBS-003 4-state failed-stream billing Codex 独立计划

| Field | Value |
| --- | --- |
| Owner directive | "Write CODEX plan for HUAKAI F-OBS-003 (4-state failed-stream billing) - Go backend feature." |
| Authorization | Owner 2026-05-15 全 session 授权 codex sandbox workspace-write；Go backend 临时解冻，quote "解冻啊。蠢啊"。 |
| Parallel draft | 本文件为 Codex 独立计划；未读取 `docs/process/plans/2026-05-15-f-obs-003-claude.md`。 |
| Execution boundary | 只计划，不实现，不 commit。 |
| Observed regions | 21 |
| Inferences | 8 |
| Open questions | 6 |

## scope (what changes)

本计划覆盖 F-OBS-003 的 Go backend 实现边界，不执行代码改动。F-OBS-003 在矩阵中要求流式失败按四种业务语义对账，并与 F-OBS-001 / F-BILL-001 共用 Tx2 结算路径，当前状态是 L2 Phase 4.5 Mandatory Roadmap。依据：`docs/03_FEATURE_PARITY_MATRIX.md:114`。

计划内要设计的变化：

- 在现有 streaming taxonomy 之上增加 failed-stream billing 语义轴，不替换 `end_class`。现有 `StreamEndClass` 已覆盖 `client_disconnect`、`total_stream_timeout`、`upstream_error_5xx`、`ambiguous_usage`、`unknown_termination` 等技术终态。依据：`backend/internal/gateway/forwarder_types.go:18`。
- 将 Owner 口径的 4-state billing lifecycle 固化为 `Acquired -> In-Flight -> Partial-Delivered -> Failed`，并把矩阵口径的 terminal class 固化为 `client_gone / upstream_timeout / output_token_zero / upstream_5xx`。这是两个轴：一个说明 attempt 生命周期，一个说明失败结算原因。
- Tx2 必须在 upstream RST / mid-stream failure 时仍写 per-attempt `usage_records` 和 `billing_events`，不得 silent drop。现有 Tx2 spec 已要求 Usage Record 与 billing event 在同一事务写入。依据：`docs/specs/observability-billing.md:63`、`docs/specs/observability-billing.md:74`、`docs/specs/observability-billing.md:83`。
- Owner-visible reconciliation 要能回答：哪次 attempt 被计费、为什么是 partial、最终 charge/refund 规则是什么。现有 admin observability 已能列 usage / claims / audit，但未显式暴露 failed-stream billing state 或 terminal class。依据：`backend/sql/queries/observability.sql:4`、`backend/sql/queries/observability.sql:45`、`backend/sql/queries/observability.sql:83`。

计划外：

- 不读取 new-api / sub2api / portkey / helicone / litellm / all-api-hub / envoy-ai-gateway 源码。
- 不改 `LICENSE`、真实 secret、生产部署脚本。
- 不在此计划中实现 pricing formula、refund worker、dashboard UI 或 payment ledger。
- 不改变现有 F-GW-002 streaming forwarder 的协议转换职责边界；Adapter 仍只返回 `UsageRecordDraft`，Executor / Ledger 触发 Tx2。依据：`docs/specs/_invariants/cross-module-boundaries.md:117`、`docs/specs/_invariants/cross-module-boundaries.md:125`。

## file-by-file impact (which Go files; cite existing backend/ paths first via grep)

后续实现建议按下面顺序改，先 backend Go hot path，再 SQL / sqlc，再 admin read API，再 tests。

| File | Impact |
| --- | --- |
| `backend/internal/gateway/forwarder_types.go` | 给 `UsageRecordDraft` 增加 HUAKAI-owned fields，例如 `AttemptBillingState`, `FailedStreamTerminalClass`, `SettlementAction`, `ChargeExplanation`。现有 draft 已承载 tokens、cost、routing、`EndClass`、`UsageSource`、`DrainOutcome`、`PendingReconciliation`。依据：`backend/internal/gateway/forwarder_types.go:78`。 |
| `backend/internal/gateway/forwarder.go` | 在 `Forward` / `finishDraft` 中把技术终态映射到 billing lifecycle + terminal class。`finishDraft` 当前只把 zero accumulator + unknown 转成 `AmbiguousUsage`，并把非 graceful reported 改成 partial source；F-OBS-003 需要把 `ClientDisconnect`、timeout、zero output、5xx/RST 分类成可对账字段。依据：`backend/internal/gateway/forwarder.go:85`、`backend/internal/gateway/forwarder.go:377`。 |
| `backend/internal/gateway/stream_scanner.go` 和 provider-specific scanner files | 仅在确需区分 upstream RST / 5xx / scanner EOF 时触碰。当前 `classifyScanError` 只有 scanner overflow / context cancel / unknown 三类，RST 与 5xx 需要从 scanner 或 upstream dispatcher 传入 typed error。依据：`backend/internal/gateway/forwarder.go:479`。 |
| `backend/internal/billing/billing.go` | 扩展 `SettleRequest`，把 draft 中的新 billing fields 传入 Tx2。现有 `SettleRequest` 已携带 `ClaimID`、`AcquisitionToken`、`ProviderAccountID`、`AttemptSeq`、`Draft`、`Fingerprint`。依据：`backend/internal/billing/billing.go:65`。 |
| `backend/internal/billing/settler.go` | 在 `Settle` 和 `Abort` 两条 Tx2 路径写入 failed-stream billing fields。现有 `Settle` 同事务写 usage record、billing event、release slot、commit claim；`Abort` 也会写 zero-cost usage record。依据：`backend/internal/billing/settler.go:37`、`backend/internal/billing/settler.go:181`。 |
| `backend/sql/queries/billing_settle.sql` | 扩展 `InsertUsageRecord` / `InsertBillingEvent` 参数，写入 `attempt_billing_state`、`terminal_class`、`settlement_action`、`settlement_reason` 等字段。现有 query 已写 `end_class`、`usage_source`、`pending_reconciliation`、`drain_outcome`。依据：`backend/sql/queries/billing_settle.sql:28`、`backend/sql/queries/billing_settle.sql:59`。 |
| `backend/sql/queries/observability.sql` | 让 `/admin/v1/usage`、`/admin/v1/billing/claims`、`/admin/v1/audit-events` 返回新的 reconciliation fields，并支持按 terminal class / billing state 过滤。现有查询已关联 `usage_records` 与 `billing_ledger_claims`。依据：`backend/sql/queries/observability.sql:4`、`backend/sql/queries/observability.sql:45`。 |
| `backend/internal/gatewayhttp/admin_observability_handler.go` | 给 `obsQuery` 和 handler 参数增加 `terminal_class`、`attempt_billing_state`、`settlement_action` 过滤。现有 handler 已解析 tenant/time/provider/pool/api_key/account/model/status/pending filters。依据：`backend/internal/gatewayhttp/admin_observability_handler.go:35`、`backend/internal/gatewayhttp/admin_observability_handler.go:123`。 |
| `backend/internal/gatewayhttp/admin_observability_helpers.go` | 如 admin rows 的 sqlc struct 不直接 JSON 输出，需要补 map helper，保证 charge/refund reason 在 audit payload 中可见。当前 audit row mapping 已把 `payload` 原样输出。依据：`backend/internal/gatewayhttp/admin_observability_helpers.go:20`。 |
| `backend/cmd/gateway/main.go` | 仅当 Executor wiring 需要传递新 field 或注册 typed scanner error 时触碰；现有 gateway 已 wiring `billing.NewSettler` 和 `StreamForwarder`。依据：`backend/cmd/gateway/main.go:166`、`backend/cmd/gateway/main.go:168`。 |
| `backend/internal/billing/settler_integration_test.go` | 扩展 integration test，覆盖 mid-stream partial settlement、zero-output no-charge、upstream timeout、upstream 5xx / RST。现有 tests 已覆盖 atomic settle、abort zero-cost usage record、ambiguous mapping。依据：`backend/internal/billing/settler_integration_test.go:30`、`backend/internal/billing/settler_integration_test.go:110`、`backend/internal/billing/settler_integration_test.go:216`。 |
| `backend/internal/gateway/forwarder_test.go` | 增加 pure unit tests，确保 `UsageRecordDraft` 的 state/class/action mapping 不依赖 DB。现有 tests 已覆盖 client disconnect drain、timeout、ambiguous usage、EOF no terminal。依据：`backend/internal/gateway/forwarder_test.go:188`、`backend/internal/gateway/forwarder_test.go:301`、`backend/internal/gateway/forwarder_test.go:379`、`backend/internal/gateway/forwarder_test.go:408`。 |
| `backend/internal/gatewayhttp/admin_observability_handler_test.go` | 增加 handler filter / response contract tests。现有 tests 已覆盖 usage / claims / audit filters、cursor、limit、tenant scope。依据：`backend/internal/gatewayhttp/admin_observability_handler_test.go:70`、`backend/internal/gatewayhttp/admin_observability_handler_test.go:79`、`backend/internal/gatewayhttp/admin_observability_handler_test.go:141`。 |

sqlc 生成文件会随 query/schema 改动被更新，但不应手写。

## data model (table/schema changes - flag for Owner if migration needed)

需要 migration。原因：当前 schema 只有 `billing_ledger_claims.status IN ('reserving','committed','aborted')`，`usage_records.end_class` 和 `usage_source`，没有 Owner 可直接查询的 attempt billing lifecycle / terminal class / settlement action。依据：`backend/sql/migrations/0002_observability_billing.up.sql:45`、`backend/sql/migrations/0002_observability_billing.up.sql:145`、`backend/sql/migrations/0002_observability_billing.up.sql:155`。

建议新增 forward-only migration，Owner 必须签字，因为 `database schema` 和 `billing ledger` 属高风险范围。依据：`docs/12_AGENT_WORKFLOW.md:186`、`docs/12_AGENT_WORKFLOW.md:194`、`docs/12_AGENT_WORKFLOW.md:196`。

建议字段：

| Table | Additive fields | Reason |
| --- | --- | --- |
| `usage_records` | `attempt_billing_state text NOT NULL DEFAULT 'in_flight' CHECK (...)` | 每条 usage record 表示一个 attempt 的最终可对账生命周期：`acquired` 只应出现在 orphan/recovery audit，不建议正常 usage row 使用；Tx2 normal write 多数为 `partial_delivered` 或 `failed`。 |
| `usage_records` | `failed_stream_terminal_class text CHECK (...)` nullable | 失败流原因：`client_gone`, `upstream_timeout`, `output_token_zero`, `upstream_5xx`。非 failed-stream 可为 NULL。 |
| `usage_records` | `settlement_action text NOT NULL DEFAULT 'charge_exact' CHECK (...)` | 对账动作：`charge_exact`, `charge_partial`, `charge_zero`, `refund_pending`, `manual_review`。 |
| `usage_records` | `settlement_reason jsonb NOT NULL DEFAULT '{}'::jsonb` | Owner-visible explanation，记录为什么 partial、为何 no-charge、是否等待 reconciliation。 |
| `billing_events` | 同步增加 `attempt_billing_state`, `failed_stream_terminal_class`, `settlement_action`, `settlement_reason` | 即使 Usage Record 后续进入 DLQ / replay，billing event 也能作为 audit fallback。现有 spec 已要求 billing event 是 durable audit trail。依据：`docs/specs/observability-billing.md:129`。 |
| `billing_ledger_claims` | 可选：`last_attempt_billing_state`, `last_failed_stream_terminal_class` | 便于 `/billing/claims` 列表直接筛选。若担心污染 claim source of truth，可不加，改为 join latest usage record。 |

不建议直接改 `billing_ledger_claims.status` 为四态，因为现有 Tx1/Tx2 状态机依赖 `reserving / committed / aborted`，扩大 enum 会增加 retry/idempotency 风险。现有 `Reserve` 会基于 `committed / reserving / aborted` 分支处理 idempotency replay 和 re-reserve。依据：`backend/internal/billing/claim_gate.go:91`。

关于 `attempt_id` / `lease_id`：cross-module invariant 已要求未来所有 ledger rows 带 request / attempt / lease 链，但当前 schema 尚未落这些列。依据：`docs/specs/_invariants/cross-module-boundaries.md:70`、`docs/specs/_invariants/cross-module-boundaries.md:141`。F-OBS-003 最小可行实现可先用 `claim_id + attempt_seq + provider_account_id` 对账；若 Owner 要严格 "which attempt billed" 达到未来 invariant，则本 feature 应合并 additive `request_id`, `attempt_id`, `lease_id` migration。这是 Owner decision point。

## happy path + failure modes (4 states diagram in ASCII)

```
Tx1 Reserve
  |
  v
Pool Claim writes provider_account_id + acquisition_token
  |
  v
[Acquired]
  |  slot acquired, no upstream bytes yet
  v
[In-Flight]
  |  upstream stream opened
  |
  +-- first billable client chunk flushed --> [Partial-Delivered]
  |                                      |
  |                                      +-- graceful terminal
  |                                      |     settlement_action=charge_exact
  |                                      |     failed_stream_terminal_class=NULL
  |                                      |
  |                                      +-- client disconnect / upstream RST / timeout / 5xx
  |                                            settlement_action=charge_partial or manual_review
  |                                            usage_source=partial or inferred
  |                                            pending_reconciliation=true when not authoritative
  |
  +-- no output token / pre-content upstream failure --> [Failed]
                                             settlement_action=charge_zero or refund_pending
                                             usage_source=ambiguous or inferred
                                             failed_stream_terminal_class=output_token_zero/upstream_timeout/upstream_5xx
```

Terminal class mapping plan:

| Existing signal | Billing state | Terminal class | Default settlement action |
| --- | --- | --- | --- |
| `ClientDisconnect` after any delivered content | `partial_delivered` | `client_gone` | `charge_partial`, pending reconciliation if drain did not reach authoritative usage. |
| `TotalStreamTimeout` / `InterEventTimeout` / first-token timeout after upstream opened | `partial_delivered` if tokens > 0 else `failed` | `upstream_timeout` | partial if tokens > 0, otherwise zero/manual review. |
| `TokensOutput == 0` with failed / ambiguous end | `failed` | `output_token_zero` | `charge_zero`, operator-visible. |
| typed upstream 5xx / RST mid-stream | `partial_delivered` if tokens > 0 else `failed` | `upstream_5xx` | partial if tokens > 0, otherwise zero/manual review. |

Failure modes to design for:

- Upstream RST after tokens but before usage frame: commit usage row with partial accumulator, `pending_reconciliation=true`, and explicit settlement reason.
- Client disconnect while upstream continues: existing drain path extracts usage and sets partial source; F-OBS-003 must persist why partial.依据：`backend/internal/gateway/forwarder.go:207`、`backend/internal/gateway/forwarder.go:321`。
- Zero output token but nonzero prompt token: do not silently charge completion; Owner must decide whether prompt-only charge is allowed. Default plan: `charge_zero` until policy is approved.
- Upstream 5xx before first content: fail / no charge, but still write abort audit if claim acquired.
- Tx2 write failure: no successful upstream response may bypass durable settlement/audit.依据：`docs/specs/observability-billing.md:144`。
- Re-attempt after aborted claim: existing `ReReserveAbortedClaim` increments `attempt_seq`; tests must prove old failed usage row remains queryable and new attempt can settle without overwriting.依据：`backend/sql/queries/billing_claims.sql:56`。

## test plan (unit + integration; sub2api/new-api have reference but DO NOT read their source)

Unit tests:

- `backend/internal/gateway/forwarder_test.go`: add mapping tests from existing `EndClass` + token accumulator to `AttemptBillingState`, `FailedStreamTerminalClass`, `SettlementAction`.
- Add a pure test for client disconnect after first chunk: expect `partial_delivered`, `client_gone`, `charge_partial`, `pending_reconciliation` true when drain cannot obtain authoritative terminal usage.
- Add a pure test for timeout with zero output: expect `failed`, `upstream_timeout`, `charge_zero`.
- Add a pure test for typed upstream 5xx/RST after partial tokens: expect `partial_delivered`, `upstream_5xx`, not `ambiguous`.
- Add a pure test for zero-output failure where input tokens are present: ensure no completion charge and explicit `output_token_zero`.

Billing integration tests:

- Extend `backend/internal/billing/settler_integration_test.go` to assert new columns on committed partial settlement, aborted zero settlement, and retry after aborted claim.
- Add "upstream RST mid-stream writes usage row" integration scenario: seed acquired claim + pool slot, call `Settle` with partial draft, assert exactly one usage row, exactly one billing event, claim committed or aborted per Owner policy, slot released once.
- Add "zero output does not disappear" scenario: assert `usage_records` has zero output, terminal class, settlement action, and audit event payload.
- Add "attempt visibility" scenario: re-reserve aborted claim, settle second attempt, verify both attempt rows are queryable by `claim_id` and distinct `attempt_seq`.
- Add "wrong tenant cannot read/update failed-stream fields" regression, following existing cross-tenant abort test.依据：`backend/internal/billing/settler_integration_test.go:165`。

Admin API tests:

- Extend `backend/internal/gatewayhttp/admin_observability_handler_test.go` for filters: `terminal_class=client_gone`, `attempt_billing_state=partial_delivered`, `settlement_action=charge_partial`.
- Add response body test that usage items contain Owner reconciliation fields.
- Add audit event payload test that failed-stream billing details survive in `/admin/v1/audit-events`.

SQL / migration checks:

- Run `sqlc generate` after query changes.
- Run Go unit tests for `backend/internal/gateway`, `backend/internal/gatewayhttp`, `backend/internal/billing`.
- Run integration PostgreSQL tests behind existing `integration_pg` build tag when DB is available.
- Before commit, run `codex exec review --uncommitted --full-auto` per project discipline. This planning task does not commit.

Reference constraint:

- sub2api / new-api are only reputation / parity labels from HUAKAI docs in this plan. No source read, no upstream mechanism claim, no copied behavior details.

## time estimate

Plan-only current task: 45-75 minutes.

Future implementation estimate after Owner approves migration and charge policy:

- Schema + SQL + sqlc: 0.5-1 day.
- Forwarder classification and SettleRequest plumbing: 0.5-1 day.
- Billing settler + admin query updates: 1 day.
- Unit + integration tests: 1-1.5 days.
- Cross-review fixes: 0.5 day.

Total: 3-5 engineering days, assuming no major Executor / attempt-id migration conflict. If Owner requires `request_id / attempt_id / lease_id` migration in same slice, add 1-2 days.

## blast radius

- Money path: high. Touches `usage_records`, `billing_events`, `billing_ledger_claims`, Tx2 code, settlement tests.
- DB schema: high if migration accepted.
- Admin API contract: medium, additive fields and filters.
- Streaming hot path: medium, because `Forward` classification changes can affect all streaming providers.
- Retry / failover: medium, because `attempt_seq` and aborted re-reserve behavior become visible reconciliation state.
- Clean-room: low if implementation stays HUAKAI-internal and no reference source is read.
- Security: low-to-medium. No secrets involved, but audit payload must not include upstream credentials or raw response bodies.

## decision points (Owner sign-off triggers)

Owner must approve before implementation:

- Migration: additive columns to `usage_records` / `billing_events` and optional `billing_ledger_claims`.
- Charge/refund policy for `output_token_zero`: zero charge vs prompt-only charge vs manual review.
- Charge/refund policy for `client_gone`: charge delivered tokens even if user disconnected, or cap/refund by operator policy.
- Whether `upstream_5xx` after partial delivery commits as `committed + partial` or `aborted + zero-cost adjustment`. My recommendation: commit partial when tokens were delivered; use append-only adjustment for refunds.
- Whether F-OBS-003 must also introduce `request_id`, `attempt_id`, `lease_id`, or whether `claim_id + attempt_seq + provider_account_id` is acceptable for Phase 4.5.
- Whether terminal class belongs as first-class columns or only in `billing_events.payload`. My recommendation: first-class columns for filterability, plus JSON reason for detail.
- Whether dashboard/frontend work is part of this feature or a follow-up Gemini UI slice.

## clean-room: which upstream references inspire this (cite by reputation only, NOT by reading source)

- `new-api`: only named by HUAKAI parity matrix as the F-OBS-003 reference label. I did not read its source and make no claim about its code behavior.依据：`docs/03_FEATURE_PARITY_MATRIX.md:114`。
- `sub2api`: only appears as HUAKAI's broader product baseline and as a known clean-room risk category in project docs; this plan does not derive implementation from its source.依据：`docs/01_PROJECT_BRIEF.md:1`、`docs/10_RISK_REGISTER.md:19`。
- Implementation source of truth for this plan is HUAKAI internal specs and code: F-OBS-001 Tx2, F-GW-002 streaming forwarder, cross-module boundaries, and current Go backend.

No non-MIT reference project source was read. No upstream function names, struct fields, comments, file structures, or algorithms are used.

## sources read: list ALL files you opened

Manual files opened and used as plan evidence:

- `.agents/skills/pm-orchestrator/SKILL.md`
- `CLAUDE.md`
- `docs/01_PROJECT_BRIEF.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/10_RISK_REGISTER.md`
- `docs/12_AGENT_WORKFLOW.md`
- `docs/RULES.md`
- `docs/specs/observability-billing.md`
- `docs/specs/streaming-forwarder.md`
- `docs/specs/_invariants/cross-module-boundaries.md`
- `backend/internal/billing/billing.go`
- `backend/internal/billing/claim_gate.go`
- `backend/internal/billing/settler.go`
- `backend/internal/billing/settler_integration_test.go`
- `backend/internal/gateway/forwarder_types.go`
- `backend/internal/gateway/forwarder.go`
- `backend/internal/gateway/forwarder_test.go`
- `backend/internal/gatewayhttp/admin_observability_handler.go`
- `backend/internal/gatewayhttp/admin_observability_helpers.go`
- `backend/internal/gatewayhttp/admin_observability_handler_test.go`
- `backend/sql/queries/billing_claims.sql`
- `backend/sql/queries/billing_settle.sql`
- `backend/sql/queries/observability.sql`
- `backend/sql/migrations/0002_observability_billing.up.sql`

Additional `rg` / `find` scans were used only to locate HUAKAI backend paths; no reference-project source was read.

Source files read: listed above
Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-05-15T13:29:22Z

Owner 中文摘要：本计划只做 F-OBS-003 的 Go backend 实施方案，没有写实现代码；真实观察来自 HUAKAI 内部 docs/specs/backend 源码，合理推断是把现有 `end_class`/`usage_source` 扩展成独立的 failed-stream billing lifecycle 与 terminal class；open questions 共 6 个，主要集中在 migration、charge/refund policy、attempt_id/lease_id 是否同步落地。没有功能缩水，clean-room 风险低，因为未读 new-api/sub2api 等参考源码；安全风险主要是 billing ledger/schema 高风险，需要 Owner 在执行前确认。下一步建议：与 Claude 独立计划交叉讨论，合成无后缀 authoritative plan 后再执行。
