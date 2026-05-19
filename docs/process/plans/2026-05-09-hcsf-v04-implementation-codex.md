# HCSF v0.4 Phased Delivery Plan — Codex Lane

**日期**: 2026-05-09
**Lane**: codex (xhigh + fast_mode)
**对应 Claude lane**: docs/process/plans/2026-05-09-hcsf-v04-implementation-claude.md（写作时未见）
**触发**: Owner 2026-05-09 "按照最优的进行"批准 HCSF v0.4 + L3+L4 PMF + inference spend metric

## TL;DR

HCSF v0.4 should ship as a capability graph, not as another single canonical message shape: the approved ingress split is OpenAI-compatible `/v1/chat/completions`, Anthropic-native `/v1/messages`, and `/v1/native/<vendor>/<capability>` for non-normalizable vendor features, with the IR between ingress and provider adapters (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:76-123`).

The current gap is real implementation, not naming: `proto.HCSF` is still a placeholder, all four `CanonicalToProviderRequest` paths are unimplemented, and the forwarder still raw-passthroughs client output when `ClientAdapter` is nil (`backend/internal/proto/proto.go:13-18`, `backend/internal/proto/anthropic_sse.go:83-89`, `backend/internal/proto/openai_sse.go:142-156`, `backend/internal/proto/gemini_sse.go:97-111`, `backend/internal/proto/bedrock_eventstream.go:67-75`, `backend/internal/gateway/forwarder.go:41-43`, `backend/internal/gateway/forwarder.go:293-298`).

The delivery plan is 7 phases over 10-15 weeks: schema struct realisation, capability graph IR, client adapters, provider adapters, native passthrough, capability/property tests, then Owner-local real-account smoke; this matches the approved non-urgent 10-15 week pace and the existing Axis 3 gap estimate (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:144-156`, `docs/research/2026-05-09-axis3-huakai-current-state.md:274-320`).

Adapter priority follows the approved PMF zone: Anthropic rich first because Chinese Claude Code / CLI compatibility is the identified pressure point, OpenAI Chat Completions remains the market storefront, OpenAI Responses is required for agent workloads, Gemini is required for multi-vendor coverage, and Bedrock-on-Anthropic is required because HUAKAI already has a Bedrock adapter surface (`docs/research/2026-05-09-issue-mining-cross-repo.md:236-249`, `docs/research/2026-05-09-market-research-codex.md:17-23`, `docs/research/2026-05-09-axis3-huakai-current-state.md:30-58`).

Every cross-vendor loss must be explicit through `protocol_loss` or native passthrough, never silently dropped; this is already a HUAKAI protocol-translation requirement and is the main execution constraint for cache, thinking, tools, multimodal, MCP, and live/batch features (`docs/specs/protocol-translation.md:87-124`, `docs/specs/protocol-translation.md:138-151`).

## 1. HCSF v0.4 Schema 设计

### 1.1 Capability list (11+)

This plan proposes 14 capability nodes. The first 11 are required by Owner; file, image, audio, and video are split into separate capability nodes because issue mining shows different breakage modes for image payloads, audio/realtime surfaces, and file/tool resources (`docs/research/2026-05-09-issue-mining-cross-repo.md:78-95`, `docs/research/2026-05-09-issue-mining-cross-repo.md:108-120`, `docs/research/2026-05-09-issue-mining-cross-repo.md:130-144`).

