# Codex Final Reviewer-Lane Report - F-RATE-001 Rate Limiting + Cooldown Synthesis

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane |
| Review date | 2026-04-28 |
| Artifact reviewed | `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md` |
| Gate | CL-001..CL-011 strict path review for F-RATE-001 |
| Verdict | APPROVE-WITH-FIXES |
| Local Sub2API source | `.omc/reference-src/sub2api` at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Inputs read | `docs/decompositions/sub2api/rate-limiting-source-verified.md`; `docs/decompositions/_cross-cutting/rate-limiting-codex.md` |
| Checklist read | `docs/specs/_REVIEW_CHECKLIST.md` |
| Format precedent read | `docs/reviews/2026-04-28-codex-pool-synthesis-v2-final-review.md` |

## Review Protocol Notes

- Pre-commitment prediction 1: the synthesis would inherit correct Sub2API behavior from the two source-verified passes, but leave release-blocking TODOs from the first pass.
- Actual: confirmed. Most spot-checked behavior is source-supported, but four TODOs remain and the file explicitly says they block Released spec.
- Pre-commitment prediction 2: at least one file:line citation would be stale because the synthesis merged Claude and Codex passes with different line references.
- Actual: confirmed. The synthesis keeps `tryTempUnschedulable` as `line 1481+`, but the function starts at `ratelimit_service.go:1432`; line 1481 is inside the function tail.
- Pre-commitment prediction 3: CL-001/CL-002 would be pressured by upstream implementation identifiers and schema keys because rate limiting is state-heavy.
- Actual: confirmed. Release-facing sections carry upstream function names, credential keys, JSON keys, state fields, and database-shaped names.
- Pre-commitment prediction 4: the feature metadata would be better than the earlier pool v2 artifact, but parity-matrix existence might still fail because `F-RATE-001` is new.
- Actual: confirmed. The header has `Feature ID`, `Lane mode`, and `Sources`, but `F-RATE-001` is absent from `docs/03_FEATURE_PARITY_MATRIX.md`.
- Pre-commitment prediction 5: HUAKAI design labels would be mostly clean, but one or two design claims would need replacement because they contradict source or local architecture.
- Actual: partially confirmed. HUAKAI design labeling is mostly clean, but `Rate-limit state transition atomic with Usage Record` conflicts with the current no-schema/no-ledger-change stage unless explicitly marked as future design.
- Review mode: escalated to ADVERSARIAL after the third MAJOR finding. I checked adjacent source around TODOs, repository write methods, model-rate-limit storage, temp-unsched matching, and token refresh recovery.
- Self-audit result: no CRITICAL finding survived. The MAJOR findings below have direct artifact evidence and either source or project-doc evidence.
- Realist check result: severity remains MAJOR, not CRITICAL, because no implementation has shipped from this synthesis and all release-blocking defects are bounded doc/spec corrections. Missing parity row and open TODOs block Released status but do not imply source behavior is false.

## §1 - CL-001..011 Verdict Matrix

