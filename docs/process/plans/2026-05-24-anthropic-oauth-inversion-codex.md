# 2026-05-24 Anthropic Pro/Max OAuth Inversion Plan - Codex Lane

| Field | Value |
| --- | --- |
| Owner directive | `[OWNER AUTHORIZED 2026-05-24T07:30Z workspace-write — refs 已拉 latest 重派] 你是 codex lane,独立写 Anthropic Pro/Max OAuth 反转的实施 plan。` |
| Output path | `/home/codex/HUAKAI/docs/process/plans/2026-05-24-anthropic-oauth-inversion-codex.md` |
| Lane | `specifier` |
| Ceremony | 高难度 |
| Reference anchor | `docs/process/2026-05-24-ref-anchor.md` |
| Primary reference | `router-for-me/CLIProxyAPI@50d19e204fed` |
| Ref source rule | 只读取 Owner 指定的 CLIProxyAPI Anthropic OAuth files; 不读取 Claude 平行 plan; 不使用 `~/refs/` old clone SHA |
| Current local gap | `backend/internal/provider/registrydefault/default.go:106` registers `anthropic_messages` to API-key passthrough only |

## §1 目标范围

### 1.1 Goal

Build a production-ready plan for Anthropic Pro/Max subscription-account OAuth inversion in the HUAKAI Go backend.

This plan covers the backend path that lets an operator acquire an Anthropic subscription credential, store it through the F-CRED-001 and F-AUTH-005 boundaries, refresh it safely, and dispatch Anthropic Messages-shaped requests using that OAuth-derived runtime credential.

The plan deliberately does not pick a transport implementation. Transport remains an Owner decision in §7 because the reference project has one concrete transport strategy, while HUAKAI already has a transport abstraction and current production constraints.

### 1.2 In Scope

- OAuth authorization URL construction with PKCE and state.
- Callback handling strategy for Anthropic OAuth flows.
- Token exchange from authorization code to access/refresh credential candidate.
- Runtime request adapter for Anthropic subscription credential material.
- Refresh behavior alignment with current `credentialworker` and F-AUTH-005.
- Registry/default routing decision for API-key Anthropic vs subscription Anthropic.
- Tests with discriminating fixtures and explicit mutation expectations.
- File-level implementation scope that avoids new files in frozen packages.
- Owner decision points with reference-project citations.

### 1.3 Out of Scope

- No code implementation in this plan-writing pass.
- No database schema migration in this plan unless Owner later decides the existing acquisition session table cannot carry required metadata.
- No `LICENSE` edits.
- No production secrets, real OAuth client secret, real refresh token, or real subscription account material.
- No Git staging, commit, or push.
- No claims about sub2api / litellm / other references beyond the anchor metadata unless a later lane reads their source under the required guard.

### 1.4 Success Criteria

1. API-key Anthropic traffic remains supported through the existing official API-key path.
2. Subscription OAuth traffic has an explicit, testable runtime adapter path and does not masquerade as API-key passthrough.
3. OAuth start/callback/finalize integrates with F-CRED-001 without persisting raw authorization code, verifier, access token, refresh token, or session material in acquisition session metadata.
4. F-AUTH-005 can resolve runtime material for `anthropic/claude_ai_oauth` and `anthropic/claude_code` into the credential type expected by the selected outbound adapter.
5. Refresh uses the existing transaction/advisory-lock discipline and adds missing Anthropic-specific classification only where needed.
6. Tests fail under the specific mutations listed in §6.
7. All new files land outside frozen packages. Existing frozen-package files may receive narrow wiring edits only if Owner chooses a route strategy that requires it.

### 1.5 Time Estimate

| Work | Estimate |
| --- | ---: |
| C1 runtime adapter and tests | 0.5-1 day |
| C2 OAuth acquisition exchange package and tests | 1 day |
| C3 callback/finalize wiring | 0.5-1 day |
| C4 refresh alignment and tests | 0.5 day |
| C5 registry/model-routing integration tests | 0.5 day |
| C6 transport decision implementation after Owner choice | 0.5-2 days |
| C7 verification/runbook/docs | 0.5 day |

Total: 3.5-7 engineering days depending on §7 transport and vendoring decisions.

### 1.6 Blast Radius

- Provider request construction for Anthropic.
- Credential acquisition callback and finalize behavior.
- Credential refresh worker behavior for Anthropic OAuth modes.
- Provider registry default mapping and tests.
- Gateway dispatch only through existing adapter/transport interfaces.
- Admin credential-acquisition HTTP route only if Owner selects server-side callback exchange.

### 1.7 Failure Modes To Plan Against

- OAuth callback state mismatch creates credential for wrong tenant/account.
- PKCE verifier lost or exposed in audit/log output.
- API-key path accidentally starts accepting subscription tokens or vice versa.
- Refresh storm caused by many expired subscription credentials.
- Retry-after handling ignored, causing repeated token endpoint hits.
- Runtime adapter sends subscription token to the official API-key header shape.
- Registry collapse makes `anthropic_messages` ambiguous across API-key and subscription paths.
- Transport mismatch either weakens stability or blocks production by default.
- Tests pass even if the code ignores PKCE/state, OAuth mode, or credential type.

### 1.8 Clean-Room Boundary

The reference project is MIT per the anchor table, so Owner permits a vendoring option under CLAUDE.md #12. However, this Codex artifact remains a specifier-lane behavior plan by default.

I read the specified source files and record only behavior-level observations. I do not copy upstream function names into the implementation plan, do not paste source, do not preserve upstream file structure as a proposed local structure, and do not translate algorithms line-by-line.

## §2 现状缺口

### 2.1 Current Registry Behavior

Observed local code:

- `ProtocolAnthropicMessages` is currently the family string for Anthropic Messages.
- Default registry maps it to API-key passthrough at `backend/internal/provider/registrydefault/default.go:106`.
- The file header explicitly says Anthropic OAuth inversion is not registered yet and unverified session placeholders must be opt-in only at `backend/internal/provider/registrydefault/default.go:4`.
- API-key passthrough accepts `apikey` and `upstream_passthrough`, rejects OAuth access token, and sends `X-API-Key`, not bearer authorization, at `backend/internal/provider/anthropic/passthrough.go:42`.

Implication:

