# HUAKAI Upgrade #6 U6-D Atomic Design — Codex Lane

| Field | Value |
|---|---|
| Lane | codex planner |
| Date | 2026-05-08 UTC |
| Task | identity -> adapter mapping strategy |
| Input read | `docs/process/plans/2026-05-08-upgrade6-u6d-design-sonnet.md`; local HUAKAI protocol selector, clientid, forwarder, capability matrix, field matrix |
| Boundary | Design only. No business implementation or code changes. |

## 0. Current-Code Facts Used

- `backend/internal/gateway/protocol_selector.go` registers upstream adapters by string `ProtocolFamily`. The default registry currently contains 19 protocol families, and the selected family is meant to come from model alias / route resolution, not from the inbound client identity.
- `backend/internal/proto/proto.go` already separates `ClientAdapter` from `UpstreamAdapter`. This is the right abstraction line for U6-D.
- `backend/internal/gateway/forwarder.go` already has a single client serialization point through `f.ClientAdapter.CanonicalEventToClientChunk(...)`, with raw SSE passthrough when `ClientAdapter == nil`.
- `backend/internal/clientid` already provides `IdentityFromContext(ctx) (Identity, confidence)`.
- `backend/internal/proto/capability_matrix.go` is protocol-level: `ClientProtocol x UpstreamProtocol x FeatureName`.
- `backend/internal/proto/field_matrix.go` is currently audit/observability oriented and defaults unknown fields to preserved. It is keyed by `ClientProtocol x UpstreamProtocol x fieldName`, not by client identity.

These facts make U6-D primarily a client-protocol selection problem, not an upstream routing problem.

## 1. Decision: Identity Must Not Override ProtocolFamily

My decision: identity must not override `registry.Resolved.ProtocolFamily`. Identity should influence the selected `ClientAdapter` / `ClientProtocol`, with explicit conflict handling against path and route metadata.

Reasoning:

- `ProtocolFamily` describes the upstream account/model wire family. If model alias resolution says `anthropic_messages`, changing that to `openai_chat` because the caller looks like Cursor would call the wrong upstream protocol.
- Client identity is spoofable. Letting spoofable request headers change upstream family creates a routing, quota, billing, and audit integrity risk.
- The existing architecture already has the correct split: upstream family selects `proto.UpstreamAdapter`; client-facing protocol selects `proto.ClientAdapter`.
- U6-D should introduce a `ClientShapeDecision`, not mutate `ForwardRequest.ProtocolFamily`.

Recommended selection model:

```text
Inputs:
  request path / endpoint family
  optional explicit client protocol from handler or route config
  clientid.Identity + confidence from context
  resolved upstream ProtocolFamily from model registry
  tenant/operator compatibility policy

Output:
  ClientProtocol
  decision source: explicit_path | route_config | identity | default
  confidence
  conflict flags
  selected ClientAdapter
```

Selection precedence:

1. Explicit route or path protocol wins, for example `/v1/chat/completions` -> OpenAI Chat and `/v1/messages` -> Anthropic Messages.
2. Route config wins for gateway-owned generic endpoints.
3. High-confidence identity fills gaps only when path/route is ambiguous or marked identity-aware.
4. Low-confidence or conflicting identity falls back to path/route default and increments conflict metrics.
5. Unknown identity never changes the adapter; it uses path or route default.

This is a deliberate adjustment to the sonnet design: identity should not be first in all cases. A path is a stronger wire contract than User-Agent-like identity evidence.

## 2. Boundary Scenario: Cursor Client + Claude Model

Scenario: `identity=cursor`, requested model alias resolves to a Claude model whose upstream `ProtocolFamily` is `anthropic_messages`.

Decision:

- Do not fail only because Cursor is paired with a Claude model.
- Do not route the Claude model through an OpenAI upstream adapter.
- Use Anthropic upstream adapter for provider communication and OpenAI Chat client adapter for client output, if the selected request path / client protocol is OpenAI Chat.
- Allow translation when `CapabilityMatrix(OpenAIChat, Anthropic)` says the requested features are `PRESERVED` or `LOSSY`.
- Fail before upstream call when the request needs a feature whose matrix verdict is `UNSUPPORTED`.

Important nuance:

- Basic text streaming should succeed as `OpenAI Chat client shape <- HCSF <- Anthropic upstream shape`.
- Known lossy mappings may proceed, but must emit `ProtocolLossEntry`, metrics, and operator-visible audit context.
- Unsupported mappings must fail loud before streaming starts, preferably with a typed 4xx protocol compatibility error. A 503 is wrong unless the provider is unavailable.
- Do not encode loss as OpenAI `finish_reason="content_filter"`. That value has a real client-facing meaning and would be a false signal. Loss belongs in HUAKAI audit/usage metadata, not in a fake provider semantic.
- If an unanticipated serializer failure happens after stream start, emit the most protocol-correct terminal/error behavior available and record the real failure class in usage/audit. Do not silently continue with malformed chunks.

So the short answer is: translate by default for supported/lossy-known paths, fail only for unsupported features or impossible serializer state. Never silently lossy-translate without a recorded loss entry.

## 3. Clean-Room Verification Without Reading Cursor/Cody Source

U6-D should not read Cursor or Cody source to determine strict wire-format requirements. Even permissive source can bias implementation, and the Owner specifically asked how to verify without doing that.

Recommended evidence sources:

- Public protocol documentation from OpenAI and Anthropic for the target wire formats.
- Public user/operator docs from client projects only when they describe configuration or supported API base types. Avoid source files and avoid copying examples from code blocks.
- OCAW black-box capture and compatibility tests run by Owner or in a controlled local harness.
- Public issue reports or discussions only as behavioral evidence, with links and timestamps. Treat each as weak evidence unless reproduced by OCAW.

OCAW plan:

1. Build a local mock gateway endpoint that can emit controlled response variants:
   - OpenAI Chat SSE shape.
   - Anthropic Messages SSE shape.
   - OpenAI Chat shape plus unknown extra fields.
   - Anthropic Messages shape plus unknown extra fields.
   - tool-call streams, usage chunks, truncation, and protocol-level error frames.
2. Configure each client through its documented custom API-base settings, proxy settings, or Owner-approved local network route. If a client cannot legally or safely be pointed at a local harness, mark that client `open_question` rather than guessing.
3. Use synthetic prompts and fake credentials only. Do not capture real secrets, real user prompts, or upstream account tokens.
4. Record only behavioral artifacts:
   - endpoint path and method;
   - whitelisted request headers with secrets redacted;
   - top-level JSON field names, not prompt values;
   - SSE event names and top-level keys;
   - client outcome: accepted, rejected, rendered incorrectly, retried, or degraded.
5. Store evidence as a research artifact with:
   - client name/version;
   - OS/runtime;
   - capture date;
   - harness variant;
   - observed behavior;
   - strict/tolerant classification;
   - remaining open questions.

Proposed strictness labels:

- `strict-openai-chat`: rejects or ignores non-OpenAI Chat stream shape.
- `strict-anthropic-messages`: rejects or ignores non-Anthropic Messages stream shape.
- `tolerant-openai-chat`: requires OpenAI Chat envelope but tolerates unknown fields.
- `tolerant-anthropic-messages`: requires Anthropic Messages envelope but tolerates unknown fields.
- `ambiguous`: OCAW not run or evidence conflicts.

Initial defaults until OCAW:

- Cursor: select OpenAI Chat only when path/route also indicates OpenAI Chat, or when identity-aware generic route is explicitly enabled. Mark strictness `ambiguous` until OCAW confirms tolerance for extras.
- Claude Code: select Anthropic Messages only when path/route is Anthropic Messages or identity-aware generic route is enabled. Mark strictness `ambiguous` until OCAW confirms extra-field behavior.
- Cody: do not assert dual-shape behavior from source. Default to path/route-based selection and require OCAW for any identity-based override.
- Chat UI and curl/script: path/route-based by default.

This differs from sonnet where Cody source-read was considered acceptable. For this atomic, black-box behavior is cleaner and enough.

## 4. Linkage With U7 Passthrough Field Matrix

U6-D should feed U7 with a selected `ClientProtocol`, but it should not add identity to the FieldMatrix key.

Recommended design:

- Keep FieldMatrix key as `(ClientProtocol, UpstreamProtocol, fieldName)`.
- Add or centralize a `ProtocolFamilyTraits` mapping that converts the 19 `ProtocolFamily` strings into coarse `proto.UpstreamProtocol` values for capability and field-matrix lookup.
- `ClientShapeDecision.ClientProtocol` becomes the client side of both matrices.
- Identity affects `ClientProtocol` only through the decision layer. It does not directly affect matrix lookup.
- A separate `ClientWirePolicy` can be keyed by identity and selected client protocol:
  - `extra_fields=preserve` for tolerant clients.
  - `extra_fields=known_only` or `allowlist` for OCAW-proven strict clients.
  - `extra_fields=audit_only` for fields that must stay in HCSF/usage audit but must not be serialized to a brittle client.
- Any client-wire pruning must create an audit-visible loss/policy entry. It is not a feature deletion because HCSF and operator records still preserve the field; only that client's output envelope is narrowed.

Why `ProtocolFamilyTraits` matters:

- The current 19-family registry and the matrices use different granularities.
- Do not infer coarse upstream protocol by substring matching family names.
- Every default protocol family must have an explicit trait test:
  - `anthropic_messages` -> Anthropic.
  - OpenAI-compatible chat families -> OpenAI.
  - `bedrock_invoke` -> Bedrock.
  - `gemini_messages` / `gemini_advanced_session` -> Gemini.
  - session reversal families stay explicit and may be `unknown/ocaw_pending` if their actual wire shape is not verified.

U7 strategy:

- U6-D-early: provide selected client protocol and upstream protocol trait to the adapter path.
- U7-current: continue preserve-by-default in HCSF.
- U7-follow-up: use OCAW strictness to decide whether unknown fields are serialized to specific clients, but never remove them from canonical/audit state.

## 5. Atomic Split

### U6-D-0: Synthesis Gate

Scope:

- Compare sonnet and codex designs.
- Owner chooses synthesized rules for precedence, unsupported-feature behavior, and OCAW gate.

Success:

- A merged plan exists before implementation.

### U6-D-1: Clean-Room Client Shape Evidence

Scope:

- Create a research artifact for Cursor, Claude Code, Cody, chat UI, and curl/script wire-shape expectations.
- Define OCAW harness cases and evidence schema.
- Do not read Cursor/Cody source.

Success:

- Each client has `strict/tolerant/ambiguous`, evidence timestamp, and open questions.

### U6-D-2: Client Shape Decision Contract

Scope:

- Define `ClientShapeDecision` and selector behavior.
- Inputs include path, route-declared client protocol, identity/confidence, and operator policy.
- Output includes selected `proto.ClientProtocol`, decision source, confidence, and conflict reason.

Success:

- Unit tests cover path wins, identity fills ambiguous route, low-confidence fallback, and conflict metrics.

### U6-D-3: ClientAdapter Registry

Scope:

- Add a client-adapter registry keyed by `proto.ClientProtocol`.
- Keep it separate from `ProtocolAdapterRegistry`.
- Register only adapters that actually exist; missing adapter must fail loud when identity-aware mode is enabled.

Success:

- Static registry tests cover duplicate registration, empty key, nil adapter, unknown client protocol, and successful lookup.

### U6-D-4: ProtocolFamilyTraits

Scope:

- Add explicit mapping from all default `ProtocolFamily` strings to coarse `proto.UpstreamProtocol` plus OCAW status where needed.
- Avoid heuristic string parsing.

Success:

- A test enumerates `BuildDefaultProtocolAdapterRegistry` families and proves every family has a trait.

### U6-D-5: Capability Preflight

Scope:

- Validate selected `ClientProtocol x UpstreamProtocol` against `CapabilityMatrix` before upstream call when request canonicalization is available.
- Allow `LOSSY` with `ProtocolLossEntry`.
- Reject `UNSUPPORTED` with typed compatibility error.

Success:

- Cursor + Claude basic text passes.
- Cursor + Claude with unsupported feature fails before streaming and records a loss/error reason.

### U6-D-6: Forwarder / Handler Integration Behind Flag

Scope:

- Wire selected client adapter into the response serialization point.
- Feature flag default off until OCAW evidence and tests pass.
- Raw passthrough remains only for legacy mode or explicit passthrough routes.

Success:

- Identity-aware mode chooses OpenAI client chunks for Cursor-compatible requests and Anthropic client chunks for Claude Code-compatible requests.
- Unknown or conflicting identity uses path/route default.

### U6-D-7: U7 Passthrough Policy Hook

Scope:

- Add `ClientWirePolicy` hook for strict/tolerant extra-field serialization.
- Keep FieldMatrix schema unchanged.
- Preserve pruned fields in canonical/audit state.

Success:

- Unknown extra field is preserved in HCSF.
- Tolerant client receives it when allowed.
- Strict client does not receive it, and audit records the client-wire policy decision.

### U6-D-8: Acceptance Tests And Observability

Scope:

- Add scenario tests:
  - Cursor identity + Claude upstream + OpenAI Chat output.
  - Claude Code identity + OpenAI upstream + Anthropic Messages output.
  - Unknown identity + explicit path fallback.
  - Spoofed/conflicting identity does not override explicit path.
  - Unsupported feature fails pre-stream.
  - Lossy-known feature emits loss entry.
  - Extra fields follow U7 client-wire policy.
- Add low-cardinality metrics:
  - identity;
  - selected client protocol;
  - upstream protocol;
  - decision source;
  - conflict yes/no;
  - verdict preserved/lossy/unsupported.

Success:

- Tests verify behavior rather than only "not bad" assertions.
- Metrics labels are finite enums, not model names or raw client headers.

## 6. Agree / Disagree With Sonnet Design

### Agreements

- Agree that identity must not override `registry.Resolved.ProtocolFamily`.
- Agree that U6-D belongs at the client-adapter selection layer.
- Agree that clean-room behavior evidence is required before claiming a client is strict or tolerant.
- Agree that FieldMatrix should not grow an identity dimension.
- Agree that feature flagging/canary rollout is appropriate.
- Agree that Cursor + Claude model should not fail solely due to cross-protocol pairing.
- Agree that acceptance tests must cover Cursor-to-Anthropic, Claude-Code-to-OpenAI, and unknown fallback paths.

### Disagreements / Adjustments

- Sonnet gives identity priority when confidence is above a threshold. I recommend explicit path/route protocol wins first; identity fills only ambiguous or identity-aware routes.
- Sonnet leans toward "lossy rather than fail" as the default. I recommend `PRESERVED/LOSSY` may proceed, but `UNSUPPORTED` must fail before upstream call when detectable.
- Sonnet suggests using a fake OpenAI `finish_reason` value for lossy signaling as one option. I reject that. Loss should be audit metadata, not a false client-visible provider semantic.
- Sonnet treats Cody source reading as acceptable because of its license. I recommend not reading client source for U6-D at all; use OCAW black-box behavior and public docs.
- Sonnet's atomic split underweights the need for explicit `ProtocolFamilyTraits`. I consider that a necessary bridge between 19 family strings and the coarse protocol matrices.
- Sonnet proposes a fixed confidence threshold such as 0.7. I recommend a policy value plus path-consistency checks; confidence alone is not enough because headers are spoofable.

## 7. Owner Decision Points

1. Should explicit path always win over identity, or should there be an operator mode where high-confidence identity overrides path?
2. Should `LOSSY` translation require tenant/operator opt-in, or is audit visibility enough for default enablement?
3. Should U6-D block on OCAW for Cursor and Claude Code, or may it ship behind a default-off flag with ambiguous strictness?
4. Should session reversal protocol families be marked `ocaw_pending` and blocked from identity-aware mode until captured?
5. Where should client-wire pruning be recorded: `ProtocolLossEntry`, usage draft audit metadata, or both?

## 8. No Feature Shrinkage / Risk Notes

- No feature is dropped. Unsupported paths become explicit compatibility failures or mandatory roadmap items, not silent removals.
- Clean-room risk is controlled by not reading Cursor/Cody source and by storing only behavior evidence.
- Security risk is controlled by not allowing spoofable identity to mutate upstream protocol family.
- Billing/quota risk is controlled by preserving registry/model-route authority for upstream selection and recording selected client/upstream protocols in audit metrics.
- Client compatibility risk remains until OCAW. The safe rollout is default-off flag plus targeted canary.

## 9. Final Recommendation

Implement U6-D as a three-part policy:

1. Upstream protocol remains registry/model driven.
2. Client protocol is selected by explicit path/route first, then high-confidence identity only for ambiguous or identity-aware routes.
3. Translation proceeds only when the capability matrix says `PRESERVED` or `LOSSY`; `UNSUPPORTED` fails before upstream call where possible.

This preserves product functionality while keeping protocol truthfulness, billing correctness, and clean-room boundaries intact.