| Capability | IR schema decision | Native passthrough trigger | Required evidence |
|---|---|---|---|
| `text` | Preserve role, ordered content blocks, stop reason, finish class, usage, and stream event boundaries; this extends existing `CanonicalMessage`, `ContentBlock`, `CanonicalEvent`, `Response`, and `Usage` instead of replacing them (`backend/internal/proto/hcsf.go:11-119`). | Use native only when provider text events cannot be represented as ordered HCSF events without altering visible output (`docs/specs/streaming-forwarder.md:54-85`). | LiteLLM has a normalized response stream object while Envoy separates translator contracts per endpoint (`BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1797-1876`, `envoyproxy/ai-gateway@4d3eae8b:internal/translator/translator.go:100-117`). |
| `tool_use` / `tool_result` | Model tool calls and tool results as paired graph edges: `tool_call_id`, display name, input JSON, result blocks, partial argument deltas, and normalized error status; this fills current HUAKAI tool-call gaps (`docs/research/2026-05-09-axis3-huakai-current-state.md:154-167`). | Native when provider tool surface includes hosted tools or external server actions that cannot be expressed as plain function calls (`docs/research/2026-05-09-issue-mining-cross-repo.md:130-144`). | LiteLLM maps Anthropic tool/result blocks through an adapter, and Portkey has tool request/choice fields in the shared request body (`BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:421-664`, `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:357-420`). |
| `thinking` / `reasoning` | Store reasoning budget, emitted reasoning blocks, hidden-token accounting, redaction class, and provider-specific signatures; Anthropic-rich is primary because thinking blocks and budgeted reasoning are first-class in observed adapter behavior (`BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:618-707`). | Native when reasoning text, signatures, or budget semantics cannot be faithfully emitted on the destination provider (`docs/specs/protocol-translation.md:108-124`). | Envoy's OpenAI schema includes thinking-budget and response thinking block structures, giving a second endpoint-shaped reference (`envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:868-885`, `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:1400-1461`). |
| `cache_control` | Represent cache scope, cache breakpoints, cache-key hint, cache read/write usage, and safety warning; existing HUAKAI usage already has cache read/write fields but not a cache-control policy graph (`backend/internal/proto/hcsf.go:95-107`, `docs/research/2026-05-09-axis3-huakai-current-state.md:129-132`). | Native when cache semantics depend on provider-specific metadata headers or prompt-cache keys that cannot be mapped without changing billing/cache behavior (`docs/research/2026-05-09-issue-mining-cross-repo.md:108-120`). | LiteLLM usage types expose cache-token accounting, and Portkey/OpenAI request surfaces expose cache-related request knobs (`BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1471-1676`, `Portkey-AI/gateway@351692fd:src/providers/openai/chatComplete.ts:90-135`). |
| `structured_output` | Store JSON-mode intent, strict schema, preferred parser mode, failure recovery mode, and provider fallback prompt/tool strategy; HUAKAI must emit `protocol_loss` when strictness is weakened (`docs/specs/protocol-translation.md:108-124`). | Native when a provider supports a schema dialect or constrained decoder not representable in the destination vendor (`docs/research/2026-05-09-market-research-codex.md:59-71`). | Envoy has response-format schema objects; Portkey exposes response format in the common request body (`envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:656-694`, `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:454-457`). |
| `computer_use` | Represent computer-use as a specialized hosted-tool capability with environment target, action request, screenshot/input blocks, approval status, and audit label; it is not flattened into generic function calls because the PMF target includes agent backends (`docs/research/2026-05-09-market-research-codex.md:17-23`, `docs/research/2026-05-09-market-research-codex.md:59-71`). | Native by default until HUAKAI has a safe sandbox and approval model; normalized downgrade is only allowed as an explicit unavailable capability (`docs/specs/protocol-translation.md:138-151`). | LiteLLM generated interaction types include computer call/output objects, while Portkey's tool structure allows provider-specific tool metadata (`BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:166-175`, `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:414-420`). |
| `file` | Store file references as content nodes with `source_kind`, media type, file id/URL digest, size metadata, and retention label; this should not be folded into image/audio/video because file APIs and assistant resources have different lifecycle (`docs/research/2026-05-09-issue-mining-cross-repo.md:130-144`). | Native when upload lifecycle, assistant resource binding, or provider file id cannot be replayed through HCSF without loss (`docs/specs/protocol-translation.md:138-151`). | Portkey exposes file routes and Envoy's OpenAI schema parses file content parts (`Portkey-AI/gateway@351692fd:src/index.ts:195-203`, `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:180-224`). |
| `image` | Store image inputs as multimodal content nodes with URI/base64/file id, media type, dimensions when known, and loss audit for unsupported vendors; current issue mining names image payloads as a live breakage class (`docs/research/2026-05-09-issue-mining-cross-repo.md:78-95`). | Native when image transport format or provider-specific validation cannot be normalized (`docs/specs/protocol-translation.md:138-151`). | Portkey transforms Anthropic image content, and Envoy's OpenAI schema includes image content parts (`Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:198-266`, `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:180-224`). |
| `audio` | Store audio input/output as a modality node with transport, format, sample metadata, transcript policy, and stream/live compatibility; this links to both chat audio and realtime/live surfaces (`docs/research/2026-05-09-market-research-codex.md:59-71`). | Native when websocket/session semantics or audio codec negotiation cannot be expressed in request/response HCSF (`docs/specs/streaming-forwarder.md:107-165`). | Portkey exposes audio routes and audio parameters, while Envoy parses OpenAI audio content parts (`Portkey-AI/gateway@351692fd:src/index.ts:190-193`, `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:427-488`, `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:107-128`). |
| `video` | Store video as a multimodal node with URL/base64/file reference, time range, and size/codec hints; this is separate from generic file because some providers treat video as a model input capability (`docs/research/2026-05-09-market-research-codex.md:59-71`). | Native when provider-side upload, chunking, or live video semantics cannot be represented as a static content node (`docs/specs/protocol-translation.md:138-151`). | LiteLLM generated interaction types include video content, and Envoy's OpenAI schema includes response input video content (`BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:459-470`, `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:4866-4910`). |
| `live` | Represent live sessions as session graph objects: connect params, bidirectional event stream, modality set, tool availability, resume token, and close reason; keep it distinct from SSE streaming because websocket state is not a simple completion stream (`docs/research/2026-05-09-market-research-codex.md:59-71`). | Native by default for Gemini Live/OpenAI Realtime until HUAKAI has a live session broker (`docs/specs/streaming-forwarder.md:107-165`). | LiteLLM has realtime event/session types, and Portkey exposes realtime routes (`BerriAI/litellm@b5d3a5fc:litellm/types/realtime.py:12-117`, `Portkey-AI/gateway@351692fd:src/index.ts:278-281`). |
| `batch` | Represent batch as an asynchronous job graph: input file/source, endpoint target, validation result, output/error files, cost attribution, and retry policy; this should use HCSF job metadata rather than streaming events (`docs/research/2026-05-09-market-research-codex.md:59-71`). | Native when vendor-specific batch upload/output lifecycle is the product feature (`docs/specs/protocol-translation.md:138-151`). | LiteLLM's HTTP handler has batch create behavior, and Portkey exposes batch routes (`BerriAI/litellm@b5d3a5fc:litellm/llms/custom_httpx/llm_http_handler.py:3340-3455`, `Portkey-AI/gateway@351692fd:src/index.ts:210-231`). |
| `mcp_server` | Represent MCP servers as external capability nodes with server label, allowed operations, approval requirement, invocation events, and result blocks; this is required for agent workloads and should not be hidden inside generic tools (`docs/research/2026-05-09-market-research-codex.md:17-23`). | Native when MCP transport, server authorization, or approval semantics exceed HCSF's normalized tool graph (`docs/specs/protocol-translation.md:138-151`). | LiteLLM generated interactions include MCP server/call/result objects, and Envoy contains MCP proxy request aggregation behavior (`BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:620-634`, `envoyproxy/ai-gateway@4d3eae8b:internal/mcpproxy/handlers.go:1493-1575`). |
| `data_retention` | Store a policy assertion node: no-train intent, zero-data-retention intent, regional constraint, request-store flag, audit label, and enforcement status; this must be explicit because business model includes enterprise/private deployment and China relay station use (`docs/01_PROJECT_BRIEF.md:22-33`, `docs/01_PROJECT_BRIEF.md:71-80`). | Native/policy-only unless vendor and account contract prove the retention guarantee; never infer ZDR from a generic API field (`docs/specs/protocol-translation.md:138-151`). | Portkey surfaces request storage/safety knobs, while Envoy's backend policy types include region/hostname matching primitives; neither is sufficient proof of vendor ZDR, so this remains a DECISION-POINT (`Portkey-AI/gateway@351692fd:src/providers/openai/chatComplete.ts:90-135`, `envoyproxy/ai-gateway@4d3eae8b:api/v1alpha1/shared_types.go:8-44`). |

### 1.2 IR schema 选型

HCSF v0.4 should introduce a versioned `HCSFEnvelope` in `backend/internal/proto` because the current `HCSF` type is empty while `CanonicalRequest`, `CanonicalEvent`, `Response`, and `Usage` already hold part of the needed message/event shape (`backend/internal/proto/proto.go:13-18`, `backend/internal/proto/hcsf.go:11-119`). The envelope should keep existing canonical text/event structs for compatibility and add graph-level capability nodes rather than forcing every feature into `ContentBlock` (`backend/internal/proto/hcsf.go:36-52`, `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:108-123`).

Proposed top-level shape:

```text
HCSFEnvelope v0.4
  request_meta: ingress API, provider target, model, stream mode, idempotency/audit ids
  messages: ordered text/multimodal/message blocks using existing CanonicalMessage lineage
  capability_graph:
    nodes: capability-specific payloads for the 14 capability names above
    edges: message->tool, tool->result, response->usage, file->modal input, mcp->tool call
  provider_projection:
    native_vendor, normalized_supported, loss_records, native_passthrough_required
  stream_plan:
    event classes, recoverability, fallback boundary, flush policy
  accounting:
    usage, cache usage, reasoning usage, batch/live job usage placeholder
  policy:
    data_retention assertion, redaction class, audit visibility
```

The IR is Anthropic-rich primary, not Anthropic-only: it preserves Anthropic-style thinking/tool/cache blocks because issue mining calls out Anthropic schema compatibility as the Chinese CLI pain point, but the approved storefront remains OpenAI-compatible and native vendor endpoints remain available when normalization is unsafe (`docs/research/2026-05-09-issue-mining-cross-repo.md:236-249`, `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:76-123`). This directly avoids the single-canonical trap documented in the LiteLLM source-read artifact while still using OpenAI Chat Completions as the market-facing entry (`docs/research/2026-05-09-axis3-protocol-translation-litellm.md:1-7`, `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:125-133`).

`protocol_loss` must become a first-class output of every provider projection: the existing protocol spec already requires capability matrix output and a loss record with field/provider/severity/reason/suggestion semantics (`docs/specs/protocol-translation.md:87-124`). Any adapter that cannot prove preservation must emit `loss_records` and either downgrade with warning, return `unsupported_capability`, or require `/v1/native/<vendor>/<capability>` (`docs/specs/protocol-translation.md:138-151`).

### 1.3 Vendor mapping matrix

Legend: `rich` means normalized with no intentional capability loss; `lossy` means normalized with mandatory `protocol_loss`; `native` means first-class native passthrough is required; `roadmap` means do not block v0.4 MVP, but the capability remains preserved as mandatory roadmap.