- A subscription credential resolved as `oauth_access_token`, `session_token`, or `upstream_passthrough` cannot safely reuse the current passthrough adapter without an explicit mode split.
- If the system simply stores an OAuth access token and lets `anthropic_messages` keep using `PassthroughAdapter`, `CredentialTypeOAuthAccessToken` is rejected.
- If the system stores the OAuth material as `upstream_passthrough`, the current passthrough adapter can send `Authorization` only when `auth_header` is set, but this would blur official API-key and subscription-account semantics.

### 2.2 Current Credential Store Support

Observed local code:

- F-AUTH-005 mode registry already has `anthropic/claude_ai_oauth` and `anthropic/claude_code` modes at `backend/internal/credentialstore/types.go:238`.
- `claude_ai_oauth` currently resolves to runtime upstream-passthrough material when it has `access_token` or `refresh_token` at `backend/internal/credentialstore/types.go:240`.
- `claude_code` currently resolves to session-token material when it has `session_token`, `access_token`, or `refresh_token` at `backend/internal/credentialstore/types.go:241`.
- `PostgresCredentialVault` prefers `account_credentials` v2 and maps runtime material to provider credential types at `backend/internal/provider/postgres_vault.go:74` and `backend/internal/provider/postgres_vault.go:150`.

Implication:

- The storage layer has enough mode vocabulary to distinguish API key, Claude web OAuth, and Claude Code-like credentials.
- The runtime adapter must be precise about which `CredentialType` values it accepts for each mode.
- If the chosen design uses one adapter for both `claude_ai_oauth` and `claude_code`, tests must prove it still rejects API keys.

### 2.3 Current Acquisition Flow Support

Observed local code:

- F-CRED-001 already has canonical start/status/callback/cancel/finalize endpoints in `gatewayhttp` at `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:76`.
- The helper route for OAuth init exists at `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:84`.
- The current callback path returns "oauth exchange adapter not configured" unless credentials are supplied in the request at `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:148`.
- The helper callback route also returns "oauth exchange adapter not configured" at `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:308`.
- The acquisition session table already contains `state_hash`, `encrypted_pkce_verifier`, `client_identity_source`, `redirect_uri`, `requested_scopes`, and `long_lived_requested` at `backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql:38`.

Implication:

- The main missing piece is a production exchange service and a clean dependency seam from the existing callback handler to that service.
- The plan should avoid new files in `gatewayhttp`; it can create a new exchange package and perform the smallest possible modification to the existing handler if server-side callback exchange is selected.

### 2.4 Current Refresh Support

Observed local code:

- `credentialworker` registers Anthropic refresh behavior for both `claude_ai_oauth` and `claude_code` at `backend/internal/credentialworker/mode_refresh.go:57`.
- Refresh happens inside a store transaction and takes the acquisition refresh lock before reloading the credential at `backend/internal/credentialworker/mode_refresh.go:143`.
- The existing Anthropic refresh adapter posts JSON refresh-token payload to a token endpoint and can reject long-lived setup-token mode unless explicitly enabled at `backend/internal/credentialworker/adapters/anthropic.go:22`.

Implication:

- Refresh should not be rebuilt from scratch.
- The implementation should upgrade error classification and backoff behavior where the current adapter is weaker than the observed reference behavior.
- Any long-lived setup-token path remains Owner-gated.

### 2.5 Current Transport Support

Observed local code:

- `UpstreamDispatcher` chooses a provider adapter, then asks `transport.Factory` for a provider/mode RoundTripper at `backend/internal/gateway/upstream_dispatcher.go:105`.
- Standard transport strips environment proxy defaults and only applies account-bound proxy through `ProxyResolver` at `backend/internal/transport/factory.go:119`.
- Mimicry transport exists but fail-closes when template registry is missing or a template is stubbed at `backend/internal/transport/factory.go:159`.
- `backend/go.mod` already includes `github.com/refraction-networking/utls v1.8.2`.

Implication:

- Transport is an implementation option, not a blocker.
- The plan must present transport choices in §7 instead of assuming upstream's transport.
- If Owner selects transport mimicry for Anthropic OAuth, it can use HUAKAI's existing transport abstraction instead of copying upstream transport layout.

## §3 ref 项目方案

### 3.1 Source Regions Read

The allowed reference source read is CLIProxyAPI's Anthropic OAuth area under the 2026-05-24 anchor SHA.

Observed behavior categories:

1. Static OAuth endpoints and public client identity are defined in the auth module.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:23`.

2. The authorization request includes response type, redirect URI, scopes, PKCE challenge, challenge method, and state.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:190`.

3. PKCE material is generated from random bytes, base64url encoding, and SHA-256 challenge derivation.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/pkce.go:21`.

4. The token exchange posts JSON with authorization code, state, grant type, public client identity, redirect URI, and PKCE verifier.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:241`.

5. The token response is parsed into access token, refresh token, email, and expiry timestamp.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:297`.

6. Refresh uses the refresh token, public client identity, JSON content type, and token endpoint.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:355`.

7. Refresh has single-flight collapse keyed by refresh token and short-term blocking for upstream rate limits.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:334`.

8. Retry delay honors both seconds/date and millisecond retry-after headers, clamped between minimum and maximum delay.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:89`.

9. Retry wrapper stops early on non-retryable refresh errors.
   Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:461`.

10. Local callback server listens for callback and success paths, checks port availability, and uses bounded read/write timeouts.
    Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:72`.

11. Callback handling rejects missing code or missing state and passes successful values to a waiting channel.
    Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:168`.

12. Token storage writes a JSON object to disk after creating the parent directory with restricted permissions.
    Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/token.go:60`.

13. The HTTP client can use a custom TLS/HTTP2 transport with per-host connection cache and optional proxy dialer.
    Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:31`.

14. The custom transport opens an HTTP/2 connection after a uTLS handshake using a browser-like client profile.
    Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:101`.

