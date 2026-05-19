# Codex Final Reviewer-Lane Report - F-AUTH-001 Auth Token Synthesis

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane |
| Review date | 2026-04-28 |
| Artifact reviewed | `docs/decompositions/_cross-cutting/auth-token-synthesis.md` |
| Gate | CL-001..CL-011 strict path review for provider-side OAuth token synthesis |
| Verdict | REJECT |
| Local Sub2API source | `.omc/reference-src/sub2api` at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |

## Review Protocol Notes

- Pre-commitment prediction 1: the synthesis would preserve stale TODOs from the two input passes.
- Actual: confirmed. Section 7 leaves three TODOs and explicitly says they block Released spec.
- Pre-commitment prediction 2: provider-specific behavior would be broadened into provider-neutral claims.
- Actual: confirmed. The 8s request-path timeout and backfill cooldown are Antigravity-specific, but section 1 presents them as convergence behavior.
- Pre-commitment prediction 3: `F-AUTH-001` might be semantically overloaded.
- Actual: confirmed. `docs/03_FEATURE_PARITY_MATRIX.md:39` defines F-AUTH-001 as user sign-in via email/OAuth identity sources, while this synthesis is provider-side upstream credential refresh.
- Pre-commitment prediction 4: source citations would mostly exist in input passes, but synthesis-level claims would not always carry inherited file:line support.
- Actual: confirmed. Many claims are true in source, but several release-facing claims need tighter inherited citations or relabeling.
- Pre-commitment prediction 5: clean-room pressure would cluster around Claude Code mimicry and upstream identifier leakage.
- Actual: confirmed. The synthesis intends cleanup before moving to `docs/specs/auth-token.md`, which means it is not itself release-ready.
- Review mode: escalated to ADVERSARIAL after the feature-ID mismatch plus CL-009 release hold were confirmed. I checked adjacent governance docs, input passes, Sub2API source, and parity rows.
- Self-audit result: CRITICAL and MAJOR findings below have direct artifact evidence and either governance-doc or source-code evidence.
- Realist check result: severity is not inflated. The feature-ID mismatch is a release-blocking governance defect, not a stylistic problem. The TODO and over-broad behavior claims are text-only fixes, but they still prevent Released status.

## 1 - CL-001..011 Verdict Matrix

| Check | Verdict | One-line justification |
| --- | --- | --- |
| CL-001 | PARTIAL | The synthesis still carries upstream function/type names and source-specific credential keys in implementer-facing prose and pseudocode, including `TokenRefreshService`, `RefreshIfNeeded`, `AccountTypeUpstream`, `persistAccountCredentials`, and `_token_version`. |
| CL-002 | PARTIAL | No DB table/column from a reference is deliberately copied, but the HUAKAI pseudocode uses upstream-shaped credential field names such as `access_token`, `refresh_token`, `expires_at`, and `_token_version` as if they were target contract fields. |
| CL-003 | PASS | No upstream UI component, class, or dashboard layout names were found. |
| CL-004 | PASS | No upstream documentation sentence longer than the allowed common technical phrases was found. |
| CL-005 | PARTIAL | Most HUAKAI improvements are expressed as guarantees, but the common refresh flow plus Claude Code mimicry sections are implementation-shaped and still too close to source-derived execution detail for a Released spec. |
| CL-006 | PASS | `Sources` names Sub2API and `E-LIC-001`; the license ledger has `E-LIC-001` for Sub2API LGPL-3.0. |
| CL-007 | FAIL | The synthesis says `Lane mode | Option C (auth core...)`, but DR-000's Option C carve-out list is billing ledger, account-pool routing, and provider failover/account-health heuristics. Auth core is not listed. |
| CL-008 | FAIL | `Feature ID | F-AUTH-001` exists, but the parity matrix row is for user sign-in via OAuth identity sources, not provider-side upstream OAuth credential refresh. |
| CL-009 | FAIL | Section 7 has three open TODOs and line 234 explicitly says they block Released spec. |
| CL-010 | PASS | No external source URL appears in the implementer-relevant sections; the file uses local docs and local source paths. |
| CL-011 | FAIL | Synthesis files may inherit citations, but spot-checks found over-broad claims and release-facing claims whose inherited source support is narrower than the wording. |

Detailed CL notes:

- CL-006 passes narrowly because the only declared source is Sub2API and it is tied to `E-LIC-001`.
- CL-007 is not a minor metadata issue. The Owner-approved DR-000 carve-out list is explicit; this file invents an "auth core" Option C rationale that DR-000 does not contain.
- CL-008 is the strongest structural blocker. Matching an existing row by string is not enough when the row describes a different capability.
- CL-009 fails by the synthesis's own text: `These do NOT block synthesis sign-off; they DO block Released spec (per CL-009).`
- CL-011 fails because the synthesis turns several source-specific or input-pass-specific facts into broad release statements without preserving precise scope.

## 2 - Spot-Check Log

Spot-check method:

- I selected claims from the synthesis's convergence list, Codex sharpenings, HUAKAI design basis, F-RATE cross-reference, and TODO list.
- I grepped local source under `.omc/reference-src/sub2api`.
- Verdict meanings:
- PASS: cited or inherited source supports the claim as written.
- PARTIAL: source supports a narrower claim than the synthesis states.
- FAIL: source contradicts the claim or governance docs reject the release framing.
- MISSING: the claim lacks adequate source or governance support for release.

### Spot-check 01 - Local Sub2API commit

- Synthesis claim: Sub2API source is pinned to `b0a2252ed19c3720e6adafde6083e64fbac2efa9`.
- Grep / command evidence: `git -C .omc/reference-src/sub2api rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`.
- Verdict: PASS.

### Spot-check 02 - F-AUTH-001 parity row semantics

- Synthesis claim: `Feature ID | F-AUTH-001` for provider-side OAuth token management.
- Evidence: `docs/03_FEATURE_PARITY_MATRIX.md:39` says `F-AUTH-001` is `A user signs in via email or any of N OAuth providers.`
- Evidence: synthesis line 13 says scope is `Provider-side OAuth (relay -> upstream credential management)` and excludes client-side JWT auth.
- Verdict: FAIL.
- Why it matters: the artifact is attached to the wrong parity capability. A Released spec under this ID would overwrite or confuse the existing user-auth feature.

### Spot-check 03 - Option C lane mode

- Synthesis claim: `Lane mode | Option C (auth core is on the Option C carve-out...)`.
- Evidence: `docs/process/decisions/DR-000-clean-room-methodology.md:77` approves Option C carve-outs for `billing ledger, account-pool routing, and provider failover/account-health heuristics`.
- Evidence: no `auth core` carve-out appears in DR-000.
- Verdict: FAIL.
- Why it matters: CL-007 requires lane mode to match the carve-out. This one does not.

### Spot-check 04 - 3m refresh skew and 5m cache skew

- Synthesis claim: `3-skew tier: pre-expiry refresh skew (3m), token cache skew (5m), backfill cooldown (5m).`
- Grep evidence: `antigravity_token_provider.go:14-16` has 3m refresh skew, 5m cache skew, and 5m backfill cooldown.
- Grep evidence: `claude_token_provider.go:12-13`, `openai_token_provider.go:14-15`, and `gemini_token_provider.go:14-15` have 3m refresh skew and 5m cache skew.
- Grep evidence: only Antigravity showed `antigravityBackfillCooldown` in the checked provider files.
- Verdict: PARTIAL.
- Required correction: split the claim into provider-wide 3m/5m refresh/cache skew and Antigravity-specific 5m backfill cooldown.

### Spot-check 05 - Request-path bounded refresh 8s

- Synthesis claim: `Request-path bounded refresh: 8s timeout; on failure -> mark temp-unsched + failover`.
- Grep evidence: `antigravity_token_provider.go:20` defines an 8s request refresh timeout.
- Grep evidence: `antigravity_token_provider.go:98-104` wraps `RefreshIfNeeded` in `context.WithTimeout` and returns on refresh error.
- Grep evidence: `claude_token_provider.go:83`, `openai_token_provider.go:161`, and `gemini_token_provider.go:74` call `RefreshIfNeeded` with caller context and no provider-local 8s timeout.
- Verdict: PARTIAL / FAIL as written.
- Required correction: state this is Antigravity-specific source behavior or HUAKAI target policy, not a convergence behavior across all four providers.

### Spot-check 06 - Token cache TTL equals expires_at minus cache skew

- Synthesis claim: token cache TTL is `expires_at - cache_skew`.
- Grep evidence: `antigravity_token_provider.go:156-168` computes TTL and subtracts `antigravityTokenCacheSkew` when remaining lifetime exceeds the skew.
- Grep evidence: `claude_token_provider.go:144-145`, `openai_token_provider.go:241-242`, and `gemini_token_provider.go:156-157` show the same cache-skew subtraction pattern.
- Verdict: PASS.
- Note: release wording should say "subtracts cache skew when remaining lifetime exceeds it; otherwise uses shorter fallback TTL" rather than absolute equality.

