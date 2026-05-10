# Codex Full-Chain Renew Review — 2026-05-09 P-0 + P-0c plan

**Date**: 2026-05-09
**Lane**: reviewer (codex full-chain pass on claude PM-orchestrator's day-of-work)
**Commits in scope**: 0f3d9a8 / 4ad7fc0 / b7d9079
**Triggered by**: Owner 2026-05-09 quote "让 codex 全程，全部 renew 一下"

## TL;DR

- **1 HIGH / 8 MED / 5 LOW**
- **Verdict: Block P-0c dispatch until H1 clean-room sanitation is committed.** After H1 is fixed, proceed with P-0c-A/B/C; the P-0c technical plan is mostly sound.
- Validation run: `cd backend && go test ./...` passed on 2026-05-10T15:21Z. Running `go test` from repo root is invalid because the Go module root is `backend/`.

## Q1 — Rules update review

- 自洽性: **concern**. #12 makes source reading mandatory for capability/mechanism/differentiation/algorithm/parity claims and requires `<repo>@<sha>:<file>:<line-range>` citations (CLAUDE.md:61-75). AGENTS mirrors the trigger matrix and 30-day stale-citation rule (AGENTS.md:432-467). That direction is correct.
- 边界清晰度: 三维 taxonomy is usable. Architecture/algorithm/ecosystem are concretely defined with examples (CLAUDE.md:89-96). Boundary overlap is acceptable if artifacts explicitly list multiple dimensions.
- 与 #11 配合: **MED concern**. The clean-room prompt still prohibits file paths in prose and says paths only belong in the tail block (AGENTS.md:379-384), while #12 requires file:line citations per claim (CLAUDE.md:69). Amend #11 to allow minimal source-location citations, while still banning copied identifiers, comments, raw code, and source structure as design.
- Vendoring: mostly sound. MIT/Apache vendoring requirements preserve LICENSE/NOTICE/MODIFICATIONS and isolation directories (CLAUDE.md:98-103); LGPL/AGPL vendoring remains forbidden (CLAUDE.md:105). Add one sentence that every "official SDK" must be license-checked before vendoring.
- First-cite recency: reasonable. 90-day project activity for first citation plus 30-day stale-citation re-fetch are different checks, not a contradiction (CLAUDE.md:76-80, AGENTS.md:465-467).
- AGENTS vs CLAUDE: no conceptual conflict on source-must-read triggers (AGENTS.md:427-441), but the #11 file-path wording must be reconciled with #12.
- 评级: **concern**, not fail.

## Q2 — Synthesis decisions (3-direction + next-pivot)

- 综合判断: mostly right. The three-direction synthesis correctly stops claiming PASR as first-mover and reframes it as finer-grained score/miss-demotion/cross-account intent (docs/plans/2026-05-09-three-directions-synthesis.md:16-24, 90-104).
- 方向 1/2/3: rejecting direction 2 as gateway scope is reasonable because the synthesis records idempotency, SSE merge, half-failure, and 1:N billing problems (docs/plans/2026-05-09-three-directions-synthesis.md:72-75). Direction 3 being deferred into reactive bridge is also reasonable given the documented economic-risk reasoning (docs/plans/2026-05-09-three-directions-synthesis.md:85-88).
- Next-pivot C vs Owner axis-3 override: both are defensible. The next-pivot doc correctly identified PASR multi-vendor repair as the best immediate tactical commit (docs/plans/2026-05-09-next-pivot-synthesis.md:11-17, 47-56). Owner's later axis-3 override is strategically defensible because HCSF canonical synthesis identifies L3+L4 PMF as capability-fidelity dependent (docs/plans/2026-05-09-hcsf-canonical-synthesis.md:54-64, 66-75).
- PASR synergy: not lost. P-0 schema preserves PASR hooks through `RequestMeta.SessionHash` and `CacheControlNode.LocalityHint` (backend/internal/proto/request_meta.go:59-60, backend/internal/proto/capability_cache.go:16-18, 38-40).
- 评级: **pass with one clean-room blocker inherited from source-read artifacts**.

## Q3 — HCSF canonical + implementation plan

- 14 capability 完整性: core set is good, but wording is confusing. The approved plan says 14 families and splits file/image/audio/video (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:38-64). Code implements 15 concrete node kinds because `tool_use` and `tool_result` are separate concrete nodes under one tool family (backend/internal/proto/capability_graph.go:3-10, 31-48). Keep "14 product families / 15 concrete node kinds" everywhere.
- Hosted tools gap: **MED**. `computer_use` and `mcp_server` exist (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:49, 56), and generic `tool_use/tool_result` exists (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:45), but there is no explicit subtype for hosted tools such as web search / code interpreter. P-1 should add a `hosted_tool_kind` or equivalent projection/native-required field rather than silently folding these into generic tools.
- Anthropic-rich primary: acceptable, not over-tilted. The canonical synthesis keeps OpenAI-compatible storefront as default market entry and Anthropic-native as side-entry (docs/plans/2026-05-09-hcsf-canonical-synthesis.md:70-75, 125-133), while Tier-A includes OpenAI Chat and OpenAI Responses (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:70-76).
- BufferedResponse + StreamEvents shape: conceptually fine, but implementation has an INV-6 bug listed below. Native passthrough doc drift exists: synthesis says native passthrough is "not in envelope" (docs/plans/2026-05-09-p0-schema-spec-synthesis.md:87-92), while implementation adds `RequestMeta.NativePassthrough` (backend/internal/proto/envelope.go:15, backend/internal/proto/request_meta.go:62-63). Treat it as an audit shell and update docs.
- 12-15 weeks: optimistic. The plan itself admits true-account smoke is Owner-local and hard-gating (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:177-184). It is plausible as a phase target, not as a committed delivery guarantee.
- 评级: **concern**.

## Q4 — P-0 code implementation

- tagged-union: implemented as planned. `CapabilityNode` has `Kind` plus nullable payload pointers (backend/internal/proto/capability_graph.go:86-123), and `validateNodeTaggedUnion` enforces exactly one matching payload (backend/internal/proto/envelope_validate.go:189-250).
- ValidateEnvelope: covers the advertised INV set partially. Version, RequestMeta, shape, node union, dangling edges, mutex duplicate, data-retention enum, mid-stream policy, and extension prefix are present (backend/internal/proto/envelope_validate.go:34-63, 66-383).
- Missing INV coverage beyond known M1/M2/M3/M4:
  - **MED**: INV-6 uses `len(env.StreamEvents) > 0`, so `stream_events: []` plus `buffered_response` passes despite the nullable-shape contract saying non-nil StreamEvents is replay shape (backend/internal/proto/envelope.go:10-17, backend/internal/proto/envelope_validate.go:97-105).
  - **MED**: edge-level `ProtocolLoss` is declared on `CapabilityEdge` but not checked for silent-drop entries (backend/internal/proto/capability_graph.go:171-172, backend/internal/proto/envelope_validate.go:142-176).
  - **MED**: edge `Type` and `ID` are documented/centralized but not validated against `AllEdgeTypes` or non-empty ID (backend/internal/proto/capability_graph.go:125-149, 151-169; backend/internal/proto/envelope_validate.go:142-176).
  - **LOW/MED**: `StreamReady` is documented required but no enum check exists (backend/internal/proto/capability_graph.go:50-57, 100-101; backend/internal/proto/envelope_validate.go:189-211).
- Alias sunset: sonnet M4 is real. `type HCSF = HCSFEnvelope` keeps compile compatibility and zero-value `&HCSF{}` invalid by version (backend/internal/proto/proto.go:13-19), but no production adapter guard calls validation yet. OpenAI/Gemini still return zero envelopes on non-streaming success (backend/internal/proto/openai_sse.go:148-155, backend/internal/proto/gemini_sse.go:103-110).
- protocol_loss v0.4 compatibility: acceptable but needs P-2 sunset discipline. v0.3 fields remain explicitly for old adapters (backend/internal/proto/protocol_loss.go:16-25, 58-70), and `IsSilentDrop` catches empty reason/note/verdict/code (backend/internal/proto/protocol_loss.go:73-77). The risk is silent migration if old fields remain after P-2 without a deprecation gate.
- Fixtures: count and breadth are good. There are 35 JSON fixtures, including 15 envelope minimal fixtures, 5 responses, 5 events, 5 regressions, and 5 edge cases. The fixture walker validates every JSON and round-trips non-negative fixtures (backend/internal/proto/fixtures_test.go:61-88, 90-119).
- 评级: **concern**, tests green but validator gaps remain.

## Q5 — P-0c follow-up plan

- M1/M2/M3: reasonable. The synthesis correctly targets missing `Capability`/`Verdict` validation, INV-13 for StreamPlan mode, and broad round-trip coverage (docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:19-31, 44-52). Codex's lane adds enum validation details and 15 concrete node coverage (docs/plans/2026-05-09-p0c-followup-plan-codex.md:23-33, 41-47).
- M4: I agree with Codex (b)+(a). Forwarder currently operates on `SSEEvent -> canonical events -> client chunks`, not full HCSF envelopes (backend/internal/gateway/forwarder.go:185-242, 293-298), so a centralized forwarder entry validate is premature. A production version guard plus debug full validation is the right P-0c shape (docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:34-42).
- D-FailLoud: not over-coupled; it is required. A Version guard would otherwise immediately expose OpenAI/Gemini zero-envelope fake success (docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:54-60). Prefer fail-loud error first unless the executor can produce a non-fabricated `RequestMeta`; the Codex lane notes the current response adapter signature lacks metadata for full `ValidateEnvelope` (docs/plans/2026-05-09-p0c-followup-plan-codex.md:58-59).
- INV-13: no numbering conflict. Existing INV-1..12 are listed in the P-0 schema synthesis (docs/plans/2026-05-09-p0-schema-spec-synthesis.md:189-204). StreamPlan mode validity is distinct from INV-6 shape and INV-11 mid-stream policy.
- Sequencing: P-0c-A/B should precede P-1 code. P-0c-C can be close behind, but do not let P-1 add more envelope consumers before M1/M2/M3 land (docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:89-92).
- 评级: **pass after H1 clean-room sanitation**.

## Q6 — Overall blindspots

- Sonnet dependence: real. The implementation synthesis explicitly records that Codex review tooling failed and sonnet served as backup (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:177-185; docs/research/2026-05-09-uncommitted-changes-review-sonnet.md:1-6). This review exists to close that gap.
- Cross-discuss: plans generally followed parallel-draft structure: implementation, schema spec, next-pivot, and P-0c all list Claude/Sonnet plus Codex lanes (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:4-7; docs/plans/2026-05-09-p0-schema-spec-synthesis.md:4-7; docs/plans/2026-05-09-next-pivot-synthesis.md:3-7; docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:4-7). The gap is per-commit Codex review enforcement, not planning absence.
- Clean-room: **HIGH issue found**. See H1. Some source-read artifacts contain upstream identifiers or code-shaped snippets despite the hard prohibition (AGENTS.md:379-388).
- Commercial sustainability: HCSF v0.4 + axis 3 can plausibly target L3+L4, but PMF proof is still inferred. The canonical synthesis admits no real customer interviews (docs/plans/2026-05-09-hcsf-canonical-synthesis.md:190-197). The monthly annualized inference spend metric is useful but aggressive; keep mock/smoke/real labels mandatory (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:99-109).

## HIGH severity findings

### H1 — Source-read artifacts leak upstream identifiers / code-shaped snippets, violating clean-room hard prohibitions

AGENTS clean-room lane guard forbids copying upstream function names, struct fields, comments, raw code blocks, and distinctive identifiers (AGENTS.md:379-388). Several committed source-read/reverify artifacts still contain such material:

- LGPL/AGPL-adjacent identifier leakage appears in the sub2api/new-api source-read summary (docs/research/2026-05-09-source-read-sub2api-newapi.md:43-52).
- LiteLLM source-read summary includes upstream class/function-style identifiers and detailed function-level citations in the behavior body (docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md:31-49, 208-218).
- Helicone reverify includes a code-shaped handler-chain snippet rather than only behavior paraphrase (docs/research/2026-05-09-helicone-chain-reverify.md:26-49).

Impact: this does not prove copied implementation entered HUAKAI code, but it contaminates the committed research/planning layer that future implementation will rely on. In this project, that is HIGH because clean-room defense depends on the artifacts.

Required fix:

1. Sanitize the named research docs: replace upstream identifiers with generic HUAKAI-side labels, remove code-shaped snippets, and keep only behavior summaries plus source-location evidence.
2. Update the #11/#12 wording conflict so file:line citations are allowed as evidence, but upstream identifiers/code/comments remain forbidden.
3. Re-run a reviewer-lane check over the sanitized docs before dispatching new code from those artifacts.

## MED severity findings

### M1 — #11 file-path prohibition conflicts with #12 per-claim citation requirement

Evidence: AGENTS bans file paths in prose except tail block (AGENTS.md:379-384), while CLAUDE #12 requires `<repo>@<sha>:<file>:<line-range>` per claim (CLAUDE.md:69). This must be reconciled or future agents will be forced to violate one rule.

### M2 — INV-6 allows ambiguous buffered response + empty stream replay

Evidence: envelope shape treats non-nil `StreamEvents` as replay (backend/internal/proto/envelope.go:10-17), but validator uses `len(env.StreamEvents) > 0` (backend/internal/proto/envelope_validate.go:97-105). Add a test with `BufferedResponse != nil` and `StreamEvents: []CanonicalEvent{}`.

### M3 — Edge-level protocol loss can silently drop

Evidence: `CapabilityEdge.ProtocolLoss` exists (backend/internal/proto/capability_graph.go:171-172), but edge validation never checks it (backend/internal/proto/envelope_validate.go:142-176). Extend INV-7 coverage to edge-level entries.

### M4 — Edge ID/type validation is missing despite enum definitions

Evidence: edge ID/type are documented as required and five legal types are centralized (backend/internal/proto/capability_graph.go:125-149, 151-169), but validator only checks From/To and mutex duplicate (backend/internal/proto/envelope_validate.go:142-176).

### M5 — ProviderProjection required-field validation is missing

Evidence: `Capability` and `Verdict` are documented required (backend/internal/proto/projection.go:20-35), but current validation only checks non-preserved loss, silent-drop, and native path (backend/internal/proto/envelope_validate.go:303-328). This matches P-0c M1.

### M6 — Alias sunset guard is not yet real

Evidence: alias comment claims zero envelopes will fail validation (backend/internal/proto/proto.go:13-19), but OpenAI/Gemini non-streaming paths return zero `&HCSF{}` with nil error (backend/internal/proto/openai_sse.go:148-155, backend/internal/proto/gemini_sse.go:103-110). This matches P-0c M4/D-FailLoud.

### M7 — Hosted tool taxonomy is too implicit

Evidence: the capability plan has generic `tool_use/tool_result`, `computer_use`, and `mcp_server` (docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:45, 49, 56), while HCSF canonical explicitly cites vendor-only hosted/tool capabilities as a reason not to flatten to OpenAI-only (docs/plans/2026-05-09-hcsf-canonical-synthesis.md:15-18). Add hosted-tool subtyping or native-required projection in P-1.

### M8 — PMF/spend metric is a strategic hypothesis, not yet evidence

Evidence: synthesis selects L3+L4 and monthly annualized inference spend (docs/plans/2026-05-09-hcsf-canonical-synthesis.md:54-64, 160-167), but also admits no real customer interviews and possible benchmark overgeneralization (docs/plans/2026-05-09-hcsf-canonical-synthesis.md:190-197). Keep mock/smoke/real labels mandatory.

## LOW severity findings

### L1 — "14 capability" wording hides 15 concrete node kinds

Evidence: plan line lists `tool_use` and `tool_result` separately inside "14 capability families" (docs/plans/2026-05-09-p0-schema-spec-synthesis.md:21-24); code correctly uses 15 concrete kind values under 14 product families (backend/internal/proto/capability_graph.go:3-10, 31-48).

### L2 — Native passthrough envelope wording drift

Evidence: schema synthesis says native passthrough is not in envelope (docs/plans/2026-05-09-p0-schema-spec-synthesis.md:87-92), while implementation records `NativePassthrough` in `RequestMeta` (backend/internal/proto/request_meta.go:62-63). Clarify "audit/projection shell only; route remains native."

### L3 — `NewEmptyEnvelope` comment says legal envelope but RequestMeta is empty

Evidence: constructor comment says minimal legal envelope (backend/internal/proto/envelope.go:57-59), but `RequestMeta` starts empty and validation requires RequestID/ClientProtocol/ProtocolFamily/Model/IngressPath (backend/internal/proto/envelope.go:61-63, backend/internal/proto/envelope_validate.go:77-94).

### L4 — Test command should record backend module root

Evidence: repo root lacks `go.mod`; `cd backend && go test ./...` passes. Future commit notes should avoid ambiguous root-level `go test ./...`.

### L5 — P-0c dispatch plan still routes implementation/review through sonnet

Evidence: synthesis dispatch table assigns executor and reviewer to sonnet (docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:94-105). After this renew pass, P-0c still needs normal Codex per-commit review before commit (AGENTS.md:480-492).

## Recommended path

1. **Do not dispatch P-0c-A yet.** First land a sanitation patch for H1 in the committed source-read/reverify artifacts and clarify #11/#12 citation wording.
2. After H1 is fixed, dispatch **P-0c-A** with M1 + M2 plus the extra INV-6 empty-slice check and edge ID/type/loss validation.
3. Then P-0c-B broadens INV-1 to all 15 concrete node kinds and edge/projection/loss combinations.
4. Then P-0c-C adds production Version guard + debug validation helper, and changes OpenAI/Gemini non-streaming fake success to fail-loud unless valid metadata is available.
5. Run `cd backend && go test ./...`, stage, and run `codex exec review --uncommitted --full-auto` before commit.

## Source files read

- `CLAUDE.md:55-115`
- `AGENTS.md:367-420`, `AGENTS.md:423-519`
- `docs/templates/codex-reviewer.md:1-77`
- `docs/plans/2026-05-09-three-directions-synthesis.md:1-201`
- `docs/plans/2026-05-09-next-pivot-synthesis.md:1-135`
- `docs/plans/2026-05-09-hcsf-canonical-synthesis.md:1-212`
- `docs/plans/2026-05-09-hcsf-v04-implementation-synthesis.md:1-207`
- `docs/plans/2026-05-09-p0-schema-spec-synthesis.md:1-255`
- `docs/plans/2026-05-09-p0c-followup-plan-synthesis.md:1-131`
- `docs/plans/2026-05-09-p0c-followup-plan-codex.md:1-146`
- `docs/plans/2026-05-09-p0c-followup-plan-claude.md:1-235`
- `docs/research/2026-05-09-uncommitted-changes-review-sonnet.md:1-115`
- `docs/research/2026-05-09-source-read-sub2api-newapi.md:1-300`
- `docs/research/2026-05-09-source-read-oneapi-portkey-litellm.md:1-333`
- `docs/research/2026-05-09-source-read-helicone-envoy-allapihub.md:1-285`
- `docs/research/2026-05-09-helicone-chain-reverify.md:1-184`
- `backend/internal/proto/envelope.go:1-93`
- `backend/internal/proto/capability_graph.go:1-173`
- `backend/internal/proto/envelope_validate.go:1-389`
- `backend/internal/proto/envelope_test.go:1-261`
- `backend/internal/proto/fixtures_test.go:1-119`
- `backend/internal/proto/proto.go:1-61`
- `backend/internal/proto/protocol_loss.go:1-77`
- `backend/internal/proto/request_meta.go:1-122`
- `backend/internal/proto/capability_cache.go:1-41`
- `backend/internal/proto/stream_plan.go:1-69`
- `backend/internal/proto/projection.go:1-51`
- `backend/internal/proto/openai_sse.go:142-155`
- `backend/internal/proto/gemini_sse.go:97-110`
- `backend/internal/proto/anthropic_sse.go:83-89`
- `backend/internal/proto/bedrock_eventstream.go:68-76`
- `backend/internal/gateway/forwarder.go:185-242`, `backend/internal/gateway/forwarder.go:293-298`
- `backend/internal/proto/fixtures/**` listing and kind scan
- Git metadata: `git show 0f3d9a8 --stat`, `git show 4ad7fc0 --stat`, `git show b7d9079 --stat`, `git diff 3f3bb0d..b7d9079 -- backend/internal/proto/ --stat`

## Tail block (per AGENTS.md template)

Source files read: HUAKAI internal docs/code only; no `~/refs/` upstream source read in this reviewer lane. See `Source files read` section above.
Lane: reviewer (full-chain renew)
Agent: GPT-5 Codex (codex lane)
UTC timestamp: 2026-05-10T15:21Z
