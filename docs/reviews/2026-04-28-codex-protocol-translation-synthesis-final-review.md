# Codex Final Reviewer-Lane Report - Protocol Translation Synthesis

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane |
| Review date | 2026-04-28 |
| Artifact reviewed | `docs/decompositions/_cross-cutting/protocol-translation-synthesis.md` |
| Gate | CL-001..CL-011 strict path review |
| Requested feature | F-PROTO-001 protocol translation synthesis |
| Verdict | REJECT |
| Local Sub2API source | `.omc/reference-src/sub2api` at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Local Portkey source | `.omc/reference-src/portkey-gateway` at `351692fd9236af222168134b416924fae0bdba23` |

## Review Protocol Notes

- Pre-commitment prediction 1: the synthesis would still carry source identifiers because it explicitly says the final spec will be "cleaned of source identifiers."
- Actual: confirmed. Source function/type names remain in implementer-facing sections.
- Pre-commitment prediction 2: at least one "both references agree" convergence claim would be over-broadened.
- Actual: confirmed. Portkey does not show the same idempotent terminal guard or explicit per-event flush semantics claimed in section 2.
- Pre-commitment prediction 3: open TODOs would block release even if the synthesis itself is coherent.
- Actual: confirmed. Section 11 says the TODOs block Released spec.
- Pre-commitment prediction 4: the feature ID would need verification because prior decomposition files used both F-PROTO-001 and F-PROTO-002 language.
- Actual: confirmed and worse than expected. The current parity matrix defines F-PROTO-001 as MCP/A2A plugin bridging, while this synthesis is cross-provider request/response translation and matches F-PROTO-002.
- Review mode: escalated to ADVERSARIAL after the CL-008 feature-ID mismatch and multiple CL-011 spot-check failures. I expanded scope to parity, acceptance-test IDs, input passes, and adjacent protocol decomposition inventory.
- Self-audit result: all CRITICAL/MAJOR findings below have direct file evidence. Low-confidence concerns were moved to Open Questions.
- Realist check result: the feature-ID mismatch stays CRITICAL because releasing under the wrong capability would mis-map phase, disposition, acceptance tests, and implementer scope. The source-claim issues stay MAJOR because they are text/spec defects, not shipped code, but they directly block CL-011.

## Section 1 - CL-001..011 Verdict Matrix

| Check | Verdict | Evidence / one-line justification |
| --- | --- | --- |
| CL-001 | FAIL | The synthesis still carries upstream function/type/config identifiers in release-facing sections: `FinalizeAnthropicResponsesStream`, `FinalizeResponsesChatStream`, `toResponsesCallID`, `fromResponsesCallID`, `ProviderConfigs[provider][endpoint]`, and exact event names at `protocol-translation-synthesis.md:33-47`, `53`, `74-88`, `108`, `141-144`. |
| CL-002 | PASS | No upstream DB table names, column names, or migration filenames were found. `tenant_id` appears only as HUAKAI domain language and is already a project rule. |
| CL-003 | PASS | No upstream UI component names, class names, or dashboard layout terms were found. |
| CL-004 | PASS | No copied upstream README/doc prose longer than the allowed technical phrase level was found in the synthesis. |
| CL-005 | PARTIAL | The high-level HUAKAI adapter design is independent, but section 6.1 and section 6.3 are still implementation-shaped pseudocode and helper naming, and one helper behavior is source-mismatched. |
| CL-006 | PASS | `protocol-translation-synthesis.md:10` cites Sub2API `E-LIC-001` and Portkey `E-LIC-006`; both rows exist in `docs/07_REFERENCE_EVIDENCE_LEDGER.md:15` and `:20`. |
| CL-007 | PASS | `protocol-translation-synthesis.md:7` uses Option B. Protocol translation is not one of the DR-000 Option C carve-outs. |
| CL-008 | FAIL | `protocol-translation-synthesis.md:6` says `F-PROTO-001`, but `docs/03_FEATURE_PARITY_MATRIX.md:59` defines F-PROTO-001 as MCP/A2A plugin bridging. The synthesis content matches `F-PROTO-002` at `docs/03_FEATURE_PARITY_MATRIX.md:64`. |
| CL-009 | FAIL | `protocol-translation-synthesis.md:190-196` leaves TODO-1..TODO-3 open and explicitly says they block Released spec. CL-009 says Open Questions are an implementer-lane hold signal. |
| CL-010 | PASS | No upstream source URL appears in Normal Path, Failure Path, Audit/Usage/Log Evidence, or Acceptance Test sections. The document uses local paths and ledger links. |
| CL-011 | FAIL | Synthesis files can inherit citations, but the inherited citations do not support every claim. Spot-checks below found failed claims for Portkey terminal idempotence, Portkey flush semantics, Sub2API tool-call ID mapping, and the unqualified `length` finish-reason loss claim. |

