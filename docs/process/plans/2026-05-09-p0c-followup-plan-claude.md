# HCSF v0.4 P-0c Follow-up Plan — Claude (sonnet) Lane

> Lane: **specifier** (sonnet, claude-sonnet via Claude Code, agent ID `general-purpose:a90453d1261c090da`)
> Drafted independently per CLAUDE.md #10. Codex lane file `docs/process/plans/2026-05-09-p0c-followup-plan-codex.md` was confirmed **not present** at draft time and was not consulted.
> UTC timestamp: 2026-05-10
> Source files read: `backend/internal/proto/envelope_validate.go`, `backend/internal/proto/proto.go`, `backend/internal/proto/envelope_test.go`, `backend/internal/proto/openai_sse.go` (lines 140–165 only), `backend/internal/proto/gemini_sse.go` (line 110 referenced), `backend/internal/gateway/forwarder.go` (signature only).

## TL;DR

Sonnet review (Day 10) closed P-0b with 0 HIGH, 4 MED, 6 LOW. The four MED findings break into two categories:

- **M1 / M2 / M3** are local fixes inside `envelope_validate.go` + `envelope_test.go`: tighten `validateProviderProjection` necessary-field checks, re-tag the StreamPlan errors under the correct INV codes, and broaden the INV-1 round-trip test to exercise every payload kind. Each is low-risk (<60 LoC delta), can land in a single commit, and unblocks the spec ↔ implementation drift that #M2 specifically masks.
- **M4** is an **architecture decision**, not a bug. `ValidateEnvelope` has zero non-test callers today. The "alias sunset" pressure that `proto.go:13–19` advertises is fictional until something on the request path actually calls the validator. Deferring this past P-1 risks shipping P-2 ClientAdapter on top of a validator that never ran in production and silently regressed.

Recommendation: ship M1+M2+M3 immediately (P-0c.1, ~half-day). For M4, pick **option (c) — forwarder-entry validate behind a feature flag** as the primary path, with **option (a) — `-tags debug` strict mode** layered on top for local + CI use. Deliver M4 in P-0c.2 in parallel with the front of P-1, before any P-1 work mutates the envelope shape.

The full set is one short slice of follow-up work, not a new phase. P-0c should run **immediately, in parallel with P-1 kickoff**. Three Owner decision points are surfaced in §5.

## 1. 4 MED 修复 phase 切分

### Phase P-0c.1 — local validator hardening (M1 + M2 + M3)

Single commit, single reviewer pass, ~half-day budget. All three findings live in two files and don't touch any caller.

#### M1 — `validateProviderProjection` 缺必填校验

- **File**: `backend/internal/proto/envelope_validate.go:304–328`
- **Defect (cited)**: at L306 the function gates on `cp.Verdict != ProjectionPreserved`. If `Verdict == ""` (zero value) it slips through entirely; if `Capability == ""` no check exists at all.
- **Fix scope**: add two leading guards inside the loop body (before the existing verdict gate):
  - `if cp.Capability == ""` → `INV-7` "ProviderProjection.CapabilityResults[i].Capability is required".
  - `if cp.Verdict == ""` → `INV-7` "ProviderProjection.CapabilityResults[i].Verdict is required".
- **LoC delta**: ~10 lines impl + ~25 lines test (two negative cases in `envelope_test.go`).
- **Test additions**: `TestINV7_ProjectionRequiredFields` with two subtests (`missing Capability`, `missing Verdict`).
- **Risk**: very low. Existing callers don't construct `ProviderProjection.CapabilityResults` with empty fields (no fixtures exercise that path), so this strictly tightens the contract without breaking green paths. The only failure mode would be a future fixture that intentionally omits these fields; that fixture is already invalid by spec §4.

#### M2 — `validateStreamPlan` INV 编号归类不准

