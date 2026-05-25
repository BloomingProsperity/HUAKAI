# ai-gateway reference delta

## Repo snapshot

- Repo: `.omc/reference-src/ai-gateway`
- Branch: `main`
- Commit: `d63a020f166b`
- Tag: `v0.6.0-rc1`
- File count: `1170`
- State: clean.

## Source areas read

- CRDs and helpers: `.omc/reference-src/ai-gateway/api/v1alpha1/*`, `.omc/reference-src/ai-gateway/api/v1beta1/*`
- Body mutation: `.omc/reference-src/ai-gateway/internal/bodymutator/*`
- Translators: `.omc/reference-src/ai-gateway/internal/translator/*`
- Metrics: `.omc/reference-src/ai-gateway/internal/metrics/genai.go`
- Examples: `.omc/reference-src/ai-gateway/examples/*`
- Quota-aware-routing proposal: `.omc/reference-src/ai-gateway/docs/proposals/009-quota-aware-routing/proposal.md`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | AIRoute combines parent refs, rules, backend refs, model matching, header matching, and backend selection. | `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route.go:56`, `:67`, `:210`, `:223`, `:231` |
| source-confirmed | Route validation distinguishes AIServiceBackend and InferencePool and forbids unsafe mixing. | `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route.go:198`, `:210`, `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route_helper.go:47`, `:55` |
| source-confirmed | Backend refs support model name override, header mutation, body mutation, weight, and priority. | `.omc/reference-src/ai-gateway/api/v1alpha1/ai_gateway_route.go:319`, `:321`, `:336`, `:347`, `:358` |
| source-confirmed | QuotaPolicy supports service quota, per-model quotas, cost expression, shared/exclusive bucket modes, and limit values. | `.omc/reference-src/ai-gateway/api/v1alpha1/quota_policy.go:14`, `:40`, `:50`, `:58`, `:90`, `:116`, `:147` |
| source-confirmed | Body mutator can remove fields, set JSON values, set raw bytes, and preserve original body for retry. | `.omc/reference-src/ai-gateway/internal/bodymutator/body_mutator.go:18`, `:77`, `:86`, `:99`, `:105` |
| source-confirmed | Body mutator tests cover set, remove, set+remove, complex values, no mutation, invalid JSON, and invalid JSON value. | `.omc/reference-src/ai-gateway/internal/bodymutator/body_mutator_test.go:17`, `:44`, `:67`, `:94`, `:147` |
| source-confirmed | Translator interface covers request body, response headers/body, and token usage extraction. | `.omc/reference-src/ai-gateway/internal/translator/translator.go:41`, `:47`, `:53`, `:62` |
| source-confirmed | Anthropic translator extracts explicit cache-token usage and streaming token usage. | `.omc/reference-src/ai-gateway/internal/translator/anthropic_anthropic.go:118`, `:167`, `:181`, `:200` |
| source-confirmed | Metrics include gen-ai token usage, server duration, TTFT, time per output token, and attributes. | `.omc/reference-src/ai-gateway/internal/metrics/genai.go:14`, `:16`, `:19`, `:86` |
| source-confirmed | Examples expose JSON access logs and token rate-limit metadata keys for input/cached/cache-creation/output/total tokens and CEL. | `.omc/reference-src/ai-gateway/examples/access-log/basic.yaml:53`, `.omc/reference-src/ai-gateway/examples/token_ratelimit/token_ratelimit.yaml:48`, `:206` |

## Inferred features

- inferred: ai-gateway is the best reference for declarative operations and Kubernetes-native route policy, not for HUAKAI's immediate SaaS admin UX.
- inferred: Body/header/model mutation can be useful for provider adapter edges, but should be locked behind explicit route policy and tests.

## Open questions

- open-question: Quota-aware routing is a proposal, not proven shipped behavior. Treat `.omc/reference-src/ai-gateway/docs/proposals/009-quota-aware-routing/proposal.md` as future-looking only.
- open-question: Need runtime controller reading before adopting CRD reconciliation behavior.

## HUAKAI delta

- HUAKAI architecture has Router.Plan and provider adapter concepts, but route policy export/import is not explicit.
- Token quota and metrics should include cache creation/read token dimensions, matching Anthropic/OpenAI Responses behavior.
- Body mutation is dangerous unless audit, validation, and original-body retry behavior are defined.

## Suggested Feature IDs

| Feature ID | Name | Level | Delta |
| --- | --- | --- | --- |
| `F-AIGW-CONFIG-001` | Declarative route policy export/import | L3 | Model/header/body mutation, weighted/priority backend refs, validation, and diffable config. |
| `F-AIGW-METRICS-001` | GenAI OTel metrics and access logs | L2 | Token usage, TTFT, time per output token, provider/model attrs, and JSON access logs. |
| `F-TOKEN-QUOTA-POLICY-001` | Token quota policy with cache dimensions | L2/L3 | CEL-like cost expression for input/output/cached/cache-creation tokens and bucket modes. |
| `F-BODY-MUT-001` | Audited request body mutation | L3 | Explicit allow-list, original body preservation, retry safety, validation tests. |
