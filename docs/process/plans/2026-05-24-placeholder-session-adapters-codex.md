# 2026-05-24 Placeholder Session Adapters Codex Plan

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: CLIProxyAPI / litellm / portkey-gateway

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

| Owner directive | `[OWNER AUTHORIZED 2026-05-24T07:30Z workspace-write — refs 已拉 latest 重派] ... 独立写 6 个 placeholder session adapter 实落 + 默认启用的 plan` |
|---|---|
| Output path | `/home/codex/HUAKAI/docs/process/plans/2026-05-24-placeholder-session-adapters-codex.md` |
| Execution status | Plan only. No adapter implementation in this work unit. |
| Git status | No staging, no commit, no branch operation. |
| Anchor rule | Only SHAs from `docs/process/2026-05-24-ref-anchor.md` are used. |
| Clean-room lane | Specifier. Behavior-only extraction from reference source. |
| Scope in | Six HUAKAI session adapters: `cursor`, `copilot`, `gemini_advanced`, `antigravity`, `kiro`, `windsurf`. |
| Scope in | Default registry enablement after placeholder removal. |
| Scope in | Endpoint, auth header, refresh integration, integration tests, risk tests. |
| Scope in | Docs updates needed to preserve parity and record safe equivalents. |
| Scope out | DB schema migration. |
| Scope out | Changing `LICENSE`. |
| Scope out | Adding runtime dependencies. |
| Scope out | Shipping unverified fake endpoints. |
| Success criteria | Env gate removed or inverted so the six adapters are registered by default. |
| Success criteria | No adapter sends credentials to a placeholder endpoint. |
| Success criteria | Each adapter has discriminating tests for endpoint, auth material, credential rejection, and refresh behavior. |
| Success criteria | Refresh scheduler can handle every session auth mode without `adapter_missing` or mock-only ambiguity. |
| Success criteria | Integration tests cover registry default-on plus fake-upstream request capture for all six vendors. |
| Time estimate | 1 planning session plus 2-3 implementation sessions. |
| Blast radius | Credential routing, outbound auth headers, refresh scheduling, account health, provider registry startup. |
| Failure mode | Wrong endpoint leaks real session token to the wrong host. Mitigation: require host allowlist and failing test per vendor. |
| Failure mode | Unsupported refresh path silently leaves expired accounts active. Mitigation: register refresh modes and assert explicit outcomes. |
| Failure mode | Placeholder remains default-on. Mitigation: grep-based verification and tests that fail on fake domains or unresolved implementation markers. |
| Failure mode | License contamination from references. Mitigation: behavior-only design, no copied source, no copied tests. |
| Decision points | D-1 default-on semantics. |
| Decision points | D-2 source of truth for Cursor/Kiro/Windsurf endpoint truth. |
| Decision points | D-3 refresh policy for session-only vendors with no refresh-token source evidence. |
| Pre-execution checklist | Confirm no implementation file is in frozen packages `gatewayhttp`, `gateway`, or `proto` unless modifying existing tests only. |
| Pre-execution checklist | Confirm no schema migration is needed. |
| Pre-execution checklist | Confirm no new runtime dependency is needed. |
| Pre-execution checklist | Confirm every reference assertion has an anchor-table SHA. |
| Pre-execution checklist | Confirm plan did not read or rely on the Claude plan artifact. |

## §1 6 Vendor 表

| Vendor | HUAKAI protocol family | Current local files | Production target | Default-on target | Ref evidence |
|---|---|---|---|---|---|
| Cursor | `cursor_session` | `backend/internal/provider/cursor/cursor_session.go`; `cursor_session_test.go`; `registrydefault/default.go` | Remove placeholder default and require a verified endpoint source before sending real tokens. | Register by default, but fail closed if endpoint provenance is missing. | No direct Cursor source was observed in the anchored refs; Portkey's explicit provider map is a bounded catalog and does not list Cursor in the read map range, so HUAKAI must not fabricate an upstream endpoint: `Portkey-AI/gateway@d2ea41f4e17c:src/providers/index.ts:78-153`. |
| Copilot | `copilot_session` | `backend/internal/provider/copilot/copilot_session.go`; `copilot_session_test.go`; `credentialworker` refresh path | Use Copilot service endpoint, service token exchange, and editor/request headers from reference behavior. | Register by default after refresh adapter and request header tests pass. | litellm has a Copilot provider path that derives a service token from GitHub auth material and reads a dynamic API base when available: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/authenticator.py:83-150`; it also sets the Copilot request base and request headers: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/common_utils.py:12-77`. |
| Gemini Advanced | `gemini_advanced_session` | `backend/internal/provider/gemini/gemini_advanced_session.go`; `gemini_advanced_session_test.go`; existing Gemini refresh | Use Google account OAuth/session material, dynamic browser endpoint fields, and cookie/SAPISID-style auth with explicit refusal when dynamic fields are missing. | Register by default after dynamic endpoint and refresh tests pass. | CLIProxyAPI shows Google OAuth for Gemini with local callback, scopes, token client creation, and token exchange: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:29-126` and `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206-371`. |
| Antigravity | `antigravity_session` | `backend/internal/provider/antigravity/antigravity_session.go`; `antigravity_session_test.go`; existing Antigravity refresh wrapper | Replace fake `api.antigravity.ai` endpoint with Google Code Assist / Antigravity control-plane behavior and required project metadata. | Register by default after project discovery/onboarding and refresh tests pass. | CLIProxyAPI shows Antigravity as a Google OAuth-backed flow with service scopes, token endpoint, user-info lookup, project discovery, and onboarding polling: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/constants.go:4-32`; `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:121-178`; `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:226-377`. |
| Kiro | `kiro_session` | `backend/internal/provider/kiro/kiro_session.go`; `kiro_session_test.go`; `transport` Kiro mimicry mode | Stop using invented `api.kiro.aws` default. Either use an Owner-provided captured endpoint or make endpoint an account credential requirement. | Register by default, but fail closed until a verified endpoint is configured. | No direct Kiro adapter source was observed in the anchored refs; Portkey has Bedrock as an explicit provider and validates provider names against a catalog, which supports fail-closed provider registration rather than inferred endpoints: `Portkey-AI/gateway@d2ea41f4e17c:src/globals.ts:117-192`; `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:111-145`. |
| Windsurf | `windsurf_session` | `backend/internal/provider/windsurf/windsurf_session.go`; `windsurf_session_test.go`; `transport` Windsurf mimicry mode | Stop using invented Codeium/Windsurf endpoint unless sourced; require verified account endpoint and exact auth header mode. | Register by default, but fail closed until endpoint provenance is present. | No direct Windsurf/Codeium provider source was observed in the anchored refs; Portkey's provider map is explicit and does not include Windsurf in the read map range, so HUAKAI should preserve feature via verified endpoint override rather than fabricate: `Portkey-AI/gateway@d2ea41f4e17c:src/providers/index.ts:78-153`. |

Adapter-level production definition for this plan:

- `endpoint 真填` means the adapter must either hard-code a source-cited official endpoint or require a configured endpoint with provenance metadata.
- `auth header 真填` means the adapter must construct the actual upstream credential carrier, not merely pass a token into a placeholder `Bearer` branch.
- `接 refresh` means the credential refresh scheduler has a registered mode adapter for the auth mode and never reports `adapter_missing` for the six vendors.
- `默认 on` means `registrydefault.Build()` registers all six families when no env var is set.
- `integration test` means fake upstream transport captures method, host, path, query, headers, and body, and a discriminating fixture proves the real guarded behavior.
- Cursor/Kiro/Windsurf have no direct anchored endpoint evidence in the supplied refs.
- Therefore Cursor/Kiro/Windsurf must use Safe Equivalent semantics: registered by default, but request build fails closed unless the account supplies a verified endpoint.
- This does not shrink feature scope because operators can enable production traffic through verified per-account endpoints.
- This avoids pretending that an unsourced endpoint is production-real.
- This also follows Truth-First: unsupported upstream claims stay out of code and docs.

