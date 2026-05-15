# 2026-05-15 Mandatory Roadmap Priority Review (Codex)

| Field | Value |
| --- | --- |
| Lane | reviewer + scribe |
| Scope | Prioritize `Mandatory Roadmap` rows in `docs/03_FEATURE_PARITY_MATRIX.md`; documentation only |
| Clean-room boundary | Read HUAKAI internal docs only; did not read forbidden reference repos |
| UTC timestamp | 2026-05-15T12:07:16Z |

## 0. Input Count Check

Owner background says the matrix contains 24 Mandatory Roadmap items. I observed 19 real rows with `Disposition = Mandatory Roadmap`, plus one `TBD` template row that must not be counted. I did not invent the missing five. This discrepancy is the first Owner decision point below.

Scoring scale:

- Operational value: 1 = narrow nice-to-have, 5 = blocks billing, reliability, or commercial operation.
- Effort: 1 = small doc/test/spec patch, 5 = schema + runtime + UI + release testing.
- Dependency depth: 1 = can start from current contracts, 5 = waits on R-3/R-D/R-E, SaaS trigger, or legal gate.
- Phase fit: recorded as the closest phase from the matrix; Phase 4.5 is preserved where the matrix already says 4.5 because mapping it to Phase 6 would be false.

## 1. Top 5 Recommended Launch Order

1. **F-OBS-003 — 4-state failed-stream billing**

Start here because it is the smallest high-value correction to the billing and usage truth path. It extends the already-released F-OBS-001 Tx2 settlement semantics, makes refund / partial-charge decisions inspectable, and reduces the risk of building later async machinery on ambiguous terminal states. The implementation is not tiny because it touches schema-facing usage fields and tests, but it has lower dependency depth than the rest of the Phase 4.5 trio.

2. **F-OBS-004 — async processor chain with per-batch drain**

This should follow F-OBS-003 because Phase 4.5 exists specifically to close the async-task axis that the architecture document marks as 0% complete. The chain is the runtime spine for durable side effects; without it, DLQ, audit fanout, usage side effects, and alert delivery remain ad hoc. Its effort is higher than F-OBS-003, but its dependency is mostly internal once the settlement/event shape is settled.

3. **F-OBS-005 — DLQ, priority lanes, and dual-write control plane**

Launch after F-OBS-004, not before it. The operator value is very high because it decides whether failed async billing and audit work can be replayed without corrupting the ledger. It depends on the chain/idempotency model, so starting it first would create duplicate abstractions. The right order is design it together with F-OBS-004, but land it after the chain contract is stable.

4. **F-BILL-002 — voucher redemption and top-up audit**

After the Phase 4.5 reliability spine, the next practical commercial item is voucher redemption because it gives the Owner a controlled money-adjacent workflow without needing the full invitation/referral growth loop first. It still needs ledger, expiry, one-time redemption, cap, and audit tests, so it is not a trivial UI feature. Its value is direct: operators can sell or grant balance with an auditable path.

5. **F-AUTH-006 — OAuth bootstrap commercial blocker**

This is the highest-value commercial blocker, but it should be started as a legal/spec/acceptance-test workstream, not as implementation before R-3/R-D/R-E gates are settled. The matrix marks it as Phase 6 and L0 commercial blocker; it also carries ToS and client-identity mimicry risk. Starting now is valuable only if Owner explicitly accepts a gated plan: legal decision first, R-D/R-E capture/mainline boundary second, implementation last.

## 2. Full Score Table