Detailed CL notes:

- CL-001 is a release-facing failure, not merely style. The file says it will be moved to `docs/specs/protocol-translation.md`; in that state, implementers would read upstream function names and type names.
- CL-005 is only partial because the adapter interface is written in HUAKAI vocabulary, but it still presents concrete method shapes and exact ID helper semantics before the source questions are closed.
- CL-006 passes for license tier, but release form should still split a formal `## Sources` section like `docs/specs/pool-routing.md:16-24`.
- CL-008 is the strongest blocker. This cannot be released as F-PROTO-001 without rewriting the parity matrix or renaming the synthesis to F-PROTO-002.
- CL-009 is independently blocking. The synthesis itself admits the TODOs block Released spec.
- CL-011a source clone status passes: Sub2API and Portkey local clones exist at the pinned commits.
- CL-011b is weak: the inputs use KEEP/IMPROVE/AVOID, but the synthesis uses custom "Where X sharpens Y" sections and does not keep a formal AVOID section.

## Section 2 - Spot-Check Log

Spot-check method:

- I checked the synthesis, both input passes, and local source clones.
- I used `rg -n` and line-numbered `Get-Content` reads against `.omc/reference-src/sub2api` and `.omc/reference-src/portkey-gateway`.
- Verdict meanings:
- PASS: cited source exists and supports the claim.
- FAIL: cited source exists but does not support the exact claim.
- PARTIAL: source supports a narrower claim than the synthesis states.
- MISSING: the synthesis claim lacks a release-adequate source citation or remains TODO.

### Spot-check 01 - Sub2API hub request chain

- Synthesis claim: Sub2API uses a canonical intermediate, with Chat -> Responses -> Anthropic and Responses -> Anthropic.
- Inherited citation: `docs/decompositions/sub2api/protocol-translation-source-verified.md:18-36`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:53` calls `ResponsesToAnthropicRequest(responsesReq)`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_responses.go:49` calls `ResponsesToAnthropicRequest(&responsesReq)`.
- Verdict: PASS.
- Note: this supports the hub direction for the mined Sub2API path.

### Spot-check 02 - Sub2API buffered Anthropic -> Responses -> Chat chain

- Synthesis claim: buffered upstream response can move Anthropic -> Responses -> Chat.
- Inherited citation: `docs/decompositions/sub2api/protocol-translation-source-verified.md:24-26`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:309-311` calls `AnthropicToResponsesResponse(finalResp)` then `ResponsesToChatCompletions(...)`.
- Verdict: PASS.

### Spot-check 03 - Six Anthropic SSE event types

- Synthesis claim: six Anthropic SSE event types are handled.
- Inherited citation: `docs/decompositions/sub2api/protocol-translation-source-verified.md:42-57`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:168-186` dispatches `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`, `message_stop`, with default `return nil`.
- Verdict: PASS.

### Spot-check 04 - Sub2API synthetic terminal finalize

- Synthesis claim: `FinalizeAnthropicResponsesStream` and `FinalizeResponsesChatStream` synthesize terminal events/chunks idempotently.
- Inherited citations: `docs/decompositions/sub2api/protocol-translation-source-verified.md:97-117` and `:152-170`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:192-205` guards `!CreatedSent || CompletedSent` and emits `response.completed`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:174-199` guards `state.Finalized`.
- Verdict: PASS for Sub2API.

### Spot-check 05 - Portkey endpoint-as-canonical fan-out

- Synthesis claim: Portkey uses endpoint-local canonical surfaces and provider adapter fan-out.
- Inherited citation: `docs/decompositions/portkey/protocol-translation-source-verified.md:39-49`, `:67-88`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/services/transformToProviderRequest.ts:149-161` resolves `ProviderConfigs[provider]` and endpoint `fn`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/types.ts:149-160` defines `ProviderConfigs`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/index.ts:78-151` defines the provider map.
- Verdict: PASS.

### Spot-check 06 - Portkey per-stream mutable state

- Synthesis claim: Portkey has equivalent per-stream state in chunk handlers.
- Inherited citation: `docs/decompositions/portkey/protocol-translation-source-verified.md:43-45`, `:90-97`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/handlers/streamHandler.ts:151` creates `const streamState = {}` per stream.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/handlers/streamHandler.ts:189-195` passes `streamState` to the transform function.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/anthropic/chatComplete.ts:648-649` initializes `streamState.toolIndex`.
- Verdict: PASS.

### Spot-check 07 - Portkey idempotent terminal emission