## §2 现状缺口

Local registry gap:

- `registrydefault.Build()` currently states that six session adapters contain unverified placeholder endpoints and should not be registered by default: `backend/internal/provider/registrydefault/default.go:127-137`.
- The gate is controlled by `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS`, and the helper only returns true for string `true`: `backend/internal/provider/registrydefault/default.go:83-84`; `backend/internal/provider/registrydefault/default.go:142-144`.
- The existing default registry test asserts that the six families are absent when the env var is unset: `backend/internal/provider/registrydefault/default_test.go:116-125`.
- The existing opt-in test asserts the six are present only when env is set: `backend/internal/provider/registrydefault/default_test.go:127-147`.
- Production default-on requires replacing those tests, not just removing code.

Local request adapter gap:

- Cursor has a concrete-looking endpoint and several optional headers, but still records OCAW gaps for trace, timezone, and request-id fields: `backend/internal/provider/cursor/cursor_session.go:23-28`; `backend/internal/provider/cursor/cursor_session.go:121-140`.
- Cursor tests include skipped expired-session and 5xx paths, so response-driven refresh and DLQ behavior are not covered: `backend/internal/provider/cursor/cursor_session_test.go:112-118`.
- Copilot has a current endpoint and editor headers, but the test only proves request construction and skips response/refresh behavior: `backend/internal/provider/copilot/copilot_session.go:23-28`; `backend/internal/provider/copilot/copilot_session.go:120-158`; `backend/internal/provider/copilot/copilot_session_test.go:112-118`.
- Gemini Advanced endpoint is explicitly incomplete because dynamic browser query data is not present in the default: `backend/internal/provider/gemini/gemini_advanced_session.go:24-28`.
- Gemini Advanced auth injects cookie and optional SAPISID-style value, but dynamic browser fields and response handling remain skipped: `backend/internal/provider/gemini/gemini_advanced_session.go:105-150`; `backend/internal/provider/gemini/gemini_advanced_session_test.go:140-146`.
- Antigravity file states endpoint, header, and body shape are placeholder before real capture: `backend/internal/provider/antigravity/antigravity_session.go:3-9`.
- Antigravity default endpoint is fake-looking and marked unconfirmed: `backend/internal/provider/antigravity/antigravity_session.go:23-29`.
- Antigravity tests explicitly describe the request contract as placeholder: `backend/internal/provider/antigravity/antigravity_session_test.go:60-106`.
- Kiro file states credentials and endpoint are speculative and should not go online before capture: `backend/internal/provider/kiro/kiro_session.go:3-12`.
- Kiro default endpoint is unconfirmed: `backend/internal/provider/kiro/kiro_session.go:25-31`.
- Kiro tests cover the placeholder contract rather than a source-backed endpoint: `backend/internal/provider/kiro/kiro_session_test.go:60-107`.
- Windsurf file states endpoint and body/header shape are not publicly documented in the local comments: `backend/internal/provider/windsurf/windsurf_session.go:3-11`.
- Windsurf default endpoint is unconfirmed: `backend/internal/provider/windsurf/windsurf_session.go:25-31`.
- Windsurf tests cover the current placeholder contract only: `backend/internal/provider/windsurf/windsurf_session_test.go:60-107`.

Local credential and refresh gap:

- `credentialstore` currently names only Anthropic, OpenAI, and Gemini as first-class vendors: `backend/internal/credentialstore/types.go:13-17`.
- Existing auth modes cover OpenAI, Anthropic, Gemini, and Antigravity-as-Gemini, but not Cursor, Copilot, Kiro, or Windsurf first-class modes: `backend/internal/credentialstore/types.go:18-31`.
- Runtime material has a `session_token` kind, so the runtime abstraction can carry session credentials without schema change: `backend/internal/credentialstore/types.go:42-47`.
- Default credential handlers include Gemini Code Assist, Google One, and Antigravity, but no Cursor/Copilot/Kiro/Windsurf handler: `backend/internal/credentialstore/types.go:238-255`.
- Mode refresh registry currently registers Gemini/Antigravity refresh paths but not Cursor/Copilot/Kiro/Windsurf session modes: `backend/internal/credentialworker/mode_refresh.go:52-72`.
- Refresh execution fails and records `adapter_missing` when a vendor/auth mode is not registered: `backend/internal/credentialworker/mode_refresh.go:163-180`.
- Legacy refresh adapter registry explicitly marks Cursor, Copilot, Kiro, and Windsurf as mock-only: `backend/internal/credentialworker/refresh_adapter.go:83-111`.
- Production enablement must eliminate mock-only ambiguity for these six session paths.

Local protocol and transport gap:

- Protocol adapters already register all six session families, but comments say some response shapes are unconfirmed and reuse OpenAI parsing as placeholder: `backend/internal/gateway/protocol_selector.go:112-123`.
- Stream scanner registers all six as SSE: `backend/internal/gateway/stream_scanner.go:127-153`.
- Transport policy has provider codes and allowed mimicry modes for all six session targets: `backend/internal/transport/policy.go:23-45`; `backend/internal/transport/policy.go:140-178`.
- Mimicry registry recognizes all six relevant mode names: `backend/internal/transport/mimicry/registry.go:15-24`; `backend/internal/transport/mimicry/registry.go:141-160`.
- Therefore the immediate blocker is not protocol registration.
- The blocker is trustworthy endpoint/auth/refresh behavior in provider and credential layers.

Structure-rule gap:

- The frozen packages are `backend/internal/gatewayhttp`, `backend/internal/gateway`, and `backend/internal/proto`.
- This plan avoids adding files to those frozen packages.
- Existing files in `backend/internal/gateway` may need test-only assertions later, but no new gateway files are required.
- New implementation files, if any, should go under non-frozen cohesive packages:
- `backend/internal/provider/sessionadapter` for shared endpoint/header helpers if duplication becomes meaningful.
- `backend/internal/credentialworker/adapters` for refresh adapters.
- Existing vendor packages for modifying the six session adapter files and tests.
- Existing `credentialstore/types.go` for constants/handler registration only.
- Existing `credentialacq/types.go` for admin acquisition mode exposure only if UI/API acquisition needs it.

Clean-room gap:

- Local files contain prior comments with broad claims about upstream behavior.
- Implementation must remove or rewrite unsupported comments.
- Reference-derived behavior must be in HUAKAI vocabulary.
- No upstream code, tests, function names, or file structure may be copied.
- Apache/MIT reference evidence can inform behavior, but the implementation remains independently designed.
- For vendors without source-backed endpoint evidence, the correct result is a Safe Equivalent, not an invented endpoint.

## §3 Per-Vendor Ref Evidence

### §3.1 Cursor