| Feature | Operational value | Effort | Dependency depth | Phase fit |
| --- | --- | --- | --- | --- |
| F-OBS-003 | 5 — Billing correctness and operator reconciliation depend on knowing whether a stream ended by client disconnect, upstream timeout, zero output, or upstream 5xx. | 3 — It mainly extends F-OBS-001 settlement and usage tests, but likely needs a backwards-compatible usage-field migration. | 2 — It depends on F-OBS-001 Tx2 semantics, which are already released, and does not wait on R-E. | Phase 4.5 — The matrix explicitly places it in the async/failed-stream expansion before Phase 6. |
| F-OBS-004 | 4 — Durable async side effects reduce operational blind spots across usage, audit, and alert paths. | 4 — A 14-slot chain, idempotency keys, batch drain boundaries, and tests make this a shared runtime component. | 3 — It should follow F-OBS-003 event semantics and precede F-OBS-005 replay mechanics. | Phase 4.5 — The delivery plan names it as part of the axis 5 async backbone. |
| F-OBS-005 | 5 — DLQ, priority lanes, and replay are what prevent failed billing/audit side effects from becoming unrecoverable operator incidents. | 4 — Replay idempotency, starvation prevention, and main/backup divergence checks require broad failure-path tests. | 4 — It depends on F-OBS-004 chain boundaries and F-OBS-001 idempotency keys being stable. | Phase 4.5 — It is listed as the third async-backbone deliverable and should land after the chain. |
| F-BILL-002 | 4 — Voucher redemption directly supports controlled balance top-up and manual commercial operations. | 3 — Entity, one-time redemption, expiry, cap, and audit tests are meaningful but narrower than full payment orchestration. | 3 — It should align with billing ledger direction and F-PAY-001, but does not require R-E. | Phase 6 — The matrix labels it Phase 6+ and it fits usage/quota/billing preparation. |
| F-AUTH-006 | 5 — The matrix calls OAuth bootstrap a commercial blocker for turning upstream subscriptions into usable provider credentials. | 5 — It spans bootstrap endpoints, token-window state, client-identity policy, audit, tests, and legal gating. | 5 — It should wait for R-3/R-D/R-E transport and capture gates before any real upstream implementation. | Phase 6 with gate — Phase 6 is the business target, but execution must be gated by R-E/legal decisions. |
| F-COMM-001 | 4 — Invitation/referral helps growth and SaaS commercialization but is less foundational than balance issuance. | 4 — It needs schema, anti-abuse, credit/commission events, UI, and billing linkage. | 4 — It shares the F-PAY-001 / billing ledger surface and should not precede voucher/ledger decisions. | Phase 6 — The matrix says Phase 6+ commercial and first-class schema, not plugin. |
| F-CACHE-001 | 4 — Simple cache can reduce cost and latency quickly, while semantic cache improves later parity. | 3 — TTL, invalidation, keying, and cache-hit billing/tests are moderate for simple cache but heavier for semantic cache. | 2 — Simple cache can start without R-E; semantic cache should wait for model/capability maturity. | Phase 6 / L2 simple — The row says L2 simple and L3 semantic, so split delivery is appropriate. |
| F-UI-001 | 3 — Branding/onboarding improves operator launch readiness but does not unblock core routing or billing. | 3 — It needs UI, sandboxed iframe policy, settings persistence, and abuse controls for initial balance. | 3 — It should wait for Admin Lite settings patterns and identity-verification policy, but not R-E. | Phase 7 — The row already assigns it to ship-quality admin/onboarding UI. |
| F-I18N-001 | 3 — Five-language native UI improves market reach and support quality, but can follow the first admin workflows. | 3 — Glossary lock, extraction, locale QA, and drift control are sustained UI/process work. | 2 — It can start with frontend infrastructure and glossary work without backend R-E. | Phase 7 — The matrix says L1 ships English + Simplified Chinese only, with 5+ languages at Phase 7+. |
| F-OPS-003 | 4 — A multi-source operator dashboard directly addresses daily balance, usage, health, and price comparison operations. | 4 — It aggregates credentials-sensitive data, health, usage, and UI workflows across sources. | 3 — It needs admin API/UI foundations and credential redaction policies, but not R-E as a hard blocker. | Phase 7 — This belongs with Admin Lite and operations dashboard maturity. |
| F-SEC-003 | 4 — Signed images and SBOM are production/SaaS trust gates and reduce supply-chain uncertainty. | 3 — Signing, SBOM generation, verification docs, and CI checks are bounded but release-critical. | 4 — Meaningful artifact signing should wait until R-E decides final Rust/Go artifact composition. | Phase 8 — It is production hardening and explicitly required for SaaS Edition. |
| F-OPS-002 | 3 — In-dashboard upgrade improves operator ergonomics, but rollback mistakes are dangerous. | 5 — Secure update, rollback, audit, artifact verification, UI, and failure recovery make this high complexity. | 5 — It depends on signed artifacts, deployment packaging, and R-E final artifact boundaries. | Phase 8 — It should follow F-SEC-003 and deployment packaging rather than lead them. |
| F-DEPLOY-001 | 4 — A single artifact matrix prevents managed/self-host/local drift and simplifies operations. | 5 — Multi-target build, feature-flag matrix, tests, and docs touch release engineering broadly. | 5 — It should wait for R-E mainline because Rust data-plane packaging changes the artifact shape. | Phase 8 — The row marks Phase 8+ and it is production-hardening packaging work. |
| F-DEPLOY-002 | 3 — K8s/operator deployment matters for enterprise parity but can exclude simpler self-hosters if rushed. | 5 — CRD/operator deployment adds deployment, reconciliation, docs, tests, and support burden. | 5 — It should wait for R-E mainline and final topology decisions. | Phase 8+ / SaaS-adjacent — The matrix says Phase 8+, while architecture notes K8s blueprint as later SaaS packaging. |
| F-MM-001 | 4 — Multi-modal normalization expands product breadth but must not overclaim provider capability. | 5 — Text, vision, audio, image generation, response shapes, provider variance, and tests are broad. | 5 — It needs capability matrix maturity and likely R-E/protocol adapter stability. | Phase 9 — The matrix explicitly says Phase 9+ and requires per-model capability matrix before claim. |
| F-RT-001 | 4 — Realtime WebSocket support is high user value for advanced clients but introduces settlement and resume complexity. | 5 — WebSocket protocol, resume, partial usage, streaming tests, and incident recovery are major work. | 5 — It should wait for stream settlement, async backbone, and data-plane maturity. | Phase 9 — The matrix explicitly assigns it to Phase 9+. |
| F-MODEL-002 | 3 — Native rerank unlocks an additional model surface, but it is less central than chat/billing reliability. | 4 — Dedicated API surface and response-shape compatibility tests are nontrivial but bounded. | 4 — It depends on model registry/provider capability matrix and protocol adapter maturity. | Phase 9 — The row says Phase 9+ and should not preempt core catalog/routing work. |
| F-CRED-001 | 4 — Enterprise credential providers and pre-rotation are valuable for SaaS tenants with external identity/cloud trust chains. | 5 — OIDC/cloud STS, pre-rotation windows, failure handling, UI/API, and security tests make it large. | 5 — It depends on SaaS enterprise boundaries, F-AUTH-005/F-AUTH-006 separation, and likely R-E. | Phase 9+ / L4 — The feature-level matrix labels it L4 Better Than Reference. |
| F-ARCH-001 | 3 — Optional two-tier topology is valuable for enterprise scale but adds major operational complexity. | 5 — Outer/inner tiering changes deployment, routing, auth, limits, observability, and rollback. | 5 — It should wait for SaaS trigger, R-E mainline, and deployment topology decisions. | L4 SaaS / Phase 10+ — The matrix says Personal stays single-tier and SaaS unlocks this later. |

