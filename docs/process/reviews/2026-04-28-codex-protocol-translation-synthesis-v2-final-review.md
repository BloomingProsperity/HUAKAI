# Codex Final Reviewer-Lane Report - F-PROTO-002 Protocol Translation Synthesis v2

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane |
| Review date | 2026-04-29 |
| Requested filename date | 2026-04-28 |
| Artifact reviewed | `docs/decompositions/_cross-cutting/protocol-translation-synthesis.md` |
| Gate | CL-001..CL-011 strict review against `docs/specs/_REVIEW_CHECKLIST.md` |
| Verdict | APPROVE-WITH-FIXES |
| Local Sub2API source | `.omc/reference-src/sub2api` at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Local Portkey source | `.omc/reference-src/portkey-gateway` at `351692fd9236af222168134b416924fae0bdba23` |

## Review Protocol Notes

- Pre-commitment prediction 1: v2 would fix the prior F-PROTO-001/F-PROTO-002 identity error but leave old input-file residue.
- Actual: confirmed. The synthesis header now says `F-PROTO-002`, but the Sub2API input file still names `F-PROTO-001`; the synthesis mostly quarantines that as provenance.
- Pre-commitment prediction 2: citation quality would be uneven because the synthesis carries claims but few direct file:line references.
- Actual: confirmed. Several claims are inherited correctly from input passes, but some release-facing claims have no direct source line and one boundary claim is source-inaccurate.
- Pre-commitment prediction 3: Portkey license metadata might be stale because prior files used several reference projects in parallel.
- Actual: confirmed. The synthesis maps Portkey to `E-LIC-008`, but the ledger says `E-LIC-008` is Envoy and Portkey is `E-LIC-006`.
- Pre-commitment prediction 4: HUAKAI-DESIGN labels would be better than v1 but one design-vs-source boundary would remain blurred.
- Actual: confirmed. Most HUAKAI additions are labeled, but `signature_delta`, request-side field loss, and `length` finish reason wording still blur source behavior and HUAKAI improvements.
- Pre-commitment prediction 5: test numbering would have a minor bookkeeping problem after renumbering from AT-PROTO-001.
- Actual: confirmed. Section 11 title says `AT-PROTO-002-01..15` but the list includes `AT-PROTO-002-16`.
- Review mode: stayed THOROUGH. I found no CRITICAL issue and fewer than three MAJOR issues after self-audit; adversarial escalation was considered but not warranted.
- Self-audit result: no low-confidence scored findings remain in CRITICAL/MAJOR. Speculative concerns were moved to Open Questions.
- Realist check result: defects are release blockers but bounded text/spec fixes. No shipped code, data loss, security breach, or financial impact is created by this artifact as-is.

## §1 - CL-001..011 Verdict Matrix

| Check | Verdict | One-line justification |
| --- | --- | --- |
| CL-001 | PARTIAL | Upstream source file/function names remain in provenance and verified-resolution prose, especially `chatcompletions_to_responses.go`, `responses_to_anthropic_request.go`, and Sub2API call-ID helper names inherited from input context. |
| CL-002 | PASS | I found no copied upstream table, column, or migration names. `Usage Record`, `Route`, and `Provider Account` align with HUAKAI domain vocabulary. |
| CL-003 | PASS | No upstream UI component names, CSS class names, or dashboard component paths appear. |
| CL-004 | PASS | No long upstream documentation sentence is copied verbatim. The file uses behavioral paraphrase and short common technical terms. |
| CL-005 | PARTIAL | The hub-and-spoke choice is stated as a guarantee, but section 9.1 contains code-shaped adapter-interface pseudocode that should be softened before Released. No direct upstream pseudocode copy was found. |
| CL-006 | FAIL | `protocol-translation-synthesis.md:10` cites Portkey as `E-LIC-008`, but `docs/07_REFERENCE_EVIDENCE_LEDGER.md:20` says Portkey is `E-LIC-006`; `E-LIC-008` is Envoy at line 22. New API `E-NAI-003` is referenced at synthesis line 20 but not listed in `Sources`. |
| CL-007 | PASS | `protocol-translation-synthesis.md:7` says Option B; F-PROTO-002 is not an Option C carve-out in DR-000/checklist terms. |
| CL-008 | PASS | `F-PROTO-002` exists and matches the synthesis scope at `docs/03_FEATURE_PARITY_MATRIX.md:64`. |
| CL-009 | PARTIAL | The file says all TODOs are closed, but source-inaccurate or uncited behavior claims remain. These must either be fixed or moved into Open Questions before release. |
| CL-010 | PASS | No external source URL appears in implementer-relevant sections. Local paths and local doc links are present. |
| CL-011 | FAIL | The synthesis inherits many correct citations, but not every behavior claim has a direct or inherited file:line citation; at least one `length` finish-reason boundary claim is contradicted by source. |