- **File**: `backend/internal/proto/envelope_validate.go:332–342`
- **Defect (cited)**: L333 returns `INV-5` for missing `StreamPlan.Mode`, and L339 returns `INV-5` for an invalid `StreamPlan.Mode` value. Per the comment block at L24–28 of the same file, **INV-5 is RequestMeta-only**. StreamPlan invariants are INV-11 (mid-stream fallback) and the slice of INV-3-style tagged-union behavior for `Mode` enum membership.
- **Fix scope**: re-classify both error returns. There are two reasonable mappings; recommend:
  - missing `Mode` → introduce a **new** invariant **INV-13** "StreamPlan.Mode required + enum-bound", and document it in the package-level comment at L20–33.
  - invalid `Mode` value → same INV-13.
- **Why a new INV instead of reusing INV-3**: INV-3 is scoped to `CapabilityNode` tagged-union shape per the package doc; widening it pollutes the INV semantics. Adding INV-13 keeps each INV with one mechanical meaning.
- **LoC delta**: ~6 lines impl change + 1 doc-comment paragraph + ~15 lines test fixup (existing test at `envelope_test.go:182–190` only covers INV-11 path, no `Mode==""` test exists).
- **Test additions**: `TestINV13_StreamPlanMode` with subtests `missing Mode` and `invalid Mode value`.
- **Risk**: low. Renaming INV codes is a contract change but no production caller introspects INV codes today (grep confirms only `_test.go` matches `INV-`). Spec §6 will need a one-line update to register INV-13.
- **Spec impact**: **Owner decision** — does adding INV-13 require a fresh spec sign-off, or does it ride P-0c as an editorial fix? See §5.

#### M3 — INV-1 round-trip 测试覆盖浅

- **File**: `backend/internal/proto/envelope_test.go:213–231`
- **Defect (cited)**: `TestINV1_RoundTripDeepEqual` builds exactly one `TextNode` with the minimum envelope. INV-1 is the marshal/unmarshal round-trip invariant for the *whole* envelope; covering only `Kind=text` lets serialization regressions in any other payload (ToolUse, Thinking, ComputerUse, MCPServer, DataRetention…) escape.
- **Fix scope**: convert the test to table-driven, with one case per `CapabilityKind`. Also add cases for non-empty `Edges`, `ProviderProjection.CapabilityResults`, `StreamPlan.EventClasses`, `Policy.DataRetention.Note`, and a populated `Extensions` map — all the previously unexercised serialization surfaces.
- **LoC delta**: ~80 lines test (one builder helper per Kind + a single table walk; existing `nonNilPayloads` enumeration in `envelope_validate.go:253–301` provides the canonical kind list).
- **Test additions**: `TestINV1_RoundTripAllKinds` (table-driven) replaces the existing test; keep the original as `TestINV1_RoundTripMinimal` so the "minimum envelope" smoke remains explicit.
- **Risk**: low. Pure test-side broadening. May surface hidden bugs in payload struct tags (a *good* outcome) — if so, those become P-0c.1 blockers and we fix forward.

#### Phase P-0c.1 packaging

