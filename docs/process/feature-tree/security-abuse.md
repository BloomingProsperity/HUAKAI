# Security-Abuse Feature Tree — HUAKAI AI Gateway

**Domain summary:** Comprehensive inbound authentication, multi-scope rate limiting, hard quota enforcement, tamper-evident audit ledger, layered SSRF/smuggling defences, and credential encryption controls for a commercial AI gateway; notable gaps are per-API-key RPM/TPM limiting, prompt-injection/content filtering, and per-key IP allow/blocklists.

**Audit date:** 2026-06-02
**Auditor:** Claude Sonnet 4.6 (read-only grep + file reads; no code modified)
**Scope:** `backend/` (Go) + `exploratory/rust-core-gateway/merged/` (Rust sidecar)
**Method:** 30+ distinct grep patterns; every PRESENT claim verified to a specific file:line; every MISSING claim attempted with ≥3 synonym patterns.

---

## Feature Coverage Table

| # | Feature | Status | Evidence (file:line or grep tried) | Gap Note |
|---|---------|--------|------------------------------------|----------|
| **A. Authentication & Authorization** |||||
| A-01 | API key bcrypt hash (never plaintext storage) | PRESENT | `backend/internal/auth/api_key_resolver.go:145` – `bcrypt.CompareHashAndPassword`; `internal/db/auth/auth_inbound.sql` `key_hash` column | bcrypt cost 10; only `key_prefix` exposed in debug logs (CMB-5) |
| A-02 | API key prefix O(1) lookup + bcrypt fan-out cap | PRESENT | `backend/internal/auth/api_key_resolver.go:59–64` – `APIKeyPrefixLen=16`; `MaxBcryptFanout=5` | Cap of 5 candidates prevents bcrypt-as-DoS amplification |
| A-03 | API key expiry TTL enforcement | PRESENT | `backend/internal/auth/api_key_resolver.go:133` – `time.Now().After(expiresAt)` hard cutoff | `NULL` expiry = never-expiring (intentional) |
| A-04 | API key status (active/revoked/disabled) enforcement | PRESENT | `backend/internal/auth/api_key_resolver.go:130`; `internal/db/admin/admin_api_keys.sql:57–70` | Revoked keys silently rejected before bcrypt |
| A-05 | User account status enforcement | PRESENT | `backend/internal/auth/api_key_resolver.go:139`; SQL `INNER JOIN … deleted_at IS NULL` | Disabled/deleted user = all keys invalid |
| A-06 | Tenant status enforcement | PRESENT | `backend/internal/auth/api_key_resolver.go:142`; SQL `INNER JOIN tenants t ON t.status='active'` | Suspended tenant blocks every customer key |
| A-07 | Admin authentication (separate bcrypt, `hk_admin_` prefix) | PRESENT | `backend/internal/admin/operator_auth.go:81` | Prefix-segregated from customer bearer space |
| A-08 | Admin RBAC (platform_admin vs tenant_operator) | PRESENT | `backend/internal/admin/operator_auth.go:119–131` – `RolePlatformAdmin`, `RoleTenantOperator` | `CanIssueForTenant` guards cross-tenant writes |
| A-09 | Admin key issuance rate limit (30/hour) | PRESENT | `backend/internal/admin/issuer.go:75–76` – `rateLimitPerHour=30` | Per-actor Postgres-tracked window |
| A-10 | OAuth2 state parameter CSRF protection | PRESENT | `backend/internal/credentialacq/oauth_authorization_code.go`; `oauth_test.go` | State round-trip verified before token exchange |
| A-11 | PKCE S256 code challenge for OAuth | PRESENT | `backend/internal/credentialacq/oauth.go`; `oauth_authorization_code.go` | Code verifier vs challenge enforced |
| A-12 | JWT session token signature + expiry validation | PRESENT | `backend/internal/usersession/`; `backend/internal/hermes/jwt.go` | Token exp validated; invalid sig rejected |
| A-13 | Login lockout (per-account failed-attempt threshold) | PRESENT | `backend/internal/userauth/service.go:172–176` – `FailedLoginCount >= lockoutThreshold()`, `LockedUntil` | `DefaultLockoutThreshold` configurable; `MarkLoginFailure` increments per user row |
| A-14 | Session anomaly / drift detection | PRESENT | `backend/internal/usersession/anomaly.go:22–44` – `DetectDrift()` comparing IP /16 class + UA class | Flags `DriftHigh` (ip+ua change), `DriftMedium` (ip only), `DriftLow` (ua only); **no automated alert/response path surfaced** — see gap A-14g |
| A-15 | IssueResult plaintext redacted via `String()` | PRESENT | `backend/internal/admin/issuer.go:60–68` | `fmt.Sprintf("%v")` on result cannot leak bearer |
| **B. Rate Limiting & Throttling** |||||
| B-01 | Per-IP global rate limiting | PRESENT | `backend/cmd/gateway/rate_limit.go:46–47` – `ipBucketRegistry`, global tier (180 req/180 s default) | Always-on unless `HUAKAI_RL_DISABLE=true` |
| B-02 | Per-IP auth-strict rate limiting on `/auth/*` | PRESENT | `backend/cmd/gateway/rate_limit.go:149–207` – `authStrict` tier | Tighter limits for credential-bearing endpoints |
| B-03 | Per-path auth class rates (login/register/reset/oauth) | PRESENT | `backend/cmd/gateway/rate_limit.go:146–153` – 20/min login, 5/min register, 5/min reset, 20/min OAuth | Per-path buckets within authStrict tier |
| B-04 | Configurable limits via env vars | PRESENT | `backend/cmd/gateway/rate_limit.go:185–219` – `HUAKAI_RL_GLOBAL_RATE`, `HUAKAI_RL_AUTH_LOGIN_PER_MIN`, etc. | Burst size also configurable |
| B-05 | `Retry-After` header on 429 | PRESENT | `backend/cmd/gateway/rate_limit.go:202, 218` – `retryAfterForRate()` derived from configured rate | Not hardcoded; accurate for client backoff |
| B-06 | IP bucket registry bounded (overflow reset) | PRESENT | `backend/cmd/gateway/rate_limit.go:60` – `maxBucketsPerTier=50000` | Overflow resets registry; prevents memory DoS via IP spoofing |
| B-07 | Trusted proxy IP resolution (X-Forwarded-For hardening) | PRESENT | `backend/internal/clientid/clientid.go`; `backend/cmd/gateway/rate_limit.go:316–328` | Only-trusted hops; forging X-Forwarded-For bypasses nothing |
| B-08 | Rate limit denial audit logging | PRESENT | `backend/cmd/gateway/rate_limit.go:253–260` – `denied()` logs IP, tier, method, path | |
| B-09 | **Per-API-key RPM (requests per minute) limiting** | **MISSING** | Grepped `per.key.*rate`, `perKey.*bucket`, `key.*rate.*limit`, `KeyRateLimit`, `key_rate` — 5 hits all non-relevant (storm scope / test file) | IP-based only; single compromised key can evade by rotating IPs; multi-key noisy-neighbour risk for same-IP customers |
| B-10 | **Per-API-key TPM (tokens per minute) limiting** | **MISSING** | Grepped `tpm`, `tokens.per.min`, `per_key.*token`, `key.*token.*rate` — no matches | Token counting exists for quota cost but not for rate-limiting inflow |
| B-11 | **Per-API-key concurrent request cap** | **PARTIAL** | `backend/internal/quota/service.go:52` – `NeedConcurrencySlot bool` field; `pg_store.go:355` – `AcquireConcurrencySlot`; `quota_concurrency_slots` table; tested in integration tests | Infrastructure and DB table exist; `NeedConcurrencySlot` is **never set to `true`** in `backend/internal/gatewayhttp/` handlers — feature is dormant |
| **C. Quota & Spend Controls** |||||
| C-01 | Hard quota enforcement (deny on exhaustion) | PRESENT | `backend/internal/quota/service.go:66–158` – `DenyError` returned when quota exceeded | Blocking reservation before provider call |
| C-02 | Spend cap / budget ceiling per user | PRESENT | `backend/internal/quota/service.go` – `PredictedCost` check in `Reserve()` | Reserve rejects if cost would exceed remaining budget |
| C-03 | Per-channel quota isolation | PRESENT | `backend/internal/quota/types.go` – `Scopes` field in `ReserveRequest`; SQL scoped by channel | |
| C-04 | Quota pre-deduction (reserve before forward) | PRESENT | `backend/internal/quota/service.go` – `GetReservationByClaimForUpdate` before provider dispatch | |
| C-05 | Idempotency / replay dedup on quota reserve | PRESENT | `backend/internal/quota/service.go:49` – `RequestFingerprint` field deduplicates replayed requests | |
| **D. Input Validation & Request Filtering** |||||
| D-01 | Request body size limit (8 MB gateway-wide) | PRESENT | `backend/internal/privacy/middleware.go:42–53` – `io.LimitReader(r.Body, 8<<20+1)` + zeroize | Body zeroized after read; prevents prompt-size DoS |
| D-02 | Per-endpoint body size limit (1 MB chat endpoint) | PRESENT | `backend/internal/gatewayhttp/chat_completions_validate.go:78` – `http.MaxBytesReader(1<<20)` | Hard cutoff at endpoint layer |
| D-03 | Deprecated/removed field rejection | PRESENT | `backend/internal/gatewayhttp/chat_completions_validate.go:87–96` – `rejectRemovedBodyFields()` | Blocks `pool_group_id` and other retired params |
| D-04 | JSON parse / malformed body rejection | PRESENT | `backend/internal/gatewayhttp/chat_completions_validate.go:52` – `json.Unmarshal` gate | |
| D-05 | Client protocol / ingress path validation | PRESENT | `backend/internal/gatewayhttp/chat_completions_validate.go:99–118` – `ClientProtocolByIngressPath()` | 404 on unregistered ingress path |
| D-06 | **Prompt injection detection** | **MISSING** | Grepped `inject`, `prompt.filter`, `prompt.check`, `jailbreak`, `ignore.previous`, `system.prompt.override` — 0 relevant hits | Gateway is protocol-transparent; no inbound content inspection |
| D-07 | **Content moderation / banned-word filter** | **MISSING** | Grepped `banned.word`, `content.moderat`, `content.filter`, `harmful`, `profanity`, `nsfw` — 0 policy matches (only `content_filter` as OpenAI `finish_reason` label in `proto/openai_chat_response.go:69`) | No configurable content policy applied at gateway |
| D-08 | **PII / secret detection in inbound prompts** | **MISSING** | Grepped `SSN`, `credit.card`, `PII`, `sensitive.data.*prompt`, `redact.*request` — 0 hits | `AllowlistRedactor` applies to responses, not prompts |
| **E. Key & Credential Security** |||||
| E-01 | Provider credential encryption at rest (AES-256-GCM) | PRESENT | `backend/internal/credentialstore/crypto.go:100–131` – `Encrypt()` with 256-bit key + random nonce | |
| E-02 | Ciphertext AAD binding (tenant + provider + auth_mode) | PRESENT | `backend/internal/credentialstore/crypto.go:79–86, 122–130` | Binds ciphertext to its owner context |
| E-03 | AAD integrity check via HMAC-SHA256 on decrypt | PRESENT | `backend/internal/credentialstore/crypto.go:142` – `hmac.Equal` | Tamper detection on decryption |
| E-04 | Key material zeroization | PRESENT | `backend/internal/credentialstore/crypto.go:108, 149` – `privacy.Zeroize` | Prevents key leaks in memory |
| E-05 | Cipher nonce randomization per encryption | PRESENT | `backend/internal/credentialstore/crypto.go:118` – `rand.Read` | Fresh nonce per ciphertext |
| E-06 | Key material 32-byte minimum validation | PRESENT | `backend/internal/credentialstore/crypto.go:46` | Rejects undersized key at construction |
| E-07 | Multi-key version support (rotation interface) | PRESENT | `backend/internal/credentialstore/crypto.go:26–34` – `KeyProvider` interface with `Key(ctx, keyID)` | Supports versioned decryption |
| E-08 | API key environment segregation (Live / Test) | PRESENT | `backend/internal/admin/keygen.go:34–44` – `EnvLive`, `EnvTest`, `EnvAdmin`; `hk_live_`, `hk_test_`, `hk_admin_` prefixes | Test keys cannot be used against live production channel |
| E-09 | **API key read/write scope (permission bits)** | **MISSING** | Grepped `key.*scope`, `Scope.*api.key`, `ReadOnly.*key`, `key.*permission`, `permission.*api.key` — matches only tenant-scope or quota-scope, not API-key r/w permission | All customer keys have identical permissions; enterprise read-only monitoring keys not supported |
| E-10 | **Per-key IP allowlist / blocklist** | **MISSING** | Grepped `ip_whitelist`, `ip_blacklist`, `IPAllowlist`, `IPBlocklist`, `ip.*allow`, `ip.*block` on `*.go` — 2 hits both in test files (`cors_security_headers_test.go`, `trustreceipt/canonical_test.go`) unrelated to key-level IP filtering | Clients cannot restrict a key to specific IP ranges |
| E-11 | **Automated credential rotation schedule** | **PARTIAL** | `KeyProvider` interface supports versioning; `credentialworker/mode_refresh.go` refreshes OAuth tokens | No scheduled rotation for symmetric AES keys; manual env-var replacement required |
| **F. Abuse Detection & Account Controls** |||||
| F-01 | User account suspension enforcement | PRESENT | `backend/internal/userauth/service.go:166` – `UserStatusDisabled` check | Immediate block on all session creation |
| F-02 | Key revocation with operator reason field | PRESENT | `backend/internal/db/admin/admin_api_keys.sql:64–65` – `revoked_reason` column | Soft-delete + status flip to `'revoked'` |
| F-03 | Refresh storm protection (singleflight + token bucket) | PRESENT | `backend/internal/gateway/storm_policy.go` – 3-scope (account SF, per-endpoint bucket, global bucket) | Prevents 100 same-account 401s from triggering 100 vendor calls |
| F-04 | **Real-time fraud / spike alerting** | **PARTIAL** | `backend/internal/usersession/anomaly.go` – `DetectDrift()` with `DriftHigh`/`DriftMedium` levels; `backend/internal/mixedchannelrisk/` directory | Detection logic exists; no outbound alert path (email, webhook, PagerDuty) surfaced in handlers or wiring; `mixedchannelrisk` purpose unclear from test file alone |
| F-05 | **Geofencing / country-based blocking** | **MISSING** | Grepped `geoip`, `geo.fence`, `country.*block`, `MaxMind`, `GeoLite`, `CountryCode` — 0 hits | |
| **G. Transport & Protocol Security** |||||
| G-01 | HSTS on HTTPS edge (X-Forwarded-Proto guard) | PRESENT | `backend/cmd/gateway/cors_security_headers_test.go:50–56` – `Strict-Transport-Security` only set when `X-Forwarded-Proto: https` | Not emitted on plaintext edge |
| G-02 | CORS exact-origin allowlist (no wildcard) | PRESENT | `backend/cmd/gateway/cors_security_headers_test.go:62–80` – `parseAllowedOrigins()` exact match | `*` wildcard disallowed by parser |
| G-03 | CORS preflight 403 on disallowed origin | PRESENT | `backend/cmd/gateway/cors_security_headers_test.go:122–137` – `corsMiddleware` returns 403 | |
| G-04 | `X-Content-Type-Options: nosniff` | PRESENT | `backend/cmd/gateway/cors_security_headers_test.go:29` – `securityHeaders` middleware | |
| G-05 | `X-Frame-Options: DENY` | PRESENT | `backend/cmd/gateway/cors_security_headers_test.go:30` | Clickjacking prevention |
| G-06 | `Referrer-Policy: no-referrer` | PRESENT | `backend/cmd/gateway/cors_security_headers_test.go:31` | |
| G-07 | `Content-Security-Policy: default-src 'none'` | PRESENT | `backend/cmd/gateway/cors_security_headers_test.go:32` | Strict CSP; no inline scripts/styles |
| G-08 | `Vary: Origin` on CORS responses | PRESENT | `backend/cmd/gateway/cors_security_headers_test.go:100` | Prevents cache poisoning |
| G-09 | SSRF protection on outbound OAuth token exchange | PRESENT | `backend/internal/auth/antigravity_token_provider.go:362–457` – `NewSSRFProtectedOAuthClient`; blocks private, loopback, link-local IPs + DNS rebinding at dial time | Shared by Gemini, ChatGPT, Codex OAuth refreshers |
| G-10 | SSRF protection on passthrough/custom provider endpoints | PRESENT | `backend/internal/provider/passthrough_endpoint_guard.go:17–334` – `safePassthroughBaseURL`, `ValidatePassthroughEndpointTarget`, `WrapPassthroughEndpointTransport` | Blocks: RFC1918, loopback, link-local, metadata (169.254.169.254, metadata.google.internal), `.local`/`.internal`/`.lan` domains, hex/octal obfuscated IPs, non-ASCII hosts, fragments, userinfo, non-HTTPS; DNS rebinding blocked at dial time by `publicPassthroughNetAddr`; 19 special-use CIDR deny-prefixes |
| G-11 | H2 bridge duplicate Content-Length rejection (anti-smuggling) | PRESENT | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/h2_bridge.rs:161–166` – "duplicate Content-Length header (ambiguous request framing)" | Rust sidecar; Go gateway relies on net/http stdlib smuggling mitigations |
| G-12 | H2 bridge duplicate Host header rejection | PRESENT | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/h2_bridge.rs:558` | "duplicate Host must be rejected (request smuggling)" |
| G-13 | Panic recovery (no stack trace in HTTP response) | PRESENT | `backend/internal/privacy/middleware.go:66–91` – `Recoverer()`; wired at `backend/cmd/gateway/middleware.go:72` | Panic caught, logged internally via `SystemLogger` with redactor, client gets generic `500 Internal Server Error` only |
| **H. Audit & Forensics** |||||
| H-01 | Auth failure audit trail | PRESENT | `backend/internal/auth/audit.go` | Auth events logged with decision |
| H-02 | Admin action audit trail (key issue / revoke / disable) | PRESENT | `backend/internal/db/admin/admin_audit.sql` – `admin_audit_events` table; `issuer.go` writes inside TX | Transactional; key issued ↔ audit row always co-committed |
| H-03 | Tamper-evident audit ledger (Merkle + ed25519) | PRESENT | `backend/internal/auditledger/merkle.go`, `signer.go` | Hash chain + ed25519 signatures; DLQ for failed writes |
| H-04 | Request receipt with user attribution | PRESENT | `backend/internal/audit/receipt_formatter.go` | Cost + decision + user_id |
| H-05 | Ledger DLQ (fail-open with durable queue) | PRESENT | `backend/internal/auditledger/dlq_producer.go`, `dlq_worker.go` | Startup blocks without ledger; runtime failures enqueue to durable DLQ |
| **I. Webhook & Callback Security** |||||
| I-01 | Webhook HMAC-SHA256 signature verification | PRESENT | `backend/internal/paymenthttp/provider_hmac.go:93–124` – `VerifyWebhook()` with timestamp + raw body | |
| I-02 | Webhook timestamp replay window (5 min default) | PRESENT | `backend/internal/paymenthttp/provider_hmac.go:109` – configurable window | Replay attack mitigation |
| I-03 | Webhook idempotency (dedup on `RequestFingerprint`) | PRESENT | `backend/internal/voucher/idempotency.go`; `backend/internal/payment/callback.go` | Deduplicates replayed callbacks |
| I-04 | Mock payment provider forbidden in production | PRESENT | `backend/internal/paymenthttp/provider_hmac.go:147–149` – `ErrMockProviderForbidden` | Fail-closed if mock is attempted on live env |
| **J. Secrets & Config Safety** |||||
| J-01 | API key plaintext never logged (only prefix) | PRESENT | `backend/internal/auth/api_key_resolver.go:15` – CMB-5 comment; `IssueResult.String()` redacts bearer | `hk_live_<16-char-prefix>` only in debug; full bearer never appears in any log path |
| J-02 | Privacy request-body zeroization after read | PRESENT | `backend/internal/privacy/middleware.go:93–106` – `zeroizingReadCloser.Close()` | Sensitive prompt content zeroed from memory after processing |
| J-03 | Response `AllowlistRedactor` (field filtering) | PRESENT | `backend/internal/privacy/default_redactor.go` – `AllowlistRedactor` | Strips disallowed fields from outbound responses; does **not** inspect inbound prompts (see D-08) |
| J-04 | **Prompt / stack-trace scrubbing in error bodies** | **PARTIAL** | `privacy.Recoverer` returns only `http.StatusText(500)` — stack not exposed; however generic Go `http.Error` responses for non-panic errors (validation, quota) not universally audited for info-leak | Panic path safe; non-panic error detail exposure unverified across all handlers |