Detailed CL notes:

- CL-001 is not a clean-room contamination finding by itself because this file is still a decomposition synthesis.
- It is still not clean enough to move into `docs/specs/protocol-translation.md` without removing upstream names from implementer-facing sections.
- CL-006 is a hard metadata failure: the wrong license row points reviewers to Envoy instead of Portkey.
- CL-011 is the main evidence-quality failure. The synthesis is much better than the prior rejected version, but "source-verified" still overstates what is directly cited in this file.
- CL-009 is partial rather than fail because the explicit TODO list is closed; the remaining issue is hidden unresolved evidence, not an honest Open Questions section with release holds.

## §2 - Spot-Check Log

Spot-check method:

- I sampled Sub2API and Portkey claims across architecture, stream state, terminal behavior, tool IDs, request transformation, usage/finish reasons, and Bedrock handling.
- I used `rg -n` and line-window reads against local cloned source under `.omc/reference-src/`.
- Verdict meanings:
- PASS: the source supports the claim as written.
- FAIL: the source exists but contradicts or materially narrows the claim.
- MISSING: the synthesis makes a behavior claim without adequate file:line citation in the synthesis or inherited input.

### Spot-check 01 - Sub2API canonical intermediate / hub-and-spoke

- Synthesis claim: Sub2API uses a canonical intermediate and HUAKAI keeps hub-and-spoke.
- Inherited evidence: Sub2API input cites gateway chained conversion and apicompat paths in `protocol-translation-source-verified.md:17-36`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/chatcompletions_to_responses.go:18` defines `ChatCompletionsToResponses`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_anthropic_request.go:13` defines `ResponsesToAnthropicRequest`.
- Verdict: PASS.
- Release note: implementer spec should say HUAKAI canonical model, not OpenAI Responses as implementation instruction.

### Spot-check 02 - Stateful Anthropic SSE converter

- Synthesis claim: Sub2API uses a stateful streaming converter.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:168-187` dispatches event types through a state object.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:138-142` has `CompletedSent` and `OutputIndex` in stream state.
- Verdict: PASS.

### Spot-check 03 - Unknown Anthropic event silently drops

- Synthesis claim: unknown event/block/delta types are Sub2API-verified silent drops that HUAKAI improves with typed warnings.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:185-186` returns `nil` for the event switch default.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:342-347` returns `nil` for `signature_delta` and falls through to `nil` for unknown delta types.
- Verdict: PASS.
- Release note: the spec should keep "source drops unknown elements" but remove upstream function names.

### Spot-check 04 - Synthetic terminal events / idempotent finalization

- Synthesis claim: Sub2API emits synthetic terminal events when upstream EOF lacks `message_stop`, guarded against double emission.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:190-193` names finalization for stream-ended-without-message-stop and checks `CompletedSent`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:403-419` returns nil if completed and sets `CompletedSent` after emitting completion.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:170-178` makes Chat finalization idempotent with `Finalized`.
- Verdict: PASS.

### Spot-check 05 - OutputIndex monotonic increment

- Synthesis claim: `OutputIndex` increments monotonically and preserves interleaving order.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:438` increments `state.OutputIndex`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:441-442` emits the done event with `OutputIndex: state.OutputIndex - 1`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:133` maps Responses output indices to Chat tool indices.
- Verdict: PASS.