15. Failed HTTP/2 requests remove the cached connection for that host before returning the error.
    Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:126`.

### 3.2 Behavior To Preserve In HUAKAI

HUAKAI should preserve these user-visible outcomes:

- Admin can start a browser OAuth flow for an Anthropic subscription account.
- The flow returns a URL containing PKCE and state.
- Callback cannot complete without matching state and a stored verifier.
- Exchange yields a credential candidate with access token, refresh token, account email, and expiry metadata.
- Refresh deduplicates same-credential refreshes and respects upstream rate-limit backoff.
- Runtime dispatch uses the access token in a subscription-account auth shape, not the API-key `X-API-Key` shape.
- Operator sees redacted status and audit events without token bytes.
- Account-bound proxy and transport policy remain HUAKAI decisions, not hidden inside the auth exchange logic.

### 3.3 Behavior To Improve In HUAKAI

HUAKAI should improve over the observed reference shape in these ways:

- Use tenant-scoped acquisition sessions instead of a local loopback server as the only callback mechanism.
- Store PKCE verifier encrypted in HUAKAI's acquisition session boundary rather than in process memory only.
- Enforce idempotent callback/finalize consumption with database state, not only a local channel.
- Keep refresh under the existing F-AUTH-005 transaction and advisory-lock discipline.
- Keep runtime dispatch inside provider adapter + transport factory boundaries.
- Preserve API-key and subscription-account paths as separate modes with separate tests.
- Apply privacy redaction and audit allowlists before any event/log write.

### 3.4 Clean-Room Implementation Stance

Default stance:

- Reimplement behavior in HUAKAI-owned packages and types.
- Use local naming aligned with `credentialacq`, `credentialstore`, `provider`, and `transport`.
- Do not mirror upstream file names or package boundaries.
- Do not copy upstream function names or data structures.

Permitted alternative:

- Since the anchor table records CLIProxyAPI as MIT, Owner may choose direct vendoring for selected files into an isolated `backend/vendor/` or `pkg/external/` area with preserved license and modification notes.
- This remains a decision point in §7 because vendoring speeds transport/auth parity but increases local maintenance and attribution surface.

## §4 文件级范围

### 4.1 Frozen Package Rule

Frozen packages from AGENTS.md:

- `backend/internal/gatewayhttp` - no new files.
- `backend/internal/gateway` - no new files.
- `backend/internal/proto` - no new files.

Allowed in frozen packages:

- Narrow edits to existing files only when necessary for wiring or tests.
- No new handler family file in `gatewayhttp`.
- No new dispatcher file in `gateway`.
- No new protocol adapter file in `proto`.

### 4.2 Proposed New Files Outside Frozen Packages

| File | Package | Frozen? | Responsibility |
| --- | --- | --- | --- |
| `backend/internal/provider/anthropic/oauth_session.go` | `anthropic` | No | Runtime provider adapter for Anthropic subscription-account OAuth/session material. |
| `backend/internal/provider/anthropic/oauth_session_test.go` | `anthropic` | No | Discriminating adapter tests for credential types, headers, endpoint, body, and API-key rejection. |
| `backend/internal/anthropicoauth/flow.go` | `anthropicoauth` | No | HUAKAI-owned OAuth URL and token-exchange service using local PKCE/state contracts. |
| `backend/internal/anthropicoauth/flow_test.go` | `anthropicoauth` | No | PKCE/state/exchange tests with local mock HTTP server and mutation checks. |
| `backend/internal/anthropicoauth/redaction.go` | `anthropicoauth` | No | Token-free preview and error classification helpers for acquisition and refresh. |
| `backend/internal/anthropicoauth/redaction_test.go` | `anthropicoauth` | No | Tests proving no token-shaped data enters preview/error output. |

Package notes:

- `backend/internal/provider/anthropic` remains cohesive because it owns Anthropic outbound request construction.
- `backend/internal/anthropicoauth` is a new package because acquisition/exchange is not provider adapter construction and should not grow `credentialacq` or `gatewayhttp`.
- `backend/internal/credentialacq` already has 16 Go files; avoid adding provider-specific OAuth files there unless Owner explicitly prefers a subpackage under it.
- `backend/internal/credentialworker/adapters` is not frozen; edits to existing Anthropic refresh adapter are acceptable, but adding many provider-specific files should be avoided.

### 4.3 Existing Files Likely Modified

| File | Package | Scope | Risk |
| --- | --- | --- | --- |
| `backend/internal/provider/registrydefault/default.go` | `registrydefault` | Register selected Anthropic subscription adapter or new protocol family after Owner chooses §7 D2. | Medium: changes startup registry. |
| `backend/internal/provider/registrydefault/default_test.go` | `registrydefault` | Assert API-key and subscription protocol family mapping. | Low. |
| `backend/internal/credentialstore/types.go` | `credentialstore` | Possibly adjust runtime kind for `claude_ai_oauth` / `claude_code` if §7 D3 chooses a unified runtime type. | Medium: credential runtime contract. |
| `backend/internal/credentialstore/types_test.go` | `credentialstore` | Assert mode registry and runtime material discrimination. | Low. |
| `backend/internal/credentialworker/adapters/anthropic.go` | `credentialworker/adapters` | Add retry-after/backoff classification if §7 D6 chooses refresh upgrade. | Medium: refresh behavior. |
| `backend/internal/credentialworker/refresher_test.go` | `credentialworker` | Add Anthropic refresh backoff and non-retryable tests. | Low. |
| `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go` | `gatewayhttp` | Existing-file-only wiring to call `anthropicoauth` exchange service if §7 D4 chooses server-side callback exchange. | Medium: frozen package existing file; no new file. |
| `backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go` | `gatewayhttp` | Existing test file update only if route wiring changes. | Low/Medium. |
| `backend/cmd/gateway/wiring.go` | `gateway` cmd package | Inject exchange service / transport setting after Owner decision. | Medium: production wiring. |

### 4.4 Files To Avoid

- Do not create `backend/internal/gatewayhttp/anthropic_oauth_handler.go`.
- Do not create `backend/internal/gateway/anthropic_oauth_dispatcher.go`.
- Do not create `backend/internal/proto/anthropic_oauth*.go`.
- Do not edit `LICENSE`.
- Do not add runtime dependencies without Owner confirmation.
- Do not add schema migrations without Owner confirmation.

### 4.5 Config Surface

Config keys should be explicit and non-secret by default:

- `HUAKAI_ANTHROPIC_OAUTH_CLIENT_ID` optional override.
- `HUAKAI_ANTHROPIC_OAUTH_AUTH_URL` optional override for tests/dev.
- `HUAKAI_ANTHROPIC_OAUTH_TOKEN_URL` optional override for tests/dev.
- `HUAKAI_ANTHROPIC_OAUTH_REDIRECT_BASE_URL` required if server-side callback is selected.
- `HUAKAI_ANTHROPIC_OAUTH_TRANSPORT_MODE` only if Owner chooses configurable transport.
- No client secret is committed or required for the public-client path.

If Owner chooses vendoring, add:

- `backend/vendor/cliproxyapi/README.md`.
- `backend/vendor/cliproxyapi/LICENSE`.
- `backend/vendor/cliproxyapi/NOTICE`.
- `backend/vendor/cliproxyapi/MODIFICATIONS.md`.

That vendoring path is not the default recommendation.

## §5 切片 C1..Cn

### C1 - Runtime Anthropic Subscription Adapter

Goal:

- Introduce a provider adapter that accepts subscription OAuth/session runtime material and rejects API-key credentials.

Files:

- Create `backend/internal/provider/anthropic/oauth_session.go`.
- Create `backend/internal/provider/anthropic/oauth_session_test.go`.
- Modify `backend/internal/provider/registrydefault/default.go` only after §7 D2 is decided.
- Modify `backend/internal/provider/registrydefault/default_test.go` only after §7 D2 is decided.

Behavior:

- Accept `CredentialTypeOAuthAccessToken` for `claude_ai_oauth`.
- Accept `CredentialTypeSessionToken` if Owner wants `claude_code` to use the same runtime adapter.
- Accept `CredentialTypeUpstreamPassthrough` only when the credential explicitly supplies a safe auth header.
- Reject `CredentialTypeAPIKey` and direct the operator to API-key passthrough.
- Build POST request to the selected Anthropic endpoint.
- Preserve inbound Anthropic Messages body as provided by HCSF/protocol layer.
- Set `Content-Type` and `Accept` to JSON.
- Set the auth header selected by the mode decision.
- Preserve optional Anthropic version/beta headers only through explicit `Extra` keys.

Reference basis:

- Token exchange returns access and refresh material plus account email/expiry metadata, which makes runtime dispatch depend on access-token material rather than API-key material. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:297`.
- Refresh returns the same token-data shape after exchanging a refresh token. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:420`.

Implementation checks:

- Run `go test ./internal/provider/anthropic ./internal/provider/registrydefault` from `backend/`.
- Mutation: if the adapter starts accepting `apikey`, `TestOAuthSessionAdapterRejectsAPIKey` must fail.
- Mutation: if the adapter sends `X-API-Key` for OAuth/session mode, `TestOAuthSessionAdapterUsesSubscriptionAuthShape` must fail.

### C2 - OAuth URL + PKCE + Exchange Service

Goal:

- Build a HUAKAI-owned service that creates authorization instructions and exchanges callback material for a credential candidate.

Files:

- Create `backend/internal/anthropicoauth/flow.go`.
- Create `backend/internal/anthropicoauth/flow_test.go`.
- Create `backend/internal/anthropicoauth/redaction.go`.
- Create `backend/internal/anthropicoauth/redaction_test.go`.

Behavior:

- Generate verifier and challenge with cryptographic randomness and S256 challenge method.
- Build authorization URL with public client identity, redirect URI, scopes, challenge, challenge method, and state.
- Return a redacted `StartResult` to acquisition flow: URL, state hash reference, verifier envelope reference, expiry, scopes, client identity source.
- Exchange callback code using stored verifier.
- Validate state before any token endpoint request.
- POST JSON token exchange request to configured token endpoint.
- Parse access token, refresh token, expiry, account email, and any safe organization/account metadata into a credential candidate.
- Never put raw code/verifier/tokens into redacted context or audit payload.

Reference basis:

- Authorization URL includes state, challenge, and S256 method. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:190`.
- PKCE challenge generation uses random verifier bytes and SHA-256 challenge derivation. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/pkce.go:21`.
- Token exchange sends code, state, grant type, client identity, redirect URI, and verifier. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:241`.