### Spot-check 07 - Refresh lock pattern

- Synthesis claim: cache-level lock with bounded TTL prevents same-account thundering herd.
- Grep evidence: `oauth_refresh_api.go:21` sets default refresh lock TTL to 60s.
- Grep evidence: `oauth_refresh_api.go:76-95` takes a local mutex and optional distributed refresh lock; lock held returns without refreshing.
- Grep evidence: legacy/fallback path in `antigravity_token_provider.go:119-122` uses a 30s cache refresh lock.
- Verdict: PASS.
- Note: release wording should distinguish shared API 60s lock from legacy provider fallback 30s lock.

### Spot-check 08 - Token version check

- Synthesis claim: gateway in-memory copy vs DB; use DB version when newer.
- Grep evidence: `token_cache_invalidator.go:79-87` reads current and latest `_token_version`.
- Grep evidence: `token_cache_invalidator.go:102-107` returns latest account when DB version is newer.
- Grep evidence: `antigravity_token_provider.go:148-153` uses `CheckTokenVersion` before cache population.
- Verdict: PASS.
- Note: this supports cache freshness only, not DB write CAS.

### Spot-check 09 - Two-write temp-unsched with background context

- Synthesis claim: temp-unsched writes DB and Redis; uses `bgCtx` when request ctx may be canceled.
- Grep evidence: `antigravity_token_provider.go:190-193` computes temp-unsched and uses `context.Background()`.
- Grep evidence: `antigravity_token_provider.go:193-199` calls DB `SetTempUnschedulable`.
- Grep evidence: `antigravity_token_provider.go:205-218` writes temp-unsched cache.
- Verdict: PASS for Antigravity request-path refresh failure.
- Required correction: do not imply every provider request-path refresh failure uses this path.

### Spot-check 10 - Static credential support

- Synthesis claim: `AccountTypeUpstream` skips refresh entirely.
- Grep evidence: `antigravity_token_provider.go:73-80` returns `api_key` directly for upstream accounts.
- Codex input also cites equivalent behavior across providers, but the synthesis itself does not cite line ranges.
- Verdict: PASS for the sampled Antigravity path; inherited cross-provider claim needs exact citation if kept in Released spec.

### Spot-check 11 - OAuth 401 force-refresh path

- Synthesis claim: upstream 401 invalidates cache, forces expiry, sets temp-unsched 10m default.
- Grep evidence: `ratelimit_service.go:198-203` sets `expires_at` to now and persists credentials.
- Grep evidence: `ratelimit_service.go:208-214` uses configured cooldown, defaults to 10 minutes, and sets temp-unsched.
- Input-pass evidence cites invalidation in the same branch; the sampled snippet confirms force-expiry and temp-unsched.
- Verdict: PASS.
- Note: release wording should cite the invalidation line directly if retained.

### Spot-check 12 - Refresh retry exhausted = temp-unsched, not error

- Synthesis claim: refresh-retry-exhausted marks temp-unsched and preserves active status for background retry.
- Grep evidence: `token_refresh_service.go:298-323` marks temp-unsched after retry exhaustion and returns the last error.
- Grep evidence: source comments at `token_refresh_service.go:309` say it avoids marking error and keeps status active.
- Verdict: PASS.
- Required correction: distinguish retryable exhaustion from non-retryable failures, because `token_refresh_service.go:267-279` sets account error for non-retryable refresh errors.

### Spot-check 13 - Shared refresh coordinator and parallel slice fragility

- Synthesis claim: `TokenRefreshService` has `refreshers` and `executors` slices indexed by provider order.
- Grep evidence: `token_refresh_service.go:20-27` has `refreshers` and `executors`.
- Grep evidence: `token_refresh_service.go:66-78` populates both slices in matching order.
- Grep evidence: `token_refresh_service.go:166-186` iterates refreshers and chooses matching executor by index.
- Verdict: PASS.

### Spot-check 14 - Persistence atomicity gap