| Capability | Anthropic Messages | OpenAI Chat Completions | OpenAI Responses | Gemini | Bedrock-on-Anthropic | Default fallback |
|---|---|---|---|---|---|---|
| `text` | rich | rich | rich | rich | rich | normalized events; no native needed unless stream shape fails (`docs/specs/streaming-forwarder.md:54-85`). |
| `tool_use` / `tool_result` | rich | rich but tool-result semantics audited | rich | lossy until Gemini-specific tool semantics are tested | rich if Bedrock Anthropic surface matches Messages | no silent drop; return unsupported or loss record (`docs/specs/protocol-translation.md:138-151`). |
| `thinking` / `reasoning` | rich | lossy unless reasoning tokens/effort are enough | rich for reasoning surfaces | lossy/roadmap | rich if Anthropic thinking is available through Bedrock | native for visible thinking/signatures that cannot map (`BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:618-707`). |
| `cache_control` | rich | lossy through prompt-cache key/cache accounting | lossy/rich depending endpoint | lossy through cached content | rich/unknown until Bedrock smoke | native when cache billing can change (`docs/research/2026-05-09-issue-mining-cross-repo.md:108-120`). |
| `structured_output` | lossy via system/tool strategy | rich | rich | lossy/rich after Gemini schema smoke | lossy | return loss if strict decoding is weakened (`docs/specs/protocol-translation.md:108-124`). |
| `computer_use` | native first | unsupported/native hosted-tool bridge only | native if provider supports hosted tool | roadmap | native/roadmap | native passthrough or explicit unsupported (`docs/research/2026-05-09-market-research-codex.md:59-71`). |
| `file` | lossy/rich by content type | rich for supported file parts | rich for file/resource workflows | lossy | lossy | native for file lifecycle APIs (`Portkey-AI/gateway@351692fd:src/index.ts:195-203`). |
| `image` | rich | rich | rich | rich | rich/unknown until smoke | normalized content if media validation passes. |
| `audio` | roadmap | rich for chat audio subset | rich/live bridge | live/native for Gemini Live | roadmap | native for websocket/session audio (`BerriAI/litellm@b5d3a5fc:litellm/types/realtime.py:12-117`). |
| `video` | roadmap | lossy/unsupported | rich for response input video where supported | rich/unknown | roadmap | native or unsupported, never strip video block (`envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:4866-4910`). |
| `live` | roadmap | native/realtime | native/realtime | native Gemini Live | roadmap | native session endpoint only in v0.4 (`Portkey-AI/gateway@351692fd:src/index.ts:278-281`). |
| `batch` | rich/native batch | rich/native batch | rich/native batch | rich/native batch | roadmap | async native job surface (`BerriAI/litellm@b5d3a5fc:litellm/llms/custom_httpx/llm_http_handler.py:3340-3455`). |
| `mcp_server` | native/agent bridge | lossy unless function-only | rich/native | roadmap | roadmap | native/approval-required (`envoyproxy/ai-gateway@4d3eae8b:internal/mcpproxy/handlers.go:1493-1575`). |
| `data_retention` | policy/native | policy/native | policy/native | policy/native | policy/native | policy assertion; no inferred ZDR (`docs/01_PROJECT_BRIEF.md:71-80`). |

## 2. 阶段分解 (10-15 周)

### Phase overview

| Phase | Goal | 工作量 | Owner 授权点 | 输出 |
|---|---|---:|---|---|
| P-0 | Schema decision + `proto.HCSF` struct 实化 | 1-2 周 | Approve HCSF v0.4 field names and compatibility policy | `HCSFEnvelope`, capability enum, loss model, docs/spec update |
| P-1 | Capability graph IR | 2 周 | Confirm 14-node capability list and data-retention vocabulary | graph builder, validation, matrix schema |
| P-2 | `ClientAdapter` 落地 | 2 周 | Approve ingress compatibility behavior for `/v1/messages` and `/v1/chat/completions` | client adapters, no nil fallback for covered routes |
| P-3 | `CanonicalToProviderRequest` Phase B | 2 周 | Confirm lossy downgrade rules per vendor | Anthropic/OpenAI/Gemini/Bedrock provider projections |
| P-4 | Per-vendor native passthrough endpoints | 1-2 周 | Confirm auth/audit policy for `/v1/native/...` | native routes, guardrails, audit/loss emission |
| P-5 | Capability matrix + property tests | 2 周 | Confirm release gate thresholds | unit/property/integration tests and golden matrices |
| P-6 | Owner-local real-account smoke | 1-2 周 | Owner supplies local real credentials only outside repo | smoke scripts, redacted logs, acceptance report |
| P-7 | Hardening + PMF gate | 1 周 | Decide whether to enter PMF validation | release gate, known gaps, roadmap lock |

The phase order starts with schema because the present code cannot express HCSF payloads yet, then removes the `ClientAdapter` nil-passthrough, then fills provider rendering; this sequence follows the current gap order in Axis 3 and the Phase A-E state recorded for protocol translation (`docs/research/2026-05-09-axis3-huakai-current-state.md:182-225`, `docs/specs/protocol-translation.md:45-85`).

### P-0 schema 决策 + HCSF struct 实化 (Week 1-2)

Goal: replace empty `proto.HCSF` with a versioned envelope, capability enum, loss record, and compatibility helpers while keeping existing `CanonicalRequest` and `CanonicalEvent` stable for the current streaming forwarder (`backend/internal/proto/proto.go:13-34`, `backend/internal/proto/hcsf.go:11-119`).

Inputs: approved HCSF v0.4 synthesis, current Axis 3 code inventory, and protocol loss spec (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:76-123`, `docs/research/2026-05-09-axis3-huakai-current-state.md:14-25`, `docs/specs/protocol-translation.md:108-124`).

Outputs: `HCSFEnvelope` type, `CapabilityKind` constants for 14 nodes, `CapabilitySupport` matrix structs, `ProtocolLoss` struct, and docs update mapping legacy `Canonical*` types into the envelope.

Tests: compile-level tests for schema JSON shape, loss severity validation, and round-trip stability for existing text-only canonical events.

Owner authorization: DECISION-POINT for final field names and whether HCSF v0.4 persists to any database table; schema migration is high risk and should not be bundled into P-0 without explicit Owner approval under the project risk rules.

Failure modes + signals: type churn breaks current forwarder tests; signal is existing streaming tests failing before any adapter behavior changes (`docs/specs/streaming-forwarder.md:177-200`). Mitigation is adapter-free compatibility wrappers around current `CanonicalRequest` / `CanonicalEvent`.

Three-dimensional delta: architecture delta is a capability graph instead of one request struct; algorithm delta is validation/loss projection before provider render; ecosystem delta is a stable internal contract for future native vendor plugins (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:135-142`).

### P-1 capability graph IR (Week 2-4)

Goal: implement capability graph building and validation for text, tools, thinking, cache, structured output, computer-use placeholder, file/image/audio/video, live, batch, MCP, and data-retention policy (`docs/research/2026-05-09-issue-mining-cross-repo.md:204-249`).

Inputs: P-0 envelope, current `Usage` cache fields, and feature parity obligations for multimodal, realtime, protocol translation, billing evidence, and model metadata (`backend/internal/proto/hcsf.go:95-107`, `docs/03_FEATURE_PARITY_MATRIX.md:57-65`).

Outputs: `CapabilityGraphBuilder`, `CapabilityValidator`, static vendor support registry, and a generated matrix endpoint or test fixture that reports supported/lossy/native/unsupported per capability (`docs/specs/protocol-translation.md:87-106`).

Tests: unit tests per capability with valid/invalid payloads; validation tests for "unsupported but present" returning a structured error or loss, not deletion (`docs/specs/protocol-translation.md:138-151`).

Owner authorization: DECISION-POINT for data-retention vocabulary and whether computer-use/MCP are exposed as beta flags by default.

Failure modes + signals: graph overfits Anthropic and loses OpenAI Responses fields; detection is Anthropic->IR->Anthropic and OpenAI Responses->IR->Responses property tests plus explicit lossy fields for cross-vendor projection.

Three-dimensional delta: architecture delta is graph nodes/edges; algorithm delta is capability validation before adapter routing; ecosystem delta is future plugin/native endpoints can register capability claims without changing the storefront.

### P-2 ClientAdapter 落地 (Week 4-6)