Implementation checks:

- Run `go test ./internal/anthropicoauth`.
- Mutation: if state validation is moved after token exchange, the mock token server call count test must fail.
- Mutation: if the verifier is omitted from token exchange, request-body assertion must fail.
- Mutation: if redaction allows token-shaped values, redaction test must fail.

### C3 - Acquisition Callback Wiring

Goal:

- Connect F-CRED-001 callback/finalize to the Anthropic exchange service without growing frozen packages.

Files:

- Prefer dependency injection through existing `AdminCredentialAcquisitionDeps` in `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go`.
- Add tests in existing `backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go`.
- Wire service in `backend/cmd/gateway/routes.go` or `backend/cmd/gateway/wiring.go` depending on current route construction.

Behavior:

- For `vendor=anthropic` and `auth_mode=claude_ai_oauth` or `claude_code`, callback can invoke the exchange service if configured.
- For other vendors/modes, preserve existing behavior.
- If exchange service is missing, return a typed unavailable error rather than silently accepting raw callback material.
- Callback state mismatch must fail before exchange call.
- Callback replay must not call exchange service twice.
- Finalize still calls F-AUTH-005 `credentialstore` finalizer exactly once.
- Audit event contains vendor, auth mode, flow kind, error class, and redacted metadata only.

Reference basis:

- Reference callback server rejects missing code or missing state before success handling. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:168`.
- Reference local server bounds startup and wait behavior; HUAKAI should map this to tenant-scoped DB flow expiry and idempotency instead of a local-only channel. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:72`.

Implementation checks:

- Run focused `go test ./internal/gatewayhttp -run CredentialAcquisition`.
- Mutation: if callback replay calls exchange twice, mock exchange call count test must fail.
- Mutation: if wrong-state callback still invokes exchange, mock exchange call count test must fail.
- Mutation: if token bytes appear in response/audit payload, redaction assertion must fail.

### C4 - Refresh Alignment

Goal:

- Preserve current F-AUTH-005 refresh transaction discipline and add Anthropic-specific retry/backoff classification only if needed.

Files:

- Modify `backend/internal/credentialworker/adapters/anthropic.go`.
- Modify `backend/internal/credentialworker/refresher_test.go`.
- Possibly add `backend/internal/credentialworker/adapters/anthropic_test.go` only if the package currently lacks a focused test home; this package is not frozen.

Behavior:

- Same credential refresh remains serialized by existing refresh lock.
- Refresh token request includes public client identity when configured or present in credential material.
- 429 response records a retry-after-based next attempt if available.
- 5xx remains retryable; 4xx invalid grant-like failures become non-retryable/operator-action.
- Long-lived setup-token mode remains disabled unless Owner explicitly enables it.

Reference basis:

- Reference refresh collapses same-token refresh through a single-flight group. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:342`.
- Reference refresh blocks repeat attempts after rate-limit response using parsed retry delay. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:396`.
- Reference retry loop stops on non-retryable error. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:461`.

Implementation checks:

- Run `go test ./internal/credentialworker ./internal/credentialworker/adapters`.
- Mutation: if 429 ignores retry-after, the next-attempt assertion must fail.
- Mutation: if 400 invalid grant is retried like 5xx, non-retryable test must fail.
- Mutation: if long-lived setup token refresh works without flag, gated-mode test must fail.

### C5 - Registry And Protocol-Family Integration

Goal:

- Make routing explicit so API-key and subscription-account traffic do not collide.

Files:

- Modify `backend/internal/provider/registrydefault/default.go`.
- Modify `backend/internal/provider/registrydefault/default_test.go`.
- Modify model registry seed/config docs only if model bindings need a new protocol family.

Behavior options:

- If Owner chooses separate family, add `ProtocolAnthropicClaudeSession` or equivalent HUAKAI-owned string and register the OAuth/session adapter there.
- If Owner chooses credential-sensitive adapter behind `anthropic_messages`, replace passthrough with a composite adapter that dispatches by credential type.
- In both options, tests prove API-key traffic still builds API-key request and OAuth traffic builds subscription request.

Reference basis:

- Reference OAuth module keeps token exchange/refresh behavior separate from request storage and callback handling. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:228`.
- Reference storage separates access token, refresh token, email, type, and expiry in persisted auth data. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/token.go:18`.

Implementation checks:

- Run `go test ./internal/provider/registrydefault ./internal/provider/anthropic`.
- Mutation: if registry returns API-key passthrough for subscription family, adapter type test must fail.
- Mutation: if API-key path is removed, existing default protocol family tests must fail.

### C6 - Transport Policy Implementation After Owner Decision

Goal:

- Implement only the Owner-selected transport mode.

Files:

- Prefer existing `backend/internal/transport` and `backend/internal/transport/mimicry` if transport mimicry is selected.
- Avoid new `gateway` files.
- Avoid introducing upstream transport file structure unless Owner chooses MIT vendoring.

Behavior options:

- Standard transport: use existing standard RoundTripper and account-bound proxy.
- HUAKAI mimicry transport: use existing transport factory and a non-stub Anthropic/Claude template.
- Vendored/reference transport: isolate code under vendor/external path with license/notice/modification records.

Reference basis:

- Reference transport has optional proxy dialer and per-host HTTP/2 connection caching. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:31`.
- Reference transport opens a TLS/HTTP2 connection through a browser-like client profile. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:101`.

Implementation checks:

- Run `go test ./internal/transport ./internal/gateway -run UpstreamDispatcher`.
- Mutation: if standard transport starts reading `HTTP_PROXY` implicitly, existing proxy isolation tests must fail.
- Mutation: if mimicry mode falls back to a stub template in production mode, transport fail-closed test must fail.

### C7 - End-To-End Mock Flow

Goal:

- Prove the whole no-real-secret path works with a local mock token endpoint and mock upstream dispatch.

Files:

- Add or update tests in non-frozen packages first.
- Existing `gatewayhttp` test file update only if callback route wiring changes.
- Existing `cmd/gateway` smoke test update only if production wiring changes.

Scenario:

1. Admin starts Anthropic OAuth acquisition for tenant/account.
2. System returns authorization URL and redacted flow metadata.
3. Mock callback sends matching state and code.
4. Mock token endpoint returns access token, refresh token, expiry, and email.
5. Finalize stores credential through F-AUTH-005.
6. CredentialVault resolves runtime material.
7. Provider adapter builds subscription-auth request to mock Anthropic endpoint.
8. Refresh worker updates expired credential once under concurrent refresh attempts.

Reference basis:

- Reference start/exchange/refresh chain uses authorization URL, token exchange, token storage, and refresh. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:190`, `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:241`, `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:355`, `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/token.go:60`.

Implementation checks:

- Run focused mock E2E tests with `go test`.
- Do not require real Anthropic credentials.
- Live upstream validation waits for Owner-provided credentials and a separate runbook gate.

### C8 - Docs And Runbook

Goal:

- Record operational behavior without turning plan prose into implementer-only source.

Files:

- Update `docs/runbooks/r-d-smoke-runbook.md` if R-D smoke matrix tracks Anthropic OAuth status.
- Update relevant spec/test matrix only after Owner synthesis approves this plan.
- Do not update parity matrix in this Codex-lane plan unless assigned separately.

Content:

- Mock-only verification command.
- Live validation preconditions.
- Redaction expectations.
- Rollback plan: unregister subscription protocol family or disable acquisition exchange config.
- Known Owner decisions from §7.

Implementation checks:

- Markdown links resolve.
- No token examples use realistic secret material.

## §6 风险测试矩阵