- Synthesis claim: persistence uses repository-level update, not transactional row lock with CAS.
- Grep evidence: `account_credentials_persistence.go:9-17` clones credentials and calls `UpdateCredentials`.
- Grep evidence: `account_repo.go:387-395` updates credentials by account ID with `SetCredentials(...).Save(ctx)` and syncs scheduler snapshot.
- Grep evidence: no `FOR UPDATE`, CAS predicate, or transaction appears in that update path.
- Verdict: PASS.

### Spot-check 15 - Raw OAuth response body leakage risk

- Synthesis claim: raw OAuth error bodies can appear in logs/account errors/temp-unsched reasons.
- Grep evidence: `openai_oauth_service.go:112` returns an error including `resp.String()` for token refresh failure.
- Grep evidence: `claude_oauth_service.go:263` returns an error including `resp.String()` for token refresh failure.
- Grep evidence: `openai_token_provider.go:166` and `claude_token_provider.go:88` log refresh errors.
- Grep evidence: `token_refresh_service.go:286-288` can persist original error text into account error status.
- Verdict: PASS.

### Spot-check 16 - Claude Code client detection TODO

- Synthesis TODO-3: check `gateway_service.go:3720 isClaudeCodeClient`.
- Grep evidence: `gateway_service.go:3712-3724` defines `isClaudeCodeClient` and requires `strings.HasPrefix(userAgent, "claude-cli/")` plus parsed `metadata.user_id`.
- Verdict: PASS as now-verifiable source, FAIL as open TODO.
- Required correction: close TODO-3 and convert it into source-backed acceptance criteria or remove it from Released spec.

## 3 - Findings

### CRITICAL Findings

1. The synthesis uses a real Feature ID for the wrong capability.
   - Evidence: synthesis line 6 says `Feature ID | F-AUTH-001`.
   - Evidence: synthesis line 13 scopes the artifact to `Provider-side OAuth (relay -> upstream credential management)`.
   - Evidence: `docs/03_FEATURE_PARITY_MATRIX.md:39` defines `F-AUTH-001` as `A user signs in via email or any of N OAuth providers.`
   - Confidence: HIGH.
   - Why this matters: this is not a naming nit. Releasing provider-side upstream credential refresh under `F-AUTH-001` would corrupt the parity matrix and mask the existing user-auth OAuth identity-source feature. It also makes the acceptance test IDs collide: existing `AT-AUTH-001` is for user identity-source auth, while this synthesis defines `AT-AUTH-001..017` for provider credential refresh.
   - Fix: do not release this artifact as `F-AUTH-001`. Either create/assign a distinct parity row for provider-side upstream OAuth credential refresh, or explicitly merge it into an existing provider-account/credential capability with a valid disposition and non-colliding acceptance test IDs. This needs Owner/PM confirmation before Released status.

2. The artifact itself admits CL-009 release blockage.
   - Evidence: synthesis lines 228-232 list TODO-1 through TODO-3.
   - Evidence: synthesis line 234 says `These do NOT block synthesis sign-off; they DO block Released spec (per CL-009).`
   - Confidence: HIGH.
   - Why this matters: the requested gate is final reviewer-lane for release. A file that says its own TODOs block Released spec cannot be approved for release.
   - Fix: close every TODO before release. TODO-1 is answerable from `oauth_refresh_api.go:75-155`; TODO-2 is largely answered by the Codex provider matrix; TODO-3 is answerable from `gateway_service.go:3712-3724`.

### MAJOR Findings

1. Lane mode is unsupported by DR-000's Option C carve-out.
   - Evidence: synthesis line 7 says `Lane mode | Option C (auth core is on the Option C carve-out...)`.
   - Evidence: `docs/process/decisions/DR-000-clean-room-methodology.md:77` limits Option C carve-outs to `billing ledger, account-pool routing, and provider failover/account-health heuristics`.
   - Confidence: HIGH.
   - Why this matters: CL-007 requires the lane mode to match the feature carve-out. The artifact invents an Option C rationale that is not in the controlling decision record.
   - Fix: either change lane mode to Option B, or update DR-000 / project governance with an Owner-approved Option C carve-out for provider-side OAuth credential refresh before release.