Goal: implement client-side adapters for `/v1/chat/completions`, `/v1/messages`, and the minimal `/v1/responses` internal path needed to preserve OpenAI Responses-style agent workloads, removing nil raw-passthrough for covered HCSF routes (`backend/internal/gateway/forwarder.go:41-43`, `backend/internal/gateway/forwarder.go:293-298`).

Inputs: P-1 graph, protocol-family scanner/adapter selection in the forwarder, and current route behavior that already fails loud on unknown protocol family (`backend/internal/gateway/forwarder.go:66-94`).

Outputs: `OpenAIChatClientAdapter`, `AnthropicMessagesClientAdapter`, `OpenAIResponsesClientAdapter` or internal response projection shim, and client response renderers for SSE and buffered responses.

Tests: request parse tests, response render tests, streaming event render tests, and "no nil fallback on covered route" regression.

Owner authorization: DECISION-POINT for public `/v1/responses` timing; if it is not public in v0.4, the adapter should still support native passthrough for Responses under `/v1/native/openai/responses`.

Failure modes + signals: Claude Code-style Anthropic schema breaks because the storefront silently normalizes to OpenAI shape; detection is issue-derived fixtures for Anthropic schema and cache/tool/result blocks (`docs/research/2026-05-09-issue-mining-cross-repo.md:236-249`).

Three-dimensional delta: architecture delta is bi-entry storefront; algorithm delta is client render projections from the same graph; ecosystem delta is Chinese CLI compatibility without abandoning OpenAI-compatible market entry (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:125-133`).

### P-3 CanonicalToProviderRequest Phase B (Week 6-8)

Goal: implement provider-side renderers for Anthropic, OpenAI Chat, OpenAI Responses, Gemini, and Bedrock-on-Anthropic so that all current `ErrNotImplemented` provider render paths are replaced by capability-aware projection (`backend/internal/proto/anthropic_sse.go:83-89`, `backend/internal/proto/openai_sse.go:142-156`, `backend/internal/proto/gemini_sse.go:97-111`, `backend/internal/proto/bedrock_eventstream.go:67-75`).

Inputs: P-1/P-2 graph and client adapters, current provider parsers, and the existing protocol translation normal path (`docs/specs/protocol-translation.md:45-85`).

Outputs: provider request builders, provider response-to-HCSF parsers where missing, capability loss emitters, and model/provider matrix updates.

Tests: per-provider unit tests for text/tool/image/cache/thinking where supported, lossy downgrade tests, and negative tests for unsupported capabilities.

Owner authorization: DECISION-POINT for lossy thresholds: for L3/L4 PMF, Anthropic thinking/tool/cache should block release if lossy on Anthropic-native routes; OpenAI/Gemini lossy projection can proceed only with visible audit.

Failure modes + signals: provider render silently drops a capability; detection is matrix tests requiring one of `supported`, `lossy`, `native_required`, or `unsupported_error` for every capability/provider cell (`docs/specs/protocol-translation.md:87-124`).

Three-dimensional delta: architecture delta is provider projection per capability; algorithm delta is loss-aware transformation instead of direct raw mapping; ecosystem delta is minimum viable multi-vendor adapter coverage for L3/L4.

### P-4 per-vendor native passthrough endpoints (Week 8-10)

Goal: add `/v1/native/<vendor>/<capability>` for capabilities that cannot be normalized without loss, with explicit audit, policy, and billing/quota hooks, not untracked raw proxying (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:108-123`, `docs/01_PROJECT_BRIEF.md:82-89`).

Inputs: capability matrix from P-3, loss records, and existing gateway guardrails for fail-loud protocol selection (`backend/internal/gateway/forwarder.go:66-94`).

Outputs: native route registry, per-vendor capability allowlist, native passthrough request/response audit envelope, redacted error model, and docs.

Tests: route allow/deny tests, unauthorized vendor/capability tests, audit event tests, billing/quota fixture tests if hooks already exist.

Owner authorization: native passthrough touches auth, billing/quota hooks, and real credential handling boundaries; any change to auth core, billing ledger, quota enforcement, secrets, or schema must stop for Owner confirmation under AGENTS risk rules.

Failure modes + signals: native endpoint becomes a shadow bypass around policy; detection is tests proving native routes still emit usage/audit/loss markers and do not expose unsupported vendors by default.

Three-dimensional delta: architecture delta is a safe extension valve; algorithm delta is loss-driven routing to native; ecosystem delta is future vendor coverage without shrinking the core IR.

### P-5 capability matrix 测试 + property test (Week 10-12)

Goal: make capability support measurable and non-regressive through unit tests, integration matrix tests, and property tests for canonical round-trip preservation (`docs/specs/protocol-translation.md:175-197`).

Inputs: P-0 through P-4 implementation, issue-mined fixtures, and streaming failure taxonomy (`docs/research/2026-05-09-issue-mining-cross-repo.md:258-267`, `docs/specs/streaming-forwarder.md:66-85`).

Outputs: generated capability matrix, test fixtures per capability/provider, property tests for Anthropic->IR->Anthropic and OpenAI Chat->IR->OpenAI Chat, stream interruption tests, and regression tests for nil adapter fallback.

Tests: this phase is the test harness; it must include normal, failure, and operator recovery scenarios.

Owner authorization: DECISION-POINT for release gate thresholds, especially whether `COVERED-WEAK` cells block PMF smoke.

Failure modes + signals: tests assert only "not bad" instead of exact expected output; detection is cross-review using project smell rules and strict expected-good assertions.

Three-dimensional delta: architecture delta is a matrix as product contract; algorithm delta is property preservation; ecosystem delta is confidence to add vendors without re-opening canonical debates.

### P-6 真账号 smoke (Week 12-14)

Goal: run Owner-local smoke against real Anthropic, OpenAI Chat, OpenAI Responses, Gemini, and Bedrock-on-Anthropic accounts where credentials exist, with redacted logs and no repo-stored secrets (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:160-167`, `docs/research/2026-05-09-axis3-huakai-current-state.md:236-270`).

Inputs: P-5 test suite, Owner local verification expectation, and real credentials supplied outside the repository (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:164-167`).

Outputs: smoke scripts, redacted transcripts, capability matrix evidence marked mock/smoke/real, and release gate report.

Tests: real text/tool/cache/thinking/image stream for Anthropic; real text/tool/structured output/cache-key smoke for OpenAI Chat; Responses agent/tool/MCP/file smoke if account allows; Gemini text/image/cache/live smoke if available; Bedrock-Anthropic text/tool/cache smoke if AWS account allows.

Owner authorization: real credentials, paid API spend, AWS access, regional routing, and any production endpoint use all need explicit Owner action; no agent should ask for or write secrets into the repo.

Failure modes + signals: local smoke passes mock but fails provider schema validation; detection is real provider error capture redacted into the smoke report and mapped back to capability/loss matrix.

Three-dimensional delta: architecture delta is evidence-tagged capability support; algorithm delta is real-provider validation; ecosystem delta is readiness for L3/L4 PMF conversations.

### P-7 hardening + PMF gate (Week 14-15)

Goal: freeze v0.4 release criteria, classify all remaining capability/vendor gaps as Implemented, Implemented Better, Merged Equivalent, Safe Equivalent, Plugin, Feature Flag, or Mandatory Roadmap, and decide whether to enter PMF validation (`docs/03_FEATURE_PARITY_MATRIX.md:109-114`, `docs/01_PROJECT_BRIEF.md:82-89`).