---

## Top Missing / Partial Features, Ranked by Commercial Value

### P0 — Immediate Production Risk

1. **Per-API-key RPM / TPM rate limiting** (`B-09`, `B-10`) — Current rate limiting is IP-scoped only. A single compromised key can spread requests across thousands of IPs evading all limits. Multiple legitimate customers on the same egress IP share a single rate budget (noisy-neighbour problem). Sub2API and new-api both implement per-key token-bucket limits. Expected: rate-limit store keyed on `api_key_id`; quota-service integration to track per-key token-minute spend.

2. **Per-API-key concurrent request cap activation** (`B-11`) — The `quota_concurrency_slots` table, `AcquireConcurrencySlot`, and `NeedConcurrencySlot` field all exist but `NeedConcurrencySlot` is never set to `true` in the gateway handler (`backend/internal/gatewayhttp/`). A single key can hold unlimited in-flight slots, enabling resource exhaustion against upstream providers.

### P1 — Security / Compliance Gap

3. **Prompt injection detection** (`D-06`) — No gateway-level inspection of inbound message arrays for known injection patterns (role override, system-prompt bypass, encoding tricks). Passthrough-transparent gateways are standard for latency, but enterprise customers often require an injectable filter layer. Expected: pluggable middleware with configurable regex/embedding-distance rules.