### Spot-check 06 - Tool-call ID translation format

- Synthesis claim: Sub2API converts Anthropic `toolu_<hex>` to Responses `fc_toolu_<hex>` and back.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses.go:287-293` states an Anthropic tool ID becomes a Responses API function-call ID starting with `fc_`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses.go:296-304` strips `fc_` when the remainder has `toolu_` or `call_`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_anthropic_request.go:311-320` strips `fc_` or synthesizes `toolu_` when needed.
- Verdict: PASS.
- Release note: the exact upstream helper names and prefixes should be evidence-only; HUAKAI canonical should use local neutral IDs.

### Spot-check 07 - `length` finish reason at Responses-to-Chat boundary

- Synthesis claim: `Sub2API drops length finish_reason at Responses→Chat boundary`.
- Grep evidence contradicting universal wording: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:101-105` maps `incomplete` plus `max_output_tokens` to Chat `length`.
- Grep evidence contradicting universal wording: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:310-314` does the same in the streaming completion handler.
- Grep evidence for the real loss point: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:413-418` emits streaming completion with hardcoded `completed` and no incomplete details.
- Verdict: FAIL.
- Required correction: say streaming Anthropic-to-Responses loses the max-token stop signal before Chat conversion; do not say the Responses-to-Chat boundary always drops `length`.

### Spot-check 08 - Request-side translator field handling

- Synthesis claim: Sub2API request-side translators silently drop unsupported fields.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/chatcompletions_to_responses.go:29-38` constructs a new Responses request from selected fields.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/types.go:352-362` includes Chat request fields such as `StreamOptions` and `Stop`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/chatcompletions_to_responses.go:18-82` does not carry `StreamOptions` or `Stop` into the output object.
- Verdict: PASS for "selected-field rebuild causes drops"; MISSING for release-grade exact citation in the synthesis.
- Required correction: cite the selected-field constructor and the omitted source fields, or state the behavior as a HUAKAI audit requirement without overclaiming exact unsupported field inventory.

### Spot-check 09 - Buffered empty response normalization

- Synthesis claim: Sub2API synthesizes an empty message item for empty upstream content.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:80-88` appends a completed empty assistant message when outputs are empty.
- Verdict: PASS.

### Spot-check 10 - Bedrock has separate stream handling

- Synthesis claim: Bedrock has its own SSE handling and does not piggyback on Anthropic-conversion state machine.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:32` defines a Bedrock stream handler.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:51-61` describes Bedrock EventStream binary format and event channel setup.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:137-151` transforms Bedrock metrics and writes SSE events by `eventType`.
- Verdict: PASS.
- Required correction: add the source lines to the synthesis if this remains in the release candidate.

### Spot-check 11 - Portkey endpoint-as-canonical fan-out

- Synthesis claim: Portkey uses endpoint-as-canonical fan-out rather than a universal Responses hub.
- Inherited evidence: Portkey input cites `src/handlers/chatCompletionsHandler.ts:21-29`, `src/handlers/messagesHandler.ts:21-29`, and `src/handlers/modelResponsesHandler.ts:17-25`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/handlers/streamHandler.ts:139-151` passes per-endpoint stream transform functions with a per-stream state object.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/index.ts:78-151` shows provider map fan-out.
- Verdict: PASS.

### Spot-check 12 - Portkey stream terminal behavior

- Synthesis claim: Portkey lacks Sub2API-style synthetic terminal finalization; Sub2API sharpens Portkey here.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/handlers/streamHandler.ts:153-169` flushes leftover buffer and breaks on upstream EOF.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/handlers/streamHandler.ts:376-380` logs stream processing errors and closes the writer.
- Verdict: PASS.
- Note: this supports keeping Sub2API-style finalization in HUAKAI.

### Spot-check 13 - Portkey `max_tokens` finish mapping

- Synthesis claim: Portkey sharpens finish reason handling compared with Sub2API.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/utils/finishReasonMap.ts:17-21` maps Anthropic `max_tokens` to OpenAI `length`.
- Verdict: PASS.