| Check | Verdict | One-line justification |
| --- | --- | --- |
| CL-001 | FAIL | Release-facing prose contains upstream function/method/config-style identifiers including `CheckErrorPolicy`, `HandleUpstreamError`, `PreCheckUsage`, `handle403`, `handle429`, `tryTempUnschedulable`, `SetRateLimited`, `ClearRateLimit`, `TokenRefreshService`, `temp_unschedulable_enabled`, and exact header/key names. |
| CL-002 | FAIL | The synthesis exposes upstream schema/field-like names such as `extra.model_rate_limits`, `rate_limit_reset_at`, `expires_at`, `temp_unschedulable_until` semantics, and the temp-unsched rule object keys. |
| CL-003 | PASS | No upstream UI component names, CSS classes, or dashboard layout names were found. |
| CL-004 | PASS | I found no copied upstream documentation sentence longer than common technical phrases; most source evidence is paraphrased. |
| CL-005 | PARTIAL | The HUAKAI algorithm is framed as guarantees, but sections 1.2, 1.4, 1.6, 1.8, and 1.9 still read like implementation extraction and carry function/field-specific mechanics. |
| CL-006 | PASS | `Sources` cites Sub2API `E-LIC-001` and LiteLLM `E-LIC-005`; both ledger rows exist in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`. |
| CL-007 | PASS | Header line 7 declares `Lane mode | Option C`; DR-000 allows Option C for provider failover/account-health heuristics, which this cooldown state machine materially affects. |
| CL-008 | FAIL | `Feature ID | F-RATE-001` is not present in `docs/03_FEATURE_PARITY_MATRIX.md`; `rg -n "F-RATE"` returned no row. |
| CL-009 | FAIL | The file has `Open TODOs` and explicitly says they block Released spec at lines 281-288. |
| CL-010 | PASS | No external source URL is embedded in implementer-relevant sections; the file uses local doc links and local source provenance. |
| CL-011 | PARTIAL | Most inherited claims spot-check against source, but the synthesis itself does not carry file:line citations next to behavior claims and one inherited line reference is stale (`tryTempUnschedulable` line 1481+). |

Detailed CL notes:

- CL-001 and CL-002 are not theoretical. The file says it will be moved to `docs/specs/rate-limiting.md` after review, but the current body still contains source-facing names throughout the implementer-relevant sections.
- CL-006 passes narrowly. LiteLLM is listed only as behavioral cross-reference and the synthesis does not claim LiteLLM-specific rate-limit behavior.
- CL-007 passes because the behavior directly participates in Provider Account health and scheduler availability. The earlier Claude input said Option B, but the synthesis chooses Option C and the project rules support Option C for provider failover/account-health heuristics.
- CL-008 is a hard structural miss. Existing rate-limit-adjacent rows are `F-SEC-001` and `F-SEC-004`; neither is `F-RATE-001`.
- CL-009 is the strongest release blocker. The artifact itself says the TODOs block Released spec.
- CL-011 is not a wholesale source failure. It is a release hygiene failure: citations live mostly in inputs, not the synthesis, and one inherited pointer is wrong.

## §2 - Spot-Check Log

Spot-check method:

- I selected claims across entry points, status dispatch, 429 fallback, 403 handling, temp-unsched rules, model-level cooldown, cascade clearing, token refresh, repository writes, and metadata rows.
- I used `rg -n` and targeted line reads against `.omc/reference-src/sub2api`.
- Verdict meanings:
- PASS: cited source or inherited citation supports the claim.
- FAIL: source exists but does not support the exact claim or cited line.
- MISSING: claim lacks enough file:line support for release even if it may be true.

### Spot-check 01 - Local Sub2API clone pinned

- Synthesis claim: Sub2API source is at commit `b0a2252...`.
- Grep/command evidence: `git -C .omc/reference-src/sub2api rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`.
- Verdict: PASS.

### Spot-check 02 - `PreCheckUsage` is Gemini-only

- Synthesis claim: `PreCheckUsage` is a proactive pre-dispatch quota check, currently Gemini-only.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:279` defines `PreCheckUsage`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:280-281` returns true when account is nil or not `PlatformGemini`.
- Verdict: PASS.
- Release note: source function name must be paraphrased in the Released spec.

### Spot-check 03 - Pool-mode short-circuit before error handling

- Synthesis claim: pool-mode plus no custom error codes means no local state change.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:124-126` returns false when `account.IsPoolMode() && !customErrorCodesEnabled`.
- Verdict: PASS.
- Release note: keep the behavior as "pooled accounts can opt out of local poisoning by uncustomized upstream errors"; remove source method names.

### Spot-check 04 - OpenAI 403 counter cache behavior

- Synthesis claim: OpenAI 403 with counter cache temp-unschedules before disabling; without counter cache, it disables immediately.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:705-707` calls auth-error handling when the counter cache is nil.
- Grep evidence: Codex input cites `ratelimit_service.go:710`, `717`, `723`, `725` for increment, threshold, and temp-unsched path; local `rg -n "openAI403CounterCache|SetTempUnschedulable"` confirms those references in the same handler.
- Verdict: PASS.

### Spot-check 05 - Anthropic 429 without reset does not set local cooldown

- Synthesis claim: Anthropic 429 with no reset header is pass-through, no state change.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:856-863` describes no reset time as likely not a real rate limit and returns for Anthropic.
- Verdict: PASS.

### Spot-check 06 - Non-Anthropic 429 fallback writes a 5-minute account cooldown