- Synthesis claim at `protocol-translation-synthesis.md:35`: both references guard against double-emitting `[DONE]` / `response.completed`.
- Inherited Portkey citation area: `docs/decompositions/portkey/protocol-translation-source-verified.md:90-119`, `:181-205`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/anthropic/chatComplete.ts:659-660` returns `data: [DONE]` on `event: message_stop`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/handlers/streamHandler.ts:153-169` breaks on upstream EOF after leftover handling.
- Negative evidence: no terminal flag comparable to `CompletedSent` / `Finalized` is shown in the Portkey stream path.
- Verdict: FAIL.
- Required correction: say Sub2API has explicit idempotent finalizers; Portkey emits terminal chunks when it sees provider terminal events and lacks a generic terminal synthesis guard in the checked path.

### Spot-check 08 - Portkey per-event flush

- Synthesis claim at `protocol-translation-synthesis.md:34`: both references flush after every event.
- Grep evidence: Sub2API flushes at `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:429` and final flushes at `:482`.
- Grep evidence: Portkey writes yielded chunks into a `TransformStream` at `.omc/reference-src/portkey-gateway/src/handlers/streamHandler.ts:365-375`.
- Negative evidence: the checked Portkey path does not expose an explicit per-event flush primitive analogous to `c.Writer.Flush()`.
- Verdict: PARTIAL / FAIL as written.
- Required correction: state "Portkey yields transformed chunks per complete stream part" instead of "flushes after every event."

### Spot-check 09 - Tool-call ID bidirectional translation exact form

- Synthesis claim at `protocol-translation-synthesis.md:108`: Anthropic-style `toolu_<hex>` -> canonical `call_<hex>` -> OpenAI client.
- Inherited citation: `docs/decompositions/sub2api/protocol-translation-source-verified.md:264-273`, with TODO-2 still open at `:350`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses.go:289-293` returns `"fc_" + id`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses.go:298-306` strips `fc_` only if the remainder starts with `toolu_` or `call_`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_anthropic_request.go:311-322` uses the same `fc_` wrapper and may synthesize `toolu_` when missing.
- Verdict: FAIL.
- Required correction: remove the false `toolu_ -> call_` source claim. Either describe HUAKAI's desired local canonical IDs as HUAKAI-DESIGN or cite the actual Sub2API wrapper behavior.

### Spot-check 10 - `length` finish_reason loss across Responses -> Chat

- Synthesis claim at `protocol-translation-synthesis.md:62` and `:187`: Sub2API loses `length` across Responses -> Chat when upstream is `max_tokens`.
- Inherited citation: `docs/decompositions/sub2api/protocol-translation-source-verified.md:152-172`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:101-106` maps `status=incomplete` and `max_output_tokens` to `length` in non-streaming conversion.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:310-314` maps streaming `response.incomplete` with `max_output_tokens` to `length`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:391-400` updates usage on `message_delta` but does not propagate `Delta.StopReason` into stream state.
- Verdict: PARTIAL / FAIL as written.
- Required correction: narrow the gap to the Anthropic streaming path if that is the intended defect. Do not claim the entire Responses -> Chat boundary loses `length`.

### Spot-check 11 - Empty upstream response normalization

- Synthesis claim at `protocol-translation-synthesis.md:46` and `:120`: Sub2API synthesizes an empty message item when upstream returns zero blocks.
- Inherited citation: `docs/decompositions/sub2api/protocol-translation-source-verified.md:203-217`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:80-88` appends a message output with empty `output_text` when `len(outputs) == 0`.
- Verdict: PASS.

### Spot-check 12 - Buffered-path interleaving loss

- Synthesis claim at `protocol-translation-synthesis.md:65` and `:184`: Sub2API buffered translator batches text into one message item, losing interleaving with tool_use.
- Inherited citation: `docs/decompositions/sub2api/protocol-translation-source-verified.md:176-201`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:33-67` appends reasoning and function_call outputs during iteration but stores text blocks in `msgParts`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses_response.go:69-77` appends one message output after the loop.
- Verdict: PASS.

### Spot-check 13 - Request-side translator full-body TODO

- Synthesis TODO at `protocol-translation-synthesis.md:192`: full bodies were not read to verify silent field drops.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/chatcompletions_to_responses.go:205-210` ignores unsupported assistant content formats by returning empty content.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_anthropic_request.go:291-305` only converts `output_text` / `text` content parts and falls back to an empty text block when none are recognized.
- Verdict: MISSING / now source-resolved.
- Required correction: close TODO-1 with a verified behavior and decide whether HUAKAI keeps, improves, or avoids those drops.

### Spot-check 14 - Bedrock state-machine TODO

- Synthesis TODO at `protocol-translation-synthesis.md:194`: does Bedrock piggyback on Anthropic SSE state or have its own?
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:25-55` defines a Bedrock streaming handler with its own AWS EventStream decoder.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/bedrock_stream.go:126-159` extracts base64 Claude SSE data, normalizes Bedrock usage metrics, writes event/data lines, and flushes.
- Verdict: MISSING / now source-resolved.
- Required correction: close TODO-3. It is a separate Bedrock stream handler that passes through/normalizes Claude SSE data; it does not use the apicompat Anthropic -> Responses state machine in the checked file.

### Spot-check 15 - Portkey provider-specific usage shapes

- Synthesis claim at `protocol-translation-synthesis.md:37`, `:54`, and `:67`: usage extraction and response interpretation are provider-specific.
- Inherited citation: `docs/decompositions/portkey/protocol-translation-source-verified.md:145-179`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/anthropic/chatComplete.ts:695-765` splits Anthropic stream usage between `message_start` and `message_delta`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/cohere/chatComplete.ts:240-254` handles nested `delta.usage` and `billed_units`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/google/chatComplete.ts:767-889` maps `usageMetadata`.
- Grep evidence: `.omc/reference-src/portkey-gateway/src/providers/bedrock/messages.ts:596-616` emits Anthropic-style `message_delta` usage from Bedrock usage.
- Verdict: PASS.