2. Section 1 overstates provider convergence for the 8s request-path refresh behavior.
   - Evidence: synthesis line 20 says `Request-path bounded refresh: 8s timeout; on failure -> mark temp-unsched + failover`.
   - Evidence: `antigravity_token_provider.go:20` and `antigravity_token_provider.go:98-104` support this for Antigravity.
   - Evidence: `claude_token_provider.go:83`, `openai_token_provider.go:161`, and `gemini_token_provider.go:74` call `RefreshIfNeeded` with caller context and no provider-local 8s timeout.
   - Confidence: HIGH.
   - Why this matters: implementers would treat an Antigravity-specific path as source-inherited common provider behavior. That changes timeout policy, failover semantics, and test expectations for OpenAI/Claude/Gemini.
   - Fix: rewrite as `Antigravity has an 8s request-path refresh timeout and immediate temp-unsched on failure; OpenAI/Claude/Gemini use different request-path policies. HUAKAI may choose a provider-neutral bounded timeout as HUAKAI-DESIGN.`

3. Section 1 overstates the "3-skew tier" as provider-wide convergence.
   - Evidence: synthesis line 19 says `3-skew tier: pre-expiry refresh skew (3m), token cache skew (5m), backfill cooldown (5m)`.
   - Evidence: all four checked providers have 3m refresh and 5m cache skews: `claude_token_provider.go:12-13`, `openai_token_provider.go:14-15`, `gemini_token_provider.go:14-15`, `antigravity_token_provider.go:14-15`.
   - Evidence: only Antigravity has `antigravityBackfillCooldown` at `antigravity_token_provider.go:16` and backfill cooldown logic at `antigravity_token_provider.go:180-184`.
   - Confidence: HIGH.
   - Why this matters: the Released spec would falsely imply every provider has a backfill cooldown. Gemini has project-id handling, but not the same 5m Antigravity backfill cooldown in the sampled token provider.
   - Fix: split into `common 3m refresh skew + 5m cache skew` and `Antigravity-specific 5m project-id backfill cooldown`.

4. The synthesis does not preserve the Codex input's provider-policy divergence in the final target contract.
   - Evidence: Codex input says OpenAI and Claude fail-open on request-path refresh errors, while Gemini and Antigravity return errors: `auth-token-codex.md:53`.
   - Evidence: source confirms policy differences in `refresh_policy.go:32-60`.
   - Evidence: synthesis line 119-120 collapses network/timeout errors into `mark_temp_unsched(account, sanitized_reason); return error`.
   - Confidence: HIGH.
   - Why this matters: this throws away an operationally important divergence. Fail-open with stale token, fail-closed, wait-for-cache, and temp-unsched are different operator policies with different incident behavior.
   - Fix: add a provider-policy matrix to the Released spec: refresh-error action, lock-held action, failure TTL, request-path timeout, and temp-unsched behavior. Then state HUAKAI's default and allowed per-provider overrides.

5. The CL-011 inheritance is too weak in the synthesis body.
   - Evidence: synthesis line 17 says `These behaviors are source-verified by both Claude ... and Codex...`, but the bullets on lines 19-28 have no file:line citations.
   - Evidence: at least two bullets are over-broad after source spot-checks: 8s timeout and backfill cooldown.
   - Confidence: HIGH.
   - Why this matters: CL-011 allows synthesis inheritance, not citation laundering. If inherited support is narrower than the synthesis wording, the synthesis must narrow the wording or carry precise citations.
   - Fix: add a compact source-evidence table per KEEP behavior with provider scope and file:line ranges, or rewrite each KEEP claim to exactly match the source-backed input claims.

6. Claude Code mimicry is still too implementation-shaped for a clean Released spec.
   - Evidence: synthesis line 169 lists concrete mimicry components: `system_rewrite / cache_strip / breakpoints / tool_obfuscation / metadata_user_id`.
   - Evidence: input pass lines 152-161 enumerated a six-step transform from `gateway_service.go:1187-1243`.
   - Confidence: MEDIUM.
   - Why this matters: the behavior may be product-critical, but a Released implementer spec should define compatibility goals, opt-in controls, audit evidence, and legal/security gates. It should not hand implementers a direct transform checklist derived from LGPL source.
   - Fix: rewrite mimicry as a provider compatibility profile with acceptance criteria: opt-in disabled by default, legal review ID required, audit emitted, no raw source-specific transform names in core spec, and plugin/adapter boundary for provider-specific request shaping.

7. Acceptance test IDs collide with existing project acceptance IDs.
   - Evidence: synthesis line 205 says `Test Scenarios (AT-AUTH-001..017)`.
   - Evidence: `docs/11_ACCEPTANCE_TEST_MATRIX.md:19` already defines `AT-AUTH-002` for session survival under F-AUTH-002.
   - Evidence: `docs/03_FEATURE_PARITY_MATRIX.md:39` uses `AT-AUTH-001` for the existing user-auth F-AUTH-001 row.
   - Confidence: HIGH.
   - Why this matters: a Released spec would create ambiguous test ownership and make acceptance tracking unreliable.
   - Fix: assign a new feature ID first, then allocate non-colliding acceptance test IDs, for example `AT-PROVIDER-AUTH-001..` or another project-approved namespace.