- Synthesis claim: default fallback is 5 minutes for non-Anthropic.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:897-917` includes SetRateLimited fallback writes; Codex input cites `ratelimit_service.go:866-869` for the explicit 5-minute calculation.
- Verdict: PASS.
- Note: source line numbers in the two input passes differ because one pass uses a shifted local view, but the behavior exists.

### Spot-check 07 - Temp-unsched rule shape is source-backed

- Synthesis claim: temp-unsched rules carry `error_code`, `keywords`, `duration_minutes`, and `description`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/account.go:284` reads `temp_unschedulable_rules`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/account.go:301-305` parses `error_code`, `keywords`, `duration_minutes`, and `description`.
- Verdict: PASS for source behavior.
- Release note: CL-002 fails if these upstream key names remain in the Released spec as HUAKAI schema.

### Spot-check 08 - Temp-unsched matching body location is stale in synthesis TODO

- Synthesis claim/TODO: `tryTempUnschedulable` body starts at `line 1481+`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:1432` defines `tryTempUnschedulable`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:1481-1482` is only the function tail returning false.
- Verdict: FAIL.
- Why it matters: this is exactly the class of stale inherited file:line reference CL-011 was added to catch.

### Spot-check 09 - Temp-unsched matcher cap and structured reason are source-backed

- Synthesis claim: body-match is capped at 64 KiB, keyword match is case-insensitive, and triggered state stores status, keyword, rule index, and a 2-KiB message snapshot.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:1462-1466` caps and lowercases the body.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:1501-1510` lowercases keyword comparison through `strings.Contains`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:1528-1534` stores until, triggered time, status, matched keyword, rule index, and truncated message.
- Verdict: PASS.

### Spot-check 10 - Model-level cooldown storage and read-side behavior

- Synthesis claim: model-level rate limit is stored under `model_rate_limits` and is active when `rate_limit_reset_at` parses RFC3339 and is in the future.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/model_rate_limit.go:9` defines the storage key.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/model_rate_limit.go:80-88` reads `model_rate_limits` and `rate_limit_reset_at`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/model_rate_limit.go:92` parses RFC3339; lines immediately after return nil on parse failure and future-active duration.
- Verdict: PASS for source behavior.
- Release note: these exact upstream key names must not become HUAKAI schema names.

### Spot-check 11 - Cascade clearing exists

- Synthesis claim: account-level clear cascades through overload, model-level, temp-unsched, temp-unsched cache, and OpenAI 403 counter.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:1256-1275` calls repository clear-rate-limit, clears Antigravity scopes, clears model limits, clears temp-unsched, deletes temp cache when present, and resets OpenAI 403 counter.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/repository/account_repo.go:1136-1142` clears account rate-limited and overload timestamps together.
- Verdict: PASS.

### Spot-check 12 - Token refresh retry exhaustion is temp-unsched, not error

- Synthesis claim: retry-exhausted refresh failure sets temp-unsched and preserves active status for future retry; non-retryable errors set account error.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/token_refresh_service.go:267-279` sets account error on non-retryable refresh failure.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/token_refresh_service.go:298-323` sets temporary unschedulability on retry exhaustion instead of status error.
- Verdict: PASS.

### Spot-check 13 - Successful token refresh clears temp-unsched and invalidates cache

- Synthesis claim: refresh success clears temp-unsched, deletes temp cache, invalidates OAuth cache.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/token_refresh_service.go:340-357` clears temp-unsched and deletes temp-unsched cache.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/token_refresh_service.go:358-367` invalidates token cache for OAuth accounts.
- Verdict: PASS.

### Spot-check 14 - `SetRateLimited` is not row-level locked in source

- Synthesis TODO: verify whether `SetRateLimited` is row-level locked in PostgreSQL or just cached.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/repository/account_repo.go:1020-1026` is a direct Ent update setting timestamps; there is no explicit `FOR UPDATE`, transaction, or compare-and-swap in the method body.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/repository/account_repo.go:1030-1033` then enqueues scheduler outbox and syncs scheduler snapshot.
- Verdict: PASS as verified negative, not as an open TODO.
- Required release action: close TODO-4 and state that Sub2API uses a direct DB state update plus scheduler sync; HUAKAI row-lock/serializable behavior is design.

### Spot-check 15 - CL-008 parity row absence

- Synthesis claim: `Feature ID | F-RATE-001`.
- Grep evidence: `rg -n "F-RATE" docs/03_FEATURE_PARITY_MATRIX.md` returned no rows.
- Grep evidence: nearby rows only include `F-SEC-001` and `F-SEC-004` for abuse/tier rate limiting, not upstream cooldown synthesis.
- Verdict: FAIL.

### Spot-check 16 - CL-006 license rows

- Synthesis claim: Sources are Sub2API `E-LIC-001` and LiteLLM `E-LIC-005`.
- Grep evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:15` has `E-LIC-001 | Sub2API | ... | LGPL-3.0`.
- Grep evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:19` has `E-LIC-005 | LiteLLM | ... | MIT`.
- Verdict: PASS.

## §3 - Findings

### Critical Findings

None.

No CRITICAL finding survived self-audit. The artifact is not ready for Released status, but the failures are bounded documentation/spec corrections, not unrecoverable source falsification or implementation damage.

### Major Findings

#### Major Finding 1 - `F-RATE-001` does not exist in the parity matrix

- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:6` declares `Feature ID | F-RATE-001`.
- Evidence: `rg -n "F-RATE" docs/03_FEATURE_PARITY_MATRIX.md` returned no rows.
- Evidence: CL-008 requires the `Feature ID` field to match an existing parity matrix row.
- Confidence: HIGH.
- Why this matters: the synthesis cannot become `docs/specs/rate-limiting.md` Released while its feature identity is absent from the project’s authoritative feature ledger.
- Fix: add a parity matrix row for `F-RATE-001` before release, or change the synthesis to a valid existing feature ID if the Owner intends to fold this into `F-SEC-001`/`F-SEC-004`. Do not release with a dangling feature ID.