Inputs: P-6 real smoke report, capability matrix, and monthly annualized inference spend mock/smoke/real metrics (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:54-64`, `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:160-167`).

Outputs: release gate document, roadmap items for Bedrock-Llama/Cohere/Mistral/xAI/Cursor/Copilot/Kiro/Windsurf/Antigravity, PMF demo scope, and known-risk register.

Tests: final regression suite, smoke replay, and Codex review before any commit under project per-commit review discipline.

Owner authorization: DECISION-POINT for PMF validation start, demo vendor list, and any public/native endpoint exposure.

Failure modes + signals: roadmap hides a feature drop; detection is mandatory mapping of every capability/vendor cell to a status (`docs/03_FEATURE_PARITY_MATRIX.md:109-114`).

Three-dimensional delta: architecture delta is stabilized HCSF v0.4; algorithm delta is measured capability preservation; ecosystem delta is PMF-ready L3/L4 vendor coverage.

## 3. Vendor adapter 优先级

| Vendor / surface | 完整度目标 | 阶段 | clean-room 风险 |
|---|---|---|---|
| Anthropic Messages | `rich`: text, tool_use/result, thinking, cache_control, image/file where supported, structured-output fallback with audit | P-0 to P-3; smoke in P-6 | High feature importance but low license risk if implemented from HUAKAI spec and public protocol behavior; do not copy upstream adapter code (`docs/research/2026-05-09-issue-mining-cross-repo.md:236-249`). |
| OpenAI Chat Completions | `rich` for storefront text/tools/structured/image/audio subset; `lossy` for Anthropic thinking/cache semantics | P-2 to P-3; smoke in P-6 | Low if using HCSF contract; risk is treating OpenAI shape as single canonical again (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:125-133`). |
| OpenAI Responses | `rich` for agent workload, file/tool/MCP/reasoning where possible; native passthrough for endpoint-specific features | P-2 to P-4; smoke in P-6 | Medium because Responses has endpoint-specific event/tool types; keep mapping behavior-only and cite official docs before implementation if needed. |
| Gemini | `rich` for text/image/tool where implemented; `native` for Live until websocket broker exists; `lossy` for cache/structured output until smoke proves behavior | P-3 to P-6 | Low-to-medium; source evidence shows Gemini has distinct transform/config surfaces, so do not assume OpenAI parity (`BerriAI/litellm@b5d3a5fc:litellm/llms/gemini/chat/transformation.py:18-80`). |
| Bedrock-on-Anthropic | `rich` target for Anthropic-compatible features already in current adapter inventory; smoke gated by AWS Owner credential | P-3 and P-6 | Medium because Bedrock policy/auth/region handling is environment-sensitive; avoid schema/auth/quota changes without Owner confirmation (`docs/research/2026-05-09-axis3-huakai-current-state.md:30-58`). |
| Bedrock-on-Llama | `roadmap`: `native-passthrough-only` first, normalized text/tools later | P-7 roadmap | Low implementation now because not in v0.4 core; must not be silently dropped (`docs/03_FEATURE_PARITY_MATRIX.md:109-114`). |
| Cohere | `roadmap`: `native-passthrough-only` first, normalized text/rerank later | P-7 roadmap | Low now; future rerank may require separate capability node if Owner expands HCSF. |
| Mistral | `roadmap`: `native-passthrough-only` first, normalized text/tools later | P-7 roadmap | Low now; keep as Mandatory Roadmap, not omitted. |
| xAI Grok | `roadmap`: `native-passthrough-only` until demand validates full adapter | P-7 roadmap | Low now; market research says model churn is a trend, not a v0.4 blocker (`docs/research/2026-05-09-market-research-codex.md:59-71`). |
| Cursor / Copilot / Kiro / Windsurf / Antigravity | `roadmap`: plugin/native adapter class, likely policy-gated; no core backend dependency in v0.4 | P-7 roadmap | Medium future risk because these may combine IDE auth, proprietary flows, and nonstandard schemas; classify as Mandatory Roadmap/Plugin rather than core drop. |

## 4. 测试策略

Unit tests per capability: each capability node gets valid, invalid, unsupported-provider, lossy-provider, and native-required cases. This directly enforces the protocol spec's requirement that unsupported or lossy features return explicit loss/error data instead of silent deletion (`docs/specs/protocol-translation.md:87-124`, `docs/specs/protocol-translation.md:138-151`).

Property tests for canonical round-trip: Anthropic Messages -> HCSF -> Anthropic Messages must preserve text order, tool call ids, tool results, thinking budget/blocks where enabled, cache_control breakpoints, image/file references, stop reason, and usage fields. OpenAI Chat -> HCSF -> OpenAI Chat must preserve messages, tools, structured-output request fields, image/audio content, finish class, and usage where available (`backend/internal/proto/hcsf.go:11-119`, `docs/research/2026-05-09-axis3-huakai-current-state.md:154-167`).

Capability matrix integration tests: generate a matrix for Anthropic, OpenAI Chat, OpenAI Responses, Gemini, and Bedrock-on-Anthropic with every capability cell in exactly one state: `supported`, `lossy`, `native_required`, `unsupported_error`, or `roadmap`. This implements the spec's capability matrix obligation and the project rule that no reference feature is silently dropped (`docs/specs/protocol-translation.md:87-106`, `docs/03_FEATURE_PARITY_MATRIX.md:109-114`).

Streaming tests: include start-time failure, mid-stream provider failure, post-final cleanup, blocked fallback after bytes are flushed, and opt-in soft termination. This follows the existing streaming forwarder failure taxonomy and the acceptance tests around mid-stream fallback behavior (`docs/specs/streaming-forwarder.md:66-85`, `docs/specs/streaming-forwarder.md:107-165`, `docs/specs/streaming-forwarder.md:177-200`).

Real-account smoke gating: Owner-local smoke should cover Anthropic Messages, OpenAI Chat Completions, OpenAI Responses, Gemini, and Bedrock-on-Anthropic when credentials are available, and every result must be tagged `mock`, `smoke`, or `real` before it influences PMF claims (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:160-167`, `docs/research/2026-05-09-axis3-huakai-current-state.md:236-270`).

Cross-review: any implementation commit should stage changes and run `codex exec review --uncommitted --full-auto`; slice-completion gates should use the full reviewer-lane template because the project requires per-commit Codex review and quoted evidence for slice reviews.

## 5. 失败模式 + 检测

| Failure mode | Required behavior | Detection signal |
|---|---|---|
| Cross-vendor capability does not exist | Return explicit `unsupported_capability` or route to native passthrough; never remove the block silently (`docs/specs/protocol-translation.md:138-151`). | Matrix test requires one status for every capability/vendor cell (`docs/specs/protocol-translation.md:87-106`). |
| Anthropic-rich -> OpenAI lossy projection | Emit `protocol_loss` with field, provider, severity, reason, and suggestion (`docs/specs/protocol-translation.md:108-124`). | Property test fails if lossy downgrade lacks an audit record. |
| Streaming provider fails before first byte | Retry/fallback may be allowed if policy permits and no client bytes were flushed (`docs/specs/streaming-forwarder.md:107-145`). | Streaming failure test checks no partial client transcript exists before fallback. |
| Streaming provider fails after bytes flushed | Do not cross-vendor fallback unless an explicit soft-termination policy is enabled; this avoids mixed-provider output (`docs/specs/streaming-forwarder.md:177-200`, `docs/specs/streaming-forwarder.md:422-428`). | Mid-stream fallback test asserts blocked fallback or clearly marked soft termination. |
| Tool-call argument partials malformed | Surface stream error or recover with typed partial state; never fabricate a completed tool call (`docs/research/2026-05-09-axis3-huakai-current-state.md:154-167`). | Tool delta property test requires completed id/name/arguments only after valid completion. |
| Cache metadata strips provider-required cache hints | Preserve cache graph node or emit loss; never add unrelated metadata that breaks provider cache semantics (`docs/research/2026-05-09-issue-mining-cross-repo.md:108-120`). | Cache fixture compares cache nodes and usage accounting before/after projection. |
| Native passthrough bypasses audit/billing/quota | Native route still emits audit envelope and capability/loss metadata; auth/billing/quota core changes require Owner confirmation. | Native route tests assert audit event and usage placeholder; no real secrets in fixtures. |
| Real credentials invalid or insufficient | Return redacted provider error, mark smoke as failed/blocked, and do not infer provider capability from mocks (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:164-167`). | Smoke report has redacted error class, vendor, capability, and next operator action. |
| Data retention guarantee overclaimed | Mark policy as `asserted`, `provider_contract_required`, or `unknown`; no ZDR claim without vendor/account evidence (`docs/01_PROJECT_BRIEF.md:71-80`). | Release gate rejects any `data_retention=zdr` cell without evidence tag. |

