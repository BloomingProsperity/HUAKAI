# ai-gateway topology / policy deep dive

## Snapshot

- Reference repo: `.omc/reference-src/ai-gateway`
- Branch: `main`
- Commit: `d63a020f166b`
- Tag / describe: `v0.6.0-rc1`
- Tracked file count: `1202`
- State: clean
- Review mode: source-level behavior extraction. This project is most useful for declarative edge policy and GenAI telemetry, less useful for HUAKAI's immediate self-hosted admin workflow.

## Source areas read

- Route CRD and helper logic:
  - `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route.go`
  - `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route_helper.go`
- Quota policy CRD:
  - `.omc/reference-src/ai-gateway/api/v1alpha1/quota_policy.go`
- Body mutation:
  - `.omc/reference-src/ai-gateway/internal/bodymutator/body_mutator.go`
  - `.omc/reference-src/ai-gateway/internal/bodymutator/body_mutator_test.go`
- Translator and token extraction:
  - `.omc/reference-src/ai-gateway/internal/translator/translator.go`
  - `.omc/reference-src/ai-gateway/internal/translator/anthropic_anthropic.go`
- Metrics and examples:
  - `.omc/reference-src/ai-gateway/internal/metrics/genai.go`
  - `.omc/reference-src/ai-gateway/examples/token_ratelimit/token_ratelimit.yaml`
  - `.omc/reference-src/ai-gateway/examples/access-log/basic.yaml`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | `AIGatewayRoute` is a declarative route object that attaches AI service backends to Gateway resources. | `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route.go:13`, `:37`, `:57` |
| source-confirmed | A route rule can reference AIServiceBackend or InferencePool, but validation forbids mixing them in one rule and allows only one InferencePool backend. | `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route.go:198`, `:199`, `:210`, `:211`, `:214` |
| source-confirmed | Helpers expose typed checks for InferencePool vs AIServiceBackend and rule-level backend type checks. | `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route_helper.go:46`, `:47`, `:55`, `:56`, `:60`, `:73` |
| source-confirmed | Backend references support model override, header mutation, body mutation, weight, and priority. | `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route.go:319`, `:321`, `:327`, `:329`, `:336`, `:338`, `:347`, `:348`, `:358` |
| source-confirmed | QuotaPolicy supports global quota, per-model quotas, CEL cost expression, shared/exclusive bucket modes, client selectors, and limit values. | `.omc/reference-src/ai-gateway/api/v1alpha1/quota_policy.go:24`, `:33`, `:40`, `:50`, `:54`, `:58`, `:90`, `:112`, `:116`, `:131`, `:150` |
| source-confirmed | Cost expressions explicitly model cache-aware token burn such as cached input and output multipliers. | `.omc/reference-src/ai-gateway/api/v1alpha1/quota_policy.go:54`, `:58`, `:83`, `:87`, `.omc/reference-src/ai-gateway/examples/token_ratelimit/token_ratelimit.yaml:49`, `:52`, `:54`, `:57`, `:59` |
| source-confirmed | Body mutation removes JSON fields and sets raw JSON or string values. | `.omc/reference-src/ai-gateway/internal/bodymutator/body_mutator.go:86`, `:90`, `:99`, `:105`, `:106`, `:109` |
| source-confirmed | Body mutation tests cover set, remove, set+remove, complex values, malformed JSON, and invalid JSON value behavior. | `.omc/reference-src/ai-gateway/internal/bodymutator/body_mutator_test.go:17`, `:44`, `:67`, `:96`, `:149`, `:157`, `:171` |
| source-confirmed | Translator interface covers request body, response headers, response body, and token usage extraction. | `.omc/reference-src/ai-gateway/internal/translator/translator.go:41`, `:47`, `:53`, `:62`, `:65` |
| source-confirmed | Anthropic translator extracts cache-read and cache-creation tokens from non-streaming responses and streaming buffer events. | `.omc/reference-src/ai-gateway/internal/translator/anthropic_anthropic.go:118`, `:121`, `:122`, `:167`, `:170`, `:171`, `:181`, `:200`, `:204` |
| source-confirmed | Metrics include GenAI token usage, server request duration, TTFT, time per output token, provider/model attributes, and token-type labels for cached/cache-creation/reasoning. | `.omc/reference-src/ai-gateway/internal/metrics/genai.go:14`, `:16`, `:17`, `:20`, `:21`, `:22`, `:23`, `:53`, `:54`, `:55`, `:65`, `:75`, `:79` |
| source-confirmed | Examples expose JSON access logs, buffer limits, and request-cost metadata for input/output/total tokens. | `.omc/reference-src/ai-gateway/examples/access-log/basic.yaml:51`, `:91`, `:93`, `:95`, `:97`, `:120` |