#### Major Finding 2 - Open TODOs explicitly block Released status

- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:281-286` lists TODO-1 through TODO-4.
- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:288` says `These do NOT block synthesis sign-off; they DO block Released spec (per CL-009).`
- Confidence: HIGH.
- Why this matters: CL-009 says unresolved behavior must appear honestly, and Open Questions are a hold signal. The artifact admits it is not release-ready.
- Fix: close or convert each TODO before release:
- TODO-1: replace with verified model-level cooldown summary from `model_rate_limit.go:9`, `30-41`, and `80-92`; retain only edge cases that are not needed for the Released spec.
- TODO-2: correct the line reference to `ratelimit_service.go:1432-1482` and summarize verified matching behavior.
- TODO-3: either remove one-api from scope or add a source-verified one-api comparison; do not leave it as a KEEP-candidate fishing expedition.
- TODO-4: close as verified negative: `SetRateLimited` is a direct DB timestamp update plus scheduler sync, not explicit row-level lock/serializable mutation.

#### Major Finding 3 - Release-facing body leaks upstream function and method identifiers

- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:18-20` names `CheckErrorPolicy`, `HandleUpstreamError`, and `PreCheckUsage`.
- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:51`, `64`, `98`, `119`, `124`, `150`, `152`, `155`, `210`, `258`, and `270` name upstream handlers/services/methods including `handleCustomErrorCode`, `handle403`, `TokenRefreshService`, `SetRateLimited`, `ClearRateLimit`, and `model_rate_limit.go`.
- Evidence: CL-001 prohibits upstream function, method, handler, or configuration-constant names from non-MIT references in release specs.
- Confidence: HIGH.
- Why this matters: implementers reading Released specs should see HUAKAI behavior contracts, not source-derived implementation names. This is exactly the contamination channel the checklist exists to prevent.
- Fix: move source identifiers to a reviewer-only evidence appendix or remove them. In implementer-facing sections, replace with HUAKAI terms such as "pre-decision error policy check", "central upstream-error handler", "proactive quota precheck", "403 forbidden classifier", "account cooldown write", and "runtime recovery clear".

#### Major Finding 4 - Release-facing body exposes upstream schema/credential keys

- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:74-81` reproduces the temp-unsched credential rule object shape with `temp_unschedulable_enabled`, `error_code`, `keywords`, `duration_minutes`, and `description`.
- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:94-95` carries `expires_at`.
- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:106-108` carries `extra.model_rate_limits` and `rate_limit_reset_at`.
- Evidence: CL-002 prohibits upstream schema column names, and CL-001a warns that config/key names can become fingerprints when paired with behavior.
- Confidence: HIGH.
- Why this matters: this would let a future implementer reproduce upstream state shape instead of designing HUAKAI-native entities and fields.
- Fix: replace the schema-shaped block with a behavior contract: "operator-enabled temporary exclusion policy with status matcher, keyword matcher, duration, and label." Use HUAKAI-owned names only in the Released spec.

#### Major Finding 5 - CL-011 citation inheritance is incomplete and one line reference is wrong

- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:24` cites only `Codex pass §HandleUpstreamError`, not a source file/line.
- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:56` cites only `Claude pass §3 + Codex confirmation`, not a source file/line.
- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:284` says `tryTempUnschedulable` is `line 1481+`, while `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:1432` is the function definition and `1481-1482` is only the tail.
- Confidence: HIGH.
- Why this matters: the file is labeled "Source-Verified", but a reviewer cannot validate several claims without chasing input docs, and at least one inherited location is stale.
- Fix: add exact source locations for each KEEP/source-backed behavior in either inline citations or a compact source-evidence table. Correct `tryTempUnschedulable` to `ratelimit_service.go:1432-1482` plus `1517-1557` for trigger persistence.