## 6. PMF + Metric 连接

The metric remains monthly annualized inference spend, using mock/smoke/real evidence labels until real traffic exists; OpenRouter's spend anchor is the comparison point in the approved synthesis, but v0.4 should report HUAKAI capability readiness before claiming market spend (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:54-64`, `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:160-167`).

| Phase | Spend metric state | L3 AI-agent backend serviceability | L4 China relay serviceability | PMF gate |
|---|---:|---:|---:|---|
| P-0 | `$0 mock`; schema only | 10%: can describe but not serve agent features | 10%: can describe but not serve CLI schema | HCSF v0.4 fields approved |
| P-1 | `$0 mock`; capability matrix simulated | 25%: matrix exposes agent gaps | 25%: matrix exposes Anthropic/cache/tool gaps | 14 capability nodes validated |
| P-2 | `$0 mock`; local fixtures | 40%: client ingress accepts key agent shapes | 50%: Anthropic-native ingress no longer relies on raw passthrough | covered ingress routes reject nil fallback |
| P-3 | `$0 mock`; provider projections in tests | 60%: OpenAI Responses and Anthropic provider render paths exist | 65%: Anthropic/OpenAI/Gemini/Bedrock projections measurable | every current `ErrNotImplemented` path replaced or explicitly gated |
| P-4 | `$0-$100 smoke budget pending Owner`; native endpoints local | 70%: native bridge for non-normalizable MCP/live/computer/batch | 70%: native bridge for vendor-specific cache/tool edges | native endpoints audited and policy-gated |
| P-5 | mock matrix; no real traffic claim | 80%: capability coverage is test-proven | 80%: CLI pain fixtures covered | property/matrix tests pass; no HIGH cross-review findings |
| P-6 | real smoke spend measured locally | 85%+ if required vendors pass smoke | 85%+ if Anthropic/OpenAI/Gemini/Bedrock smoke pass | Owner approves PMF evidence label as `smoke` or `real` |
| P-7 | real or smoke annualized projection | 90% demo-ready for L3 scope if roadmap gaps are explicit | 90% demo-ready for L4 scope if Chinese CLI path is rich | enter PMF validation or hold with Mandatory Roadmap |

The percentages are planning estimates, not performance claims; they must be replaced by matrix-derived percentages once P-5 produces a generated capability report (`docs/specs/protocol-translation.md:87-106`).

## 7. Fusion-upgrade 三维 delta 表（per capability per vendor）

### 7.1 Per-capability delta

| Capability | upstream A cite | upstream B cite | HUAKAI delta | 维度 |
|---|---|---|---|---|
| `text` | LiteLLM response/stream model: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1797-1876` | Envoy endpoint translator split: `envoyproxy/ai-gateway@4d3eae8b:internal/translator/translator.go:100-117` | Keep text in existing canonical events but embed it in HCSF graph for multi-capability accounting (`backend/internal/proto/hcsf.go:54-119`). | 架构 |
| `tool_use` / `tool_result` | LiteLLM Anthropic adapter behavior: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:421-664` | Portkey shared tool request shape: `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:357-420` | Tool calls/results become graph edges with exact round-trip tests, not best-effort text flattening (`docs/research/2026-05-09-axis3-huakai-current-state.md:154-167`). | 算法 |
| `thinking` / `reasoning` | LiteLLM Anthropic thinking/budget mapping: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:618-707` | Envoy OpenAI thinking structures: `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:868-885`, `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:1400-1461` | Preserve budget and visible reasoning as separate nodes; lossy cross-vendor projection must warn. | 算法 |
| `cache_control` | LiteLLM cache usage fields: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1471-1676` | Portkey OpenAI cache-related request params: `Portkey-AI/gateway@351692fd:src/providers/openai/chatComplete.ts:90-135` | Cache hints and cache-token accounting become auditable nodes because issue mining shows cache metadata is fragile (`docs/research/2026-05-09-issue-mining-cross-repo.md:108-120`). | 生态 |
| `structured_output` | Envoy response-format schema: `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:656-694` | Portkey response format field: `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:454-457` | Strictness is represented explicitly; prompt/tool fallback is lossy by design and must be reported (`docs/specs/protocol-translation.md:108-124`). | 算法 |
| `computer_use` | LiteLLM computer interaction types: `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:166-175` | Portkey arbitrary tool metadata slot: `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:414-420` | Treat as hosted-tool/native capability with approval/audit, not generic function-call parity. | 生态 |
| `file` | LiteLLM file create handler: `BerriAI/litellm@b5d3a5fc:litellm/llms/custom_httpx/llm_http_handler.py:3007-3060` | Portkey file routes: `Portkey-AI/gateway@351692fd:src/index.ts:195-203` | Model file lifecycle separately from multimodal content so lifecycle loss can be audited. | 架构 |
| `image` | Portkey Anthropic image transform: `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:198-266` | Envoy OpenAI content parts: `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:180-224` | Preserve image transport and validation metadata; do not collapse unsupported image to text. | 算法 |
| `audio` | Portkey audio route/request fields: `Portkey-AI/gateway@351692fd:src/index.ts:190-193`, `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:427-488` | Envoy audio content schema: `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:107-128` | Split chat audio from live session audio so websocket/live semantics are not hidden. | 架构 |
| `video` | LiteLLM video content type: `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:459-470` | Envoy OpenAI video content schema: `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:4866-4910` | Keep video as its own modality node and mark unsupported providers explicitly. | 生态 |
| `live` | LiteLLM realtime types: `BerriAI/litellm@b5d3a5fc:litellm/types/realtime.py:12-117` | Portkey realtime routes: `Portkey-AI/gateway@351692fd:src/index.ts:278-281` | v0.4 exposes live as native/session capability, not SSE completion fallback. | 架构 |
| `batch` | LiteLLM batch create handler: `BerriAI/litellm@b5d3a5fc:litellm/llms/custom_httpx/llm_http_handler.py:3340-3455` | Portkey batch routes: `Portkey-AI/gateway@351692fd:src/index.ts:210-231` | Represent batch as async job graph with spend attribution and output evidence. | 生态 |
| `mcp_server` | LiteLLM MCP generated objects: `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:620-634` | Envoy MCP aggregation behavior: `envoyproxy/ai-gateway@4d3eae8b:internal/mcpproxy/handlers.go:1493-1575` | MCP becomes a first-class server/tool capability for L3 agent backends. | 生态 |
| `data_retention` | Portkey storage/safety params: `Portkey-AI/gateway@351692fd:src/providers/openai/chatComplete.ts:90-135` | Envoy backend policy primitives: `envoyproxy/ai-gateway@4d3eae8b:api/v1alpha1/shared_types.go:8-44` | HUAKAI should model retention as evidence-tagged policy, not infer ZDR from request fields. | 生态 |

### 7.2 Per-vendor adapter delta