- Observed anchored refs did not include a Cursor session implementation.
- The only relevant reference comparison is architectural: Portkey validates providers against an explicit catalog and rejects unknown provider inputs: `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:111-145`.
- Portkey's provider map is a bounded explicit registry; Cursor is not present in the observed registry range: `Portkey-AI/gateway@d2ea41f4e17c:src/providers/index.ts:78-153`.
- HUAKAI implication: do not keep `https://api2.cursor.sh/...` as a production default unless an Owner-approved capture or official source is attached.
- HUAKAI safe equivalent: keep `cursor_session` registered, but make endpoint provenance mandatory.
- HUAKAI request adapter must accept one of:
- a source-cited default endpoint added in a future slice;
- an account-level `base_url`/`session_endpoint` with `endpoint_provenance=owner_verified`;
- a test fake endpoint under `_test.go` only.
- BuildRequest must reject missing verified endpoint before reading or attaching the credential.
- That order is a risk test requirement: no real token may be formatted if endpoint provenance fails.
- Auth carrier remains session token or upstream passthrough.
- Header set should be allowlisted and sourced from account metadata, not hard-coded from stale comments.
- Refresh integration should be Manual First unless a refresh-token source is supplied.
- Manual First means scheduler records an explicit rotation-required outcome instead of mock-only or adapter-missing.
- This preserves feature: Cursor accounts can be used when the operator provides verified session endpoint details.
- This avoids functionality removal and avoids unsupported endpoint claims.

### §3.2 Copilot

- litellm shows a Copilot path with a GitHub auth token, service-token exchange, cache file, expiry check, and dynamic API base readback: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/authenticator.py:83-150`.
- litellm exchanges a GitHub token for a Copilot service token through GitHub's Copilot internal token endpoint and retries failures: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/authenticator.py:152-193`.
- litellm constructs GitHub-device auth material through device-code and polling paths: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/authenticator.py:226-315`.
- litellm sets Copilot-specific request metadata, including editor identity, user-agent, API version, intent, and request id: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/common_utils.py:12-77`.
- litellm's Copilot chat config chooses API base from configured value, cached endpoint, environment, then default base: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/chat/transformation.py:27-48`.
- HUAKAI implication: Copilot should not use a raw GitHub OAuth token directly against the chat endpoint.
- HUAKAI refresh path should turn long-lived GitHub auth material into a short-lived Copilot service token.
- HUAKAI request adapter should consume the service token as runtime material.
- HUAKAI should preserve account-level API base override because reference behavior reads an endpoint from token material.
- HUAKAI tests must distinguish GitHub bearer vs Copilot service token.
- HUAKAI tests must fail if editor headers are omitted or stale values are ignored when account metadata supplies them.
- HUAKAI should support service-token expiry and refresh-before scheduling.
- No litellm code should be copied; behavior is independently represented as a credentialworker refresh adapter plus provider request builder.

### §3.3 Gemini Advanced

- CLIProxyAPI's Gemini path uses Google OAuth client configuration with cloud/userinfo scopes and local callback flow: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:29-99`.
- CLIProxyAPI loads or obtains an OAuth token, then returns an HTTP client that refreshes through the OAuth library: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:100-126`.
- CLIProxyAPI records token metadata including token endpoint, client identity, scopes, account email, and project data: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:140-190`.
- CLIProxyAPI supports manual callback paste when browser callback does not complete: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206-371`.
- CLIProxyAPI token storage serializes Google token data plus project/account metadata: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_token.go:17-87`.
- HUAKAI implication: Gemini Advanced should reuse the existing Gemini refresh-token machinery where possible.
- HUAKAI should not hard-code only the bare browser endpoint without dynamic query fields.
- HUAKAI adapter should require either a current `session_endpoint` with browser query values or an internal endpoint resolver supplied by a later capture module.
- HUAKAI request should prefer cookie/SAPISID-style auth material for web reverse path.
- HUAKAI credential handler can remain under Gemini vendor with `google_one` or a new `gemini_advanced` auth mode, but runtime kind remains `session_token`.
- HUAKAI tests must prove the endpoint query is not silently dropped.
- HUAKAI tests must prove API-key credentials still route to the standard Gemini adapter and are rejected by the Advanced session adapter.

### §3.4 Antigravity

- CLIProxyAPI's Antigravity path uses Google OAuth client credentials, a local callback port, and cloud/userinfo plus Antigravity-specific scopes: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/constants.go:4-18`.
- CLIProxyAPI uses Google OAuth endpoints for auth, token, and user-info: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/constants.go:20-25`.
- CLIProxyAPI points Antigravity control-plane calls at Google Code Assist style hosts and an internal API version: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/constants.go:27-32`.
- CLIProxyAPI exchanges authorization code for tokens and validates non-2xx token responses explicitly: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:121-178`.
- CLIProxyAPI reads account email from Google user-info with Bearer auth: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:181-224`.
- CLIProxyAPI discovers or provisions a project through control-plane calls, including metadata and polling: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:226-377`.
- HUAKAI implication: local `https://api.antigravity.ai/v1/chat/completions` must be removed as a production default.
- HUAKAI Antigravity request path should use Google control-plane-derived endpoint/account metadata, not an invented vendor domain.
- Existing `adapters.AntigravityRefresh` already wraps Gemini refresh and preserves project/plan metadata: `backend/internal/credentialworker/adapters/antigravity.go:18-53`.
- HUAKAI must connect this refresh path to runtime adapter defaults and integration tests.
- HUAKAI tests must prove project metadata is preserved on refresh.
- HUAKAI tests must prove missing project metadata becomes operator-attention rather than silently sending a malformed request.

### §3.5 Kiro

- No direct Kiro source was observed in the anchored refs provided for this task.
- Portkey has Bedrock in its provider catalog and validates provider names against an explicit list: `Portkey-AI/gateway@d2ea41f4e17c:src/globals.ts:117-192`; `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:130-145`.
- Portkey transforms provider requests through a provider config lookup and errors if an endpoint function is unsupported: `Portkey-AI/gateway@d2ea41f4e17c:src/services/transformToProviderRequest.ts:143-162`.
- HUAKAI local transport already models Kiro either as independent Kiro provider or Bedrock-backed mimicry: `backend/internal/transport/policy.go:125-129`; `backend/internal/transport/policy.go:150-155`.
- HUAKAI implication: Kiro should be default-registered but not default-send to `api.kiro.aws`.
- HUAKAI Safe Equivalent: require operator-verified endpoint or route Kiro through a Bedrock-backed account where credentials are Bedrock/SigV4 and Kiro mimicry is transport-only.
- If Owner supplies a Kiro capture, a later slice may replace endpoint-required mode with a source-cited default.
- Until then, refresh should be Manual First or Bedrock static, not fake Cognito refresh.
- Tests must fail if the adapter uses the old hard-coded fake endpoint.

### §3.6 Windsurf

- No direct Windsurf or Codeium source was observed in the anchored refs provided for this task.
- Portkey's provider map is explicit and does not include Windsurf in the observed registry range: `Portkey-AI/gateway@d2ea41f4e17c:src/providers/index.ts:78-153`.
- Portkey's config schema requires either provider credentials, strategy targets, retry/cache/timeout settings, or provider-specific required fields: `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:12-166`.
- HUAKAI implication: do not keep `api.codeium.com/.../windsurf...` as a production default without source evidence.
- HUAKAI Safe Equivalent: require an Owner-verified endpoint and header profile in credential metadata before building the outbound request.
- Auth carrier may remain session token or upstream passthrough.
- Refresh path should be Manual First unless a refresh-token source is supplied.
- Tests must fail if the adapter accepts missing endpoint provenance.
- Tests must fail if default-on registration can route a live token to the old placeholder domain.

## §4 切片

### Slice 0: Plan Handoff And Guardrails