### MINOR Findings

1. `Sources` is minimal. It names Sub2API and E-LIC-001, but not the two input files as source-verified decomposition inputs in a structured source table.

2. Line 12 says the file moves to `docs/specs/auth-token.md` after review, but the title and scope are not aligned with the existing auth feature taxonomy. The filename would likely mislead implementers into user-auth/session work.

3. The pseudocode uses `provider_accounts` and credential-field names as if local schema names are settled. If those are HUAKAI design names, they need to be reconciled with `docs/19_DOMAIN_MODEL.md`; if source-derived, they should be paraphrased.

4. The `max N refreshes per N-min window` language in H10 is not concrete enough for release acceptance. Use symbolic policy fields or specific default values, not `N`.

5. The report says "Neither Reference Has" in section 3, but only Sub2API is declared in `Sources`. Say "not observed in Sub2API" unless additional references are added.

## 4 - FINAL VERDICT

Verdict: REJECT.

This artifact is not ready to move to `docs/specs/auth-token.md` as Released.

Reasons:

1. CL-008 fails structurally: the `F-AUTH-001` parity row is user-facing identity-provider auth, while this synthesis is provider-side upstream credential refresh.
2. CL-009 fails explicitly: the synthesis contains TODOs and says those TODOs block Released spec.
3. CL-007 fails: Option C lane mode is not supported by DR-000's approved carve-out list.
4. CL-011 fails in practice: spot-checks found over-broad source-backed claims, especially the 8s timeout and 5m backfill cooldown.
5. The acceptance test namespace collides with existing auth acceptance tests.

What would need to change:

1. Assign the provider-side OAuth credential refresh capability to a valid parity row.
   - Recommended replacement for synthesis line 6 if Owner creates a new row:
   - `Feature ID | <new provider-credential-refresh feature ID>`
   - If it is merged into an existing capability, add the merged-equivalent disposition in `docs/03_FEATURE_PARITY_MATRIX.md` before release.

2. Fix lane mode.
   - Recommended replacement for synthesis line 7:
   - `Lane mode | Option B`
   - Or, only after Owner updates DR-000:
   - `Lane mode | Option C (provider-side OAuth credential refresh carve-out approved by <decision doc>)`

3. Close all release-blocking TODOs.
   - TODO-1 replacement: `RefreshIfNeeded verified: local mutex, optional distributed lock, DB reread, second needs-refresh check, provider refresh, `_token_version` stamp, direct credential persistence. Evidence: oauth_refresh_api.go:75-155.`
   - TODO-2 replacement: `Provider divergence verified from Codex matrix and source: OpenAI/Claude fail-open with short cache, Gemini/Antigravity return refresh errors, only Antigravity has provider-local 8s timeout and request-path temp-unsched.`
   - TODO-3 replacement: `isClaudeCodeClient verified: requires claude-cli User-Agent prefix and parseable metadata.user_id. Evidence: gateway_service.go:3712-3724.`

4. Rewrite section 1 convergence bullets.
   - Replacement for line 19:
   - `Common provider pattern: 3m pre-expiry refresh skew and 5m access-token cache skew. Antigravity additionally has a 5m project-id backfill cooldown.`
   - Replacement for line 20:
   - `Antigravity-specific request path: 8s refresh timeout; on failure, mark temp-unsched. OpenAI/Claude/Gemini have different request-path policies and must be represented separately.`

5. Add a provider policy matrix to preserve Codex's source-verified divergence.
   - Include provider, request-path timeout, refresh-error action, lock-held action, failure TTL, temp-unsched behavior, and source lines.

6. Replace direct acceptance IDs.
   - Do not use `AT-AUTH-001..017` until the feature ID is corrected and test IDs are checked against `docs/11_ACCEPTANCE_TEST_MATRIX.md`.

7. Rewrite Claude Code mimicry as a clean-room compatibility profile.
   - Remove source-shaped transform component names from the implementer-facing spec.
   - Keep the product outcome: opt-in provider compatibility mode, disabled by default, legal/security review required, audit every invocation, adapter/plugin boundary.