| Vendor adapter | upstream A cite | upstream B cite | HUAKAI target | 维度 |
|---|---|---|---|---|
| Anthropic Messages | LiteLLM Anthropic pass-through adapter translates through OpenAI-shaped internals: `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/architecture.md:20-50` | Portkey Anthropic Messages config exposes tools/thinking/MCP-style fields: `Portkey-AI/gateway@351692fd:src/providers/anthropic-base/messages.ts:3-67` | `rich` Anthropic-native side-entry, preserving Anthropic-specific capability nodes. | 架构 |
| OpenAI Chat Completions | LiteLLM base transform centers OpenAI parameter mapping: `BerriAI/litellm@b5d3a5fc:litellm/llms/base_llm/chat/transformation.py:264-272` | Portkey OpenAI chat params include modalities/reasoning/cache fields: `Portkey-AI/gateway@351692fd:src/providers/openai/chatComplete.ts:90-135` | `rich` storefront, but not the only canonical; lossy Anthropic projections audited. | 生态 |
| OpenAI Responses | LiteLLM generated interaction types include Responses-style tools/modalities/MCP: `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:62-117`, `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:580-634` | Portkey OpenAI Responses route and params: `Portkey-AI/gateway@351692fd:src/index.ts:233-253`, `Portkey-AI/gateway@351692fd:src/providers/open-ai-base/createModelResponse.ts:90-120` | `rich/native` agent workload surface; public timing is DECISION-POINT. | 生态 |
| Gemini | LiteLLM Gemini adapter has provider-specific transform/config: `BerriAI/litellm@b5d3a5fc:litellm/llms/gemini/chat/transformation.py:18-80` | Portkey common provider framework enumerates provider/endpoint routing: `Portkey-AI/gateway@351692fd:src/providers/types.ts:19-83` | `rich` for core chat/multimodal; `native` for Live until session broker exists. | 架构 |
| Bedrock-on-Anthropic | HUAKAI already inventories Bedrock adapter work: `docs/research/2026-05-09-axis3-huakai-current-state.md:30-58` | Envoy translator pattern keeps provider-family contracts explicit: `envoyproxy/ai-gateway@4d3eae8b:internal/translator/translator.go:34-76` | `rich` where Anthropic-compatible; smoke is Owner/AWS-gated. | 架构 |
| Bedrock-on-Llama | Envoy endpoint schema shows endpoint-specific rather than one-canonical handling: `envoyproxy/ai-gateway@4d3eae8b:internal/endpointspec/endpointspec.go:127-145` | Portkey provider routing supports many provider/endpoint variants: `Portkey-AI/gateway@351692fd:src/providers/types.ts:47-120` | Roadmap native-passthrough first, normalized later. | 生态 |
| Cohere | Envoy has a rerank endpoint family separate from chat/messages: `envoyproxy/ai-gateway@4d3eae8b:internal/endpointspec/endpointspec.go:361-381` | Portkey provider abstraction supports endpoint-specific config: `Portkey-AI/gateway@351692fd:src/providers/types.ts:131-160` | Roadmap; likely separate rerank capability if Owner adds it. | 生态 |
| Mistral | Portkey provider abstraction allows provider-specific request handlers: `Portkey-AI/gateway@351692fd:src/providers/types.ts:131-160` | Envoy translator interface keeps request/response translation bounded per provider: `envoyproxy/ai-gateway@4d3eae8b:internal/translator/translator.go:34-76` | Roadmap native-passthrough first. | 架构 |
| xAI Grok | Market research treats xAI/model churn as a 2026 trend: `docs/research/2026-05-09-market-research-codex.md:59-71` | Portkey multi-provider routing abstraction: `Portkey-AI/gateway@351692fd:src/providers/types.ts:19-83` | Roadmap native-passthrough first; no v0.4 blocker. | 生态 |
| Cursor / Copilot / Kiro / Windsurf / Antigravity | Market research identifies L3 agent backends as PMF target: `docs/research/2026-05-09-market-research-codex.md:17-23` | Envoy MCP proxy shows agent-tool gateway complexity beyond chat translation: `envoyproxy/ai-gateway@4d3eae8b:internal/mcpproxy/handlers.go:1493-1575` | Roadmap plugin/native adapters, likely policy-gated. | 生态 |
| Native passthrough route | Portkey exposes separate endpoint routes for files/batches/responses/realtime: `Portkey-AI/gateway@351692fd:src/index.ts:195-281` | Envoy has endpoint-specific schema switches: `envoyproxy/ai-gateway@4d3eae8b:internal/endpointspec/endpointspec.go:127-145`, `envoyproxy/ai-gateway@4d3eae8b:internal/endpointspec/endpointspec.go:338-353` | `/v1/native/<vendor>/<capability>` with audit/loss/policy, not raw bypass. | 架构 |
| Capability matrix adapter contract | HUAKAI protocol spec requires matrix/loss reporting: `docs/specs/protocol-translation.md:87-124` | LiteLLM generic chunk/usage types show normalized stream/accounting pressure: `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:275-285`, `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1471-1676` | Matrix is the adapter release contract and PMF evidence base. | 算法 |

## 8. Decision points（标 DECISION-POINT 等 Owner 拍板）

DECISION-POINT-001: approve 14 capability nodes for HCSF v0.4, or collapse file/image/audio/video back into one `multimodal` node. Codex recommends 14 because issue mining and upstream surfaces show separate breakage and lifecycle behavior (`docs/research/2026-05-09-issue-mining-cross-repo.md:78-95`, `Portkey-AI/gateway@351692fd:src/index.ts:190-231`).

DECISION-POINT-002: approve exact HCSF v0.4 Go type names and JSON field names before P-0 implementation; renaming after adapter work will create churn because `proto.HCSF` is currently empty and the first real type will set the contract (`backend/internal/proto/proto.go:13-18`).

DECISION-POINT-003: decide whether HCSF v0.4 persists to database in this slice. If yes, database schema changes are high risk under project rules and need explicit Owner confirmation before implementation.

