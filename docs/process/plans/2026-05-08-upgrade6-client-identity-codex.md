# HUAKAI Upgrade #6 — Client Identity Detector Plan

| Field | Value |
| --- | --- |
| Owner directive | "HUAKAI Upgrade #6 — client identity detector" |
| Lane | codex |
| Time | 2026-05-08 |
| Mode | PLANNER only; no code execution in this lane |
| Execution boundary | `execution_boundary_c`: detector + labels only; strong mimicry implementation remains paused |

## Scope

In scope:

- Add an inbound client identity detector design for `Cursor`, `Claude Code`, `Cody`, `custom_script`, and `unknown`.
- Use only standard-library signals: HTTP headers, request metadata available from `http.Request`, limited TLS metadata if `r.TLS` exists, and coarse behavior signals derived from already-read request body shape.
- Run detection after API key auth succeeds and the request body is bounded/read, before Router planning.
- Produce a request-scoped label: `client_kind`, `confidence`, `identity_hash`, `primary_signal`, `signal_summary`, and `low_confidence_reason`.
- Keep raw identity material out of logs. Hash stable signal values with HMAC/SHA-256 when a secret is configured; otherwise fail open to `unknown`.
- Preserve the current Auth -> Registry -> Router -> Billing -> Pool flow; do not make Router read headers, credentials, or perform IO.
- Add focused unit tests for detector scoring and handler propagation, plus one regression test proving no mimicry transport/body rewrite is invoked.

Out of scope:

- No strong mimicry implementation, no TLS ClientHello cloning, no uTLS/http2 fork, no new runtime dependency.
- No schema migration in this slice unless Owner separately approves it. Existing `docs/specs/client-identity.md` mentions future `request_attempts` and `identity_signal_config` tables; those are high-risk under project rules and should stay follow-up.
- No per-client quota enforcement yet. This slice only labels requests so later quota/rate/abuse modules have a safe input.
- No public response header by default; exposing detected client identity to callers helps adversaries tune spoofing.

## Success

- A deterministic detector can classify synthetic Cursor / Claude Code / Cody / custom-script fixtures without any network call or external dependency.
- `User-Agent` alone never yields high confidence.
- API key binding contributes as a tenant-local stable signal but raw bearer/API key material is never read by the detector and never logged.
- Unknown or conflicting signals fail open to `client_kind=unknown` with low confidence; request processing continues.
- Detector output can be attached to the request pipeline before `router.Plan` without violating Router purity.
- Tests cover spoofed headers, missing TLS metadata, reverse-proxy/no-TLS deployment, malformed body, conflicting signals, and no-mimicry boundary.

## Time Estimate

- Code context confirmation and final synthesized implementation plan: 30-45 minutes.
- Detector package and unit tests: 2-3 hours.
- Handler wiring and propagation tests: 1-1.5 hours.
- Documentation/test matrix update: 30-45 minutes.
- Full local checks and Codex review before commit: 30-60 minutes.

Total implementation estimate: 4-6 hours for detector + labels only.

## Blast Radius

- `backend/internal/gatewayhttp`: one new optional dependency in `ChatHandlerDeps` and one call site in `NewChatCompletionsHandler`.
- New package, recommended name `backend/internal/clientidentity`, using stdlib only.
- `backend/internal/router`: avoid changing public structs in the first implementation. If identity must influence Router immediately, that is a public contract change and needs DR/Owner confirmation.
- `backend/internal/pool` / `backend/internal/rate` / `backend/internal/billing`: no changes in this slice unless the synthesized plan explicitly upgrades from "tag only" to "enforcement".
- Tests: detector table tests plus minimal handler stub tests.

## Failure Modes

- Spoofed `User-Agent`: mitigate by low weight and never high-confidence by itself.
- Header collisions across clients: require multi-signal scoring; conflicting high-level signals downgrade to `unknown` or medium/low confidence.
- TLS unavailable behind a reverse proxy: treat TLS signal as absent, not failure. Optional future trusted-edge header requires explicit config.
- Behavior-pattern false positives: keep behavior signals coarse and low/medium weight; avoid prompt-content inspection except body schema shape already parsed by handler.
- HMAC secret missing: fail open to `unknown`, emit operator-visible diagnostic, do not block the request.
- Privacy leakage: log only enum labels, confidence bucket, signal names, and truncated hash; never log raw header values, raw user/session IDs, bearer tokens, cookies, or prompt content.
- Coupling to mimicry: detector output must not select transport modes or trigger body mutation in this slice.
- Contract creep: if downstream quota/pool APIs are extended now, risk expands into public contracts. Keep current slice label-only unless Owner approves enforcement.

## Decision Points

1. 是否信任 `User-Agent`?
   - Codex recommendation: no. Treat it as a low-weight hint only. `User-Agent` can distinguish `custom_script` at low confidence, but cannot independently classify Cursor / Claude Code / Cody as high confidence.