| ID | Risk | Discriminating fixture | Mutation self-check | Expected red/green signal |
| --- | --- | --- | --- | --- |
| AT-ANTH-OAUTH-001 | OAuth start missing PKCE challenge | Start flow; parse URL; assert challenge method is S256 and challenge is non-empty; baseline URL builder without challenge differs. | Remove challenge fields from URL builder. | Test fails because parsed query lacks challenge/method. |
| AT-ANTH-OAUTH-002 | State mismatch exchanges code anyway | Mock exchange server records calls; callback with wrong state. | Move state validation after token request or stub it true. | Test fails because call count becomes 1 instead of 0. |
| AT-ANTH-OAUTH-003 | PKCE verifier omitted in token exchange | Mock token server requires verifier field and records request JSON keys. | Delete verifier from exchange payload. | Test fails before candidate creation. |
| AT-ANTH-OAUTH-004 | Callback replay creates duplicate credential | Same flow receives two callback/finalize attempts; mock finalizer records count. | Remove consumed/finalized guard. | Test fails because finalizer count becomes 2. |
| AT-ANTH-OAUTH-005 | Token bytes leak in response or audit | Token endpoint returns distinctive token strings; response/audit captured. | Return raw candidate or raw error body. | Test fails by finding token fragments in JSON/log capture. |
| AT-ANTH-OAUTH-006 | API-key adapter path accepts subscription token | Build request with OAuth token against passthrough path. | Add OAuth token to passthrough acceptable types. | Existing/new test fails because API-key path no longer rejects OAuth. |
| AT-ANTH-OAUTH-007 | Subscription adapter accepts API key | Build request with API key against subscription adapter. | Add API key to subscription acceptable list. | Test fails because adapter returns nil error. |
| AT-ANTH-OAUTH-008 | Subscription auth uses API-key header | Build subscription request; inspect headers. | Set `X-API-Key` instead of selected subscription auth header. | Test fails because wrong header is present and expected header absent. |
| AT-ANTH-OAUTH-009 | API-key path broken by registry change | Resolve default `anthropic_messages`; build API-key request. | Replace passthrough registration without composite/split. | Test fails because API-key credential is rejected or wrong header used. |
| AT-ANTH-OAUTH-010 | Protocol family ambiguity routes OAuth to API-key adapter | Resolve subscription family or credential-sensitive adapter; use OAuth credential. | Leave registry at passthrough only. | Test fails with unsupported credential type. |
| AT-ANTH-OAUTH-011 | Refresh storm hits token endpoint many times | N concurrent refresh requests for same credential; mock token server counts. | Remove refresh lock/singleflight-like collapse. | Test fails because count > 1 for same expired credential. |
| AT-ANTH-OAUTH-012 | 429 retry-after ignored | Mock token endpoint returns 429 with retry-after; inspect saved failure next-attempt. | Ignore retry-after parsing. | Test fails because next attempt is not delayed/clamped. |
| AT-ANTH-OAUTH-013 | Non-retryable OAuth failure loops | Mock token endpoint returns invalid-grant-like 400. | Classify all refresh HTTP errors retryable. | Test fails because state is not operator-action/permanent or retry loop continues. |
| AT-ANTH-OAUTH-014 | Expired flow accepts callback | Create flow with expired timestamp; callback with otherwise valid state/code. | Skip expiry check. | Test fails because exchange server is called or flow validates. |
| AT-ANTH-OAUTH-015 | Cross-tenant callback consumes another tenant flow | Start T1/T2 flows; call T2 path with T1 state. | Remove tenant/account path matching. | Test fails because wrong flow consumed or finalizer called. |
| AT-ANTH-OAUTH-016 | Long-lived setup token enabled by accident | Store setup-token payload with flag false; trigger refresh. | Remove flag guard. | Test fails because refresh succeeds instead of returning gated error. |
| AT-ANTH-OAUTH-017 | Transport silently falls back from mimicry to standard | Configure mimicry mode with missing/stub template. | Return standard transport on missing template. | Test fails because expected fail-closed error is absent. |
| AT-ANTH-OAUTH-018 | Account-bound proxy bypassed | Configure account proxy and subscription request; mock transport records dial/proxy. | Ignore `ProxyResolver` or use env proxy. | Test fails because proxy source is not account-bound. |
| AT-ANTH-OAUTH-019 | Token expiry parsed as zero and never refreshes | Token endpoint returns short expiry; store candidate; run refresh scheduler cutoff. | Drop expiry mapping. | Test fails because credential lacks refresh window. |
| AT-ANTH-OAUTH-020 | Redacted metadata admits token-shaped unknown key | Candidate metadata includes `access_token_shadow`. | Allow unknown keys into redacted context. | Test fails by token-shape detector. |

Test quality requirements:

- Every test must state the defect it guards in the test name or a short comment.
- Every fixture must produce different output under broken code.
- Prefer self-proving tests: compare correct path and baseline/broken path inside the test where possible.
- Do not use nil stubs that make the risky call impossible; use mocks that count calls and assert payloads.
- For concurrency tests, the goroutine count in the test name/comment must match the actual constant.
- For status/body classification, use bodies that distinguish body-aware logic from status-only logic.

## §7 D 决策点

### D1 - Implementation Basis: Paraphrase Or MIT Vendoring

| Option | Description | Reference comparison | Risk | Codex recommendation |
| --- | --- | --- | --- | --- |
| D1-A Paraphrased HUAKAI implementation | Rebuild OAuth URL, PKCE, exchange, refresh classification, and runtime adapter using HUAKAI packages. | CLIProxyAPI has a compact OAuth flow with PKCE and exchange behavior that can be behavior-paraphrased without preserving its package layout. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:190` and `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/pkce.go:21`. | Lowest clean-room and maintenance risk; more implementation work. | Recommended. |
| D1-B Vendor selected MIT files | Put selected MIT code in an isolated vendor/external directory with LICENSE/NOTICE/MODIFICATIONS. | CLIProxyAPI anchor is MIT in `docs/process/2026-05-24-ref-anchor.md`; the source contains complete exchange/refresh/transport behavior. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:241`. | License-compatible but higher attribution and code ownership burden; risks importing upstream structure. | Owner may choose only if speed beats maintainability. |
| D1-C Hybrid | Paraphrase auth/exchange; vendor only transport if transport parity becomes the hard blocker. | Transport behavior is specialized and self-contained around HTTP/2/uTLS connection handling. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:31`. | Medium; keeps core OAuth clean but imports sensitive networking behavior. | Acceptable fallback after transport decision. |

### D2 - Protocol Family Shape

| Option | Description | Reference comparison | Risk | Codex recommendation |
| --- | --- | --- | --- | --- |
| D2-A New subscription protocol family | Keep `anthropic_messages` as official API-key path; add a separate subscription family such as `anthropic_claude_session`. | Reference has OAuth-specific acquisition/refresh behavior separated from token storage and request auth. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:228`. | Requires model binding/config update; clearest operational semantics. | Recommended. |
| D2-B Composite adapter behind existing family | Register one Anthropic adapter for `anthropic_messages` that dispatches by credential type. | Reference token storage can distinguish access/refresh material from other fields, but it does not model HUAKAI protocol families. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/token.go:18`. | Fewer registry strings; higher risk of API-key/OAuth behavior collision. | Use only if Owner wants zero model-registry changes. |
| D2-C Feature-flagged replacement | Replace default only when an env flag enables subscription OAuth. | Reference auth client constructs OAuth-capable HTTP client explicitly. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:150`. | Reduces rollout risk; can hide missing production coverage if flag remains off. | Good staged rollout, not final architecture. |