8. Tighten source evidence.
   - Add a source-evidence appendix that maps each KEEP behavior to file:line citations and provider scope.
   - Keep upstream identifiers in the appendix only; implementer-facing sections should use HUAKAI domain vocabulary.

Upgrade condition:

- This can be resubmitted after the feature ID and lane mode are corrected, TODOs are closed, convergence claims are narrowed, and test IDs are deconflicted.
- If the author only closes TODOs but keeps `F-AUTH-001`, the verdict remains REJECT.
- If the author changes the feature ID without updating parity and acceptance matrices, the verdict remains REJECT.

## 5 - Owner-Facing Chinese Summary

结论：本轮 `auth-token-synthesis.md` 不能进入 Released，最终 verdict 是 REJECT。最大问题不是 Sub2API 源码证据不存在，而是治理锚点错了：当前 `F-AUTH-001` 在 parity matrix 里是“用户通过 email/OAuth 登录”，但这份 synthesis 实际写的是“上游 Provider Account OAuth 凭据刷新”，两者不是同一个功能。文件还保留了 3 个 TODO，并且自己写明这些 TODO 会阻塞 Released spec；此外 8 秒刷新超时、5 分钟 backfill cooldown 被写成通用 convergence，但源码显示它们主要是 Antigravity 特有行为。没有发现必须删除功能的 clean-room 风险，但 Claude Code mimicry 和上游函数名需要在 Released 版中改写为 HUAKAI 自己的兼容性 profile 和行为验收标准。

## Appendix A - Key Assumptions, Pre-Mortem, and Dependency Audit

### Key Assumptions Extracted

| Assumption | Rating | Evidence / concern |
| --- | --- | --- |
| `F-AUTH-001` is the correct feature ID for provider-side OAuth credential refresh. | FRAGILE / FALSE | Parity row defines user sign-in via OAuth identity sources. |
| Auth core is already an Option C carve-out. | FRAGILE / FALSE | DR-000 approved carve-outs do not list auth core. |
| Open TODOs do not block release. | FALSE | Synthesis line 234 says they do block Released spec. |
| The 8s request-path timeout is common source behavior. | FALSE | Only Antigravity has provider-local 8s timeout in checked source. |
| The 5m backfill cooldown is common source behavior. | FALSE | Only Antigravity has the sampled backfill cooldown. |
| Token version checking is equivalent to DB CAS. | FALSE | Source supports cache freshness; persistence update is direct by ID. The synthesis correctly proposes CAS as HUAKAI design. |
| Raw OAuth response-body leakage risk exists. | VERIFIED | OpenAI and Claude refresh errors include `resp.String()` and provider paths log/persist errors. |
| Same-account refresh lock exists. | VERIFIED | OAuthRefreshAPI uses local mutex plus optional distributed lock. |

### Pre-Mortem

Assume this synthesis was released exactly as written and failed. Specific failure scenarios:

1. Implementer builds provider-side OAuth refresh under `F-AUTH-001`, and the original user-auth OAuth provider feature loses its release track.
   - Covered by synthesis? No.
   - Finding: Critical Finding 1.

2. Implementer treats 8s request timeout as source-inherited common behavior for OpenAI/Claude/Gemini.
   - Covered by synthesis? No.
   - Finding: Major Finding 2.

3. Implementer writes AT-AUTH-001..017 tests and collides with existing auth acceptance IDs.
   - Covered by synthesis? No.
   - Finding: Major Finding 7.

4. Clean-room reviewer rejects `docs/specs/auth-token.md` because it still contains upstream function names and source-shaped mimicry mechanics.
   - Covered by synthesis? Partially, line 12 says cleaned before move, but the artifact itself is not clean.
   - Finding: Major Finding 6.

5. Product owner assumes Option C was already authorized for auth core and implementation lane is organized incorrectly.
   - Covered by synthesis? No.
   - Finding: Major Finding 1.

6. Operator expects all providers to mark temp-unsched on refresh failure, but OpenAI/Claude source behavior is fail-open with old token and short TTL.
   - Covered by synthesis? No.
   - Finding: Major Finding 4.

7. TODOs are left in the Released spec and implementers either block unnecessarily or fill gaps from memory/source exposure.
   - Covered by synthesis? It admits the problem.
   - Finding: Critical Finding 2.

### Dependency Audit

