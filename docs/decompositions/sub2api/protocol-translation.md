# Sub2API — Protocol Translation Pipeline

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | Sub2API (LGPL-3.0, [E-LIC-001](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | [F-PROTO-002](../../03_FEATURE_PARITY_MATRIX.md) (L2) — cross-format protocol translation |
| Evidence ledger rows | [E-S2A-PROXY-014, E-S2A-PROXY-015, E-S2A-PROXY-016, E-S2A-PROXY-017](../../07_REFERENCE_EVIDENCE_LEDGER.md) |
| Specifier session | Codex (`omc ask codex --agent-prompt critic`, 2026-04-28T05:31 UTC) |
| Specifier date | 2026-04-28 |
| Reviewer session | TBD |
| Reviewer date | TBD |
| Source files read | Sub2API backend protocol translation + envelope normalization + vision normalization + thinking-mode mapping files (paths verified by Codex; recorded in `.omc/artifacts/ask/`) |

## 1. WHY

A relay-station's whole value proposition is that a User writes one OpenAI-compatible request and the gateway routes it across heterogeneous upstream providers (OpenAI Chat / Responses, Anthropic Messages, Google Gemini, Azure variants). The translation layer is where heterogeneity is hidden — but it is also where capabilities silently degrade. Tool-calling shapes diverge across providers; vision payloads have four common encodings; reasoning-effort metadata is a per-provider design. A naive translator either rejects half the upstream catalog or silently produces incorrect outputs. Sub2API's design pressure is "make all providers look like one without lying about what was lost"; the operator-visible compatibility note is the honest disclosure of any conversion that downgraded behavior.

## 2. WHAT

The translation pipeline runs as **full-body parse-and-rebuild before upstream dispatch**, not as streaming byte transformation. The flow:

1. Inbound request body is fully parsed into a JSON envelope.
2. The envelope is identified by shape (Chat / Responses / Anthropic / Gemini / Azure variant). The shape determines which canonical normalizer is invoked.
3. Each normalizer extracts a **canonical** representation of: roles, tool definitions, tool-choice policy, function-call records (assistant tool calls + tool results), streaming preference, response-format constraints (JSON mode, schema), reasoning-effort or thinking metadata, vision payloads.
4. The selected Provider Account dictates the **target** protocol shape. The canonical representation is rebuilt into the target shape, with three sub-flows:
   - **Vision normalization** ([E-S2A-PROXY-016](../../07_REFERENCE_EVIDENCE_LEDGER.md)): image payloads are converted between URL, data-URI, base64-inline, and provider-specific inline-media representations. Unsupported or empty media may be skipped or flattened depending on policy.
   - **Thinking-mode mapping** ([E-S2A-PROXY-017](../../07_REFERENCE_EVIDENCE_LEDGER.md)): reasoning fields are mapped between effort levels (high/medium/low), explicit token budgets, and provider-signature shapes (e.g. anthropic-style "thinking blocks"). Invalid metadata may be downgraded or disabled.
   - **Tool / function-call envelope** ([E-S2A-PROXY-015](../../07_REFERENCE_EVIDENCE_LEDGER.md)): roles, tool registration shapes, tool-choice (auto / none / required / specific tool), function-call records (mid-conversation tool invocations), and tool-result records are translated. The shape varies meaningfully across providers.
5. The rebuilt envelope is dispatched upstream. Streaming preference may or may not be honored depending on provider capabilities.
6. On the response side, the inverse translation runs: provider-shape events are re-canonicalized and re-emitted in the client's expected shape (handled by the streaming forwarder ([decompositions/sub2api/streaming-forwarder.md](streaming-forwarder.md)) for streaming, or single-shot translation for non-streaming).

## 3. INPUTS (signals consumed, state mutated)

- Inbound: client request body (full JSON), client `Accept` header (streaming vs non-streaming), client-requested model id, client tool definitions and tool-choice, client vision payloads, client reasoning-effort hint.
- Per-Channel: target Provider protocol family, per-Channel capability matrix (tools? vision? thinking? streaming?), per-Channel response-format support, per-Channel allowed image encodings, per-Channel thinking-effort mapping table.
- Per-Account: provider-specific authentication header injection (handled at transport layer, not in the translator).
- State mutated: an internal `compatibility_notes` accumulator that gathers each lossy or downgrading conversion for later persistence to Usage Record metadata.

## 4. FAILURE MODES HANDLED

- **Inbound shape unrecognized**: rejected at parse time with a 4xx; no upstream call attempted.
- **Target Channel does not support tools** but client provided tools: tools are dropped; a `compatibility_note` is recorded.
- **Target Channel does not support vision** but client provided images: images are flattened to text descriptions or dropped (per policy); compatibility note recorded.
- **Target Channel uses different image encoding**: payload converted (URL → base64, data URI → inline-media, etc.).
- **Reasoning effort hint conflicts with target's signature shape**: downgraded to nearest supported (e.g. `high` becomes the highest budget the target supports); compatibility note recorded.
- **Tool-choice "required" with a specific tool but target only supports "auto"**: downgraded; compatibility note recorded.
- **Streaming requested but target only supports non-streaming**: target called non-streaming; response wrapped into single-event SSE for client.

## 5. FAILURE MODES NOT HANDLED (gaps)

- **Compatibility-note observability is implicit**: notes are accumulated and may flow to Usage Record metadata, but the operator-visible surface is not consistently wired. An operator investigating "why did this user get a degraded answer" must reconstruct from logs.
- **Lossy transformations can be silent**: when a downgrade happens but no note is emitted (because the downgrade path forgot to write to the accumulator), the client sees a degraded result with no signal that anything was lost. This is critical defect 4 in [07 §Critical Defects](../../07_REFERENCE_EVIDENCE_LEDGER.md).
- **Capability matrix per Channel** is operator-set scalar configuration; no automatic capability discovery from the upstream provider. Stale capability matrix produces incorrect downgrade decisions.
- **Function-call record translation across providers** is fragile when assistant-message ordering or tool-call id schemes differ. Edge cases in multi-turn tool-using conversations can produce malformed envelopes.
- **Body-size pressure**: full-body parse-and-rebuild loads the entire request into memory; very large vision-multimodal requests can pressure memory and increase latency. There is no streaming alternative.

## 6. KEEP / IMPROVE / AVOID for HUAKAI

- **KEEP**: full-body parse-and-rebuild (not streaming transformation). This is the right call for correctness; the alternative (transform-as-bytes-flow) is much harder to get right and has worse error reporting. ([E-S2A-PROXY-014 KEEP](../../07_REFERENCE_EVIDENCE_LEDGER.md))
- **KEEP**: canonical-then-rebuild architecture (parse to internal canonical, rebuild to target shape). One canonical representation simplifies new-provider onboarding to writing one normalizer + one rebuilder per provider.
- **IMPROVE — explicit payload-size budget** ([E-S2A-PROXY-014 KEEP caveat](../../07_REFERENCE_EVIDENCE_LEDGER.md)): every Route declares `max_request_body_bytes`. Requests exceeding the budget are rejected with a typed error before parse, not after.
- **IMPROVE — typed compatibility output** (critical defect 4 countermeasure): every translation invocation returns a structured triple — `losses[]`, `downgrades[]`, `compatibility_mode` — that is **persisted on the Usage Record**. The operator UI surfaces a per-request "compatibility note" badge and an aggregate dashboard ([F-OBS-001](../../03_FEATURE_PARITY_MATRIX.md) extension).
- **IMPROVE — vision fail-closed by default** ([E-S2A-PROXY-016 IMPROVE](../../07_REFERENCE_EVIDENCE_LEDGER.md)): when target Channel does not support vision but client sent images, the request fails with a typed "unsupported-media" error by default. Operator may opt into "drop and warn" via Route policy.
- **IMPROVE — capability matrix freshness**: operator-set capability matrix is the floor; HUAKAI runs a periodic capability probe (small synthetic request) per Channel to detect provider capability shifts and warn when matrix and reality diverge.
- **IMPROVE — thinking downgrade visibility** ([E-S2A-PROXY-017 KEEP caveat](../../07_REFERENCE_EVIDENCE_LEDGER.md)): when reasoning-effort is downgraded, the Usage Record carries `reasoning_downgraded: true` plus the original and applied levels. Operator dashboards can filter by this.
- **IMPROVE — function-call envelope test matrix**: HUAKAI's test suite must cover multi-turn tool-using conversations across each provider pairing in the Channel set. Edge cases caught at test time, not in production.
- **AVOID — silent compatibility loss**: any code path that does not write to `compatibility_notes` when it should is treated as a defect. This is the load-bearing rule that makes the translation honest.
- **AVOID — translator-internal hidden retries**: if a translation fails (e.g. envelope cannot be rebuilt for the target), the failure surfaces as a typed error, not a silent fallback to a different shape. Retry policy belongs at the Route layer, not inside the translator.
- **AVOID — mixing translation logic with transport / authentication**: the translator works on canonical envelopes only; provider-specific authentication header injection happens at the transport layer ([decompositions/sub2api/upstream-transport.md](upstream-transport.md), TBD).

## 7. ATTRIBUTION

- Source files read: Sub2API backend translation files (specifier session output retained in `.omc/artifacts/ask/codex-role-codex-specifier-lane-miner-for-huakai-owner-directive-2-2026-04-28T05-31-56-975Z.md`, gitignored). Behaviors above are paraphrased from that specifier-lane session output; no upstream function name, struct field, file path, or distinctive identifier appears here.
- Specifier-lane session: Codex (gpt-5.5 + xhigh, critic agent), 2026-04-28T05:31 UTC.
- Reviewer-lane session: TBD — must be a different agent session than the specifier above. Reviewer must verify the file against [_REVIEW_CHECKLIST.md](../specs/_REVIEW_CHECKLIST.md) CL-001..010 before Status moves to Reviewed.

## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | (pending) |
| Review date | (pending) |
| Checks passed | (pending; CL-001..010 must all pass) |
| Notes | Third decomposition file. Pairs with [streaming-forwarder.md](streaming-forwarder.md) — together these two cover the request-side and response-side of HUAKAI's reverse-proxy core. |
