# 2026-06-03 billingdsl settle codex

| Owner directive | "Wire billingdsl into the settle cost path as an OPT-IN, DATA-GATED tiered override that coexists with flat, fail-soft to flat." |
| Scope | In: backend settle pricing path, new `backend/internal/pricingeval` package, nullable `usage_records.cost_snapshot`, sqlc query regeneration, focused unit tests. Out: runtime flags, payment/auth/quota schema redesign, non-MIT reference source reads/copying, commits. |
| Success criteria | Flat `pricing_data` keeps existing costs byte-identical; tiered `pricing_data` spanning tiers charges tiered; invalid tiered data falls back to flat and emits an observability signal; non-streaming and streaming usage drafts carry the pricing model snapshot; requested build/vet/test gate passes locally. |
| Time estimate | 1-2 hours wall clock; one Codex implementation session. |
| Blast radius | Money-path cost computation, `usage_records` insert/replay payloads, generated sqlc params, gatewayhttp settlement drafts. |
| Failure modes | Precision drift: reuse decimal-only math and parity tests. Silent zero/failed settle on tier errors: fail-soft resolver with fallback metric/log. Frozen package violation: no new files under `internal/gatewayhttp`, `internal/gateway`, or `internal/proto`; only edit existing files. Migration incompatibility: nullable column with down migration. Weak tests: fixtures assert broken flat/tier/fallback/snapshot paths differ. |
| Decision points | Owner/PM deep-review before land because this changes real charging when tiered data is configured; Owner Docker integration gate required for PostgreSQL settle behavior. |
| Pre-execution checklist | Read `AGENTS.md` and root `CLAUDE.md`; note `backend/CLAUDE.md` is absent; re-confirm pricing seam; inspect billingdsl API; coordinate edits via `.coordination`; write failing tests before implementation; verify with requested commands. |

## Reference Scope

Default mirrors per project rule: CLIProxyAPI, sub2api, new-api. This execution does not read or paraphrase non-MIT reference source and makes no new upstream behavior claims; it implements the Owner-approved HUAKAI-internal design against local code only.

## Target Files And Package Discipline

- New non-frozen package: `backend/internal/pricingeval`.
- Existing frozen-package edits only: `backend/internal/gatewayhttp/chat_completions_pricing.go`, `backend/internal/gatewayhttp/chat_completions_billing.go`, `backend/internal/gatewayhttp/chat_completions_stream.go`, `backend/internal/gateway/forwarder_types.go`, plus existing tests.
- SQL/migration: `backend/sql/migrations/0083_usage_records_cost_snapshot.*.sql`, `backend/sql/queries/billing_settle.sql`, generated `backend/internal/db/billing/billing_settle.sql.go`.
- Settler/DLQ support: existing `backend/internal/billing/settler.go`, `backend/internal/dlq/handlers.go`.

## Concrete Execution Order

1. Add failing resolver tests for tiered override, flat no-op, fail-soft fallback signal, and model snapshot output.
2. Add the `pricingeval` resolver using decimal-only flat math and billingdsl for gated tiered data.
3. Wire gatewayhttp completion pricing to pass per-model raw JSON, usage, flat fallback, and billing policy version into the resolver.
4. Carry `cost_snapshot` through non-streaming and streaming usage drafts into `UsageRecordDraft`.
5. Add nullable migration and update sqlc insert query/generated code plus settler/DLQ payload mapping.
6. Add focused gatewayhttp/billing tests for draft population and usage insert parameter mapping.
7. Run requested verification and report local unit/build limits versus Owner PostgreSQL integration gate.