### Spot-check 14 - Sources field license rows

- Synthesis claim: `Sources` lists Sub2API `E-LIC-001` and Portkey `E-LIC-008`.
- Ledger evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:20` says `E-LIC-006` is Portkey AI Gateway, MIT.
- Ledger evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:22` says `E-LIC-008` is Envoy AI Gateway, Apache-2.0.
- Ledger evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:82` says `E-NAI-003` belongs to New API `E-LIC-002`, AGPL.
- Verdict: FAIL.
- Required correction: replace Portkey `E-LIC-008` with `E-LIC-006`; add New API `E-LIC-002 / E-NAI-003` as scope evidence or remove the `Source: New API E-NAI-003` wording.

## §3 - Findings

### Critical Findings

None.

### Major Findings

1. CL-006 fails because the Portkey license row is wrong and New API scope evidence is not represented in `Sources`.
   - Evidence: `protocol-translation-synthesis.md:10` says `Portkey ([E-LIC-008]..., MIT, commit pinned in input file)`.
   - Evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:20` says `E-LIC-006` is Portkey AI Gateway, MIT.
   - Evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:22` says `E-LIC-008` is Envoy AI Gateway, Apache-2.0.
   - Evidence: `protocol-translation-synthesis.md:20` says `Source: New API E-NAI-003`, while `docs/07_REFERENCE_EVIDENCE_LEDGER.md:82` ties `E-NAI-003` to New API `E-LIC-002`.
   - Confidence: HIGH.
   - Why this matters: release metadata would misstate the reference license basis and point reviewers to the wrong project.
   - Fix: change line 10 to cite `Portkey ([E-LIC-006]..., MIT, commit 351692fd9236af222168134b416924fae0bdba23)` and include `New API ([E-LIC-002], AGPL-3.0-or-later, behavior ledger row E-NAI-003 only; no source code used)` if line 20 remains.

2. CL-011 fails because the `length` finish-reason loss is attributed to the wrong boundary.
   - Evidence: `protocol-translation-synthesis.md:83` says `Sub2API drops length finish_reason at Responses→Chat boundary`.
   - Evidence: `protocol-translation-synthesis.md:130` repeats `Sub2API loses this` for `length` across Responses-to-Chat.
   - Source evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:101-105` maps Responses `incomplete/max_output_tokens` to Chat `length`.
   - Source evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:310-314` maps streamed terminal Responses status to Chat `length`.
   - Source evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:413-418` shows the Anthropic streaming converter emits hardcoded `completed` with no incomplete details.
   - Confidence: HIGH.
   - Why this matters: an implementer would fix the wrong adapter boundary and write the wrong acceptance test.
   - Fix: rewrite the claim as `Sub2API preserves length when a Responses terminal event carries incomplete/max_output_tokens, but the Anthropic streaming converter does not propagate max_tokens into that terminal event; HUAKAI must preserve the upstream max-token stop signal through canonical and client adapters.`

3. CL-011/CL-009 are weakened by claims marked VERIFIED without direct file:line evidence in the synthesis.
   - Evidence: `protocol-translation-synthesis.md:232` says request-side translators silently drop unsupported fields but cites only file names.
   - Evidence: `protocol-translation-synthesis.md:234` says Bedrock has its own SSE handling but cites no file or line.
   - Evidence: `docs/specs/_REVIEW_CHECKLIST.md` CL-011 requires behavior claims to cite a specific source location, and synthesis files must inherit citations from input passes.
   - Source evidence for request-side field loss exists at `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/chatcompletions_to_responses.go:29-38`, `types.go:352-362`, and `chatcompletions_to_responses.go:18-82`.
   - Source evidence for Bedrock exists at `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:32`, `51-61`, and `137-151`.
   - Confidence: HIGH.
   - Why this matters: the file repeats the exact failure mode that caused earlier rejections: source-verified claims without enough line-level evidence for a reviewer or implementer to audit.
   - Fix: add a compact Source Evidence Appendix with direct file:line entries for every "VERIFIED" item in section 12, or downgrade uncited statements to Open Questions.