## 3. Three Readiness Buckets

### A. 立刻可启动

Count: 9.

These can start as spec/test/planning or bounded implementation work without waiting for R-3 R-E:

| Feature | Start shape |
| --- | --- |
| F-OBS-003 | Implement/spec failed-stream terminal class and settlement tests first. |
| F-OBS-004 | Draft async chain contract in parallel with F-OBS-003, land after terminal semantics. |
| F-OBS-005 | Design with F-OBS-004, land after chain/idempotency contract. |
| F-CACHE-001 | Split simple TTL cache now; semantic cache remains later. |
| F-BILL-002 | Start voucher ledger/audit spec and acceptance tests. |
| F-COMM-001 | Start only after Owner chooses commercial ledger order; do not implement before anti-abuse/ledger design. |
| F-UI-001 | Start UI assumptions and sandbox/security requirements. |
| F-I18N-001 | Start glossary and locale infrastructure with frontend owners. |
| F-OPS-003 | Start dashboard information architecture and redaction rules. |

### B. 等待 R-3 R-E 完成

Count: 5.

These should not become production implementation before R-E mainline/capture/deployment boundaries are settled:

| Feature | Why it waits |
| --- | --- |
| F-AUTH-006 | Bootstrap/client-identity risk intersects transport mimicry, Owner real capture, and legal/ToS gates. |
| F-SEC-003 | Signed images/SBOM should describe the final Go + Rust artifact set after R-E. |
| F-OPS-002 | Self-upgrade must verify and roll back the final packaged artifact, so it depends on F-SEC-003 and R-E packaging. |
| F-DEPLOY-001 | Single artifact strategy changes once Rust data plane moves into mainline. |
| F-DEPLOY-002 | K8s/operator packaging should target the final topology, not the exploratory Rust layout. |