- Files to modify in later implementation: none in this slice.
- Confirm implementation will not add files to `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- Confirm any edits to frozen packages are limited to existing tests only, if needed.
- Confirm no DB migration.
- Confirm no `LICENSE` edit.
- Confirm no new runtime dependency.
- Confirm reference citations remain behavior-only.
- Confirm all reference claims use anchor SHAs from `docs/process/2026-05-24-ref-anchor.md:9-18`.
- Confirm implementation worker has not read the Claude plan.
- Output of this slice is Owner approval for D decisions or synthesized plan.

### Slice 1: Registry Default-On

- Modify `backend/internal/provider/registrydefault/default.go`.
- Remove the env gate from default registration.
- Keep the protocol family constants unchanged.
- Remove or deprecate `placeholderSessionAdaptersEnv`.
- Update file comments so they no longer call the six adapters placeholders after slices finish.
- Register all six adapters unconditionally in `Build()`.
- Modify `backend/internal/provider/registrydefault/default_test.go`.
- Replace `TestBuild_PlaceholderSessionAdaptersDefaultOff` with a default-on test.
- Keep a test proving all six families return adapters when env is unset.
- Add a regression test that setting `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS=false` does not disable production-ready adapters.
- Remove the opt-in test or rewrite it as backwards-compat no-op.
- Mutation check: reintroducing the gate must make the default-on test fail.
- Verification command: `cd backend && go test ./internal/provider/registrydefault`.

### Slice 2: Shared Session Safety Contract

- Preferred file scope: existing vendor adapter files plus optional new package `backend/internal/provider/sessionadapter`.
- Create `backend/internal/provider/sessionadapter` only if at least three adapters need the same endpoint provenance and header helper.
- This package is not frozen and keeps shared responsibilities out of the six vendor packages.
- Responsibilities:
- validate endpoint provenance before attaching credentials;
- reject known placeholder domains;
- resolve account-level endpoint override;
- format auth header/cookie from runtime material;
- keep no vendor-specific endpoint constants except a denylist of old placeholders.
- Do not read environment variables inside adapters.
- Do not perform network IO inside adapters.
- Do not parse upstream responses inside adapters.
- Tests should live in the same package or vendor package test files.
- Mutation check: moving credential header construction before endpoint validation must fail a test.
- Verification command: `cd backend && go test ./internal/provider/...`.

### Slice 3: Copilot Production Adapter And Refresh

- Modify `backend/internal/provider/copilot/copilot_session.go`.
- Replace stale defaults with source-backed base behavior from litellm reference.
- Preserve endpoint override from credential metadata or cached refresh payload.
- Build auth from Copilot service token, not raw GitHub access token.
- Include editor metadata, request id, API version, user-agent, and intent headers.
- Keep caller override support for metadata values.
- Reject API key credentials.
- Reject missing model id.
- Reject empty token.
- Modify `backend/internal/provider/copilot/copilot_session_test.go`.
- Add fake upstream capture proving host/path/header/body.
- Add fixture with raw GitHub access token and service token different from each other.
- Assert the service token reaches the Copilot chat request.
- Assert raw GitHub token is used only by refresh adapter.
- Add mutation self-check comment: replacing service token with access token must fail.
- Add `backend/internal/credentialworker/adapters/copilot.go`.
- Use behavior from litellm: GitHub auth material is exchanged for a Copilot service token, expiry is stored, endpoint can be read from returned token metadata.
- Do not copy litellm code, class names, file layout, or tests.
- Add `backend/internal/credentialworker/adapters/copilot_test.go`.
- Test valid refresh writes service token, expiry, token source, and optional API base.
- Test expired service token refreshes before use.
- Test HTTP 401/403 returns invalid-grant or needs-rotation class.
- Modify `backend/internal/credentialworker/mode_refresh.go`.
- Register Copilot session auth mode with new refresh adapter.
- Modify `backend/internal/credentialstore/types.go`.
- Add first-class Copilot vendor/auth mode if Owner accepts D-3 option A.
- Runtime kind should be `session_token`.
- Refreshable should be true.
- Allow grace should be true.
- Verification command: `cd backend && go test ./internal/provider/copilot ./internal/credentialworker/... ./internal/credentialstore`.

### Slice 4: Gemini Advanced Endpoint/Auth/Refresh

- Modify `backend/internal/provider/gemini/gemini_advanced_session.go`.
- Remove bare dynamic endpoint default as a production default.
- Require dynamic endpoint query fields in credential metadata or configured endpoint.
- Preserve cookie/SAPISID-style auth behavior.
- Ensure API-key credentials still fail.
- Ensure standard Gemini API key path remains separate.
- Add strict host check for `gemini.google.com` unless test endpoint is explicitly injected.
- Add account metadata keys for browser query, auth-user index, origin, referer, and user-agent.
- Do not embed a stale browser build id.
- Modify `backend/internal/provider/gemini/gemini_advanced_session_test.go`.
- Add fixture where endpoint query contains dynamic values.
- Assert query survives exactly.
- Assert dropping query fails.
- Assert cookie path does not create Bearer header.
- Assert SAPISID-style auth path creates the expected auth header.
- Modify `backend/internal/credentialworker/mode_refresh.go` only if a new auth mode is added.
- Prefer reusing existing Gemini refresh adapter for `google_one` or new `gemini_advanced` auth mode.
- Add or extend tests in `backend/internal/credentialworker/adapters/gemini_test.go`.
- Test refresh preserves session-first runtime material.
- Test refresh-token absence gives a classified refresh failure, not adapter missing.
- Verification command: `cd backend && go test ./internal/provider/gemini ./internal/credentialworker/... ./internal/credentialstore`.

### Slice 5: Antigravity Production Adapter And Refresh Wiring

- Modify `backend/internal/provider/antigravity/antigravity_session.go`.
- Remove `api.antigravity.ai` placeholder default.
- Use Google Code Assist / Antigravity account metadata as the endpoint basis.
- Require project metadata before sending a request.
- Require source-backed host allowlist or configured endpoint provenance.
- Attach Bearer access token only after endpoint validation.
- Attach project metadata headers/body fields through a HUAKAI-owned request shaping layer.
- Do not copy upstream request body names or helper names.
- Modify `backend/internal/provider/antigravity/antigravity_session_test.go`.
- Rename placeholder-contract test to production-contract test.
- Add fixture proving project metadata is present.
- Add fixture proving missing project metadata fails before credential header attach.
- Add fixture proving old placeholder domain is rejected.
- Extend `backend/internal/credentialworker/adapters/antigravity_test.go`.
- Existing refresh wrapper should preserve project and plan metadata; tests must assert that behavior.
- Add mode-registry test proving `gemini/antigravity` refresh is registered.
- Verification command: `cd backend && go test ./internal/provider/antigravity ./internal/credentialworker/...`.

### Slice 6: Cursor Safe Equivalent

- Modify `backend/internal/provider/cursor/cursor_session.go`.
- Remove default endpoint as production fallback unless Owner supplies source citation.
- Require `Credential.Extra["session_endpoint"]` or `Extra["base_url"]` plus `endpoint_provenance=owner_verified`.
- Reject old placeholder endpoint if provenance is missing.
- Validate host before auth header is set.
- Keep content type behavior only if backed by capture; otherwise require `content_type` metadata.
- Keep checksum/client-version fields as account metadata, not hard-coded truth.
- Modify `backend/internal/provider/cursor/cursor_session_test.go`.
- Add test where missing endpoint returns error and no Authorization header can be observed.
- Add test where verified endpoint builds request with token.
- Add test where old default endpoint without provenance fails.
- Add test proving upstream passthrough preserves full auth header.
- Add refresh mode.
- Preferred refresh mode: Manual First session rotation adapter.
- Manual First adapter returns explicit outcome such as `manual_rotation_required`, not `ErrMockOnly`.
- Scheduler test must assert no `adapter_missing`.
- Verification command: `cd backend && go test ./internal/provider/cursor ./internal/credentialworker/...`.

### Slice 7: Kiro Safe Equivalent

- Modify `backend/internal/provider/kiro/kiro_session.go`.
- Remove `api.kiro.aws` placeholder default.
- Support two production modes:
- Mode A: Bedrock-backed Kiro account, using Bedrock credentials and Kiro mimicry transport.
- Mode B: Owner-verified Kiro endpoint with session token.
- Reject session-token Kiro when endpoint provenance is absent.
- Keep AWS/Cognito-specific headers only as optional account metadata.
- Do not infer Cognito or API Gateway shape from memory.
- Modify `backend/internal/provider/kiro/kiro_session_test.go`.
- Add test proving old placeholder endpoint is rejected.
- Add test proving Bedrock-backed mode does not format Kiro session Bearer token.
- Add test proving verified endpoint mode formats the supplied auth header after endpoint validation.
- Add refresh mode.
- Preferred refresh mode: static/no-refresh for Bedrock-backed Kiro, Manual First for session-token Kiro.
- Scheduler must record explicit outcome and audit.
- Verification command: `cd backend && go test ./internal/provider/kiro ./internal/credentialworker/... ./internal/transport/...`.

### Slice 8: Windsurf Safe Equivalent

- Modify `backend/internal/provider/windsurf/windsurf_session.go`.
- Remove unverified Codeium/Windsurf default endpoint.
- Require verified endpoint and header profile in credential metadata.
- Validate endpoint before attaching token.
- Support explicit auth header name/value style for upstream passthrough.
- Treat telemetry/version headers as optional account metadata, not hard-coded source truth.
- Modify `backend/internal/provider/windsurf/windsurf_session_test.go`.
- Add test proving missing endpoint fails.
- Add test proving verified endpoint with token succeeds.
- Add test proving old placeholder endpoint fails.
- Add test proving header profile omission fails when vendor requires it.
- Add refresh mode.
- Preferred refresh mode: Manual First session rotation until source evidence exists.
- Verification command: `cd backend && go test ./internal/provider/windsurf ./internal/credentialworker/...`.

### Slice 9: Cross-Vendor Integration Tests

- Create or extend `backend/internal/provider/session_adapter_integration_test.go` only if package structure remains cohesive.
- Alternative: keep per-vendor integration-style tests inside existing vendor packages.
- Do not add files to frozen packages.
- Add registry integration test in `backend/internal/provider/registrydefault/default_test.go`.
- Test with env unset.
- Assert all six protocols registered.
- For each protocol, build a request using safe fixture material.
- For each protocol, fake upstream captures method, host, path, query, headers, body.
- For Cursor/Kiro/Windsurf, fixture must include verified endpoint metadata.
- For Copilot/Gemini/Antigravity, fixture must use source-backed default or source-backed dynamic endpoint.
- Add refresh integration test in `backend/internal/credentialworker/mode_refresh_test.go`.
- For all six auth modes, registry lookup must return a mode adapter.
- For refreshable modes, fake HTTP token endpoint should return new token and expiry.
- For manual-first modes, test must assert explicit outcome and audit, not `adapter_missing`.
- Add risk test that scans session adapter source for old placeholder domains.
- The scan must fail if any old fake endpoint remains outside tests/docs.
- Verification command: `cd backend && go test ./internal/provider/... ./internal/credentialworker/... ./internal/credentialstore ./internal/transport/...`.

### Slice 10: Docs And Release Evidence

- Update `docs/03_FEATURE_PARITY_MATRIX.md` rows for six session adapters.
- Classify Copilot, Gemini Advanced, and Antigravity as Implemented or Implemented Better after tests pass.
- Classify Cursor, Kiro, Windsurf as Safe Equivalent if endpoint provenance is required.
- Update `docs/07_REFERENCE_EVIDENCE_LEDGER.md`.
- Add behavior-only evidence rows for Copilot, Gemini, Antigravity.
- Add no-equivalent evidence rows for Cursor, Kiro, Windsurf with Portkey explicit-catalog citations.
- Update `docs/10_RISK_REGISTER.md`.
- Add risk for unsourced endpoint fabrication.
- Add mitigation: verified endpoint provenance and fail-closed build order.
- Update `docs/11_ACCEPTANCE_TEST_MATRIX.md`.
- Add acceptance IDs for default-on, refresh, endpoint provenance, and manual-first rotation.
- Do not modify `LICENSE`.
- Do not copy reference file layout.
- Verification command: docs-only grep for uncited reference names.

## §5 风险测试（CLAUDE.md #14）

Default-on regression tests:

- Test name: `TestBuild_SessionAdaptersRegisteredByDefault`.
- Guarded defect: old env gate silently disables production session adapters.
- Fixture: env unset, expect six adapters present.
- Mutation self-check: reintroduce `if placeholderSessionAdaptersEnabled()` and the test turns red.
- Expected failure signal: missing protocol family.

Endpoint provenance tests:

- Test name: `TestSessionAdapter_RejectsPlaceholderEndpoint`.
- Guarded defect: live token sent to fake placeholder host.
- Fixture: credential has real-looking token but endpoint is old placeholder.
- Mutation self-check: remove placeholder denylist and the test turns red.
- Expected failure signal: BuildRequest returns nil error when it should fail.

Credential attachment order tests:

- Test name: `TestSessionAdapter_DoesNotAttachCredentialBeforeEndpointValidation`.
- Guarded defect: error path still formats or logs token.
- Fixture: invalid endpoint plus sentinel token.
- Mutation self-check: set Authorization before endpoint validation and fake recorder catches it.
- Expected failure signal: captured header includes sentinel.

Copilot refresh tests:

- Test name: `TestCopilotRefresh_UsesServiceTokenForRuntime`.
- Guarded defect: raw GitHub token is sent to Copilot chat endpoint.
- Fixture: raw token `github-token-A`, service token `copilot-token-B`.
- Assertion: runtime material equals service token, not raw token.
- Mutation self-check: return raw token from refresh and the test turns red.
- Reference basis: litellm separates access-token acquisition from Copilot service-token retrieval: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/authenticator.py:83-193`.

