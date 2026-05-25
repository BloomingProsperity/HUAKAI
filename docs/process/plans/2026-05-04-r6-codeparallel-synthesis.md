# R6 Error Normalization — Code-Parallel Synthesis (first under 2026-05-04 rule expansion)

Date: 2026-05-04
Lane mode: 双 lane code 平行交叉 (CLAUDE.md #10 + 2026-05-04 Owner directive "所有功能包括代码都要进行平行交叉法" → first validation)

## Inputs

- Claude lane: `c:/tmp/parallel-r6-claude/error_normalize.go` + tests (14 tests pass, gatewayparallel pkg)
- Codex lane: `c:/tmp/parallel-r6-codex/error_normalize.go` + tests (16+3 tests pass, gatewayparallel pkg)
- Spec: [docs/specs/rate-limiting.md §A13](../../specs/rate-limiting.md)
- Decision: [DR-009-algorithm-upgrade-policy.md](../decisions/DR-009-algorithm-upgrade-policy.md) §1 Q1 + §6.6
- Synthesis source: [2026-05-02-huakai-algo-upgrade-synthesis.md §1 A13](2026-05-02-huakai-algo-upgrade-synthesis.md)

Both lanes worked independently (no cross-reading). Each successfully built + ran its own tests in isolation.

## Convergence (~95%)

Both implementations agreed on:

- ERROR_RULES table + Classify() entry point + Classification struct shape
- 12 ErrorClass enum (slight name variation, same coverage)
- DisableTier (iron_clad / ambiguous / none) + RetryAction (5 actions) + Confidence
- Same R-001..R-016 core rules, same priority bands (10 / 20 / 30 / 40 / 50 / 60 / 70)
- Provider wildcard + body keyword substring matching
- Retry-After integer-seconds parsing
- §6.6 invariant: ambiguous-tier rules never reach `disabled`

## Conflicts → resolution

| # | Topic | Claude | Codex | **Merged** |
|---|---|---|---|---|
| 1 | `Classify` return signature | `Classification` (no error) | `(Classification, error)` | **Codex** — explicit error path; reject negative status |
| 2 | Status field type | `int` w/ `0/-1` sentinels | `string` w/ `"*"/"5xx"` | **Codex** — readable rule definitions |
| 3 | FsmTransition representation | bare event string | typed enum + `transitionFor(action,tier)` | **Codex** — derived consistently; structural §6.6 enforcement |
| 4 | Header matching | only Retry-After | full `HeaderMatch{Name,Equals,Contains}` | **Codex** — extensible for future header-based rules |
| 5 | Retry-After parsing | integer seconds only | float seconds OR HTTP-date (RFC 7231) | **Codex** — RFC-correct |
| 6 | Priority resolution | array order | explicit `betterRule()` w/ priority + provider specificity | **Codex** — flexible reordering |
| 7 | Provider normalization | lowercase only | + alias `anthropic_messages → anthropic` | **Codex** — alias support |
| 8 | Iron-clad keyword set | exactly 5 (per task spec) | 9 keywords (looser) | **Claude** — task spec said exactly 5 |
| 9 | Anthropic 403 validation rule | provider=`antigravity` (incorrect) | provider=`anthropic` | **Codex** — antigravity is not a real vendor |
| 10 | Multi-vendor coverage | OpenAI/Anthropic only | + Gemini permission_denied + Bedrock throttling | **Codex** — broader R-017/R-018 |
| 11 | Synthesized timeout (status 0) | R-016a body=timeout | not present | **Claude** — R-019 in merged for synth network timeout |
| 12 | OAuth refreshable distinction | R-009a separate from R-009 | folded into auth_other | **Simplified** — keep R-009 iron_clad permanent for ambiguity safety; F-AUTH-005 spec already handles refresh-window separately |

## Final merged file

`backend/internal/gateway/error_normalize.go` (real `package gateway`):
- 18 rules (R-001..R-019, R-009a dropped after simplification — net 19 numbered slots, 18 active)
- 12 ErrorClass constants
- typed FsmTransition with structural §6.6 enforcement in `transitionFor`
- exactly-5 `IronCladKeywords` map + `IsIronCladKeyword()` helper
- HeaderMatch struct (extensibility hook; no rule uses it yet but `headerMatches()` ready)
- `Classify(httpStatus, headers, body, provider) (Classification, error)`

`backend/internal/gateway/error_normalize_test.go`:
- 22 test functions covering AT-RATE-021/022/023, §6.6 invariant, all rule branches, RFC 7231 HTTP-date, provider alias, body case-insensitivity, ErrorClass cardinality, rule-ID uniqueness, edge cases
- Result: `ok github.com/BloomingProsperity/HUAKAI/internal/gateway 0.625s`
- Full package: `ok github.com/BloomingProsperity/HUAKAI/internal/gateway 0.778s` (unaffected pre-existing tests still green)
- `go vet ./internal/gateway/...` clean

## Validation of code-parallel rule

This is the **first** code artifact written under the 2026-05-04 rule expansion. Outcome:

- Both lanes converged on shape (95%) — high signal that the spec is unambiguous enough for independent impls
- Codex caught Claude's "antigravity" confabulation (non-existent provider) — single-lane Claude would have shipped that
- Claude caught Codex's looser 9-keyword iron-clad set — single-lane Codex would have permanent-disabled on `validation` keyword (forbidden per task spec exactly-5)
- Codex's structural extras (HeaderMatch, RFC-7231 date, provider alias, betterRule priority) were strict improvements over Claude's minimal version
- Synthesis time: ~5 min review + ~10 min merge write + 1 test run = 16 min
- Lane execution: ~3 min Claude + ~3 min Codex (background, parallel) = 3 min wall-clock

**Net effort vs single-lane**: ~1.4x. **Defect catch**: 2 (antigravity, keyword overreach). Worth it for money-grade money-adjacent paths.

## Followup

- Wire `Classify()` into `forwarder.go` upstream-error path (separate small commit)
- A22 FSM impl is gated on this `FsmTransition` enum (next code-parallel task)
- Rule table → DB-backed versioned table (per spec; current array is bootstrap)