## Section 3 - Findings

### Critical Findings

1. Wrong Feature ID: this is not F-PROTO-001 in the current parity matrix.
   - Evidence: `protocol-translation-synthesis.md:6` says `Feature ID | F-PROTO-001`.
   - Evidence: `docs/03_FEATURE_PARITY_MATRIX.md:59` defines `F-PROTO-001` as "Gateway bridges external agent/tool protocols (MCP, A2A)" with disposition `Plugin` and phase 9+.
   - Evidence: `docs/03_FEATURE_PARITY_MATRIX.md:64` defines `F-PROTO-002` as cross-provider protocol translation: OpenAI <-> Claude / Gemini -> OpenAI, with explicit capability matrix and `protocol_loss`.
   - Evidence: the older Sub2API decomposition already maps this surface to F-PROTO-002 at `docs/decompositions/sub2api/protocol-translation.md:7`.
   - Confidence: HIGH.
   - Why this matters: releasing this as F-PROTO-001 corrupts the feature parity matrix, acceptance-test IDs, phase plan, and plugin boundary. It would turn an L2 gateway translation spec into the MCP/A2A plugin capability.
   - Fix: do not release under F-PROTO-001. Refile as F-PROTO-002, or get Owner/PM confirmation to rewrite the parity matrix and all dependent inventory/test IDs. Until then this artifact is not releaseable.

### Major Findings

1. CL-011 fails: several inherited citations do not support the synthesis claims.
   - Evidence: `protocol-translation-synthesis.md:35` claims both references guard against double terminal emission, but Portkey evidence only shows `message_stop` -> `[DONE]` at `.omc/reference-src/portkey-gateway/src/providers/anthropic/chatComplete.ts:659-660` and EOF handling at `streamHandler.ts:153-169`; no terminal idempotency flag exists in the checked path.
   - Evidence: `protocol-translation-synthesis.md:108` claims `toolu_<hex>` -> canonical `call_<hex>`, but Sub2API source wraps IDs as `fc_` at `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/anthropic_to_responses.go:289-306`.
   - Evidence: `protocol-translation-synthesis.md:62` says Sub2API loses `length` across Responses -> Chat, but `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_chatcompletions.go:101-106` and `:310-314` map incomplete/max-output to `length`.
   - Confidence: HIGH.
   - Why this matters: CL-011 exists specifically to block hallucinated KEEP/IMPROVE claims from reaching implementers. These are not stylistic issues; they change the behavioral contract.
   - Fix: rewrite each failed claim to match source exactly, or label it HUAKAI-DESIGN and remove any inherited-source attribution.

2. The synthesis leaves release-blocking TODOs open and then tries to waive them for synthesis sign-off.
   - Evidence: `protocol-translation-synthesis.md:192-194` lists TODO-1 through TODO-3.
   - Evidence: `protocol-translation-synthesis.md:196` says they "DO block Released spec."
   - Evidence: CL-009 at `docs/specs/_REVIEW_CHECKLIST.md:51-53` says implementer lane treats Open Questions as a hold signal.
   - Confidence: HIGH.
   - Why this matters: the requested verdict is whether this can move to Released. The artifact itself says no.
   - Fix: close all TODOs with source-backed statements, or keep them as Open Questions and do not request Released approval.