### C. Phase 9+ 远景

Count: 5.

These remain preserved roadmap items, but starting them now would distract from L2/L3 reliability and Phase 6 commercial basics:

| Feature | Deferral reason |
| --- | --- |
| F-MM-001 | Needs broad provider capability matrix and multi-modal adapter contracts. |
| F-RT-001 | Needs mature streaming settlement, resume, and async failure handling. |
| F-MODEL-002 | Needs model registry/capability surface maturity before a native rerank API is credible. |
| F-CRED-001 | Enterprise STS/IdP pre-rotation is L4 SaaS and depends on tenant/security foundations. |
| F-ARCH-001 | Two-tier SaaS topology should wait for explicit SaaS Edition trigger and R-E packaging. |

## 4. Owner Decision Points

1. **Row-count reconciliation**: accept this triage as covering the 19 real Mandatory Roadmap rows currently in `docs/03_FEATURE_PARITY_MATRIX.md`, or ask PM to locate/add the missing five rows implied by the 24-item background.
2. **Next-slice priority**: choose whether Phase 4.5 reliability/settlement work (F-OBS-003/004/005) preempts Phase 6 commercial work, or whether a commercial item should run in parallel.
3. **Commercial order**: choose whether voucher redemption (F-BILL-002) is the first Phase 6 monetization work, or whether OAuth bootstrap (F-AUTH-006) gets a legal/spec-only start first despite its R-E dependency.
4. **F-AUTH-006 legal gate**: decide whether to authorize a legal/ToS decision artifact for OAuth bootstrap and client-identity policy before any implementation plan.
5. **R-E-dependent hardening bundle**: decide whether F-SEC-003, F-OPS-002, F-DEPLOY-001, and F-DEPLOY-002 should be planned as one post-R-E release-hardening bundle or split into separate smaller work units.

## 5. Scribe Notes

- No Mandatory Roadmap item is dropped or downgraded.
- No risk register changes were made.
- No implementation work was done.
- L2-A5 closure belongs on the F-AUTH-005 status row because that row is the matrix home for provider-side credential management and mimicry policy; this does not mark R-D or R-E complete.

Source files read:

- `.agents/skills/pm-orchestrator/SKILL.md`
- `.agents/skills/feature-parity-auditor/SKILL.md`
- `docs/RULES.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/16_PHASED_DELIVERY_PLAN.md`
- `docs/17_FEATURE_LEVEL_MATRIX.md`
- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` (search snippets)
- `docs/plans/2026-05-14-r3-on-merged-closure-codex.md`
- `docs/plans/2026-05-15-r-c-lane-2-architecture-codex.md`
- `docs/reviews/2026-05-15-l2-lane2-retrospective-bulk-codex-review.md`
- `docs/reviews/2026-05-15-l2-a5-4-retrospective-codex-review.md` (search snippets)
- `docs/reviews/2026-05-15-l2-a5-5-codex-review.md`
- `docs/specs/upstream-credential-management.md` (search snippets)
- `docs/specs/api-contract.md` (search snippets)
- `docs/specs/_invariants/cross-module-boundaries.md` (search snippets)
- `docs/decompositions/_cross-cutting/auth-token-synthesis.md` (search snippets)
- `docs/decompositions/sub2api/auth-token-source-verified.md` (search snippets)
- `docs/plans/2026-05-14-rust-contract-fix-codex.md` (search snippets)
- `docs/plans/2026-05-14-r3-phase-cde-closure-codex.md` (search snippets)
- `docs/plans/2026-05-15-l2-a5-1-openssl-profile-codex.md` (search snippets)
- `docs/plans/2026-05-15-l2-a5-2-openssl-groups-sigalgs-codex.md` (search snippets)
- `docs/plans/2026-05-15-l2-a5-3-ec-point-formats-codex.md` (search snippets)
- `docs/plans/2026-05-15-l2-a5-4-extension-22-codex.md` (search snippets)
- `docs/plans/2026-05-15-l2-a5-5-extension-list-codex.md` (search snippets)

Lane: reviewer + scribe

Agent: Codex GPT-5

UTC timestamp: 2026-05-15T12:07:16Z