DECISION-POINT-004: decide whether `/v1/responses` is public in v0.4 or only available through `/v1/native/openai/responses`; the approved synthesis requires OpenAI-compatible storefront and native passthrough, but public Responses route timing is not fixed (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:76-123`).

DECISION-POINT-005: define `data_retention` vocabulary and acceptable evidence labels. Codex recommends `unknown`, `request_store_false`, `provider_contract_required`, `regional_asserted`, and `zdr_verified`, with `zdr_verified` forbidden unless Owner supplies vendor/account proof.

DECISION-POINT-006: set release gate thresholds for P-5. Codex recommends no HIGH findings, no silent-drop cells, Anthropic-rich pass for text/tool/thinking/cache/image, and OpenAI Chat pass for text/tool/structured/image before PMF smoke.

DECISION-POINT-007: approve real-account smoke vendor list and budget. Codex recommends Anthropic, OpenAI Chat, OpenAI Responses, Gemini, and Bedrock-on-Anthropic; AWS/Bedrock can be marked blocked if Owner does not provide local credentials (`docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:164-167`).

DECISION-POINT-008: decide native passthrough auth policy before P-4. Native routes can become a policy bypass unless every route is allowlisted, audited, and tied to existing quota/billing boundaries.

DECISION-POINT-009: decide whether any new runtime dependency is allowed for property testing or schema validation. Adding runtime dependencies is high risk under project instructions; test-only dependencies are still safer if Owner approves.

## 9. 风险与盲点（自评）

Observed regions: 54. Inferences: 11. Open questions: 9.

This plan is implementation-oriented but not execution approval: it deliberately stops before code changes because the Owner requested an independent plan lane and because cross-agent synthesis must happen before non-trivial execution.

Clean-room risk is controlled but nonzero: upstream source was read for behavior evidence only; no upstream code, comments, schemas, or file structures should be copied into HUAKAI implementation. The future implementation should use this artifact as requirements evidence, then write HUAKAI-native code and tests from scratch.

Data-retention support is the weakest capability because observed upstream fields such as store/safety or backend policy primitives are not proof of vendor zero-data-retention. Treating ZDR as a normalized boolean would be a product/security risk.

Timing risk is moderate: 10-15 weeks is plausible only if P-0/P-1 avoid over-design and P-3 limits rich support to Anthropic, OpenAI Chat, OpenAI Responses, Gemini, and Bedrock-on-Anthropic. Roadmap vendors must be explicitly preserved as roadmap/native-only rather than pulled into v0.4 implementation.

Testing risk is high if property tests are shallow. The test suite must assert exact expected preservation, not only "not equal to bad"; this follows the project's cross-review smell library.

Security risk is mainly native passthrough, real credentials, data retention, and billing/quota hooks. Those are Owner-gated and should not be changed silently.

## 10. Source citations

Internal HUAKAI sources read and used:

- `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:13-25` — approved HCSF v0.4 synthesis summary.
- `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:36-40` — PMF zone.
- `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:54-64` — inference spend metric framing.
- `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:76-123` — ingress/capability/native architecture.
- `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:125-142` — lane mapping and three-dimensional delta.
- `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md:144-167` — 10-15 week pace and Owner-local smoke.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:14-25` — current HCSF placeholder state.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:30-58` — current provider adapter inventory.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:67-78` — forwarder adapter contract.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:81-132` — coverage and missing vendor-specific fields.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:147-167` — state machine and tool-call gaps.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:182-225` — nil ClientAdapter and phase gaps.
- `docs/research/2026-05-09-axis3-huakai-current-state.md:236-320` — test gaps and top Axis 3 gaps.
- `docs/research/2026-05-09-axis3-protocol-translation-litellm.md:1-7` — LiteLLM single-canonical trap summary.
- `docs/research/2026-05-09-axis3-protocol-translation-portkey.md` — Portkey bi-canonical research artifact used as internal context.
- `docs/research/2026-05-09-axis3-protocol-translation-envoy.md` — Envoy per-endpoint canonical research artifact used as internal context.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:78-95` — Sub2API issue-derived pain points.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:108-120` — New API issue-derived pain points.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:130-144` — Portkey issue-derived pain points.
- `docs/research/2026-05-09-issue-mining-cross-repo.md:204-249` — systemic issues and HUAKAI requirements.
- `docs/research/2026-05-09-market-research-codex.md:17-23` — L3/L4 customer tiers.
- `docs/research/2026-05-09-market-research-codex.md:59-71` — 2026 capability trends.
- `docs/01_PROJECT_BRIEF.md:22-33` — business model.
- `docs/01_PROJECT_BRIEF.md:71-89` — editions and no feature drop gates.
- `docs/03_FEATURE_PARITY_MATRIX.md:57-65` — multimodal/realtime/protocol/model parity rows.
- `docs/03_FEATURE_PARITY_MATRIX.md:109-114` — feature parity review rules.
- `docs/specs/protocol-translation.md:45-85` — protocol translation normal path.
- `docs/specs/protocol-translation.md:87-124` — capability matrix and protocol loss.
- `docs/specs/protocol-translation.md:138-151` — unsupported/lossy failure paths.
- `docs/specs/protocol-translation.md:175-197` — protocol translation acceptance tests.
- `docs/specs/streaming-forwarder.md:54-85` — streaming normal path and end classes.
- `docs/specs/streaming-forwarder.md:107-165` — streaming failure paths.
- `docs/specs/streaming-forwarder.md:177-200` — streaming acceptance tests.
- `docs/specs/streaming-forwarder.md:422-428` — soft termination reserve.
- `backend/internal/proto/proto.go:13-34` — empty `HCSF` and adapter interfaces.
- `backend/internal/proto/hcsf.go:11-119` — existing canonical request/event/usage structs.
- `backend/internal/gateway/forwarder.go:41-43` — optional `ClientAdapter`.
- `backend/internal/gateway/forwarder.go:66-94` — protocol family and adapter validation.
- `backend/internal/gateway/forwarder.go:293-298` — nil `ClientAdapter` raw SSE fallback.
- `backend/internal/proto/anthropic_sse.go:83-89` — unimplemented Anthropic provider request render.
- `backend/internal/proto/openai_sse.go:142-156` — unimplemented OpenAI provider request render.
- `backend/internal/proto/gemini_sse.go:97-111` — unimplemented Gemini provider request render.
- `backend/internal/proto/bedrock_eventstream.go:67-75` — unimplemented Bedrock provider request render.

Source files read:

- `BerriAI/litellm@b5d3a5fc:litellm/llms/base_llm/chat/transformation.py:264-272`
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/architecture.md:20-50`
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:21-1193`
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:1229-1574`
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/streaming_iterator.py:31-455`
- `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:275-285`
- `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1471-1676`
- `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1797-1876`
- `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:62-117`
- `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:166-175`
- `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:382-470`
- `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:580-634`
- `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:785-803`
- `BerriAI/litellm@b5d3a5fc:litellm/types/interactions/generated.py:979-990`
- `BerriAI/litellm@b5d3a5fc:litellm/llms/gemini/chat/transformation.py:18-80`
- `BerriAI/litellm@b5d3a5fc:litellm/llms/custom_httpx/llm_http_handler.py:3007-3060`
- `BerriAI/litellm@b5d3a5fc:litellm/llms/custom_httpx/llm_http_handler.py:3340-3455`
- `BerriAI/litellm@b5d3a5fc:litellm/types/realtime.py:12-117`
- `Portkey-AI/gateway@351692fd:src/providers/types.ts:19-160`
- `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:248-488`
- `Portkey-AI/gateway@351692fd:src/providers/anthropic-base/messages.ts:3-67`
- `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:198-266`
- `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:580-830`
- `Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:145-476`
- `Portkey-AI/gateway@351692fd:src/index.ts:190-281`
- `Portkey-AI/gateway@351692fd:src/providers/openai/chatComplete.ts:90-135`
- `Portkey-AI/gateway@351692fd:src/providers/open-ai-base/createModelResponse.ts:90-120`
- `envoyproxy/ai-gateway@4d3eae8b:internal/translator/translator.go:34-117`
- `envoyproxy/ai-gateway@4d3eae8b:internal/endpointspec/endpointspec.go:127-145`
- `envoyproxy/ai-gateway@4d3eae8b:internal/endpointspec/endpointspec.go:338-381`
- `envoyproxy/ai-gateway@4d3eae8b:internal/endpointspec/endpointspec.go:560-600`
- `envoyproxy/ai-gateway@4d3eae8b:internal/extproc/processor_impl.go:86-105`
- `envoyproxy/ai-gateway@4d3eae8b:internal/extproc/processor_impl.go:240-247`
- `envoyproxy/ai-gateway@4d3eae8b:internal/extproc/processor_impl.go:297-327`
- `envoyproxy/ai-gateway@4d3eae8b:internal/extproc/processor_impl.go:387-435`
- `envoyproxy/ai-gateway@4d3eae8b:internal/extproc/processor_impl.go:580-600`
- `envoyproxy/ai-gateway@4d3eae8b:internal/extproc/processor_impl.go:706-756`
- `envoyproxy/ai-gateway@4d3eae8b:api/v1alpha1/shared_types.go:8-153`
- `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:67-224`
- `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:656-694`
- `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:868-885`
- `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:980-1030`
- `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:1129-1130`
- `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:1400-1461`
- `envoyproxy/ai-gateway@4d3eae8b:internal/apischema/openai/openai.go:4866-4910`
- `envoyproxy/ai-gateway@4d3eae8b:internal/mcpproxy/handlers.go:1493-1575`

## Tail block (per AGENTS.md template)

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: LiteLLM / Portkey gateway / Envoy AI Gateway

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER copy file paths verbatim into output (cite as "Source files read"
    block ONLY at end, not as in-prose references)
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: see Section 10
  Lane: specifier
  Agent: GPT-5 Codex (Codex lane)
  UTC timestamp: 2026-05-09T15:56:05Z

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===