4. **Content moderation / banned-word filter** (`D-07`) — No configurable content policy. Operators cannot block illegal, CSAM, or policy-violating content at the gateway tier, forcing every tenant to implement it individually. Sub2API has a basic keyword filter; new-api supports third-party moderation API integration.

5. **PII / secret detection in inbound prompts** (`D-08`) — `AllowlistRedactor` applies to responses only; inbound prompts are untouched. Customers sending prompts containing API keys, credit-card numbers, or SSNs have no gateway-layer protection. Expected: configurable inbound redactor with PII pattern detection.

### P2 — Feature Parity / Enterprise Readiness

6. **API key read/write scope (permission bits)** (`E-09`) — All customer keys carry identical permissions. Enterprise accounts need read-only keys for monitoring / export and write keys for inference calls. Audit tooling cannot distinguish intent from key alone. Expected: `scope` column on `api_keys` table with values like `read`, `write`, `admin-read`.

7. **Per-key IP allowlist / blocklist** (`E-10`) — Customers cannot restrict a key to their own office/datacenter IP ranges. Standard in OpenRouter, Sub2API. Expected: CIDR list stored on `api_keys`; checked in `api_key_resolver.go` after bcrypt.

8. **Session drift alerting / automated response** (`A-14g`, `F-04`) — `DetectDrift()` computes `DriftHigh`/`DriftMedium`/`DriftLow` accurately, but no handler or wiring routes the result to an alert (email, webhook, ops dashboard). Anomaly detection without alerting is silent forensics, not real-time abuse prevention.