### Minor Findings

1. Test scenario heading is off by one.
   - Evidence: `protocol-translation-synthesis.md:204` says `AT-PROTO-002-01..15`, but lines 225-226 include `AT-PROTO-002-15` and `AT-PROTO-002-16`.
   - Fix: change the heading to `AT-PROTO-002-01..16`.

2. Released-spec cleanup is still promised rather than done.
   - Evidence: `protocol-translation-synthesis.md:12` says the file moves after being `cleaned of source identifiers`.
   - Risk: if moved as-is, it will carry upstream file names and helper/function names into an implementer-facing spec.
   - Fix: perform the cleanup before the move; keep source identifiers only in an evidence appendix or review record.

3. The adapter interface block is code-shaped for a future implementation.
   - Evidence: `protocol-translation-synthesis.md:143-155` provides Go-style interfaces and method names.
   - Fix: convert to behavioral contract bullets in the Released spec, unless HUAKAI already has these exact local interface names approved.

4. The `protocol_loss` example uses source-specific notes in the record payload.
   - Evidence: `protocol-translation-synthesis.md:83-84` embeds notes naming Sub2API and policy internals.
   - Fix: make example Usage Record notes operator-facing and source-neutral, e.g. "upstream max-token stop signal could not be represented without downgrade" rather than naming reference projects.

5. The capability matrix lists cells but does not define how `PRESERVED`, `LOSSY`, and `UNSUPPORTED` are decided.
   - Evidence: `protocol-translation-synthesis.md:58-73` lists fields and values; section 11 later adds a property test, but no decision rule exists.
   - Fix: add a short rule: `PRESERVED` means semantically round-trippable into the client contract; `LOSSY` means accepted but downgraded/dropped with a protocol_loss entry; `UNSUPPORTED` means rejected before upstream dispatch unless route policy explicitly allows downgrade.

6. Tenant isolation appears as an invariant without source or design basis.
   - Evidence: `protocol-translation-synthesis.md:201` says every translator state object is scoped to `tenant_id`.
   - Fix: label it HUAKAI-DESIGN and tie it to gateway tenancy/security requirements, or remove it from protocol translation if owned by another spec.

## §4 - FINAL VERDICT

Verdict: APPROVE-WITH-FIXES.

Meaning:

- Do not move `protocol-translation-synthesis.md` to `docs/specs/protocol-translation.md` Status=Released as-is.
- The prior REJECT blockers are materially addressed: the feature ID is corrected to F-PROTO-002, capability matrix and `protocol_loss` are now in scope, and TODOs are nominally closed.
- The remaining defects are bounded enough for APPROVE-WITH-FIXES rather than REJECT.
- If the fixes below are not applied, the verdict downgrades to REJECT because CL-006 and CL-011 would still fail.

Required fixes before Released:

1. Fix `Sources` at `protocol-translation-synthesis.md:10`.
   - Recommended replacement:
   - `Sources | New API behavior ledger row E-NAI-003 ([E-LIC-002], AGPL-3.0-or-later, no source code used); Sub2API ([E-LIC-001], LGPL-3.0, commit b0a2252ed19c3720e6adafde6083e64fbac2efa9); Portkey ([E-LIC-006], MIT, commit 351692fd9236af222168134b416924fae0bdba23).`

2. Fix the line 20 scope wording.
   - Recommended replacement:
   - `Source basis: New API ledger row E-NAI-003 identifies the parity surface; Sub2API and Portkey source passes provide implementation-pattern evidence.`