3. The acceptance-test IDs collide with existing matrix semantics and are not release-ready.
   - Evidence: `protocol-translation-synthesis.md:170-188` defines `AT-PROTO-001..014` for Anthropic/Responses/Chat translation scenarios.
   - Evidence: `docs/03_FEATURE_PARITY_MATRIX.md:59` already ties `AT-PROTO-001` to F-PROTO-001 MCP/A2A plugin bridging.
   - Evidence: `docs/11_ACCEPTANCE_TEST_MATRIX.md:13-27` has no `AT-PROTO-*` rows yet; only the required group "Protocol compatibility" appears at `docs/11_ACCEPTANCE_TEST_MATRIX.md:44-47`.
   - Confidence: HIGH.
   - Why this matters: implementers and test writers will attach the wrong tests to the wrong capability. This is especially damaging because F-PROTO-001 and F-PROTO-002 have different phase and risk profiles.
   - Fix: rename these as subcases under F-PROTO-002, or add real acceptance matrix rows with non-colliding IDs after the feature ID is corrected.

4. The synthesis narrows F-PROTO-002 and drops required parity outcomes if refiled under the correct feature.
   - Evidence: F-PROTO-002 at `docs/03_FEATURE_PARITY_MATRIX.md:64` requires a per-pair capability matrix and `protocol_loss` recording on Usage Record.
   - Evidence: prior Sub2API protocol decomposition covers full-body parse/rebuild, vision normalization, thinking-mode mapping, function-call envelope translation, compatibility notes, and lossy-transform observability at `docs/decompositions/sub2api/protocol-translation.md:21-31`, `:52-68`.
   - Evidence: the synthesis focuses on OpenAI Chat / OpenAI Responses / Anthropic Messages hub-and-spoke and does not specify a per-pair capability matrix or `protocol_loss` persistence.
   - Confidence: HIGH.
   - Why this matters: simply changing the ID to F-PROTO-002 is not enough. The current synthesis would under-spec the actual parity row and silently shrink the feature.
   - Fix: restore the missing F-PROTO-002 outcomes: capability matrix, `protocol_loss` field, vision/thinking/tool downgrade handling, and explicit per-pair loss reporting.

5. CL-001 clean-room pressure remains in implementer-facing prose.
   - Evidence: `protocol-translation-synthesis.md:45`, `:47`, `:53`, `:76-87`, `:108`, `:141-144`, and `:193` expose upstream function/type/helper names and exact source vocabulary.
   - Evidence: CL-001 says specs must not contain upstream function, method, handler, or configuration-constant names from non-MIT references.
   - Confidence: HIGH.
   - Why this matters: the synthesis says it will become `docs/specs/protocol-translation.md`. The Released spec cannot teach implementers through upstream names.
   - Fix: keep upstream identifiers only in source-evidence appendices for reviewer/specifier use. Implementer-facing sections must use HUAKAI domain terms only.

