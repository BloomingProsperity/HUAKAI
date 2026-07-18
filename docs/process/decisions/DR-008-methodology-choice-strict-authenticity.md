# DR-008: Methodology Choice — Strict Authenticity Over Speed

| Field | Value |
| --- | --- |
| Status | 历史决策；具体执行流程已被 2026-07-18 最新 Owner 指令替代 |
| Date opened | 2026-04-28 |
| Date decided | 2026-04-28 |
| Owner | Owner |
| Affected docs | 旧总纲已删除；当前只影响 truth-first、源码证据和 release gate，不再恢复双计划或固定模型角色。 |
| Supersedes | — |
| Superseded by | [`AGENTS.md`](../../../AGENTS.md) 的当前单执行者、唯一计划和独立只读 review 合同 |

## Question

Given Owner's "必须真实，慢无所谓" rule and the honest progress estimate of ~250–500 focused engineering hours to a Model-1-commercializable product, which methodology does HUAKAI execute?

当时旧总纲提出三个候选；旧总纲已删除，原文可从 Git 历史追溯：

- **A — Strict**: every L1/L2 feature passes deep decomposition + mutual review + reviewer-lane sign-off + spec release before implementation. Slow but authentic.
- **B — Partial strict**: 3–5 Money-grade core algorithms (Quota+Billing claim gate, Pool selection, streaming forwarder) go strict; outer features allow experiential / shorter-cycle decomposition.
- **C — Accelerate**: existing inventory + partial decomposition is treated as sufficient; jump to Phase 2 contract lock and Phase 3 implementation now.

## Owner Decision

| Field | Value |
| --- | --- |
| Decision | **A — Strict.** |
| Decision date | 2026-04-28 |
| Reasoning | Owner directive 2026-04-28: "A". Consistent with prior Owner directives "必须真实, 慢无所谓" and "必须每个项目每个功能单独的拆解，学习，优化". Money-grade correctness is non-negotiable for Model 1 commercial launch ([DR-002 §Owner Refinement](DR-002-product-editions.md)). Authenticity over speed is the project identity. |
| Constraints attached | (binding constraints below) |

### Binding Constraints

1. **No L1 feature ships before its spec is released.** Every L1 row in [03_FEATURE_PARITY_MATRIX.md](../../03_FEATURE_PARITY_MATRIX.md) requires a `Released` prose decomposition under [decompositions/](../../decompositions/) AND, for Option C carve-out features, a `Released` strict spec under [specs/](../../specs/). No Phase 4+ implementation work begins on a feature whose spec is `Draft`.
2. **No L2 feature ships before its spec is released.** Same rule for L2 rows. Phase 5–9 implementation gates on spec release.
3. **Mutual review is mandatory, not optional.** Every prose decomposition that drives an Option C strict spec must be authored independently by Claude AND Codex; both passes saved; cross-review filed; synthesis written; reviewer-lane sign-off by a third agent session before `Released`.
4. **Reviewer-lane = different agent session** from both authors. Reviewer is responsible for CL-001..010 sign-off (see [_REVIEW_CHECKLIST.md](../../specs/_REVIEW_CHECKLIST.md)).
5. **Phase 2 contract lock cannot begin** until Phase 1.2 (deep decompositions) is complete for every L1+L2 feature and Phase 1.3 (mutual review on Money-grade core) is complete for at least the carve-out areas (Pool selection / billing reconciliation / Provider health).
6. **Time expectation: 250–500 focused engineering hours** to Model-1-commercializable. Schedule slip is preferred over scope cut. If schedule pressure rises, the answer is to apply Codex parallelism, not to compromise authenticity.

## Implications That Flow Immediately

- **历史 Phase 1.2 priority queue**：Quota+Billing claim gate v2 fixes → Pool selection strict spec → streaming forwarder spec → typed failure taxonomy spec。该队列不再是当前执行计划；当前目标只服从仓内唯一最新计划。
- **No Phase 2 architecture documents** (data model field-level lock, API endpoint-level OpenAPI lock, UI screen-level wireframe lock) until Phase 1.2/1.3 are complete on the core.
- **No Phase 3 skeleton code** even at "scaffolding" level. The temptation to "just write the Go module structure now" is rejected; Phase 3 starts only after Phase 2 contracts are locked.
- **Reference Tracking Continuous Gate ([15](../../15_RELEASE_GATES.md))** runs in parallel from now on; baseline files for all 8 references must be captured before Phase 2 entry.

## Propagation Checklist

- [x] 旧总纲已被删除；本 DR 保留“真实优先”的历史证据，不再承担当前计划入口职责。
- [ ] Update [22_DEEP_MINING_MANDATE.md](../../22_DEEP_MINING_MANDATE.md) — strict mode is now the project default; remove any "L3/L4 may be shallow" loophole that conflicts with Owner Decision A (note: L3/L4 shallow is still allowed UNTIL the feature is promoted to active scope, at which point it must reach `Released` decomposition).
- [ ] Update [16_PHASED_DELIVERY_PLAN.md](../../16_PHASED_DELIVERY_PLAN.md) — Phase 2/3 entry criteria gated on Phase 1.2/1.3 completion.
- [ ] Update [15_RELEASE_GATES.md](../../15_RELEASE_GATES.md) — Strict Authenticity Gate codified.
- [ ] Mark Status = Implemented when all above are done.