2. 多信号融合策略?
   - Codex recommendation: weighted evidence with conflict penalty.
   - Signal families:
     - `auth_key_binding`: stable tenant-local binding, high weight, tamper-resistant, never raw bearer.
     - `stable_client_headers`: `User-Agent` and client-specific headers, low/medium weight.
     - `protocol_shape`: endpoint/path + request JSON schema shape + stream/tool flags, medium/low weight.
     - `request_correlation`: idempotency/request/session/conversation IDs, low weight.
     - `tls_metadata`: TLS version/cipher/ALPN only when available, low weight unless trusted-edge capture is later approved.
     - `behavior_window`: short in-memory per-binding pattern such as burst cadence and repeated body shape, low weight; no persistent tracking in this slice.
   - Classification should choose the highest scoring client kind only if it clears threshold and conflict margin.

3. 误判 fail-open or fail-closed?
   - Codex recommendation: fail open for gateway serving. Low-confidence identity must not reject traffic.
   - Fail closed only for future enforcement paths after policy exists, e.g. a tenant explicitly configures "block unknown clients". That is not part of Upgrade #6 detector.

4. TLS signal depth?
   - Codex recommendation: do not implement JA3/JA4 or ClientHello capture now. Go `http.Request.TLS` does not provide enough raw handshake detail for robust client fingerprinting, and adding packet/ClientHello tooling would violate "no new deps" and the strong-mimicry pause.

5. Where does the tag live?
   - Codex recommendation: return a `clientidentity.Result` in `gatewayhttp` and attach it to `context.Context` with a typed private key for label propagation during this slice.
   - If Router/Pool/Rate need explicit fields immediately, open a small DR first because `docs/specs/_invariants/cross-module-boundaries.md` treats public contract changes as reviewer-gated.

6. Should detected identity be sent to clients?
   - Codex recommendation: no default response header. Optional debug header only behind an operator/admin debug mode, never on normal public traffic.

## Design Outline

1. Create `internal/clientidentity` package.
   - Types:
     - `Kind`: `cursor`, `claude_code`, `cody`, `custom_script`, `unknown`.
     - `Result`: kind, confidence float, confidence bucket, identity hash, primary signal, signal summaries, conflict reason.
     - `Detector`: deterministic scorer with static default rules and injectable clock/HMAC secret for tests.
   - No external dependencies.

2. Detector input.
   - Use `auth.Identity` after successful auth for tenant/API-key/user IDs.
   - Use `*http.Request` for method, path, headers, `RemoteAddr` class, and limited `TLS` metadata.
   - Use already-read request body bytes only for schema-level behavior extraction. Do not inspect or log prompt text.

3. Scoring.
   - Normalize all evidence into `(client_kind, signal_name, weight, spoof_class, present, conflict_group)`.
   - Compute per-kind raw score and subtract conflict/spoof penalties.
   - Classification thresholds:
     - high: >= 0.70 and winner margin >= 0.20.
     - medium: >= 0.40 and winner margin >= 0.10.
     - low/unknown: anything else.
   - `custom_script` is a positive classification for generic SDK/curl-like requests only when no stronger client-specific signal wins.

4. Identity hash.
   - HMAC input should include tenant ID, API key ID, detected kind, and stable non-secret signal digests.
   - Do not include raw Authorization/Cookie headers.
   - If HMAC secret is absent, return `unknown` or unhashed-disabled result per spec fail-open behavior.

5. Pipeline wiring.
   - In `gatewayhttp`, run detector after auth and body read/parse validation, before `Registry.ResolveModel` or immediately before `Router.Plan`.
   - Store result in request context for future modules.
   - Do not mutate outbound transport policy, `MimicryPlan`, or provider dispatch based on result.

6. Observability.
   - Add internal diagnostic hook or log-ready struct fields: `client_kind`, `confidence_bucket`, `primary_signal`, `identity_hash_prefix`, `request_id`.
   - Keep raw headers out.
   - If no logging abstraction is available, unit-test the result and leave structured logging as a follow-up rather than ad hoc `log.Printf`.

7. Documentation.
   - Update `docs/specs/client-identity.md` implementer notes or create a short implementation note after code lands.
   - Register tests in `docs/11_ACCEPTANCE_TEST_MATRIX.md` only if implementation is accepted for this slice.

## Test Matrix