#### Major Finding 6 - "15 reasons" taxonomy is actually 19 rows and includes an invalid source-backed custom-error recovery

- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:178` says `Failure Taxonomy (15 reasons)`.
- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:184-202` lists 19 taxonomy rows.
- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:202` says `CUSTOM_ERROR_CODE` recovery is `account_cooldown(operator_configured)` and labels it `SUB2API-VERIFIED`.
- Evidence: the Claude input says custom error code handling calls `handleCustomErrorCode + permanent disable`, and the synthesis status matrix line 51 also says `handleCustomErrorCode + disable`.
- Confidence: HIGH.
- Why this matters: the taxonomy count error is minor alone, but the `CUSTOM_ERROR_CODE` recovery is a source-claim contradiction. Implementers could build cooldown behavior where the source-backed behavior was disable/error.
- Fix: change the heading to 19 reasons or reduce the table. Replace row `CUSTOM_ERROR_CODE` recovery with `operator-configured terminal account error/disable behavior` if preserving Sub2API behavior, or label a cooldown variant as `HUAKAI-DESIGN`.

#### Major Finding 7 - AT-RATE-013 overstates atomicity of cascade clearing

- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:270` says `ClearRateLimit cascade: clears overload / model / temp / 403 counter atomically`.
- Evidence: `.omc/reference-src/sub2api/backend/internal/service/ratelimit_service.go:1256-1275` performs multiple sequential calls: clear rate limit, clear Antigravity scopes, clear model limits, clear temp-unsched, delete cache, reset counter.
- Evidence: `.omc/reference-src/sub2api/backend/internal/repository/account_repo.go:1136-1142` only clears rate-limit and overload in one repository update; model/temp/cache/counter are separate calls.
- Confidence: HIGH.
- Why this matters: the cascade is verified; the atomicity is not. A Released acceptance test that asserts atomic cross-state clearing would be a HUAKAI design requirement, not a Sub2API-inherited behavior.
- Fix: split the test wording:
- `Sub2API-inheritable: ClearRateLimit cascades through overload/model/temp/counter state.`
- `HUAKAI-design: cascade clear is atomic or compensation-safe across DB/cache/counter boundaries.`

#### Major Finding 8 - `SetRateLimited` row-lock question is already answered and contradicts the open TODO framing

