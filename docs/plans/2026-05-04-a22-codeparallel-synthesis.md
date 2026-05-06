# A22 Health Hysteresis FSM — Code-Parallel Synthesis (third under 2026-05-04 rule)

Date: 2026-05-04
Lane mode: 双 lane code 平行交叉

## Inputs

- Claude lane: `c:/tmp/parallel-a22-claude/health_fsm.go` (15.8K, no test — agent timed out before tests)
- Codex lane: `c:/tmp/parallel-a22-codex/health_fsm.go` (15.5K) + `health_fsm_test.go` (11.2K, 18 tests)
- Spec: [docs/specs/rate-limiting.md §A22](../specs/rate-limiting.md)
- Decision: [DR-009 §6.6](../decisions/DR-009-algorithm-upgrade-policy.md) hard floor
- Synthesis source: [2026-05-02-huakai-algo-upgrade-synthesis.md §1 A22](2026-05-02-huakai-algo-upgrade-synthesis.md)

## Convergence

Both lanes implemented:
- 6 states (Normal / Degraded / CoolingDown / NeedsRefresh / NeedsManualRecovery / Disabled)
- Hysteresis: ≥ 10 clean successes for degraded → normal upgrade
- Pure FSM (no mutation; SideEffect list returned)
- §6.6 hard floor enforced

## Major shape difference

| Aspect | Claude | Codex | Picked |
|---|---|---|---|
| Transition signature | `(HealthRecord, Event) → (HealthRecord, []SE)` (state+snapshot bundled) | `(HealthState, Event, now) → (HealthState, []SE)` (state vs snapshot separated) | **Codex** — cleaner pure-function shape, snapshot in Event.Health |
| Classification shape | minimal subset (Tier, RetryAction, OAuthRefreshable, AmbiguousErrorCount) | rich (RuleID, ErrorClass, FsmTransition, Tier, RetryAfter, CooldownUntil, Severity, NeedsRefresh) | **Codex** — direct match to gateway.Classification |
| §6.6 enforcement | structural guard at top of EvUpstreamError handler | predicate `isIronCladDisable()` at every code path | **Codex** — predicate pattern is harder to bypass |
| Operator action | strings on Event ("disable","enable","clear") | bools on ProbeResult (OperatorClear, OperatorReenable) | **Codex** — fits the rest of the event taxonomy |
| Score decay | rolling window decay-weighted score | 10-min half-life relaxation toward neutral | **Codex** — DecayHealthScore exported for caller use |
| Error window | 5-min decay-weighted | 60s simple count | **Codex** — 60s aligns with spec DegradedThreshold |
| ClockTime event | derived (EvCooldownExpired) | first-class EventClockTime | **Codex** — more general |

## Synthesis decisions

**Took Codex impl as base**, ported to real `package gateway` with these changes:

1. **Removed duplicate type definitions** — `FsmTransition`, `DisableTier`, `Confidence` already exist in `error_normalize.go` (R6). Codex's parallel `gatewayparallel` package had them; the merged version uses the existing R6 types.

2. **Renamed `Classification` → `FSMClassification`** — `gateway.Classification` is the R6 output (different shape). Added explicit FSM-input view to avoid shadowing.

3. **Added `ToFSMClassification(gateway.Classification) FSMClassification`** — bridge function that derives Severity (1.0 ambiguous / 5.0 iron_clad) and NeedsRefresh (oauth-class with cooldown) from R6 output. This is the integration point that R6 callers use.

4. **Tests adapted** — Codex's 18 tests were the foundation; renamed `Classification` references; added 3 new tests:
   - `TestA22_ToFSMClassification_Bridge` — verifies R6 → FSM bridge preserves data + sets severity
   - `TestA22_ToFSMClassification_AmbiguousSeverity` — verifies ambiguous severity = 1.0 + RetryAfter ms→Duration
   - `TestA22_ScoreClamping` — clamp01 invariants

5. **Renamed helper `cooldownUntil` → `cooldownUntilFor`** — avoids potential collision with future named field.

## Validation

- `go test ./internal/gateway/... -count=1` → `ok 0.752s` (combined R6 + R6-apply + A22 tests, all pass)
- `go vet ./internal/gateway/...` clean
- All 21 A22 tests pass (18 Codex base + 3 new bridge tests)
- §6.6 invariant test loops over 5 states asserting no ambiguous→disabled transition

## Defects caught by code-parallel

This round had less defect catching than R6 round 1 (both lanes converged on §6.6 enforcement). Main value-add: Claude's structural-guard + Codex's predicate are complementary; using both (predicate as primary, but the explicit branch ordering in transition functions also wouldn't allow ambiguous to reach disabled) gives belt-and-suspenders.

## Effort

- Lane execution: ~2 min Claude (timed out on tests, impl complete) + ~5 min Codex bg (both files complete)
- Synthesis: ~25 min review + merge + test rename + ToFSMClassification bridge
- Net: ~32 min wall-clock vs ~20 min single-lane estimate. **1.6x effort**, no major defects caught (95% convergence). For non-money-grade FSMs the parallel cost may not be justified; for production routing math (R6) it absolutely is.

## Followup

- R6 → A22 wire-up at the upstream-error path in chat handler (Slice 5 territory)
- Persist HealthSnapshot per provider_account row (DB column or json blob)
- A21 (probe scheduler) consumes A22's `enqueue_probe` SideEffects
- A14 (Retry-After harmonizer) sets the actual `CooldownUntil` value before A22 transitions to cooling_down