### D3 - Auth Mode Runtime Mapping

| Option | Description | Reference comparison | Risk | Codex recommendation |
| --- | --- | --- | --- | --- |
| D3-A Two modes, one runtime adapter | `claude_ai_oauth` and `claude_code` both use subscription adapter, with mode metadata in `Extra`. | Reference authorization scopes include profile, inference, and Claude Code/session capabilities in one OAuth request. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:195`. | Simple runtime path; tests must prove mode metadata is not ignored. | Recommended for first implementation. |
| D3-B Two modes, two adapters | Separate web-OAuth and Claude-Code adapters. | Reference parses one token response shape for access/refresh/email/expiry. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:297`. | More files and registry entries; better if request headers diverge later. | Defer until observed divergence. |
| D3-C Treat Claude Code as session token only | Keep `claude_code` on `CredentialTypeSessionToken`; use OAuth only for `claude_ai_oauth`. | Reference storage updates access/refresh material after refresh, not just opaque session material. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:496`. | May under-support Claude Code OAuth refresh semantics. | Not recommended unless Owner wants minimal path. |

### D4 - Callback Architecture

| Option | Description | Reference comparison | Risk | Codex recommendation |
| --- | --- | --- | --- | --- |
| D4-A HUAKAI server-side callback | Use existing F-CRED callback endpoints; exchange code on server with encrypted verifier from flow session. | Reference runs local callback server with `/callback`, validates code/state, then redirects to success. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:168`. | Best SaaS/operator fit; requires narrow `gatewayhttp` wiring. | Recommended. |
| D4-B Operator local-loopback helper | Provide CLI/browser helper instructions and paste returned material into HUAKAI finalize. | Reference local server checks port availability, starts a local listener, and waits with timeout. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:72`. | Avoids server callback exposure; worse SaaS UX and harder audit. | Good emergency/manual-first fallback. |
| D4-C Manual code paste | Admin copies code/state into callback endpoint; server still validates state and exchanges. | Reference callback parser handles code and state values from redirect query. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:178`. | Simple but error-prone; useful for Personal Edition. | Support as fallback, not primary SaaS path. |

### D5 - Token Exchange Timing

| Option | Description | Reference comparison | Risk | Codex recommendation |
| --- | --- | --- | --- | --- |
| D5-A Exchange during callback | Callback validates state, exchanges code immediately, stores validated candidate for finalize. | Reference exchange happens after code/state parsing and sends verifier to token endpoint. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:241`. | Clear operator status; code not retained. | Recommended. |
| D5-B Exchange during finalize | Callback records only validated code hash/encrypted code; finalizer exchanges later. | Reference does not defer exchange after callback in the observed flow; exchange is part of auth service. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:276`. | Requires short-lived encrypted code storage; higher secret handling risk. | Avoid unless Owner needs human approval before exchange. |
| D5-C Client-side exchange then import | Browser/helper exchanges token and imports credential candidate. | Reference token storage can write token data to local JSON. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/token.go:60`. | Moves token material to operator workstation; less SaaS-friendly. | Manual fallback only. |

### D6 - Refresh Strategy

| Option | Description | Reference comparison | Risk | Codex recommendation |
| --- | --- | --- | --- | --- |
| D6-A Upgrade existing credentialworker adapter | Keep existing F-AUTH-005 scheduler/lock and add Anthropic retry-after/non-retryable classification. | Reference refresh uses JSON refresh-token request and rate-limit block. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:355` and `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:396`. | Lowest architecture risk; aligns with current code. | Recommended. |
| D6-B New dedicated Anthropic refresh worker | Add separate worker path for Anthropic subscription accounts. | Reference has auth-specific refresh service methods, not HUAKAI's shared credential scheduler. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:330`. | Duplicates scheduling and audit concerns. | Not recommended. |
| D6-C Manual refresh only | Disable automatic refresh and require operator reacquisition on expiry. | Reference implements refresh and retry, so manual-only would reduce capability. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:449`. | Functionality shrink; bad production UX. | Reject unless legal/security gate forces temporary Manual First. |

### D7 - Transport Choice

| Option | Description | Reference comparison | Risk | Codex recommendation |
| --- | --- | --- | --- | --- |
| D7-A Standard transport first | Use HUAKAI standard transport with account-bound proxy only. | Reference uses custom transport, so this is a safer HUAKAI deviation rather than parity at the transport layer. Evidence of reference custom path: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:158`. | Lowest dependency/maintenance risk; may be less robust against upstream network controls. | Good first mock/staging default. |
| D7-B HUAKAI mimicry transport | Use `transport.Factory` mimicry mode with a non-stub Anthropic/Claude template. | Reference uses a browser-like TLS profile and HTTP/2 connection creation. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:101`. | Requires verified template and fail-closed coverage. | Recommended if Owner wants production subscription reliability now. |
| D7-C Vendor/reference transport | Vendor or port the custom transport into isolated external code. | Reference caches per-host HTTP/2 connections and removes failed cached connections. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:53` and `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go:141`. | Highest maintenance and attribution work; most behavior parity. | Use only if D7-B cannot meet live validation. |

### D8 - Long-Lived Setup Token