- Evidence: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:286` leaves open whether `SetRateLimited` is row-level locked in PostgreSQL or just cached.
- Evidence: `.omc/reference-src/sub2api/backend/internal/repository/account_repo.go:1020-1026` updates timestamp fields directly through the repository.
- Evidence: `.omc/reference-src/sub2api/backend/internal/repository/account_repo.go:1030-1033` then enqueues scheduler outbox and syncs scheduler snapshot.
- Confidence: HIGH.
- Why this matters: leaving a resolved negative as a TODO invites the Released spec to defer a core correctness invariant. It also weakens HUAKAI's design contrast.
- Fix: replace TODO-4 with: "Verified negative: Sub2API uses a direct account cooldown state update plus scheduler sync; no explicit row-level lock/serializable cooldown transition was found in `SetRateLimited`. HUAKAI row-locked/serializable transition is design."

### Minor Findings

1. The header says LiteLLM is a source, but section 10 says no LiteLLM-specific rate-limit pattern is claimed. That is acceptable but noisy. If LiteLLM contributes no F-RATE behavior, keep it as "background comparison only" outside `Sources` or add one precise MIT-safe cross-reference row.
2. `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:255` says `AT-RATE-001..017`, but the list runs through `AT-RATE-020`.
3. `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:180` references sibling syntheses by section number, but those section numbers are not verified here and are not needed in a Released spec.
4. `Status | Action Plan` at line 5 is fine for a decomposition, but must be changed before moving to `docs/specs/rate-limiting.md`.
5. The synthesis uses mojibake-rendered arrows/section symbols in several places. That is not a release blocker, but it should be normalized to ASCII before Released to avoid misread acceptance criteria.

## What's Missing

- A parity matrix row for `F-RATE-001`, or an explicit decision to merge this feature under an existing row.
- A clean-room-safe Released-spec vocabulary that removes upstream function names and field names from implementer-facing sections.
- A compact source-evidence appendix with exact file:line citations for all Sub2API-verified taxonomy rows and tests.
- A closed disposition for every TODO in section 9.
- A corrected separation between source-inherited cascade behavior and HUAKAI atomicity guarantees.
- A corrected custom-error-code taxonomy row.
- A statement of whether one-api is in or out of scope for this feature. The current TODO-3 leaves it in limbo.
- A release-ready status/sign-off block. The current sign-off is still pending.

## Ambiguity Risks

- `Feature ID | F-RATE-001` can mean a new parity feature that still needs a row.
- It can also mean an informal decomposition identifier.
- Risk if wrong interpretation chosen: release traceability breaks, and acceptance tests cannot tie back to the parity matrix.

- `CUSTOM_ERROR_CODE | operator-configured | account_cooldown(operator_configured) | SUB2API-VERIFIED` can mean custom status codes cool down accounts.
- It can also mean custom status codes are operator-configured but terminal error behavior is inherited.
- Risk if wrong interpretation chosen: implementer builds recoverable cooldown where source-backed behavior was disable/error.

- `ClearRateLimit cascade ... atomically` can mean "all effects happen as one transaction."
- It can also mean "one operation semantically clears several dependent states."
- Risk if wrong interpretation chosen: tests assert a source behavior that does not exist, or implementation misses the HUAKAI design requirement for transactional/compensation safety.

- `These do NOT block synthesis sign-off; they DO block Released spec` can mean the current reviewer may still approve the synthesis memo.
- It can also mean the reviewer is being asked to approve the move to Released.
- Risk if wrong interpretation chosen: the file is moved to `docs/specs/` with known release holds.

## Multi-Perspective Notes

- Executor perspective: an implementer following the current file would see exact upstream method/key names and may mirror the source structure. That is not acceptable under Option C.
- Stakeholder perspective: the behavior coverage is useful and substantially source-backed, but a Released spec with a dangling feature ID and open TODOs would weaken the F-POOL-001 gate precedent.
- Skeptic perspective: the synthesis is still a merged memo, not a release-grade spec. The "Becomes" line admits source identifiers will be cleaned later; that cleanup is not optional.
- Security perspective: token refresh, temp-unsched reasons, and error messages need redaction-oriented acceptance tests before implementation. The synthesis mentions token-leakage-safe logging only as HUAKAI design and a test scenario, which is good but not yet tied to source-proof or local contracts.
- New-hire perspective: the file assumes the reader knows the two input passes and source line mappings. A release spec must be self-contained enough to implement without opening non-MIT source or decomposition history.
- Ops perspective: the HUAKAI improvements are directionally correct: jitter, reason taxonomy, tenant buckets, and Retry-After propagation are operationally useful. But atomicity boundaries must be explicit because DB/cache/counter split-brain is a real incident mode.

## §4 - FINAL VERDICT

Verdict: APPROVE-WITH-FIXES.

Meaning:

- Do not move `rate-limiting-synthesis.md` to `docs/specs/rate-limiting.md` Status=Released as-is.
- The substantive Sub2API behavior picture mostly checks out against local source.
- The remaining blockers are release-gate defects: missing parity row, open TODOs, identifier/schema leakage, incomplete citation inheritance, and a few incorrect/overstated source labels.
- If any TODO remains in the Released artifact, or if `F-RATE-001` remains absent from the parity matrix, this verdict downgrades to REJECT.

### Required Fixes Before Released

1. Add or remap the parity feature row for `F-RATE-001`.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:6`; `docs/03_FEATURE_PARITY_MATRIX.md` has no `F-RATE` row.
   - Recommended action: add a row for upstream rate-limit detection, cooldown computation, provider-account quarantine, OAuth refresh cooldown, and model-level cooldown, or explicitly merge this into an existing feature ID and update the synthesis header.

2. Close all Open TODOs.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:281-288`.
   - Recommended replacement: replace section 9 with a "Resolved Source Notes" table. Mark model-rate-limit, temp-unsched matching, and SetRateLimited locking as verified; either remove one-api scope or add a source-verified one-api comparison.

3. Remove upstream function/method names from implementer-facing sections.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:18-20`, `51`, `64`, `98`, `119`, `124`, `150-155`, `210`, `258-270`.
   - Recommended replacement: use HUAKAI behavior names. Keep exact source identifiers only in an evidence appendix if needed for reviewer traceability.