Copilot header tests:

- Test name: `TestCopilotSessionAdapter_InjectsRequiredEditorHeaders`.
- Guarded defect: upstream rejects request due to missing editor/API metadata.
- Fixture: account metadata overrides editor version, plugin version, API version, intent, request id.
- Assertion: fake upstream sees exactly those values.
- Mutation self-check: ignore metadata override and the test turns red.
- Reference basis: litellm sets Copilot-specific metadata headers: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/common_utils.py:60-77`.

Gemini dynamic endpoint tests:

- Test name: `TestGeminiAdvancedSessionAdapter_PreservesDynamicEndpointQuery`.
- Guarded defect: dynamic browser query is dropped, causing upstream auth/routing failure.
- Fixture: endpoint contains query values and request id; expected URL includes them.
- Mutation self-check: use bare default endpoint and the test turns red.
- Reference basis: CLIProxyAPI's Gemini flow stores token/project/account metadata and exchanges OAuth code through a dynamic local flow: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:140-190`; `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:206-371`.

Gemini auth carrier tests:

- Test name: `TestGeminiAdvancedSessionAdapter_RejectsAPIKeyAndUsesCookieAuth`.
- Guarded defect: API key path accidentally enters web-session adapter.
- Fixture: API key credential must fail; session cookie credential must set Cookie and optional SAPISID-style auth.
- Mutation self-check: allow API key credential and the test turns red.
- Expected failure signal: request builds when it should not.