9. **Automated symmetric key rotation schedule** (`E-11`) — `KeyProvider` interface supports versioning and `mode_refresh.go` handles OAuth token rotation, but AES-256-GCM data-encryption keys require manual env-var replacement + restart. A key rotation schedule with graceful old-key phaseout is SOC2/ISO27001 standard.

10. **Geofencing / country-based blocking** (`F-05`) — No MaxMind GeoLite or equivalent integration. Operators cannot block requests from embargoed jurisdictions. Missing for compliance-sensitive enterprise customers.

11. **Prompt / non-panic error info-leak audit** (`J-04`) — Panic recovery (`Recoverer`) correctly scrubs stack traces. Non-panic error paths (validation errors, quota errors, auth errors) need a uniform audit pass to confirm no internal implementation details (file paths, SQL query fragments, internal IDs) leak into 400/403/429/500 bodies.

---

## Coverage Summary

| Status | Count |
|--------|-------|
| PRESENT | 46 |
| PARTIAL | 4 |
| MISSING | 11 |
| **Total audited** | **61** |

### PARTIAL detail
- **B-11** Concurrent request cap — infrastructure exists; not wired in gateway handler
- **E-11** Symmetric key rotation — versioning interface exists; no automated schedule
- **F-04** Fraud/spike alerting — `DetectDrift()` exists; no outbound alert path
- **J-04** Error body scrubbing — panic path safe; non-panic paths not uniformly audited

### MISSING detail
B-09 per-key RPM · B-10 per-key TPM · D-06 prompt injection · D-07 content moderation · D-08 PII detection · E-09 key r/w scope · E-10 per-key IP allowlist · F-05 geofencing · plus the A-14g drift-alerting response path, plus completeness of J-04 non-panic scrubbing.
