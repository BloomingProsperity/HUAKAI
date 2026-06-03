# 2026-06-03 models pricing discovery codex plan

| Field | Plan |
| --- | --- |
| Owner directive | "实现+验证。先读 CLAUDE.md + AGENTS.md。设计依据:读 /home/ubuntu/.claude/plans/huakai-plan-ticklish-snowglobe.md" |
| Scope | Enrich `GET /v1/models` with optional public `pricing` and `context_length`; carry `canonical_id` through registry list projection; wire the handler to the existing public rate table source; optionally defer `/v1/models/{id}` if the plan keeps it out of the minimal slice. Wiring may touch existing `cmd/gateway` files to keep the rate-table source concrete enough for the new read-only pricing interface. |
| Out of scope | No schema migration, no billing ledger writes, no quota/auth changes, no internal cost/upstream/markup/cache price disclosure, no new files in frozen `internal/{gatewayhttp,gateway,proto}` packages. |
| Success criteria | Priced models include `pricing.input_per_token`, `pricing.output_per_token`, and `context_length`; unpriced/no-context models omit those fields and still return 200; pricing lookup uses public customer price rows only and canonical id candidates; requested gate commands pass or blockers are reported truthfully. |
| Time estimate | 1-2 hours wall clock for local implementation, test red/green, and verification. |
| Blast radius | Read-only model discovery endpoint and registry list projection; `cmd/gateway/routes.go`, `cmd/gateway/wiring.go`, and `cmd/gateway/middleware.go` DI/type touches edit existing files only. |
| Failure modes | Accidentally exposing internal cost/multiplier/cache fields; losing display-alias pricing because canonical id is not projected; breaking registry UNION column shape; degrading model list if pricing lookup errors; overwriting concurrent edits. Mitigation: discriminating tests, public-only SQL filter, omitempty wire fields, coordination lock. |
| Decision points | High-risk changes such as migrations, billing ledger/quota/auth modifications, new runtime dependencies, or real secret handling require Owner confirmation. None are planned. |
| Pre-execution checklist | Read `CLAUDE.md`, `AGENTS.md`, `.coordination/README.md`, and PM plan; check/claim edit locks; inspect current `modelhttp`, `registry`, `billing`, and `routes` code; write failing tests before production code; run targeted red/green tests and final gate. |
| Concrete execution order | 1. Add discriminating modelhttp, billing, and registry tests. 2. Run targeted tests to verify RED. 3. Implement registry projection, billing public price table, handler enrichment, route wiring, and the minimal concrete source type adjustment needed for DI. 4. Run targeted tests, then requested build/vet/test gate. 5. Release coordination lock and report files, source lines, mutation evidence, gate, risks, and blockers in Chinese. |
