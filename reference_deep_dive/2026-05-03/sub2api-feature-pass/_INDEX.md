# Sub2API account-to-API feature pass

Date: 2026-05-03

Reference repo: sub2api (local reference copy)

- Branch: `main`
- Commit: `48912014a16e2dd1cfca8b7cad785d0e8e7bfeec`
- Tag: `v0.1.121-1-g48912014`
- Tracked file count: 2058
- Reference status: clean except local untracked cache

## Scope

This is a clean-room behavior audit. It extracts operating mechanisms at the behavior-summary level and does not copy sub2api implementation structure.

### Evidence granularity (post 2026-05-06 scrub)

After the 2026-05-06 clean-room scrub (per Codex review), this pass operates at **behavior-summary granularity only**. Line-level source citations were stripped from per-feature files because they violated clean-room policy (inline upstream paths + verbatim schema field names).

Each per-feature file therefore provides:
- A 2-3 sentence behavior summary in the body — the value-add for HUAKAI design.
- A tail block listing the upstream files consulted (no line numbers).
- Lane / agent / UTC timestamp metadata.

Each per-feature file does NOT provide (intentional, per scrub):
- Inline `path:line` citations.
- `Observed regions` / `Inferences` / `Open questions` per-claim sub-sections.
- Verbatim schema field names or function names from upstream.

If a downstream design claim needs line-level audit, open a follow-up
"detail pass" under `reference_deep_dive/<later-date>/` that captures
those metadata sections per claim. The current pass is intentionally
coarse-grained as a navigation index, not a courtroom-grade evidence
trail.

### Tag legend (current)

- `behavior-confirmed`: matches a real operating mechanism in the upstream codebase as observed during the scrubbing pass; no line citations retained.
- `inferred`: derived from behavior-confirmed facts plus design reasoning.
- `open-question`: not fully verified at this granularity.

## Files

- `01-account-asset-model.md`
- `02-api-key-user-group-contract.md`
- `03-account-group-pool-routing.md`
- `04-concurrency-slot-wait-plan.md`
- `05-sticky-session-context.md`
- `06-retry-failover-account-switch.md`
- `07-rate-limit-cooldown-state.md`
- `08-credential-refresh-token-cache.md`
- `09-credential-injection-protocol.md`
- `10-model-routing-capability.md`
- `11-usage-billing-settlement.md`
- `12-payment-order-recovery-refund.md`
- `13-channel-monitor-healthcheck.md`
- `14-ops-admin-investigation.md`
- `15-async-workers-cleanup.md`
- `16-security-body-log-guards.md`
- `17-transport-proxy-tls-fingerprint.md`
- `huakai-gap-and-upgrade-plan.md`

## Main finding

`MISSED_BY_HUAKAI`: sub2api's core is not just reverse proxy. It is an account-to-API operating spine:

`API key -> user/group contract -> account pool -> account slot/wait plan -> credential lease/injection -> protocol adapter -> attempt audit -> usage/billing -> cooldown/ops trace`.

HUAKAI has partial pieces, but the plan still treats binding, concurrency, credential injection, sticky, retry, usage and ops trace as separate tracks. The upgrade is to make them one traceable and testable mainline before Slice 5 real upstream code grows handler-level hardcoding.

---
Source files read: sub2api repository index (no inline source paths)
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