| Dependency | Status | Notes |
| --- | --- | --- |
| Local Sub2API clone exists and matches pinned commit. | PASS | `git rev-parse HEAD` returned the pinned commit. |
| Input file `docs/decompositions/sub2api/auth-token-source-verified.md` exists. | PASS | Read during review. |
| Input file `docs/decompositions/_cross-cutting/auth-token-codex.md` exists. | PASS | Read during review. |
| License ledger has Sub2API row. | PASS | `E-LIC-001` exists. |
| Parity matrix has `F-AUTH-001`. | PASS string match / FAIL semantic match | Existing row is user-auth OAuth identity sources, not provider-side upstream OAuth refresh. |
| DR-000 supports Option C auth-core carve-out. | FAIL | Not in approved carve-out list. |
| Synthesis has no open release TODOs. | FAIL | TODO-1..TODO-3 remain. |
| Synthesis has non-colliding acceptance IDs. | FAIL | Uses `AT-AUTH-001..017`, colliding with existing auth matrix IDs. |

### Ambiguity Risks

- `F-AUTH-001 provider-side OAuth` can mean upstream Provider Account credential refresh.
- `F-AUTH-001` in the parity matrix means user login via email/OAuth identity sources.
- Risk: the wrong product feature gets marked Released.

- `Request-path bounded refresh: 8s timeout` can mean all providers.
- It can also mean Antigravity only.
- Risk: implementer bakes the wrong timeout policy into every adapter.

- `Refresh-retry-exhausted = temp-unsched, NOT error` can mean all refresh failures.
- It can also mean retryable background refresh exhaustion only.
- Risk: non-retryable invalid_grant handling is weakened.

- `Claude Code mimicry opt-in` can mean a clean compatibility profile.
- It can also mean copying the upstream six-step transform into core.
- Risk: clean-room leakage and legal review risk.

### Multi-Perspective Notes

- Executor perspective: an implementer cannot safely start because feature ID, lane mode, and test IDs are unresolved.
- Stakeholder perspective: the product outcome is important and should be preserved, but it is attached to the wrong governance row.
- Skeptic perspective: the synthesis over-trusts "both passes agree" even where Codex's pass explicitly recorded provider divergence.
- Security perspective: the sanitizer, CAS, storm budget, and mimicry audit improvements are directionally correct, but the raw-error leak needs to be a hard acceptance criterion.
- Ops perspective: fail-open vs fail-closed provider policy must be visible because it changes incident behavior.
- New-hire perspective: the artifact reads like a synthesis memo plus target algorithm, not a self-contained Released spec.

### Self-Audit

- Critical Finding 1 confidence: HIGH. Could author refute with context? No, parity row text is explicit.
- Critical Finding 2 confidence: HIGH. Could author refute with context? No, the synthesis says TODOs block release.
- Major Finding 1 confidence: HIGH. Could author refute with later Owner decision? Only if a new decision exists; none was found in required docs.
- Major Finding 2 confidence: HIGH. Could author refute with source? Not for OpenAI/Claude/Gemini provider-local 8s timeout.
- Major Finding 3 confidence: HIGH. Could author refute with source? Not for provider-wide backfill cooldown.
- Major Finding 4 confidence: HIGH. Could author refute with policy preference? Policy preference is HUAKAI-DESIGN, not source convergence.
- Major Finding 5 confidence: HIGH. Could author refute with "synthesis exempt"? No, exemption requires inherited claims to be correct.
- Major Finding 6 confidence: MEDIUM. Could author refute with "cleaned before move"? That confirms the current artifact is not release-ready.
- Major Finding 7 confidence: HIGH. Could author refute with a new acceptance matrix update? Not present at review time.

### Realist Check

- Critical Finding 1 stays CRITICAL: realistic worst case is governance corruption and silent loss/confusion of user-auth feature tracking.
- Critical Finding 2 stays CRITICAL: realistic worst case is implementer lane receives a spec with known unresolved source questions.
- Major Finding 1 stays MAJOR: lane mode can be fixed by governance or metadata, but is a hard CL-007 failure today.
- Major Finding 2 stays MAJOR: timeout behavior drift causes wrong implementation and tests, but the fix is bounded text and policy clarification.
- Major Finding 3 stays MAJOR: provider convergence overclaim is easy to fix, but would mislead implementation if left.
- Major Finding 4 stays MAJOR: provider-policy collapse affects production behavior during OAuth incidents.
- Major Finding 6 stays MAJOR, not CRITICAL: mitigated by line 12's explicit intent to clean before moving, but the current artifact still cannot be released.