4. Remove upstream schema/credential/key names from Released spec prose.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:74-81`, `94-95`, `106-108`.
   - Recommended replacement: "operator-enabled temporary exclusion policy with status matcher, keyword matcher, duration, and label"; "credential-expiry marker"; "model-scoped cooldown map with reset timestamp."

5. Add exact file:line citations for each Sub2API-verified behavior claim.
   - File/line: examples at `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:24`, `56`, `184-202`, `257-270`.
   - Recommended replacement: add a compact evidence appendix mapping each KEEP/taxonomy/test row to `ratelimit_service.go`, `model_rate_limit.go`, `token_refresh_service.go`, `account.go`, `account_repo.go`, and Antigravity helper lines.

6. Correct the `tryTempUnschedulable` source location.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:284`.
   - Recommended replacement: `tryTempUnschedulable starts at ratelimit_service.go:1432; matching and trigger persistence are verified at ratelimit_service.go:1432-1482 and 1517-1557.`

7. Fix `CUSTOM_ERROR_CODE` recovery semantics.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:202`.
   - Recommended replacement: `CUSTOM_ERROR_CODE | operator-configured status code policy | terminal account error/disable behavior unless HUAKAI chooses a separate cooldown policy | SUB2API-VERIFIED for terminal behavior; HUAKAI-DESIGN for any recoverable cooldown variant.`

8. Fix taxonomy and test numbering.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:178`, `255`.
   - Recommended replacement: "Failure Taxonomy (19 reasons)" and "Test Scenarios (AT-RATE-001..020)" unless rows/tests are removed.

