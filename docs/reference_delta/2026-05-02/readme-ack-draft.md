# README "Reference Projects & Usage Acknowledgement" — draft

Date: 2026-05-02
Status: draft for Owner review. Not promoted into the actual `README.md` yet.

This file is the proposed section to insert into the project root `README.md`. Owner originally asked on 2026-05-02 ("只需要在 readme 里更新清楚使用情况说明"). Once Owner approves wording, the section can be appended to `README.md` as a new top-level section.

---

```markdown
## Reference Projects & Usage Acknowledgement

HUAKAI is built clean-room — no upstream source code, schemas, file paths,
struct fields, or distinctive comments are copied. Behavior, capability ideas,
operational lessons, and algorithmic shape are derived through documented
"specifier-lane" reading sessions and recorded with `<file>:<line>` evidence
in [docs/07_REFERENCE_EVIDENCE_LEDGER.md](docs/07_REFERENCE_EVIDENCE_LEDGER.md).

The following projects were studied. Their licenses, what we observed, and
what we deliberately did not take are listed below. Per-row evidence is
durable in the ledger and the reference deep-dive workspace at
[reference_deep_dive/2026-05-02/](reference_deep_dive/2026-05-02/) and
[docs/reference_delta/2026-05-02/](docs/reference_delta/2026-05-02/).

| Project | License | We read for | We did NOT take |
| --- | --- | --- | --- |
| [songquanpeng/one-api](https://github.com/songquanpeng/one-api) | MIT | Channel CRUD shape, group multipliers, per-key quota lifecycle, voucher/redemption workflow, dashboard structure, gzip middleware (negative example), panic recovery (negative example) | Source code, schema, hardcoded multipliers, default credentials, raw body in panic logs |
| [BerriAI/litellm](https://github.com/BerriAI/litellm) | MIT (excluding `enterprise/`) | Hierarchical budget scopes (org/team/user/key/model), retry/fallback policy hierarchy, cooldown-aware deployment selection, cache admin + analytics, guardrail lifecycle registry, batch endpoints | Source code, schema (Prisma), enterprise tier code |
| [Portkey-AI/gateway](https://github.com/Portkey-AI/gateway) | MIT | Declarative target strategy validation, retry budget with Retry-After awareness, fallback stop conditions, hooks/guardrails pipeline, SSRF-safe custom upstream validation, response debug headers | Source code, plugin execution structure, header/config schema |
| [envoyproxy/ai-gateway](https://github.com/envoyproxy/ai-gateway) | Apache-2.0 | AIGatewayRoute CRD shape, QuotaPolicy with cache-token cost expressions, body mutation contract, GenAI OTel metrics naming | Source code, CRD definitions |
| [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) | LGPL-3.0 | Multi-account pool concept, sticky-session affinity, per-Account concurrency budgets, channel-monitor probe taxonomy with daily rollup, payment / redemption / promo / affiliate flow shape, idempotent billing claim gate, OAuth refresh storm controls, error-passthrough rule shape, account-change outbox + scheduler snapshot refresh, payment recovery / refund state machine with provider pinning, TLS fingerprint plugin, upstream HTTP/SOCKS proxy, TOTP 2FA, user custom attributes, pending auth multi-step state machine, identity adoption decision, setup wizard, backup/restore | Source code, ent schemas (Go entgo), migration SQL, frontend Vue components, function names, distinctive variable names, exact scheduler weighting formula |
| [Helicone/ai-gateway](https://github.com/Helicone/ai-gateway) | GPL-3.0-or-later | Request explorer / investigation API, ClickHouse retention model with body TTL, user-facing request/cost rate limits with segment scoping, wallet escrow/reserve/finalize/cancel, Stripe webhook idempotency + dispute handling, body retention + redaction, AI gateway disallow-list pattern | Source code; HUAKAI does not link any GPL code into its binary; behavior derived from observation only |
| [QuantumNous/new-api](https://github.com/QuantumNous/new-api) | AGPL-3.0-or-later | Guarded request decompression with body storage tier (memory→disk), billing session with funding-source preference (wallet/subscription), versioned pricing expression with frozen snapshot, channel affinity routing with cache invalidation, top-up + Stripe + EasyPay state machines | Source code, schema, frontend code, expression DSL syntax. HUAKAI's pricing engine is independent. |
| [qixing-jk/all-api-hub](https://github.com/qixing-jk/all-api-hub) | AGPL-3.0 | Operator workflow ergonomics: external account telemetry profile, account duplicate detection, selected-row execution + retry-failed pattern, non-mutating preview before bulk write, daily-bonus check-in scheduler skip-reason taxonomy, encrypted selective WebDAV sync | Source code, browser-extension architecture, browser-local secret custody (this is an explicit anti-pattern HUAKAI rejects) |

**License posture**:
- HUAKAI's distribution license is `MIT` (see [LICENSE](LICENSE)).
- No GPL/AGPL/LGPL upstream code is linked into the binary. Behavior derived
  from those projects is implemented from scratch using the evidence ledger
  as a behavior contract.
- All authors above retain copyright in their own projects. HUAKAI claims no
  authorship over upstream work; this document acknowledges the reading.

**Verification**:
- License of each project is recorded as `E-LIC-NNN` rows in
  [docs/07_REFERENCE_EVIDENCE_LEDGER.md](docs/07_REFERENCE_EVIDENCE_LEDGER.md).
- Every behavior we adopted is recorded as a separate evidence row
  (`E-OAI-*`, `E-LM-*`, `E-PK-*`, `E-EAG-*`, `E-S2A-*`, `E-HLC-*`, `E-NAI-*`,
  `E-AAH-*`).
- The reference repository sources are pinned by commit hash in the
  delta workspace at
  [docs/reference_delta/2026-05-02/_INDEX.md §"Reference snapshots"](docs/reference_delta/2026-05-02/_INDEX.md)
  (Codex specifier-lane first pass; both `_INDEX.md` files exist —
  the per-repo commit hashes live in the `docs/reference_delta/`
  one, not in `reference_deep_dive/`).

**Clean-room workflow**:
- Specifier sessions read source and write behavior summaries with file:line
  evidence.
- Reviewer sessions verify the specifier output without re-reading source in
  the same agent session.
- Both sessions are recorded in
  [docs/05_CLEAN_ROOM_POLICY.md](docs/05_CLEAN_ROOM_POLICY.md) and the
  per-session "Source files read" footer.
```

---

## Reviewer notes on this draft

- The "We did NOT take" column is intentionally specific (not "no source") because Owner's directive on 2026-05-02 was to be **transparent**, not paranoid. Each row names the artifact class we did not adopt.
- The Helicone GPL row explicitly states HUAKAI does not link GPL code into the binary. This is the load-bearing legal sentence.
- The all-api-hub anti-pattern (browser-local secrets) is called out by name. This is for users who might assume HUAKAI mirrors all-api-hub's storage model.
- Owner can also opt to drop the "We did NOT take" column entirely if they prefer a leaner README. The information lives in the ledger anyway.
- If Owner adds the 3 newly discovered projects (CLIProxyAPI / OmniRoute / Bifrost) to the reference set, the table extends easily.

## Next step after Owner approval

1. Append the section between triple-backtick markers above into `README.md` (existing README structure not changed otherwise).
2. Verify all `[link]` references resolve (some currently point to files not yet committed: `docs/05_CLEAN_ROOM_POLICY.md`, the per-evidence ledger, etc.).
3. Move this draft file out of `docs/reference_delta/` since it is no longer pending Owner review.