Antigravity project metadata tests:

- Test name: `TestAntigravityRefresh_PreservesProjectMetadata`.
- Guarded defect: refresh drops project id or plan metadata and next request is malformed.
- Fixture: old payload has project/plan, token endpoint returns token without project.
- Assertion: refreshed payload keeps project or marks operator attention.
- Mutation self-check: remove preservation branch and the test turns red.
- Local basis: existing Antigravity refresh wrapper already handles project/plan preservation: `backend/internal/credentialworker/adapters/antigravity.go:18-53`.
- Reference basis: CLIProxyAPI performs project discovery/onboarding after auth: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:226-377`.

Antigravity endpoint tests:

- Test name: `TestAntigravitySessionAdapter_RejectsOldPlaceholderDomain`.
- Guarded defect: fake `api.antigravity.ai` remains in production path.
- Fixture: old default endpoint and valid token.
- Mutation self-check: keep old constant and the test turns red.
- Reference basis: CLIProxyAPI points Antigravity account actions to Google-hosted control-plane endpoints, not the old placeholder domain: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/constants.go:27-32`.

Cursor safe-equivalent tests:

- Test name: `TestCursorSessionAdapter_RequiresVerifiedEndpoint`.
- Guarded defect: unsourced endpoint accepted by default-on registry.
- Fixture: token present, no endpoint provenance.
- Assertion: BuildRequest fails before auth attach.
- Mutation self-check: fall back to current default endpoint and the test turns red.
- Reference comparison: Portkey uses explicit provider validation rather than accepting arbitrary unsupported providers: `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:111-145`.

Kiro safe-equivalent tests:

- Test name: `TestKiroSessionAdapter_RequiresVerifiedEndpointOrBedrockBackedMode`.
- Guarded defect: speculative Kiro endpoint becomes production default.
- Fixture A: session token and no endpoint provenance fails.
- Fixture B: Bedrock-backed mode uses Bedrock auth path and does not attach Kiro Bearer token.
- Mutation self-check: keep current placeholder endpoint and the test turns red.
- Reference comparison: Portkey has explicit Bedrock provider support and catalog validation: `Portkey-AI/gateway@d2ea41f4e17c:src/globals.ts:117-192`; `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:130-145`.

Windsurf safe-equivalent tests:

- Test name: `TestWindsurfSessionAdapter_RequiresEndpointAndHeaderProfile`.
- Guarded defect: unverified Codeium/Windsurf URL accepted.
- Fixture: session token with no verified endpoint or header profile.
- Assertion: BuildRequest fails.
- Mutation self-check: use old default endpoint and the test turns red.
- Reference comparison: Portkey provider map is explicit and lacks Windsurf in the observed range; HUAKAI must not infer: `Portkey-AI/gateway@d2ea41f4e17c:src/providers/index.ts:78-153`.

Refresh scheduler tests:

- Test name: `TestModeRefreshRegistry_AllSessionAdaptersRegistered`.
- Guarded defect: scheduler records `adapter_missing` for session adapters.
- Fixture: all six vendor/auth modes.
- Assertion: registry lookup succeeds for each.
- Mutation self-check: delete one registration and the test turns red.
- Expected failure signal: missing mode key.

Manual-first tests:

- Test name: `TestManualSessionRefresh_ReturnsRotationRequiredNotMockOnly`.
- Guarded defect: unsupported refresh hides behind mock-only behavior.
- Fixture: Cursor/Kiro/Windsurf session credential with expiry in past.
- Assertion: result is explicit manual-rotation outcome and audit-ready reason.
- Mutation self-check: return `ErrMockOnly` and the test turns red.
- Local basis: current mock-only providers list includes Cursor, Copilot, Kiro, Windsurf and must not remain the production story: `backend/internal/credentialworker/refresh_adapter.go:83-111`.

Stream/protocol tests:

- Test name: `TestSessionProtocolFamiliesHaveScannerAndUpstreamAdapter`.
- Guarded defect: default-on provider registered without stream/protocol support.
- Fixture: all six protocol families.
- Assertion: provider registry, protocol adapter registry, and stream scanner registry all resolve.
- Mutation self-check: remove one stream scanner registration and the test turns red.
- Local basis: protocol and stream registries already list all six families: `backend/internal/gateway/protocol_selector.go:112-123`; `backend/internal/gateway/stream_scanner.go:127-153`.

Secret safety tests:

- Test name: `TestSessionAdapterErrorsDoNotContainCredentialValue`.
- Guarded defect: token leaked in error on endpoint/auth failure.
- Fixture: sentinel credential value and invalid endpoint.
- Assertion: error string and logs do not contain sentinel.
- Mutation self-check: include credential value in error and the test turns red.
- This is required for all six vendors.

## §6 D 决策（CLAUDE.md #15）

