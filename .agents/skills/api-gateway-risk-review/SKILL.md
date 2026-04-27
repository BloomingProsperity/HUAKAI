---
name: api-gateway-risk-review
description: Use when reviewing gateway, routing, account, quota, billing, protocol, reliability, security, and observability designs for production risk.
---

This file is agent-facing and authoritative.

# API Gateway Risk Review

Full feature parity or better remains mandatory; production risk changes controls and rollout, not product scope.

## Areas

- Routing correctness.
- Provider fallback.
- Retry behavior.
- Streaming behavior.
- Protocol conversion.
- Provider account state.
- Quota reservation and enforcement.
- Billing and usage accounting.
- Secret handling.
- Audit logging.
- Observability.

## Review Steps

1. Identify the user or operator workflow.
2. Identify failure modes and abuse cases.
3. Check whether state changes are auditable.
4. Check whether billing and quota remain consistent under retries and concurrency.
5. Check whether secrets are redacted at capture and render boundaries.
6. Add bug patterns or tests when gaps are found.

## Output

Prioritized production risks with concrete mitigation and test direction.