## Inferred items

- inferred: ai-gateway is mainly a Kubernetes/Envoy control-plane reference. It should inform HUAKAI's declarative policy and telemetry contracts, not force HUAKAI into CRDs for the current product.
- inferred: Header/body/model mutation is powerful but dangerous. HUAKAI should only expose it behind explicit provider-adapter policy, audit, and regression tests.
- inferred: The most transferable feature is token-dimensional policy: input, cached input, cache creation, output, total, and reasoning tokens should be first-class in metrics and quota.

## Open questions

- open-question: Need controller/runtime reconciliation read before using ai-gateway as evidence for operational behavior beyond CRD semantics.
- open-question: Need confirm whether quota policy is fully shipped runtime behavior or partly example/proposal-driven in this commit.
- open-question: Need read of extproc body buffering paths before adopting its buffering limits as production precedent.

## HUAKAI delta

| HUAKAI area | Current status from plan files | Delta |
| --- | --- | --- |
| Routing | `Router.Plan` exists, and feature matrix includes weighted/priority/model routing. | Lacks explicit import/exportable route policy with validation and diff semantics. |
| Token accounting | HUAKAI already tracks cache and reasoning-related billing concerns in architecture notes. | Need make cache-read/cache-creation/reasoning token types explicit in quota, metrics, logs, and acceptance tests, not only billing. |
| Protocol adapters | HUAKAI has `F-PROTO-002` spec released. | Body/header mutation must be limited to adapter-owned transformations; arbitrary admin mutation can become a data-leak/semantic-loss vector. |
| Observability | Observability matrix says investigation path from request to user/key/route/account/usage/billing/audit. | Need standardized GenAI metric names or local equivalent: TTFT, time per output token, token-type dimension, provider/model attrs. |
| Infra model | HUAKAI is self-hosted app/gateway now, not Kubernetes-only. | Treat CRD design as L4 export/import or enterprise mode, not L1/L2 requirement. |

## Recommended HUAKAI insertions

| Feature ID | Name | Level | Recommendation |
| --- | --- | --- | --- |
| `F-AIGW-CONFIG-001` | Declarative route policy export/import | L3 | A JSON/YAML route policy with backend refs, model/header/body match, weight/priority, validation, dry-run diff, and audit. |
| `F-AIGW-METRICS-001` | GenAI metrics contract | L2 | Record token usage by input/output/cached/cache-creation/reasoning, TTFT, total request duration, time per output token, provider, original/request/response model. |
| `F-TOKEN-QUOTA-POLICY-001` | Token quota with cache dimensions | L2/L3 | Request and cost quota policy should allow different burn rates for cached input and cache-creation tokens. Acceptance tests must prove bucket math. |
| `F-BODY-MUT-001` | Audited provider-adapter body mutation | L3 | Allow only adapter-owned field set/remove with original body preserved for retry, audit log, and invalid JSON tests. Keep out of L1. |
| `F-EDGE-TOPOLOGY-001` | Optional edge/control-plane topology | L4 | For Kubernetes/enterprise deployments, support external gateway or sidecar style control plane. Not needed for Personal MVP. |

## Production reviewer critique

ai-gateway is not a "feature-complete commercial gateway" reference in the same sense as Sub2API or New API. It is a clean reference for declarative edge behavior: route objects, policy validation, token-aware quota, body/header mutation, and GenAI metrics.

For HUAKAI, the best move is to borrow the contract shape: route policy should be diffable, token classes should be explicit, and body mutation should be audited and test-covered. Do not promote Kubernetes CRD machinery into the near-term roadmap unless the deployment target really needs it.
