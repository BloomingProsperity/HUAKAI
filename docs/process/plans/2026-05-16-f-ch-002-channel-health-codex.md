# 2026-05-16 F-CH-002 Channel Health Auto-Disable

| Owner directive | "你是 HUAKAI 项目 codex spec writer lane, 任务 = F-CH-002 channel health auto-disable spec (中性运营机制)." |
| --- | --- |
| Scope | In: `docs/specs/channel-health-auto-disable.md`, `docs/decompositions/_cross-cutting/channel-health.md`, F-CH-002 parity row, AT-CH-002 acceptance outlines. Out: backend code, schema migrations, OpenAPI runtime routes, dependencies, quota/billing/auth core, anti-detection runtime details. |
| Success criteria | F-CH-002 has a standalone neutral operations spec; boundaries against F-CRED-001, F-AUTH-005, PASR-lite, and failover are explicit; parity matrix marks it Mandatory Roadmap; `AT-CH-002-001..013` cover trigger, cooldown, ramp, failover, override, audit, and multi-vendor isolation; all claims are HUAKAI-internal or Owner-directed, with no reference-project source read. |
| Time estimate | 1-2 hours Codex wall-clock for this docs wave; 1-2 implementation days remain future work after Owner confirms schema/code changes. |
| Blast radius | Low to medium docs blast radius: feature planning, release gates, and future implementation contracts can be affected by wrong thresholds or module boundaries. No production behavior changes in this wave. |
| Failure modes | Over-specifying sensitive anti-detection mechanics; mixing acquisition/credential storage with health scoring; making auto-disable too aggressive and shrinking usable capacity; hiding alerts behind silent state flips; failing to preserve failover outcome. Mitigation: keep the spec vendor-neutral, state-machine based, alert/audit first, and record unknown threshold values as tenant policy defaults rather than hardcoded implementation. |
| Decision points | Owner must later confirm real threshold defaults, schema migration, admin override API/UI, and whether `ban_signal` policy is strict per tenant or global default. This wave does not require those decisions to file a Mandatory Roadmap spec. |
| Pre-execution checklist | 1. Do not read `sub2api/new-api/portkey/helicone/litellm/all-api-hub/envoy-ai-gateway` source. 2. Read only HUAKAI docs and skill instructions. 3. Preserve existing dirty backend/F-CRED worktree changes. 4. Use `Mandatory Roadmap` disposition and avoid invalid dispositions. 5. Keep R-E+1 anti-detection details out of scope. 6. End output with required source/lane/agent/UTC tail and a Chinese summary. |

## Concrete Execution Order

1. Add the standalone F-CH-002 spec with capability, actors, states, health dimensions, cooldown triggers, recovery ramp, failover interaction, admin controls, audit, metrics, and open questions.
2. Add cross-cutting decomposition that separates F-CH-002 from F-CRED-001, F-AUTH-005, PASR-lite selection, F-RATE-001, and F-TRUST audit.
3. Update `docs/03_FEATURE_PARITY_MATRIX.md` so the current F-CH-002 row reflects Owner's Round 3 Mandatory Roadmap framing and links the new spec/decomposition.
4. Append `AT-CH-002-001..013` to `docs/11_ACCEPTANCE_TEST_MATRIX.md` with normal/failure/operator recovery coverage.
5. Run targeted `rg` sanity checks for F-CH-002, forbidden source names in new files, and accidental backend/schema changes.

## Risk Notes

- Existing F-CH-002 row currently says target disposition `Implemented Better`; this wave will change the target disposition to `Mandatory Roadmap` per Owner instruction. The local capability text should preserve the stronger design intent inside the roadmap row so the feature is not functionally shrunk.
- Existing workspace already has unrelated backend/schema/OpenAPI changes. This wave must not stage, revert, or normalize them.

