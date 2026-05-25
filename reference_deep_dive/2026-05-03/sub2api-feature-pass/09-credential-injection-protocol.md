# 09 Credential injection / protocol

## Sub2API behavior summary

Sub2API injects credentials into upstream requests at multiple points in the gateway service: Anthropic API-key passthrough rewrites auth headers, a general upstream request builder injects bearer or API key auth, OAuth mimicry and Claude Code header modes alter passthrough behavior, and Claude Code mimic headers are applied as a separate step. An antigravity forwarding path validates config and injects headers independently. OAuth bearer token and header injection behavior is covered by test assertions. Injection logic is distributed across large gateway service files rather than a single adapter boundary.

## Entity / fields

Inputs: selected account, credential type and version, provider protocol, upstream base URL, header and body rewrite rules, and mimicry profile.

## Request chain

Account selected -> credential lease -> protocol adapter -> credential injector -> sanitized upstream request -> response/error classifier.

## State machine

`credential_selected -> adapter_selected -> headers_sanitized -> credential_injected -> upstream_sent -> response_mapped/error_classified`.

## Failure modes

- Injection hardcoded in handlers spreads secrets.
- OAuth and API-key paths have incompatible passthrough rules.
- Logs can leak auth headers unless redaction is central.

## Sub2API capability

Sub2API has provider-specific injection and mimicry, though evidence suggests injection is spread across large gateway services.

## HUAKAI current capability

Audit proposes `F-ACCAPI-CRED-INJECT-001` in `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md:68`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: real upstream Slice 5 must not start with handler-level injection.

## HUAKAI stronger design

Split `ProtocolAdapter`, `CredentialInjector`, `ErrorClassifier`. Attempts record `adapter_id`, `injector_id`, `credential_version`, redacted request fingerprint.

## Suggested Feature ID / level

- `F-ACCAPI-CRED-INJECT-001`: L1
- `F-PROTO-ADAPTER-001`: L1
- `F-UPSTREAM-ERR-CLASSIFY-001`: L1

## Acceptance tests

- Client auth headers are stripped before injecting upstream credential.
- OAuth account records token version.
- Attempt logs never store tokens.

## Open questions

- open-question: Claude Code mimicry scope for Phase 1.

---
Source files read: sub2api backend/internal/service/gateway_service, backend/internal/service/antigravity_gateway_service, backend/internal/service/openai_oauth_passthrough_test
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