| Test ID | Level | Scenario | Expected |
| --- | --- | --- | --- |
| AT-IDENTITY-006-001 | unit | `User-Agent` claims Claude Code with no other signal | Low confidence; not high-confidence Claude Code |
| AT-IDENTITY-006-002 | unit | Consistent Cursor fixture with stable header + protocol/body shape | `client_kind=cursor`, high/medium confidence depending configured weights |
| AT-IDENTITY-006-003 | unit | Consistent Cody fixture | `client_kind=cody`; raw header values absent from debug summary |
| AT-IDENTITY-006-004 | unit | Generic curl/python/node style request | `client_kind=custom_script` or `unknown`, never Cursor/Claude Code/Cody |
| AT-IDENTITY-006-005 | unit | Cursor and Claude Code signals both present | Conflict penalty; downgrade to medium/low or `unknown` |
| AT-IDENTITY-006-006 | unit | `r.TLS == nil` behind reverse proxy | No panic; TLS signal absent; classification uses other signals |
| AT-IDENTITY-006-007 | unit | HMAC secret missing | Request-safe result, low confidence/unknown, no raw identity hash |
| AT-IDENTITY-006-008 | unit | Body contains prompt text | Detector uses schema-level fields only; debug summary never contains content |
| AT-IDENTITY-006-009 | handler | Auth succeeds, detector returns result, Router stub receives request without header-reading dependency | Existing handler behavior unchanged; detector called once |
| AT-IDENTITY-006-010 | boundary | Detector result says `claude_code` | No call to `ApplyMimicryPlan`, no transport mode switch, no outbound mimicry side effect |
| AT-IDENTITY-006-011 | regression | Unauthorized auth | Detector is not called; response remains 401 |
| AT-IDENTITY-006-012 | regression | Invalid JSON/body rejected | Detector either not called or returns discarded label; response remains existing 400 |

## References

- `docs/specs/client-identity.md:23-32` — local capability and related features for A23/A24 client identity.
- `docs/specs/client-identity.md:51-60` — existing draft signal list and weights.
- `docs/specs/client-identity.md:90-107` — confidence/hash/cache behavior.
- `docs/specs/client-identity.md:135-157` — fail-open HMAC-missing and high-churn behavior.
- `docs/specs/client-identity.md:215-228` — acceptance test direction.
- `docs/specs/client-identity.md:230-235` — open privacy and cache questions.
- `docs/03_FEATURE_PARITY_MATRIX.md:71` — A23/A24 currently mapped under F-AUTH-005.
- `docs/03_FEATURE_PARITY_MATRIX.md:149-150` — A23 is P0 Phase B; A24 is P1 Phase D.
- `docs/process/decisions/DR-009-algorithm-upgrade-policy.md:78-85` — Phase ordering places A23 in Phase B and A24 in Phase D.
- `backend/internal/gatewayhttp/chat_completions_handler.go:68-80` — current auth resolution point.
- `backend/internal/gatewayhttp/chat_completions_handler.go:82-112` — current bounded body read and request parse point.
- `backend/internal/gatewayhttp/chat_completions_handler.go:131-150` — current Router input assembly.
- `backend/internal/gatewayhttp/chat_completions_handler.go:198-208` — current Pool selection request after routing.
- `backend/internal/auth/api_key_resolver.go:38-46` — current `auth.Identity` fields available after API key auth.
- `backend/internal/auth/api_key_resolver.go:96-145` — bearer auth flow; detector must not re-read or log bearer token.
- `backend/internal/router/router.go:1-15` — Router purity and Auth -> Registry -> Router flow.
- `backend/internal/router/route_plan.go:52-57` — current `RequestContext` shape.
- `backend/internal/router/default_router.go:183-224` — current default Router behavior.
- `docs/specs/_invariants/cross-module-boundaries.md:32-66` — public call-order and reviewer-gated public contracts.
- `docs/specs/_invariants/cross-module-boundaries.md:101-107` — Router must not read credentials or import auth.
- `docs/specs/_invariants/cross-module-boundaries.md:133-139` — credentials and plaintext secrets must not enter logs.
- `docs/specs/_invariants/cross-module-boundaries.md:175-183` — reviewer checklist for public contract and logging changes.
- `backend/internal/transport/policy.go:1-11` — existing transport policy boundary rejects unsafe provider/mode leakage.
- `backend/internal/transport/policy.go:53-59` — TLS/HTTP2 mimicry comments; this plan does not implement them.
- `backend/internal/transport/policy.go:94-103` — allowed mode matrix, including Anthropic mimicry mode note.

## Notes From Local Read

- `backend/internal/middleware/` does not exist in this checkout. The effective request entry point is `backend/internal/gatewayhttp/chat_completions_handler.go`, with chi request ID middleware imported from `github.com/go-chi/chi/v5/middleware`.
- There is no existing `internal/fingerprint` or `internal/clientidentity` implementation. Current `fingerprint` references are billing/request-fingerprint, credential token fingerprint, protocol passthrough field names, or paused outbound transport mimicry comments.
- The existing `docs/specs/client-identity.md` is Draft, not Released. Implementation should either stay label-only or get Owner/DR confirmation before schema/public-contract expansion.

## Owner Confirmation Needed

- Confirm whether Upgrade #6 may add a new `internal/clientidentity` package and optional `ChatHandlerDeps.ClientIdentityDetector`.
- Confirm whether label-only context propagation is enough for this slice, or whether Owner wants explicit Router/Pool/Rate contract fields now.
- Confirm whether any exact Cursor / Claude Code / Cody signature catalog should come from Owner-captured evidence later. Codex should not invent exact current client fingerprints without source-backed evidence.
- Confirm whether `message_prefix_hash` is allowed at all. Codex recommends excluding prompt-prefix hashing from this detector until the privacy boundary is decided.
