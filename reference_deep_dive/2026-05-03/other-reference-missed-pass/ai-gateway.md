# Envoy AI Gateway missed pass

## Version

- Branch: `main`
- Commit: `d63a020f166b`
- Tag: `v0.6.0-rc1`
- Files: 1202

## Source areas read

- AIGatewayRoute and backend CRDs.
- Backend security policy.
- Quota policy.
- Endpoint spec parser/translator/redaction.
- Backend auth handlers.

## Behavior-confirmed capabilities

- AIGatewayRoute generates Gateway API resources and exposes model-aware matching through route rules, with separate route rule and backend ref types.
- Backend refs can represent an AI service backend or an inference pool, with fallback behavior separated by backend kind and traffic policy.
- Backend ref supports model name override and traffic weight as distinct fields.
- BackendSecurityPolicy models API key, AWS, Azure, GCP, Anthropic, and OIDC credential styles as separate policy type variants.
- Quota policy supports a default quota, per-model quota overrides, exclusive or shared quota mode, and bucket-based enforcement rules.
- EndpointSpec separates body parsing, translator selection, and sensitive request redaction into distinct interface methods.
- Streaming chat with cost metrics mutates the upstream request to include usage fields so token accounting cannot be bypassed at the stream layer.
- Backend auth handlers inject provider-specific auth headers at the transport layer, with AWS request signing as a distinct handler.

## HUAKAI gap

HUAKAI should not become Kubernetes-first, but Envoy AI Gateway shows a cleaner architecture: route policy, backend auth policy, quota policy, endpoint spec, translator, and redactor are separate. HUAKAI's account-to-API spine should copy that separation of concerns at the behavior level.

## Upgrade design

- Define `RoutePolicy`, `BackendCredentialPolicy`, `QuotaPolicy`, and `EndpointSpec` as independent contracts.
- For streaming, force usage inclusion or equivalent accounting proof before allowing cost-sensitive streams.
- Make `RedactSensitiveInfoFromRequest` mandatory for every endpoint adapter before debug logging is enabled.
- Support model-name override as part of account selection plan, not ad hoc body rewrite.

## Suggested Feature IDs

- `F-POLICY-SPLIT-001` L2: separated route/backend-auth/quota policy contracts.
- `F-STREAM-USAGE-PROOF-001` L1: streaming usage accounting guard.
- `F-ADAPTER-REDACTION-001` L1: mandatory endpoint adapter redaction.
- `F-MODEL-OVERRIDE-PLAN-001` L2: model override in route plan.

## Acceptance test direction

- Adapter without redactor cannot enable debug logging.
- Streaming request to a costed model is mutated or rejected if usage cannot be proven.
- Backend credential policy swaps API key and AWS signing without changing route handler.

## Open questions

- Whether HUAKAI should expose declarative YAML in L2 or keep policies DB-backed first.
- Whether per-model quota belongs in Quota Lite L2 or Billing L3.

---
Source files read: ai-gateway api/v1alpha1/ai_gateway_route, api/v1alpha1/backendsecurity_policy, api/v1alpha1/quota_policy, internal/endpointspec/endpointspec, internal/backendauth/auth, internal/backendauth/api_key, internal/backendauth/anthropicapikey, internal/backendauth/azureapikey, internal/backendauth/aws
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