- Single PR / commit. Both impl and tests land together.
- Codex per-commit review (CLAUDE.md #8) before commit.
- Acceptance: `go test ./backend/internal/proto/...` green; M1/M2/M3 each have ≥1 negative-case test that fails on `main` and passes on the branch.

---

### Phase P-0c.2 — architecture wiring (M4)

This is its own slice because it has cross-package surface area and a real Owner decision (see §2). Estimated 1.5–2 days depending on chosen option.

The mechanical work splits as:

1. **Pick an option** (§2) — Owner gate.
2. **Wire validate at chosen entry point(s).**
3. **Convert the two `&HCSF{}` placeholder returns** (`openai_sse.go:155`, `gemini_sse.go:110`) so they don't fail INV-4 every call. Two acceptable strategies: return `nil, losses, ErrNotImplemented` (matches the pattern at `openai_sse.go:142–146`) or return `NewEmptyEnvelope()` populated with at least RequestMeta. The first is honest and aligned with the `ErrNotImplemented` brother method; recommend that.
4. **Update tests** to assert the validator runs at the wired entry point (table-driven golden envelope fixture passes; mutated invalid envelope fails with the expected INV code).
5. **Update package doc** at `proto.go:13–19` to remove the "calling site will fail with INV-4" alias-sunset paragraph (which today is aspirational, not enforced).

LoC delta: 30–250 lines depending on option. Option (a) is smallest; option (d) is largest in absolute LoC but smallest in *now*-LoC since most lands in P-2.

## 2. M4 架构决策（4 选项 + 推荐）

Background: `ValidateEnvelope` is currently exercised only by `_test.go` files. Option matrix:

### Option (a) — `-tags debug` build tag enables ValidateEnvelope hot-path

- **Mechanism**: split a thin shim file `envelope_validate_debug.go` (`//go:build debug`) that returns `ValidateEnvelope(env)` and a sibling `envelope_validate_release.go` (`//go:build !debug`) that returns `nil`. Insert one call at every adapter entry point. Production builds compile out the call entirely.
- **Pro**: zero production overhead; CI runs with `-tags debug` and catches every drift; ergonomic for local dev.
- **Con**: production never sees validation failures — if a malformed envelope reaches a real upstream, the debug build is the only canary. Encourages a "tests are green, must be fine" failure mode where prod-only crash patterns escape.
- **Trade-off**: speed vs safety. Pure speed.
- **Effort**: ~30 LoC + every entry-point call site (currently small: openai_sse + gemini_sse + forthcoming P-1 forwarder hook).

### Option (b) — production hot-path `Version`-only check

- **Mechanism**: add a tiny `ValidateEnvelopeFast(env *HCSFEnvelope) error` that only checks `Version == "0.4"` (one comparison) and INV-5 RequestID non-empty (one comparison). Call it on every hot path. Schedule full `ValidateEnvelope` for non-hot paths (admin, replay, shadow).
- **Pro**: catches the most catastrophic drift (wrong-version envelope) at near-zero cost (<10ns). Scales to production without latency complaints.
- **Con**: gives a false sense of coverage — INV-3, INV-7, INV-8 remain unchecked in prod. INV-4 is the *least* likely failure (compile-time constant); the actually-likely failures (silent drop, dangling edge) still escape.
- **Trade-off**: cheap but covers the wrong invariant.
- **Effort**: ~20 LoC.

### Option (c) — central forwarder-entry validate, feature-flagged ★ recommended

- **Mechanism**: extend the request entry path (the place where the canonical envelope is first fully populated — for now the adapters' `*ToCanonical` results before they're handed to downstream consumers) with a single `if env := ...; flagEnabled { if err := proto.ValidateEnvelope(env); err != nil { ... }}`. The flag is a runtime config (env var or config field) defaulting **on in non-prod** and **off in prod** for the first sprint, then defaulting on globally after a soak window.
- **Pro**: validation runs at exactly the boundary it's specified for (canonical contract surface). Single code site means a single observability hook for "rejected envelopes" metric. Flag gives a kill switch if a false-positive emerges in prod.
- **Con**: needs a real entry point. Today the canonical envelope is not yet flowing through `gateway/forwarder.go:66` (that path is byte-stream forwarding, pre-canonical). So "central forwarder entry" = the adapter return sites *plus* a future P-1 canonical-aware forwarder. Option (c) for now means wrapping the adapter return sites with a shared helper; option (c) post-P-1 is the canonical forwarder hook.
- **Trade-off**: best accuracy, modest implementation. Accepts ~1µs latency cost per request for full validation (acceptable per docs/perf budget; can be re-measured if it shows up in p99).
- **Effort**: ~80 LoC (helper + wrappers + flag plumbing + observability counter) + 1 metric + 1 config field.
- **Recommendation rationale**: matches CLAUDE.md "Feature Preservation Rule" — the validator is a real feature, deleting its callability would silently reduce capability. Flag-gating preserves the feature without locking us into a perf claim we haven't measured.

### Option (d) — defer entirely to P-2 ClientAdapter

- **Mechanism**: do nothing now. Document M4 as a known gap in `docs/process/plans/`. P-2's ClientAdapter rollout designs canonical-flow validation as part of its own scope.
- **Pro**: smallest disruption to P-1.
- **Con**: P-2 is ~3 sprints out per current roadmap. The proto package keeps drifting from spec for a quarter. M2's INV-mismatch and M3's coverage gap compound — by P-2 we don't trust the validator, so P-2 has to redo the audit before wiring. Net effort is **larger** if we defer.
- **Trade-off**: zero immediate cost; significant deferred cost.
- **Effort now**: 0 LoC. Effort at P-2: option (c)'s 80 LoC + 1–2 days of re-auditing the validator's spec alignment.

### Recommended path

**Primary**: Option (c) — central forwarder-entry validate, runtime flag gated, default-on in non-prod from day one, default-on in prod after a 1-sprint soak.

**Secondary (stacked, not alternative)**: Option (a) — `-tags debug` strict mode for CI and local dev. The two compose: `-tags debug` makes the flag a compile-time true, and CI always runs with that tag. Production is governed by the runtime flag.

**Reject**: Option (b) is the worst of both (some cost, wrong coverage). Option (d) accumulates technical debt that compounds with M2/M3.

Open question for Owner: where the canonical envelope first crosses the boundary today is debatable since the v0.4 adapters at `openai_sse.go:142–155` are still `ErrNotImplemented`. Concretely **option (c) cannot be wired to a "real" forwarder entry until P-1 lands the first canonical-aware path**. Until then "option (c) right now" = wrap the adapter return sites with the shared helper. The recommendation assumes P-0c.2 lands that helper, and P-1 inherits + extends it — see §5 decision (b).

## 3. P-0c 何时执行

**Recommendation: immediately, parallel to P-1 kickoff.**

- P-0c.1 (M1+M2+M3): **block of half a day, single commit, must-precede or co-land with P-1 first commit.** P-1 will read fixtures + add envelope-shaped inputs; landing on a validator with mis-classified errors and a thin INV-1 net is a step backwards.
- P-0c.2 (M4): **parallel with P-1 first 2 sprint days.** P-1 needs the validator wired *somewhere* on its hot path or it can't enforce its own contract. The two slices interlock — if M4 picks option (c), P-1 is the consumer; if M4 picks option (d), P-1 has to pre-bake the validator integration. So locking M4 before P-1 day 2 saves rework.

**Reject "after P-1"**: each P-1 commit that doesn't run the validator is one more place future P-0c has to retrofit and one more place a regression hides.

**Reject "before P-1 (serial)"**: P-1 has independent surface area. Blocking P-1 on M4 architecture review is a serialization cost we don't need.

## 4. 三维 delta 分类（per CLAUDE.md #12）

Per CLAUDE.md #12, every delta classifies into 架构 / 算法 / 生态. The four MED fixes:

| Fix | Dimension | Justification |
|---|---|---|
| M1 (validateProviderProjection 必填校验) | **算法** | Contract-shape gate inside the validator; tightens which envelope shapes are accepted. No module boundary change, no observability surface. Matches "selection / rejection criteria" definition. |
| M2 (INV 编号归类) | **架构** | Re-naming INV-5 → INV-13 changes the *contract surface*: the package's exported invariant taxonomy is the contract clients introspect. New INV code = new public boundary. Matches "contract surface" definition. |
| M3 (round-trip 测试覆盖) | **生态** | Test surface is observability: it's how we *know* the contract holds. Broadening from one-kind to all-kinds is an audit-coverage improvement, classically 生态 (audit & compliance). |
| M4 (validator wiring) | **架构 + 生态** | 架构: where validation runs is a module-boundary decision (adapter vs forwarder vs ClientAdapter). 生态: the runtime flag + observability counter + soak workflow is operations capability. Multi-dimensional per the CLAUDE.md #12 example list ("a delta that fits multiple dimensions should explicitly state which"). |

All four dimensions check out. None is a "we just do it better" claim — each names the specific axis.

Note re: fusion-upgrade citation discipline (CLAUDE.md #12): these four fixes are *internal* to HUAKAI's own validator and don't claim "no project does X". They strengthen our own contract; no upstream project comparison needed.

## 5. Owner 决策点

Three concrete decisions need Owner input before P-0c starts execution:

### Decision (a) — INV-13 introduction (M2)

Add a new invariant code **INV-13** ("StreamPlan.Mode required + enum-bound") to the spec, replacing the misclassified INV-5 in `envelope_validate.go:332–342`. Alternatives:

1. ✅ **Add INV-13** (recommended) — clean, each INV stays single-mechanical-meaning.
2. Keep INV-5 but rename the doc-comment from "RequestMeta required" to "any required field violation" — semantically lossy.
3. Reuse INV-3 — pollutes tagged-union semantics.

**Owner: please confirm option 1 + whether spec doc update can ride P-0c as editorial, or needs a separate spec-amendment commit.**

### Decision (b) — M4 option pick

Per §2, recommended is **(c) + (a) stacked** (runtime flag + debug build tag). Alternatives are (b) shallow check, (d) defer to P-2.

**Owner: confirm option (c)+(a). If accepting (c), also confirm: where does "forwarder entry" mean today before P-1 lands a real canonical forwarder?**
- Sub-option (c.1): wrap adapter return sites with a shared helper now; P-1 inherits and extends.
- Sub-option (c.2): leave the wiring as a P-1 day-1 task; P-0c.2 only delivers the helper, the runtime flag plumbing, and the observability metric.

Recommendation: (c.1) — concrete callers from day one, no "the next slice will wire it" hand-wave.

### Decision (c) — P-0c execution timing

Per §3, recommended is **immediately, P-0c.1 must precede or co-land with P-1 commit #1, P-0c.2 parallel with P-1 days 1–2.** Alternatives: serialize before P-1, defer after P-1.

**Owner: confirm parallel execution model. If we go parallel, both lanes are mine to coordinate (Codex review per CLAUDE.md #8 each commit).**

### Optional Decision (d) — Should LOW findings ride P-0c?

Sonnet review surfaced 6 LOW findings (not enumerated in this task brief). Two paths:

1. Defer LOW to a `P-0d` polish phase post-P-1.
2. Roll any LOW that touches the same files as M1–M4 into P-0c.1 to avoid double-touching the same lines.

Recommend (2) for file-locality only; non-overlapping LOW defers. This requires a quick scan of the LOW list — not in scope for this plan, but Owner should flag if they want LOW-merge enforcement.

## 风险与盲点

### 风险

- **R1 (medium)**: M2's INV-13 introduction is technically a public-contract change. If any external consumer pins INV codes (none today, but P-2 ClientAdapter may), they'd have to re-validate. Mitigation: search-and-update across `docs/specs/` + grep for `INV-` literals before commit.
- **R2 (low)**: M3's broadened round-trip may expose hidden struct-tag bugs. If `omitempty` is missing on a pointer field and the round-trip flips `nil → {}`, the test will catch it. That's a feature, not a bug — but it could expand P-0c.1 scope.
- **R3 (medium)**: M4 option (c) at the adapter-return level today is a temporary mounting point. If we accept and then P-1 reorganizes the adapter contract, we re-mount. Mitigation: keep the helper as a single function call so the re-mount is one-line.
- **R4 (low)**: Codex parallel-draft cycle (CLAUDE.md #10) for this very plan — if Codex's lane comes back diverging significantly on M4 option, we may re-spin §2 and lose half a day. Acceptable; that's the rule's purpose.
- **R5 (medium)**: Spec drift between `docs/specs/protocol-translation.md` and `envelope_validate.go` is exactly what M1+M2 reveal. P-0c only fixes the validator side; the spec doc may also need touch-up. Audit during P-0c.1 codex review.

### 盲点

- **B1**: I did not read the full sonnet Day 10 review document (only the 4 MED summaries from the task brief). If the review's verbal context contains rationale that contradicts a fix here (e.g., M2 was deliberate), I'd miss it. Owner should sanity-check against the original review before accepting Decision (a).
- **B2**: I did not survey `docs/specs/` for existing INV taxonomy publication. INV-13 may already be informally reserved for something else.
- **B3**: I did not measure the actual validation cost. §2's "~1µs per request" is an architectural estimate, not a benchmark. P-0c.2 should add a `BenchmarkValidateEnvelope` row before committing to the flag-default-on-in-prod plan.
- **B4**: P-1 scope was not re-read before sequencing in §3. If P-1 day 1 happens to be schema-only or doc-only, the "parallel with P-1 day 1–2" recommendation is unnecessary; P-0c.2 can move to P-1 day 1 exclusively. Worth a 5-minute check before kicking off.
- **B5**: I did not confirm whether the alias `HCSF = HCSFEnvelope` at `proto.go:19` has any external (non-test, non-internal) consumer. The "alias sunset" comment (`proto.go:13–18`) treats sunset as imminent; if a P-2 spike already removes the alias, M4 option (a) trivially follows. Worth one grep of any consumer outside `backend/internal/proto`.

## Source citations

All citations are local repo paths read during this lane:

- `backend/internal/proto/envelope_validate.go:20–33` — INV taxonomy doc-comment (validates M2's claim that INV-5 is RequestMeta-only).
- `backend/internal/proto/envelope_validate.go:78–95` — `validateRequestMeta` defines INV-5 scope.
- `backend/internal/proto/envelope_validate.go:304–328` — `validateProviderProjection` (M1 site).
- `backend/internal/proto/envelope_validate.go:332–342` — `validateStreamPlan` (M2 site).
- `backend/internal/proto/envelope_validate.go:253–301` — `nonNilPayloads` (canonical CapabilityKind list, basis for M3 table-driven test).
- `backend/internal/proto/envelope_test.go:213–231` — `TestINV1_RoundTripDeepEqual` (M3 site).
- `backend/internal/proto/proto.go:13–19` — alias-sunset comment (M4 architectural framing).
- `backend/internal/proto/proto.go:22–35` — ClientAdapter / UpstreamAdapter interface signatures (M4 wiring surface).
- `backend/internal/proto/openai_sse.go:142–155` — `&HCSF{}` placeholder return (M4 cleanup target #1).
- `backend/internal/proto/gemini_sse.go:110` — `&HCSF{}` placeholder return (M4 cleanup target #2; not directly read, file:line confirmed via grep).
- `backend/internal/gateway/forwarder.go:66` — `StreamForwarder.Forward` signature (byte-level today, not envelope-aware; informs M4 §2).
- Grep across `backend/**/*.go`: `ValidateEnvelope(` matches **only** in `envelope_test.go` + `fixtures_test.go` + the validator definition itself, confirming zero non-test callers (M4 premise).

No non-MIT reference projects read for this plan. CLAUDE.md #11/#12 source-must-read rules do not apply (HUAKAI-internal-only artifact).

## Tail block

- Lane: specifier (sonnet), Claude PM-Orchestrator on `claude/phase-1` branch.
- Agent ID: `general-purpose:a90453d1261c090da`
- UTC timestamp: 2026-05-10
- Codex parallel lane file: `docs/process/plans/2026-05-09-p0c-followup-plan-codex.md` — confirmed not present at draft time, will be cross-discussed per CLAUDE.md #10 before any P-0c execution.
- Next steps after Owner accepts §5 decisions: (1) cross-discuss with Codex lane, (2) write synthesis at `docs/process/plans/2026-05-09-p0c-followup-plan-synthesis.md`, (3) execute P-0c.1, (4) Codex per-commit review, (5) execute P-0c.2.