| Option | Description | Reference comparison | Risk | Codex recommendation |
| --- | --- | --- | --- | --- |
| D8-A Keep disabled by default | Retain current HUAKAI flag gate for setup-token-like refresh material. | Reference token storage and refresh focus on access/refresh token data; setup-token handling is not the main observed flow. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/token.go:18`. | Safest. | Recommended. |
| D8-B Owner-enabled feature flag | Add explicit tenant/operator flag and UI warning for long-lived setup token acquisition. | Reference success page can surface setup-required state and platform URL. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go:231`. | Medium security/ToS risk; must audit. | Only with Owner confirmation. |
| D8-C Always allow | Accept setup-token material as normal refresh material. | Reference implements normal refresh-token path; always allowing extra long-lived material is not required by observed behavior. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:364`. | High risk. | Reject. |

## §8 验证

### 8.1 Static Checks

Run from `/home/codex/HUAKAI/backend`:

```bash
go test ./internal/provider/anthropic ./internal/provider/registrydefault
go test ./internal/anthropicoauth
go test ./internal/credentialworker ./internal/credentialworker/adapters
go test ./internal/credentialstore
go test ./internal/gatewayhttp -run CredentialAcquisition
go test ./internal/transport ./internal/gateway -run 'UpstreamDispatcher|Transport'
```

If any package does not exist yet at the time of the first implementation slice, run only the packages created or modified in that slice.

### 8.2 Mock E2E Verification

Mock E2E must prove:

- Start URL contains state and PKCE fields.
- Callback with wrong state makes zero token endpoint calls.
- Callback with right state makes one token endpoint call.
- Finalize stores redacted metadata only.
- Runtime dispatch sends subscription auth shape.
- API-key path still sends API-key header shape.
- Refresh concurrency collapses same credential refresh.
- Refresh 429 schedules retry-after.

### 8.3 Live Validation Gate

Live validation requires a separate Owner-approved runbook and real credentials outside the repo.

Preconditions:

- Owner provides a test Anthropic subscription account.
- Redirect URL is approved for the chosen callback architecture.
- Transport choice from §7 D7 is implemented.
- Secret material stored outside git and injected through approved config.
- Audit/log capture is enabled and verified redacted.

Live validation cells:

- `anthropic/api_key` official API-key path still works.
- `anthropic/claude_ai_oauth` OAuth acquisition, dispatch, refresh.
- `anthropic/claude_code` chosen mode behavior if enabled.
- Refresh failure and recovery with mock or controlled invalid token.
- Operator cancellation and expired flow recovery.

### 8.4 Rollback

Rollback must be configuration-first:

- Disable subscription protocol family registration or model binding.
- Disable Anthropic OAuth acquisition exchange service.
- Leave API-key `anthropic_messages` path intact.
- Keep stored credentials inaccessible but do not delete them automatically.
- Mark affected credentials `operator_attention` only through an audited admin action.

### 8.5 Pre-Commit Review Gate For Later Implementation

When implementation exists:

1. Run focused tests above.
2. Stage only intentional files.
3. Run `codex exec review --uncommitted --full-auto`.
4. Fix HIGH findings.
5. Do not commit without review verdict in commit body.

This plan-writing pass intentionally does not run `git add`, `git commit`, or `git push`.

## §9 Source files read

### 9.1 HUAKAI Internal Files

- `docs/process/2026-05-24-ref-anchor.md`
- `CLAUDE.md`
- `docs/RULES.md`
- `backend/internal/provider/registrydefault/default.go`
- `backend/internal/provider/registrydefault/default_test.go`
- `backend/internal/provider/adapter.go`
- `backend/internal/provider/registry.go`
- `backend/internal/provider/anthropic/passthrough.go`
- `backend/internal/provider/anthropic/passthrough_test.go`
- `backend/internal/provider/openai/codex_session.go`
- `backend/internal/provider/openai/codex_session_test.go`
- `backend/internal/provider/postgres_vault.go`
- `backend/internal/gateway/upstream_dispatcher.go`
- `backend/internal/gateway/upstream_dispatcher_hcsf.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go`
- `backend/internal/gatewayhttp/session_handler.go`
- `backend/internal/credentialstore/types.go`
- `backend/internal/credentialworker/refresh_adapter.go`
- `backend/internal/credentialworker/mode_refresh.go`
- `backend/internal/credentialworker/adapters/anthropic.go`
- `backend/internal/credentialworker/refresher_test.go`
- `backend/internal/transport/factory.go`
- `backend/cmd/gateway/wiring.go`
- `backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql`
- `docs/specs/credential-acquisition.md`
- `docs/specs/upstream-credential-management.md`

### 9.2 Reference Source Files Read Under Anchor SHA

Anchor table:

- `CLIProxyAPI | router-for-me/CLIProxyAPI | 50d19e204fed | 2026-05-23T21:19:43Z | MIT`

Files read:

- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic.go`
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go`
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/pkce.go`
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/token.go`
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/oauth_server.go`
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/utls_transport.go`

Reference projects not read in this Codex plan:

- `BerriAI/litellm@414866767176`
- `Wei-Shaw/sub2api@63b0631a5827`
- `QuantumNous/new-api@ebbe31553309`
- `Portkey-AI/gateway@d2ea41f4e17c`
- `envoyproxy/ai-gateway@3d3d346d09e4`
- `Helicone/helicone@094b210b405a`
- `theopenco/llmgateway@d4d67517cfac`

Reason:

- Owner's clean-room instruction for this dispatch specified the six CLIProxyAPI Anthropic files. Additional reference claims would require separate source reads and citations under the lane guard.

## §10 lane+UTC

Owner summary:

本计划基于 2026-05-24 anchor 表和 CLIProxyAPI latest SHA 独立起草。真实观察包括 HUAKAI 当前 Anthropic API-key passthrough 注册、F-CRED/F-AUTH 已有 acquisition/session/refresh 边界、以及 CLIProxyAPI Anthropic OAuth 的 PKCE、code exchange、refresh backoff、callback server、token storage、uTLS transport 行为。合理推断是 HUAKAI 应默认用自研 paraphrase 路径，把 OAuth acquisition、runtime adapter、refresh、transport 分层接入，且不新增 frozen package 文件。Open questions 主要集中在 §7: 是否 vendor MIT、是否新增 protocol family、callback 架构、exchange timing、refresh upgrade、transport choice、long-lived setup token gate。功能没有缩水；clean-room 风险已通过行为化描述和 anchor SHA cite 控制；安全风险集中在 token redaction、callback replay、refresh storm、transport mismatch，需要 Owner 在 §7 拍板后实施。

Source files read: `docs/process/2026-05-24-ref-anchor.md`; `CLAUDE.md`; `docs/RULES.md`; HUAKAI files listed in §9.1; `internal/auth/claude/anthropic.go`; `internal/auth/claude/anthropic_auth.go`; `internal/auth/claude/pkce.go`; `internal/auth/claude/token.go`; `internal/auth/claude/oauth_server.go`; `internal/auth/claude/utls_transport.go`

Lane: `specifier`

Agent: `Codex GPT-5`

UTC timestamp: `2026-05-24T08:23:00Z`