3. Replace the `length` example at `protocol-translation-synthesis.md:83`.
   - Recommended replacement:
   - `{ feature: "max_tokens_finish_reason", direction: "anthropic_stream_to_chat", verdict: LOSSY, note: "upstream max-token stop signal is not propagated through the streaming terminal event in the reference path" }`

4. Replace H5 at `protocol-translation-synthesis.md:130`.
   - Recommended replacement:
   - `H5 - max-token stop signal preserved through streaming Anthropic -> Canonical -> Chat translation; reference source preserves Chat length only when canonical terminal status already carries incomplete/max_output_tokens, but the Anthropic streaming path does not populate it.`

5. Add direct citations for request-side field handling at `protocol-translation-synthesis.md:232`.
   - Recommended replacement:
   - `Sub2API request conversion is selected-field rebuild: it constructs a new Responses request from mapped fields, while fields such as Chat stop/stream_options are present on input types but not carried into the output constructor. Evidence: chatcompletions_to_responses.go:29-38; types.go:352-362; chatcompletions_to_responses.go:18-82.`

6. Add direct citations for tool-call ID format at `protocol-translation-synthesis.md:233`.
   - Recommended replacement:
   - `Evidence: anthropic_to_responses.go:287-304 and responses_to_anthropic_request.go:311-320.`

7. Add direct citations for Bedrock handling at `protocol-translation-synthesis.md:234`.
   - Recommended replacement:
   - `Evidence: service/bedrock_stream.go:32, 51-61, 137-151, 231-307.`

8. Fix section 11 heading at `protocol-translation-synthesis.md:204`.
   - Recommended replacement:
   - `## 11. Test Scenarios (AT-PROTO-002-01..16)`

9. Before moving to `docs/specs/protocol-translation.md`, remove upstream identifiers from implementer-facing prose.
   - Scope:
   - `protocol-translation-synthesis.md:110-116`, `232-238`, and provenance lines should either be deleted from the Released spec or moved to a non-implementation evidence appendix.

10. Define capability verdict semantics near `protocol-translation-synthesis.md:58-73`.
   - Recommended addition:
   - `PRESERVED = accepted and emitted with equivalent client-visible semantics; LOSSY = accepted but downgraded or omitted with protocol_loss; UNSUPPORTED = rejected or feature-gated before upstream dispatch unless explicit route policy allows downgrade.`

Upgrade conditions:

- Apply all 10 fixes.
- Keep HUAKAI-DESIGN labels on every non-source-derived improvement.
- Do not add new source behavior claims without file:line citation.
- After fixes, a short re-review can upgrade this to APPROVE-FOR-RELEASED.

Realist check:

- Major Finding 1 stays MAJOR: wrong license metadata is a strict release blocker, but it is a one-line correction with clear ledger evidence.
- Major Finding 2 stays MAJOR: the boundary error can misdirect implementation and tests, but it does not invalidate the desired HUAKAI improvement.
- Major Finding 3 stays MAJOR: missing line citations repeat the earlier quality failure pattern, but source evidence exists and can be inserted without redesign.
- No finding is CRITICAL because no code has shipped and no high-risk files are changed by this review.

## What's Missing

- No direct New API source/scope row in `Sources`, despite the parity row being New API-derived.
- No compact source-evidence appendix mapping synthesis claims to file:line citations.
- No explicit decision rule for capability matrix values.
- No clear boundary saying whether native Anthropic pass-through is in F-PROTO-002 or a later protocol mode.
- No ownership boundary between protocol translation state and tenant isolation/auth specs.
- No Released-spec version of the document stripped of upstream source identifiers.
- No explicit acceptance criterion for malformed stream chunks and response-too-large behavior beyond naming the failure taxonomy.

## Ambiguity Risks

- `Both requirements are HUAKAI-DESIGN improvements over Sub2API and Portkey` at line 29 can mean neither reference has any warning/loss concept, or only that neither has the exact structured matrix/Usage Record field.
  - Risk if wrong interpretation chosen: implementer may ignore partial Portkey finish-reason preservation and Sub2API terminal-state evidence.

