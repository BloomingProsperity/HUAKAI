# Other reference missed pass

Date: 2026-05-03

Scope: after the Sub2API feature pass, check the other reference projects for capabilities HUAKAI may still be treating too coarsely. This pass does not edit backend, admin, OpenAPI, or the main feature matrix.

## Reference versions

| Project | Branch | Commit | Tag | Files | Status |
| --- | --- | --- | --- | ---: | --- |
| CLIProxyAPI | `main` | `56df36895a0e` | `v6.10.1` | 524 | clean |
| one-api | `main` | `8df4a2670b98` | `8df4a26` | 564 | clean |
| New API | `main` | `dac55f0fdeb1` | `v1.0.0-rc.2` | 1907 | clean |
| LiteLLM | `litellm_internal_staging` | `c94a8d651493` | `1.84.0-dev.2-488-gc94a8d6514` | 6828 | clean |
| Portkey Gateway | `main` | `351692fd9236` | `351692fd` | 765 | clean |
| Helicone | `main` | `3f4bd44b85f9` | `deploy-20260502-004858` | 4820 | clean |
| Envoy AI Gateway | `main` | `d63a020f166b` | `v0.6.0-rc1` | 1202 | clean |
| All API Hub | `main` | `9f397c95c211` | `nightly-2-g9f397c95` | 2100 | clean |

## Main finding

Sub2API still defines the account-to-API core. The other projects do not replace that core; they add production mechanisms around it:

1. Router self-healing: channel testing, auto-disable/re-enable, cooldown, retry hierarchy.
2. Settlement correctness: pre-consume, post-settle, rollback, pricing-version pinning.
3. Observability: prompt/usage/cost/cache traces, request properties, OpenTelemetry-safe redaction.
4. Operator workflows: key repair, model sync, bulk channel assessment, encrypted config backup.
5. Declarative control: config schema, forbidden forwarded headers, policy-as-code routing.

## Fusion vs upgrade

Fusion means HUAKAI can absorb equivalent capabilities into its matrices. Upgrade means HUAKAI changes the core architecture so these capabilities become testable, auditable, and safer than the references.

The core upgrade is:

`API key binding -> account selection plan -> capacity lease -> credential lease -> protocol adapter -> credential injector -> error classifier -> request attempts -> usage settlement -> operator trace`.

The reference projects mostly implement pieces of that chain. HUAKAI should make the chain the product spine.

## Files

- `cliproxy-api.md`
- `one-api.md`
- `new-api.md`
- `litellm.md`
- `portkey-gateway.md`
- `helicone.md`
- `ai-gateway.md`
- `all-api-hub.md`
- `huakai-missed-insertions.md`

---
Source files read: index only (no inline source paths; individual project paths listed in per-file tails)
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