6. Request-side translation behavior is materially unresolved and source already shows lossy behavior.
   - Evidence: `protocol-translation-synthesis.md:192` says full request translator bodies were not read.
   - Evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/chatcompletions_to_responses.go:205-210` ignores unsupported assistant content formats.
   - Evidence: `.omc/reference-src/sub2api/backend/internal/pkg/apicompat/responses_to_anthropic_request.go:291-305` converts only text-like content parts and falls back to an empty text block when none are recognized.
   - Confidence: HIGH.
   - Why this matters: request-side loss is central to protocol translation. A Released spec cannot leave it as "maybe silently drops fields" while claiming a loss-auditable semantic model.
   - Fix: document the verified lossy cases, classify each as KEEP / IMPROVE / AVOID, and add acceptance tests for request-side loss reporting.

### Minor Findings

1. `Status | Action Plan` at `protocol-translation-synthesis.md:5` is not release-form status. That is acceptable for a decomposition synthesis, but the final spec must be rewritten as `Status | Released`.

2. `protocol-translation-synthesis.md:10` does not pin the Portkey commit in the `Sources` field itself. The input pass pins it at `docs/decompositions/portkey/protocol-translation-source-verified.md:8`, so this is not a CL-006 failure, but the release spec should include the commit in a `Sources` appendix.

3. `protocol-translation-synthesis.md:66` sets a translation latency SLO of p99 < 100us as HUAKAI design. That is acceptable as design, but it needs measurement scope: payload size, event size, adapter class, and whether JSON parse is included.

4. Section 13's sign-off block is still pending at `protocol-translation-synthesis.md:205-213`. That is expected before review, but it cannot remain in the Released file.

## What's Missing

- No correct capability anchor. The artifact needs to be F-PROTO-002 or the matrix must be explicitly revised.
- No formal acceptance-test matrix rows for the proposed `AT-PROTO-001..014`.
- No release-ready Open Questions disposition. TODOs remain instead of closed source facts or explicit hold signals.
- No per-pair compatibility matrix despite F-PROTO-002 requiring it.
- No explicit `protocol_loss` / Usage Record contract despite F-PROTO-002 requiring it.
- No request-side loss taxonomy for roles, tool choice, function-call records, vision payloads, thinking metadata, and content arrays.
- No native protocol pass-through decision. The Portkey input asks whether native Anthropic Messages pass-through plus normalized Usage Record should be part of the formal contract.
- No clean-room source-evidence appendix boundary separating reviewer evidence from implementer-facing behavior.
- No AVOID section in the synthesis matching CL-011b style, even though the inputs use KEEP / IMPROVE / AVOID.
- No correction for the Portkey terminal-finalization difference.
- No correction for the actual Sub2API `fc_` tool-call ID wrapper.
- No narrowed statement for `length` finish reason: streaming Anthropic max-token propagation may be weak, but Responses -> Chat conversion itself does preserve `length` in checked paths.
- No Bedrock state-machine disposition after the source proves a separate Bedrock stream handler exists.

## Ambiguity Risks

- `Feature ID | F-PROTO-001` can mean the current parity matrix row (MCP/A2A plugin) or the synthesis author's intended "protocol translation" label.
  - Risk if wrong interpretation chosen: the Released spec lands under the wrong phase and plugin boundary.

- `Both references agree` can mean both implement the exact same guarantee or both have a broadly similar behavior.
  - Risk if wrong interpretation chosen: implementers copy a stronger guarantee than one reference actually has, especially for terminal idempotence and flush semantics.

- `Canonical intermediate` can mean OpenAI Responses-compatible local model, a HUAKAI-owned semantic event model, or literal OpenAI Responses types.
  - Risk if wrong interpretation chosen: local implementation either leaks upstream type vocabulary or loses the intended clean-room abstraction.

- `Tool-call ID translation is bijective` can mean Sub2API source behavior, HUAKAI desired invariant, or OpenAI/Anthropic convention.
  - Risk if wrong interpretation chosen: test vectors will assert the wrong ID shape.

- `Open TODOs do not block synthesis sign-off` can be read as approval to move on to Released after this review.
  - Risk if wrong interpretation chosen: CL-009 is bypassed even though the same paragraph says TODOs block Released spec.

## Multi-Perspective Notes

- Executor perspective: an implementer cannot execute this safely because the feature ID points to MCP/A2A plugin work while the body describes OpenAI/Responses/Anthropic translation.
- Stakeholder perspective: the document does not satisfy the actual F-PROTO-002 row because it lacks the per-pair compatibility matrix and `protocol_loss` Usage Record field.
- Skeptic perspective: the synthesis over-trusts input summaries. Spot-checking the source disproved multiple claims that survived from the input pass.
- Security perspective: native protocol pass-through and provider extras are proposed, but the spec does not yet define what gets normalized for billing/observability versus what is allowed to bypass translation.
- New-hire perspective: the document reads like an architecture memo, not a release spec. The "cleaned of source identifiers" step is still assumed, not done.
- Ops perspective: unknown-event telemetry, terminal failure typing, and latency SLOs are good directions, but they are not tied to concrete Usage Record fields, counters, or alert conditions.

## Dependency Audit

| Dependency | Status | Notes |
| --- | --- | --- |
| Sub2API clone exists | PASS | `git rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`. |
| Portkey clone exists | PASS | `git rev-parse HEAD` returned `351692fd9236af222168134b416924fae0bdba23`. |
| License ledger row for Sub2API | PASS | `E-LIC-001` exists at `docs/07_REFERENCE_EVIDENCE_LEDGER.md:15`. |
| License ledger row for Portkey | PASS | `E-LIC-006` exists at `docs/07_REFERENCE_EVIDENCE_LEDGER.md:20`. |
| F-PROTO-001 matrix row exists | FAIL FOR THIS ARTIFACT | Row exists, but it describes MCP/A2A plugin bridging, not this synthesis. |
| F-PROTO-002 matrix row exists | PASS | Row matches the synthesis domain better, but the synthesis does not fully cover it. |
| AT-PROTO rows in acceptance matrix | FAIL | No concrete `AT-PROTO-*` rows exist in `docs/11_ACCEPTANCE_TEST_MATRIX.md`. |
| TODO closure | FAIL | TODO-1..3 remain open in the synthesis. |
| Source identifiers isolated from implementer sections | FAIL | Names remain in sections 2, 3, 6, 8, 9, and 11. |

## Pre-Mortem

Assume this synthesis was moved exactly as written to `docs/specs/protocol-translation.md` and failed:

1. Implementer starts MCP/A2A plugin work because the capability matrix says F-PROTO-001 is external agent/tool protocol bridging.
   - Covered by synthesis? No.
   - Finding: Critical Finding 1.

2. Test writer creates `AT-PROTO-001` for Anthropic SSE, overwriting or colliding with the MCP/A2A plugin test direction.
   - Covered by synthesis? No.
   - Finding: Major Finding 3.

3. Implementer assumes Portkey has an idempotent terminal finalizer and builds a convergence invariant that only Sub2API actually supports.
   - Covered by synthesis? No.
   - Finding: Major Finding 1.

4. Implementer writes tool-call ID tests expecting `toolu_x -> call_x`, but the source-backed behavior is `fc_toolu_x`.
   - Covered by synthesis? No.
   - Finding: Major Finding 1.

5. Released spec omits `protocol_loss`, so lossy conversion is not visible on Usage Records.
   - Covered by synthesis? No.
   - Finding: Major Finding 4.

6. Request-side translator silently drops unsupported content and nobody adds telemetry because TODO-1 was left open.
   - Covered by synthesis? No.
   - Finding: Major Finding 6.

7. Clean-room reviewer blocks implementer release because upstream function names remain in the spec body.
   - Covered by synthesis? Partly, by "cleaned of source identifiers" in the Becomes row, but the work is not done.
   - Finding: Major Finding 5.

## Key Assumptions Extracted

| Assumption | Rating | Evidence / concern |
| --- | --- | --- |
| This review is allowed to judge release readiness, not just memo coherence. | VERIFIED | User requested final verdict `APPROVE-FOR-RELEASED / APPROVE-WITH-FIXES / REJECT`. |
| The synthesis may inherit CL-011 citations from input passes. | VERIFIED | CL-011 says synthesis files are exempt from direct CL-011 only if they inherit citations from input passes. |
| F-PROTO-001 is the intended feature ID. | FRAGILE / FALSE | `protocol-translation-synthesis.md:6` says F-PROTO-001, but the matrix row is MCP/A2A plugin bridging. |
| The actual domain is cross-format protocol translation. | VERIFIED | Body covers OpenAI Chat, OpenAI Responses, Anthropic Messages, Portkey adapters, and usage extraction. Matrix row F-PROTO-002 matches this. |
| Portkey and Sub2API agree on terminal idempotence. | FRAGILE / FALSE | Sub2API has flags; Portkey checked path does not show equivalent terminal guard. |
| Sub2API tool-call ID behavior is still unresolved. | RESOLVED FALSE | Source now shows `fc_` wrapper behavior. |
| Bedrock state-machine behavior is unresolved. | RESOLVED FALSE | Source now shows a separate Bedrock stream handler. |
| Request-side loss is not yet known. | RESOLVED PARTIAL | Source shows at least unsupported assistant content and non-text Responses content can be ignored/flattened. |
| Open TODOs can be carried into Released. | FALSE | CL-009 says Open Questions are implementer-lane hold signals; the synthesis itself says they block Released spec. |
| A later cleanup step can remove source identifiers without re-review. | FRAGILE | Removing identifiers changes the spec text and source mapping; it needs another CL pass. |

## Gap Checklist

| Gap | Release impact | Required disposition |
| --- | --- | --- |
| Wrong feature ID | Blocks release | Correct to F-PROTO-002 or revise matrix with Owner/PM confirmation. |
| Test ID collision | Blocks verification | Rename or add acceptance rows after feature ID correction. |
| Portkey terminal finalization overclaim | Blocks CL-011 | Rewrite to source-backed wording. |
| Tool-call ID shape overclaim | Blocks CL-011 | Use actual `fc_` behavior as evidence or define HUAKAI policy as design. |
| `length` finish-reason overclaim | Blocks CL-011 | Narrow to specific streaming gap. |
| Request-side lossy conversion | Blocks spec completeness | Add loss taxonomy and `protocol_loss` outputs. |
| Missing per-pair capability matrix | Blocks F-PROTO-002 parity | Add matrix or mark as Mandatory Roadmap if not in this release. |
| Missing native pass-through decision | Blocks architecture scope | Owner/PM decision or explicit hold question. |
| Source identifiers in body | Blocks clean-room release | Move to evidence appendix or remove before spec release. |
| Pending sign-off block | Mechanical release issue | Replace with final reviewer sign-off only after all checks pass. |

## Self-Audit

- Critical Finding 1 confidence: HIGH. Could author refute with missing context? No; current matrix rows are explicit.
- Major Finding 1 confidence: HIGH. Could author refute with missing context? Only by producing additional source not cited in the pass; current cited paths do not support the claims.
- Major Finding 2 confidence: HIGH. Could author refute with missing context? No; TODOs and CL-009 are explicit.
- Major Finding 3 confidence: HIGH. Could author refute with missing context? No; acceptance matrix lacks the rows and `AT-PROTO-001` is already attached to F-PROTO-001.
- Major Finding 4 confidence: HIGH. Could author refute with missing context? No; F-PROTO-002 row explicitly names the missing outcomes.
- Major Finding 5 confidence: HIGH. Could author refute with missing context? Partially only for specifier-lane evidence, but not for Released spec body.
- Major Finding 6 confidence: HIGH. Could author refute with missing context? No; source snippets show lossy request-side handling.

## Realist Check

- Critical Finding 1 stays CRITICAL: realistic worst case is not theoretical. The wrong feature row would misdirect implementation and release tracking.
- Major Finding 1 stays MAJOR: no shipped code is broken yet, but CL-011 exists to prevent exactly this source-drift before implementation.
- Major Finding 2 stays MAJOR: open TODOs are text-only to fix, but they are an explicit release hold signal.
- Major Finding 3 stays MAJOR: test ID collision causes governance and verification drift, but no tests are implemented yet.
- Major Finding 4 stays MAJOR: this is potential feature shrink for F-PROTO-002, not merely missing detail.
- Major Finding 5 stays MAJOR: source identifiers can be cleaned mechanically, but the current artifact cannot be released.
- Major Finding 6 stays MAJOR: request-side loss affects real user outcomes and must be specified before implementation.

## Section 4 - FINAL VERDICT

Verdict: REJECT.

This cannot be approved for Released and is not a bounded "apply <=10 wording fixes" situation. The artifact is anchored to the wrong capability ID, leaves release-blocking TODOs open, and has multiple source-claim mismatches under CL-011.

What would need to change:

1. Correct the capability identity.
   - Preferred: refile this synthesis as F-PROTO-002 and update the title/header/input references accordingly.
   - Alternative: Owner/PM explicitly rewrites the parity matrix so F-PROTO-001 means this surface. That is a governance change, not a reviewer fix.

2. Rebuild acceptance-test IDs after the feature ID is fixed.
   - Do not use `AT-PROTO-001` for Anthropic SSE while the matrix assigns it to MCP/A2A plugin bridging.
   - Add real acceptance matrix rows or name them as provisional subcases under the corrected feature.

3. Close TODO-1 through TODO-3.
   - TODO-1: document verified request-side drops/flattening and decide KEEP / IMPROVE / AVOID.
   - TODO-2: replace the false `toolu_ -> call_` statement with actual `fc_` wrapper evidence or HUAKAI-DESIGN local ID policy.
   - TODO-3: document Bedrock's separate stream handler and decide whether it belongs in this spec or a later Bedrock-specific adapter spec.

4. Correct the failed source claims.
   - Portkey terminal behavior: no generic idempotent terminal finalizer was verified.
   - Portkey flush behavior: yields transformed chunks, not explicit event flush.
   - Sub2API `length` behavior: narrow the gap to the Anthropic streaming path if intended; do not claim all Responses -> Chat conversion loses `length`.

5. Restore the full F-PROTO-002 scope.
   - Include per-pair capability matrix.
   - Include `protocol_loss` or equivalent Usage Record field.
   - Include request-side and response-side loss taxonomy.
   - Include vision, thinking/reasoning, tool choice, function-call, and native-pass-through decisions.

6. Clean source identifiers before any implementer-facing spec is created.
   - Move evidence-only source names to an appendix, or remove them entirely from the Released spec.
   - Use HUAKAI vocabulary in Normal Path, Failure Path, invariants, and tests.

Upgrade path:

- After the above is done, rerun CL-001..011 from scratch.
- Spot-check at least 8 citations again because the required rewrite changes source claims.
- If the feature ID is not fixed, the verdict remains REJECT regardless of other edits.

## Section 5 - Owner-Facing Chinese Summary

最终结论：`protocol-translation-synthesis.md` 不能按 `F-PROTO-001` Released，因为当前矩阵里的 `F-PROTO-001` 是 MCP/A2A 插件桥接，而本文实际写的是跨 provider 协议翻译，更接近 `F-PROTO-002`。我还抽查出多处 CL-011 问题：Portkey 终止事件幂等、逐事件 flush、Sub2API tool-call ID 形态、`length` finish_reason 归因都不能按原句成立。没有功能缩水应该发生；正确做法是重挂到 `F-PROTO-002` 后补齐 capability matrix、`protocol_loss` Usage Record 字段、请求侧 loss taxonomy，并关闭 3 个 TODO。clean-room 风险目前主要来自 Released 版本仍暴露上游函数/类型名；安全风险主要是 native pass-through 和未知事件/丢字段还没有绑定到审计与用量记录。需要 Owner/PM 确认的是：是否正式把这份 synthesis 改名/改挂为 `F-PROTO-002`，还是要改 parity matrix 重新定义 `F-PROTO-001`。