- `Sub2API drops length finish_reason at Responses→Chat boundary` at line 83 can mean all Responses-to-Chat conversion loses length, or only one chained streaming path loses it.
  - Risk if wrong interpretation chosen: implementer fixes the Chat adapter instead of preserving the upstream stop signal earlier in the stream.

- `Bedrock has its own SSE handling but does NOT piggyback on Anthropic-conversion state machine` at line 234 can mean Bedrock never emits Anthropic-shaped events, or only that Bedrock has a separate stream decoder/writer path.
  - Risk if wrong interpretation chosen: implementation may duplicate Bedrock and Anthropic semantics unnecessarily.

- `Provider adapter registry pattern` at line 122 can mean a registry of provider modules, or a literal interface contract matching section 9.1.
  - Risk if wrong interpretation chosen: implementer may overfit to a new interface before HUAKAI has its local module boundaries.

## Multi-Perspective Notes

- Executor perspective: this is close to actionable but not release-ready. The executor needs corrected boundary wording for `length`, direct file:line citations, and source-neutral interface names.
- Stakeholder perspective: the core F-PROTO-002 parity outcomes are finally present: capability matrix, operator-visible warning, and Usage Record `protocol_loss`.
- Skeptic perspective: the "source-verified" label is still stronger than the evidence in the file. A Released spec cannot rely on reviewer memory of input files.
- Security perspective: no new direct security flaw was found, but tenant isolation and native/pass-through policy need ownership boundaries before implementation.
- Ops perspective: the proposed `protocol_loss` field and dashboard counters are useful, but the matrix semantics must be defined or operators will see inconsistent LOSSY/UNSUPPORTED labels.
- New-hire perspective: the file still reads like a synthesis memo. A new implementer would not know which upstream identifiers are evidence-only versus HUAKAI design names.

## Appendix A - Assumptions, Pre-Mortem, and Dependency Audit

### Key Assumptions Extracted

| Assumption | Rating | Evidence / concern |
| --- | --- | --- |
| This artifact is now F-PROTO-002, not F-PROTO-001. | VERIFIED | Synthesis line 6; parity row at `docs/03_FEATURE_PARITY_MATRIX.md:64`. |
| F-PROTO-002 lane mode is Option B. | VERIFIED | Synthesis line 7; checklist Option C carve-outs do not include this feature. |
| Sub2API source clone is pinned correctly. | VERIFIED | Local `git rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`. |
| Portkey source clone is pinned correctly. | VERIFIED | Local `git rev-parse HEAD` returned `351692fd9236af222168134b416924fae0bdba23`. |
| Portkey license row in synthesis is correct. | FALSE | Ledger maps Portkey to `E-LIC-006`, not `E-LIC-008`. |
| New API is only parity-scope evidence, not source-code implementation evidence. | REASONABLE | Ledger row `E-NAI-003` is README behavior evidence; synthesis does not cite New API source code. |
| Every VERIFIED synthesis claim inherits direct file:line evidence. | FRAGILE / FALSE | Section 12 has file names but lacks direct lines for request-side handling and Bedrock. |
| Responses-to-Chat always loses `length`. | FALSE | Source maps incomplete/max_output_tokens to Chat `length`; streaming loss happens earlier. |
| Capability matrix cells are self-explanatory. | FRAGILE | No decision rule distinguishes LOSSY from UNSUPPORTED. |

### Pre-Mortem

Assume the synthesis is moved to Released exactly as written and fails:

1. A later license review blocks the spec because Portkey is cited as Envoy's `E-LIC-008`.
   - Covered by plan? No.
   - Finding: Major Finding 1.

2. Implementer patches the Chat adapter to preserve `length`, but the bug persists because the upstream streaming canonical event never carried incomplete details.
   - Covered by plan? No.
   - Finding: Major Finding 2.