| Decision | Recommended option | Alternative | Reference project comparison | Owner impact |
|---|---|---|---|---|
| D-1 default-on semantics | Register all six protocol families by default, but fail closed inside adapter when endpoint/auth provenance is missing. | Keep env gate until all six have hard-coded source-backed endpoints. | Portkey validates provider inputs against explicit provider catalog and rejects invalid providers, supporting default availability plus fail-closed validation: `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/index.ts:111-145`. | Recommended option satisfies Owner default-on target without sending tokens to fake endpoints. |
| D-2 Cursor/Kiro/Windsurf endpoint truth | Safe Equivalent: endpoint must be account-configured with owner-verified provenance until source/capture exists. | Invent or keep current defaults. | Portkey's provider map is explicit and does not include Cursor/Windsurf in observed range, and Kiro is not directly represented; explicit catalog beats inference: `Portkey-AI/gateway@d2ea41f4e17c:src/providers/index.ts:78-153`. | Owner only needs to confirm if they want to supply captures and make these hard-coded defaults. |
| D-3 Copilot refresh | Implement true refresh adapter that exchanges GitHub auth material for Copilot service token and stores expiry/API base. | Treat Copilot token as static session token. | litellm separates access-token acquisition, service-token refresh, cached expiry, and optional API base: `BerriAI/litellm@414866767176:litellm/llms/github_copilot/authenticator.py:83-193`. | Recommended option is required for production because static token mode will expire. |
| D-4 Gemini Advanced refresh | Reuse Gemini OAuth refresh path and require dynamic browser endpoint metadata for request build. | Hard-code browser endpoint and rely on cookie only. | CLIProxyAPI uses Google OAuth token handling and stores token/project/account metadata: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/gemini/gemini_auth.go:100-190`. | Recommended option avoids stale browser endpoint and keeps refresh connected. |
| D-5 Antigravity endpoint/auth | Replace fake domain with Google Code Assist / Antigravity control-plane metadata and project discovery. | Keep placeholder chat-completions domain. | CLIProxyAPI uses Google OAuth plus control-plane project discovery/onboarding for Antigravity: `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/constants.go:20-32`; `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/antigravity/auth.go:226-377`. | Recommended option is mandatory; keeping fake domain is not production. |
| D-6 Manual-first refresh for no-source vendors | Cursor/Kiro/Windsurf use explicit manual rotation outcome until refresh source exists. | Return `ErrMockOnly` or `adapter_missing`. | Portkey's schema validates config shape and provider requirements rather than pretending unsupported details exist: `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:12-166`. | Recommended option connects refresh and preserves ops visibility without fake automation. |
| D-7 New runtime dependency | Do not add any dependency for this slice. Use existing Go stdlib and current helpers. | Add OAuth/client packages if needed. | CLIProxyAPI and litellm use their own dependency stacks, but clean-room HUAKAI can implement behavior with existing primitives; no reference requires dependency copying. | Adding a dependency is high-risk per AGENTS and would need Owner confirmation. |
| D-8 Schema migration | Do not migrate DB. Use existing JSON payload and constants/handlers. | Add new tables or columns for endpoint provenance. | Portkey carries provider options in request config structures and validates them without a DB schema in the observed gateway path: `Portkey-AI/gateway@d2ea41f4e17c:src/types/requestBody.ts:42-81`; `Portkey-AI/gateway@d2ea41f4e17c:src/middlewares/requestValidator/schema/config.ts:40-127`. | No Owner schema approval needed if implementation stays in JSON payload/metadata. |

Decision notes:

- D-1 is the key reconciliation between "default on" and "do not send real credentials to unverified endpoints".
- D-2 is where Truth-First overrides convenience.
- D-3 is the only vendor where supplied refs support a true short-lived service-token refresh loop.
- D-4 and D-5 can reuse or extend existing Gemini-family refresh code.
- D-6 prevents feature shrink by keeping Cursor/Kiro/Windsurf operational with verified endpoint metadata.
- D-6 also avoids false confidence from a mock-only adapter.
- D-7 and D-8 keep the work below high-risk thresholds.
- Any Owner request to hard-code Cursor/Kiro/Windsurf endpoints must include a source/capture artifact and citation.

## §7 验证

Plan verification already performed:

- Read anchor table at `/home/codex/HUAKAI/docs/process/2026-05-24-ref-anchor.md`.
- Used only anchor SHAs:
- `router-for-me/CLIProxyAPI@50d19e204fed`.
- `BerriAI/litellm@414866767176`.
- `Portkey-AI/gateway@d2ea41f4e17c`.
- Read HUAKAI local registry, provider adapters, tests, credentialstore, credentialworker, protocol/stream, and transport files.
- No implementation commands were run.
- No tests were run in this planning-only task.
- No git staging or commit command was run.

Implementation verification commands:

- `cd /home/codex/HUAKAI/backend && go test ./internal/provider/...`
- Expected: all provider package tests pass.
- `cd /home/codex/HUAKAI/backend && go test ./internal/credentialstore ./internal/credentialworker/...`
- Expected: credential mode validation and refresh scheduler tests pass.
- `cd /home/codex/HUAKAI/backend && go test ./internal/transport/...`
- Expected: transport mode matrix still rejects cross-vendor mimicry.
- `cd /home/codex/HUAKAI/backend && go test ./internal/gateway/...`
- Expected: protocol and stream scanner compatibility tests pass.
- `rg -n "HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS|placeholder endpoint|OCAW\\)|api\\.antigravity\\.ai|api\\.kiro\\.aws|windsurf_v2" backend/internal/provider`
- Expected: no production source hit for old gate or fake endpoints.
- Test files may mention old placeholders only in negative tests.
- Docs may mention old placeholders only as historical risk.
- `rg -n "adapter_missing|ErrMockOnly|MockOnlyProviders" backend/internal/credentialworker`
- Expected: no production refresh path for six session vendors relies on mock-only or missing adapter.
- `rg -n "cursor|copilot|gemini_advanced|antigravity|kiro|windsurf" docs/03_FEATURE_PARITY_MATRIX.md docs/07_REFERENCE_EVIDENCE_LEDGER.md docs/10_RISK_REGISTER.md docs/11_ACCEPTANCE_TEST_MATRIX.md`
- Expected: parity, reference evidence, risk, and acceptance rows exist after implementation.
- `codex exec review --uncommitted --full-auto`
- Expected: no HIGH findings.

Acceptance verification matrix:

- AT-PSA-001: registry default-on.
- AT-PSA-002: six adapters reject API key when session-only.
- AT-PSA-003: six adapters reject empty credential.
- AT-PSA-004: six adapters reject missing model id.
- AT-PSA-005: endpoint provenance checked before auth attach.
- AT-PSA-006: Copilot refresh returns service token and expiry.
- AT-PSA-007: Gemini Advanced preserves dynamic endpoint query.
- AT-PSA-008: Antigravity preserves project metadata through refresh.
- AT-PSA-009: Cursor safe-equivalent requires verified endpoint.
- AT-PSA-010: Kiro safe-equivalent requires verified endpoint or Bedrock-backed mode.
- AT-PSA-011: Windsurf safe-equivalent requires verified endpoint/header profile.
- AT-PSA-012: refresh registry has all six auth modes.
- AT-PSA-013: manual-first refresh returns explicit rotation outcome.
- AT-PSA-014: errors and audit payloads do not leak tokens.
- AT-PSA-015: stream/protocol registry resolves all six families.
- AT-PSA-016: placeholder domain grep is clean outside negative tests/docs.

Release gate:

- Do not mark this slice Released until AT-PSA-001 through AT-PSA-016 are green.
- Do not mark Cursor/Kiro/Windsurf as Implemented if they remain endpoint-provenance Safe Equivalent.
- Do mark them as Safe Equivalent if default registration plus verified endpoint path works.
- Do not remove any feature because reference evidence is missing.
- Missing evidence becomes Safe Equivalent or Mandatory Roadmap.
- If Owner supplies captures for Cursor/Kiro/Windsurf, convert those rows from Safe Equivalent to Implemented after tests.

## §8 Source Files

HUAKAI files read:

- `/home/codex/HUAKAI/docs/process/2026-05-24-ref-anchor.md`
- `/home/codex/HUAKAI/backend/internal/provider/registrydefault/default.go`
- `/home/codex/HUAKAI/backend/internal/provider/registrydefault/default_test.go`
- `/home/codex/HUAKAI/backend/internal/provider/adapter.go`
- `/home/codex/HUAKAI/backend/internal/provider/cursor/cursor_session.go`
- `/home/codex/HUAKAI/backend/internal/provider/cursor/cursor_session_test.go`
- `/home/codex/HUAKAI/backend/internal/provider/copilot/copilot_session.go`
- `/home/codex/HUAKAI/backend/internal/provider/copilot/copilot_session_test.go`
- `/home/codex/HUAKAI/backend/internal/provider/gemini/gemini_advanced_session.go`
- `/home/codex/HUAKAI/backend/internal/provider/gemini/gemini_advanced_session_test.go`
- `/home/codex/HUAKAI/backend/internal/provider/antigravity/antigravity_session.go`
- `/home/codex/HUAKAI/backend/internal/provider/antigravity/antigravity_session_test.go`
- `/home/codex/HUAKAI/backend/internal/provider/kiro/kiro_session.go`
- `/home/codex/HUAKAI/backend/internal/provider/kiro/kiro_session_test.go`
- `/home/codex/HUAKAI/backend/internal/provider/windsurf/windsurf_session.go`
- `/home/codex/HUAKAI/backend/internal/provider/windsurf/windsurf_session_test.go`
- `/home/codex/HUAKAI/backend/internal/credentialstore/types.go`
- `/home/codex/HUAKAI/backend/internal/credentialworker/mode_refresh.go`
- `/home/codex/HUAKAI/backend/internal/credentialworker/refresh_adapter.go`
- `/home/codex/HUAKAI/backend/internal/credentialworker/adapters/gemini.go`
- `/home/codex/HUAKAI/backend/internal/credentialworker/adapters/antigravity.go`
- `/home/codex/HUAKAI/backend/internal/gateway/protocol_selector.go`
- `/home/codex/HUAKAI/backend/internal/gateway/stream_scanner.go`
- `/home/codex/HUAKAI/backend/internal/transport/policy.go`
- `/home/codex/HUAKAI/backend/internal/transport/mimicry/registry.go`

Reference files read:

- `/home/codex/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/internal/auth/antigravity/constants.go`
- `/home/codex/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/internal/auth/antigravity/auth.go`
- `/home/codex/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/internal/auth/gemini/gemini_auth.go`
- `/home/codex/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/internal/auth/gemini/gemini_token.go`
- `/home/codex/refs-latest/litellm-extracted/litellm-main/litellm/llms/github_copilot/authenticator.py`
- `/home/codex/refs-latest/litellm-extracted/litellm-main/litellm/llms/github_copilot/common_utils.py`
- `/home/codex/refs-latest/litellm-extracted/litellm-main/litellm/llms/github_copilot/chat/transformation.py`
- `/home/codex/refs/portkey-gateway/src/globals.ts`
- `/home/codex/refs/portkey-gateway/src/providers/index.ts`
- `/home/codex/refs/portkey-gateway/src/middlewares/requestValidator/index.ts`
- `/home/codex/refs/portkey-gateway/src/middlewares/requestValidator/schema/config.ts`
- `/home/codex/refs/portkey-gateway/src/services/transformToProviderRequest.ts`
- `/home/codex/refs/portkey-gateway/src/types/requestBody.ts`

Observed regions:

- Anchor ledger: lines 9-18 established project owner/repo, SHA, license, and local path.
- HUAKAI registry: lines 127-144 established env-gated default-off status.
- HUAKAI session adapters: current endpoint/auth/header/unresolved-marker/placeholder behavior per vendor.
- HUAKAI credentialstore: current vendor/auth/runtime mode limits.
- HUAKAI credentialworker: current refresh registry and mock-only gaps.
- HUAKAI protocol/stream/transport: already-known session family support.
- litellm Copilot: device/auth token behavior, service token refresh, dynamic endpoint, request metadata.
- CLIProxyAPI Gemini: Google OAuth, token storage, local callback, token refresh.
- CLIProxyAPI Antigravity: Google OAuth, scopes, control-plane host, project discovery/onboarding.
- Portkey: explicit provider registry, provider validation, config shape validation, request transform lookup.

Inferences:

- Cursor/Kiro/Windsurf cannot receive source-backed hard-coded endpoints from the supplied anchors.
- Default-on can be safe only if adapter-level endpoint provenance fails closed before credential attach.
- Copilot must distinguish GitHub auth material from Copilot service-token material.
- Gemini Advanced can reuse Gemini-family refresh machinery but cannot use a bare browser endpoint as production default.
- Antigravity should be modeled as Google Code Assist / Antigravity control-plane backed, not `api.antigravity.ai`.
- Kiro can be a Bedrock-backed safe equivalent if no direct Kiro endpoint source is supplied.

Open questions:

- OQ-1: Does Owner have fresh Cursor endpoint/header captures that can be cited?
- OQ-2: Does Owner have fresh Kiro endpoint/header captures that can be cited?
- OQ-3: Does Owner have fresh Windsurf/Codeium endpoint/header captures that can be cited?
- OQ-4: Should Copilot become first-class `credentialstore.VendorCopilot` or stay under OpenAI-family account semantics?
- OQ-5: Should Gemini Advanced be a new auth mode or reuse `google_one`?
- OQ-6: Should manual-first refresh outcomes write an audit event now or only set `last_refresh_outcome`?

## §9 Lane

- Lane: specifier.
- Agent: GPT-5 Codex.
- UTC timestamp: 2026-05-24T07:28:02Z.
- Work product: planning artifact only.
- Implementation performed: no.
- Tests run: no.
- Git staged: no.
- Git committed: no.
- Clean-room risk: low if implementation follows this plan; elevated only if uncited Cursor/Kiro/Windsurf endpoints are hard-coded.
- License risk: low; MIT/Apache references were read behavior-only; no code copied.
- Security risk: medium until endpoint provenance and token-leak tests are implemented.
- Feature preservation: no feature is removed.
- Feature mapping: Copilot/Gemini Advanced/Antigravity target Implemented; Cursor/Kiro/Windsurf target Safe Equivalent until source/capture evidence exists.
- Owner confirmation needed: D-2 and D-6 if Owner rejects Safe Equivalent and wants hard-coded defaults for Cursor/Kiro/Windsurf.
- Owner confirmation not needed: removing default-off gate after tests pass, if D-1 recommended option is accepted.

中文总结：本计划基于实际读取的 HUAKAI 本地代码和 2026-05-24 anchor SHA 的 CLIProxyAPI、litellm、Portkey 源码区域，真实观察到 Copilot、Gemini、Antigravity 有可落地的 refresh / OAuth / endpoint 证据；Cursor、Kiro、Windsurf 在给定 anchors 中没有直接 endpoint 证据，因此本计划把它们列为默认注册但 endpoint provenance fail-closed 的 Safe Equivalent，而不是编造生产 endpoint。合理推断 6 条 session adapter 可以默认 on，但必须用风险测试证明不会把 token 发到旧 placeholder；open question 共 6 个，主要集中在 Cursor/Kiro/Windsurf 是否由 Owner 补 fresh capture。

Source files read: docs/process/2026-05-24-ref-anchor.md; backend/internal/provider/registrydefault/default.go; backend/internal/provider/registrydefault/default_test.go; backend/internal/provider/adapter.go; backend/internal/provider/cursor/cursor_session.go; backend/internal/provider/cursor/cursor_session_test.go; backend/internal/provider/copilot/copilot_session.go; backend/internal/provider/copilot/copilot_session_test.go; backend/internal/provider/gemini/gemini_advanced_session.go; backend/internal/provider/gemini/gemini_advanced_session_test.go; backend/internal/provider/antigravity/antigravity_session.go; backend/internal/provider/antigravity/antigravity_session_test.go; backend/internal/provider/kiro/kiro_session.go; backend/internal/provider/kiro/kiro_session_test.go; backend/internal/provider/windsurf/windsurf_session.go; backend/internal/provider/windsurf/windsurf_session_test.go; backend/internal/credentialstore/types.go; backend/internal/credentialworker/mode_refresh.go; backend/internal/credentialworker/refresh_adapter.go; backend/internal/credentialworker/adapters/gemini.go; backend/internal/credentialworker/adapters/antigravity.go; backend/internal/gateway/protocol_selector.go; backend/internal/gateway/stream_scanner.go; backend/internal/transport/policy.go; backend/internal/transport/mimicry/registry.go; CLIProxyAPI-main/internal/auth/antigravity/constants.go; CLIProxyAPI-main/internal/auth/antigravity/auth.go; CLIProxyAPI-main/internal/auth/gemini/gemini_auth.go; CLIProxyAPI-main/internal/auth/gemini/gemini_token.go; litellm/llms/github_copilot/authenticator.py; litellm/llms/github_copilot/common_utils.py; litellm/llms/github_copilot/chat/transformation.py; portkey-gateway/src/globals.ts; portkey-gateway/src/providers/index.ts; portkey-gateway/src/middlewares/requestValidator/index.ts; portkey-gateway/src/middlewares/requestValidator/schema/config.ts; portkey-gateway/src/services/transformToProviderRequest.ts; portkey-gateway/src/types/requestBody.ts
Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-05-24T07:28:02Z