9. Fix cascade atomicity wording.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:270`.
   - Recommended replacement: `AT-RATE-013 / ClearRateLimit cascade: clears overload / model / temp / 403 counter; HUAKAI-design variant verifies the cascade is atomic or compensation-safe across DB/cache/counter state.`

10. Convert release status/sign-off before moving to `docs/specs/`.
   - File/line: `docs/decompositions/_cross-cutting/rate-limiting-synthesis.md:5`, `297-305`.
   - Recommended replacement: after fixes and reviewer re-check, set strict spec status and sign-off fields per `docs/specs/_REVIEW_CHECKLIST.md`; do not carry `(pending)` values.

### Realist Check

- Finding 1 stays MAJOR: missing parity row blocks release traceability, but the fix is a docs-row addition or remap.
- Finding 2 stays MAJOR: open TODOs block release by the file's own text, but three of four can be closed from already-read source.
- Finding 3 stays MAJOR: upstream identifiers in implementer-facing prose are clean-room risk under Option C, but they can be moved to an evidence appendix.
- Finding 4 stays MAJOR: upstream schema/key exposure is more dangerous than function-name exposure because implementers may reproduce storage shape; still fixable by vocabulary rewrite.
- Finding 5 stays MAJOR: CL-011 is partially satisfied through input passes, but the stale line pointer proves the synthesis needs its own evidence table before release.
- Finding 6 stays MAJOR: wrong custom-error recovery would change behavior; it is a one-row correction.
- Finding 7 stays MAJOR: atomicity overstatement can create false acceptance tests; source behavior remains useful once worded correctly.
- Finding 8 stays MAJOR: resolved negative left as TODO blocks release but strengthens HUAKAI design once closed.

### Upgrade Conditions

- Upgrade to APPROVE-FOR-RELEASED after all 10 fixes are applied and `F-RATE-001` exists in the parity matrix or is intentionally remapped.
- Rerun at least 8 citation spot-checks after the evidence appendix is added.
- Reject if the author keeps any TODO in the Released artifact.
- Reject if upstream schema/key names remain as HUAKAI implementation names.

## Appendix A - Key Assumptions, Pre-Mortem, and Dependency Audit

### Key Assumptions Extracted

| Assumption | Rating | Evidence / concern |
| --- | --- | --- |
| The artifact is intended to become `docs/specs/rate-limiting.md`. | VERIFIED | Line 12 says it moves after CL review. |
| `F-RATE-001` is an existing parity feature. | FRAGILE / FALSE | `rg -n "F-RATE"` finds no parity row. |
| Option C is appropriate. | REASONABLE | DR-000 allows Option C for provider failover/account-health heuristics; cooldown state affects account health and routing. |
| Both input passes are source-verified. | VERIFIED | Inputs cite local Sub2API clone and file lines extensively. |
| Open TODOs do not block synthesis sign-off. | REASONABLE | The file says that, but this review is for release readiness. |
| Open TODOs do not block Released spec. | FALSE | Line 288 explicitly says they do block Released spec. |
| Custom error code recovery is account cooldown. | FRAGILE / FALSE | The same synthesis line 51 and Claude input describe disable behavior. |
| Cascade clearing is atomic in Sub2API. | FRAGILE / FALSE | Source uses multiple sequential calls beyond the first repository update. |

### Pre-Mortem

Assume this synthesis was released exactly as written and failed. Specific failure scenarios:

1. Implementer copies upstream credential keys and state fields into HUAKAI schema.
   - Covered by plan? No.
   - Finding: Major Findings 3 and 4.

2. Release manager cannot map acceptance tests to the parity matrix because `F-RATE-001` has no row.
   - Covered by plan? No.
   - Finding: Major Finding 1.

3. Implementer treats custom error codes as recoverable cooldowns instead of terminal account errors.
   - Covered by plan? Contradicted internally.
   - Finding: Major Finding 6.

4. Test writer asserts cascade clear is atomically inherited from Sub2API and builds a false source-backed acceptance test.
   - Covered by plan? No.
   - Finding: Major Finding 7.

5. Implementer blocks on TODO-4 even though the source already shows a direct update path.
   - Covered by plan? No.
   - Finding: Major Finding 8.

6. Reviewer later fails CL-011 because source citations are buried in input docs and one line pointer is stale.
   - Covered by plan? No.
   - Finding: Major Finding 5.

7. One-api rate-limit candidates get silently omitted or added without source verification because TODO-3 is open-ended.
   - Covered by plan? No.
   - Finding: Major Finding 2.

### Dependency Audit

| Dependency | Status | Notes |
| --- | --- | --- |
| Local Sub2API clone exists. | PASS | Commit matches `b0a2252ed19c3720e6adafde6083e64fbac2efa9`. |
| Sub2API license row exists. | PASS | `E-LIC-001` in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`. |
| LiteLLM license row exists. | PASS | `E-LIC-005` in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`. |
| Input Claude pass exists. | PASS | `docs/decompositions/sub2api/rate-limiting-source-verified.md`. |
| Input Codex pass exists. | PASS | `docs/decompositions/_cross-cutting/rate-limiting-codex.md`. |
| Feature parity row exists. | FAIL | No `F-RATE-001` row found. |
| Open TODOs are resolved. | FAIL | Four TODOs remain. |
| Source citations are self-contained in synthesis. | PARTIAL | Many claims rely on input-pass section references. |
| Release clean-room vocabulary is ready. | FAIL | Upstream identifiers and key names remain. |

### Self-Audit

- Major Finding 1 confidence: HIGH. Could author refute with context? No, unless the parity matrix is updated after this review.
- Major Finding 2 confidence: HIGH. Could author refute with context? No, the file says TODOs block Released spec.
- Major Finding 3 confidence: HIGH. Could author refute with context? Partially only by saying this is not the Released form; kept as release blocker, not source falsification.
- Major Finding 4 confidence: HIGH. Could author refute with context? No, exact upstream key names are present.
- Major Finding 5 confidence: HIGH. Could author refute with context? No for the stale line reference; yes for inherited citations generally, so verdict is PARTIAL not FAIL.
- Major Finding 6 confidence: HIGH. Could author refute with context? No, internal contradiction is visible.
- Major Finding 7 confidence: HIGH. Could author refute with context? No, source calls are sequential outside the first repository update.
- Major Finding 8 confidence: HIGH. Could author refute with context? Only if another wrapper transaction is always present, but the repository method itself has no explicit row-lock claim. The TODO should still close as a verified source note.

## §5 - Owner-Facing Chinese Summary

最终结论：`rate-limiting-synthesis.md` 可以 `APPROVE-WITH-FIXES`，但不能直接移动到 `docs/specs/rate-limiting.md` 并标记 Released。
主要阻塞是 `F-RATE-001` 还没有进入 parity matrix、文件保留 4 个明确的 Open TODO、Released 版本正文仍暴露 Sub2API 的函数名和字段/配置键名，并且至少一个源代码行号引用已经过期。
好消息是核心 Sub2API 行为大多能在本地源码中验证，问题主要是 release gate 和 clean-room 规格化，不需要删除功能，也没有发现新的功能缩水。
下一步应先补 parity row、关闭 TODO、把源代码标识符移动到 evidence appendix 或改写为 HUAKAI 术语，然后重新做至少 8 条 citation spot-check。