3. A reviewer asks for Bedrock proof and the Released spec only says "VERIFIED" without line references.
   - Covered by plan? No.
   - Finding: Major Finding 3.

4. Operators see matrix cells marked LOSSY and UNSUPPORTED inconsistently because no semantics are defined.
   - Covered by plan? Partially through AT-PROTO-002-15, but not enough.
   - Finding: Minor Finding 5.

5. An implementer copies adapter interface names from the synthesis into code before architecture boundaries are settled.
   - Covered by plan? No.
   - Finding: Minor Finding 3.

6. The Released spec keeps upstream source identifiers and fails a later CL-001 pass.
   - Covered by plan? Only by a promise in line 12.
   - Finding: Minor Finding 2.

7. Acceptance test inventory tooling expects `01..15` and misses `AT-PROTO-002-16`.
   - Covered by plan? No.
   - Finding: Minor Finding 1.

### Dependency Audit

| Dependency | Status | Notes |
| --- | --- | --- |
| `docs/specs/_REVIEW_CHECKLIST.md` read. | PASS | CL-001..011 applied. |
| Synthesis file read. | PASS | `docs/decompositions/_cross-cutting/protocol-translation-synthesis.md`. |
| Sub2API input pass read. | PASS | Still labels its own scope F-PROTO-001, but synthesis corrected final feature ID. |
| Portkey input pass read. | PASS | Source clone path is `.omc/reference-src/portkey-gateway`. |
| Prior F-PROTO review read. | PASS | Used to confirm old blockers and remediation. |
| Prior F-POOL review format read. | PASS | Used as format precedent. |
| Sub2API source clone exists. | PASS | Commit matches requested value. |
| Portkey source clone exists. | PASS | Commit matches input file. |
| License ledger has Sub2API. | PASS | `E-LIC-001`, LGPL-3.0. |
| License ledger has Portkey. | PASS | `E-LIC-006`, MIT. |
| Synthesis uses correct Portkey row. | FAIL | Uses `E-LIC-008`, Envoy. |
| Parity matrix has F-PROTO-002. | PASS | Row exists at line 64. |

### Self-Audit

- Major Finding 1 confidence: HIGH. Could author refute with context? No; ledger lines are explicit.
- Major Finding 2 confidence: HIGH. Could author refute with context? No for the exact wording; source directly maps incomplete to `length`.
- Major Finding 3 confidence: HIGH. Could author refute with context? Only by saying citations exist in another unpublished pass; the requested artifact does not carry them.
- Minor Finding 1 confidence: HIGH. Could author refute with context? No; numbering mismatch is visible.
- Minor Finding 2 confidence: MEDIUM. Could author refute with process context? Yes, if cleanup is guaranteed during move. Kept as minor because it is not yet a Released spec.
- Minor Finding 3 confidence: MEDIUM. Could author refute with architecture intent? Yes. Kept as minor/style-risk, not a blocker.

## §5 - Owner-Facing Chinese Summary

最终结论：`protocol-translation-synthesis.md` 可以 `APPROVE-WITH-FIXES`，但不能 as-is 移到 `docs/specs/protocol-translation.md` 并标为 Released。
这版已经修正了最关键的身份错误：它现在正确挂到 `F-PROTO-002`，也补上了 capability matrix 和 `protocol_loss` 的核心范围；没有发现功能缩水。
必须先修三类问题：`Sources` 里 Portkey 的 license row 写错了，`length finish_reason` 的丢失边界写错了，若干 `VERIFIED` 行还缺少可复核的 source file:line citation。
clean-room 风险主要来自 Released 前还没有清掉上游函数/文件名；安全风险不是新增实现风险，而是 tenant isolation、native pass-through、未知事件处理这些边界还要在正式 spec 中写清楚。
Owner 不需要确认删功能；下一步建议让 specifier 按上面 10 条 fix 精确修正，然后做一次短复核即可升级为 APPROVE-FOR-RELEASED。
