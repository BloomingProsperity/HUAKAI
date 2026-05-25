# F-RATE-001 Rate Limiting + Cooldown Computation (Codex independent pass)

| Field | Value |
| --- | --- |
| Status | Source-verified independent decomposition |
| Author | Codex |
| Date | 2026-04-28 |
| Lane | Specifier-lane independent parallel pass |
| Reference | Sub2API at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Local clone | `c:/HUAKAI/repo/.omc/reference-src/sub2api/` |
| Scope | F-RATE-001: multi-platform 429 detection, cooldown computation, OAuth 401 refresh, custom error policy |
| Clean-room stance | Source read for behavior evidence only; HUAKAI design is separated below and must be implemented independently. |

## Source Files Read

- `backend/internal/service/ratelimit_service.go`: entry policy, status-code decision tree, 403, 429, 529, session-window update, temp-unsched, stream-timeout runtime states (`ratelimit_service.go:121`, `ratelimit_service.go:795`, `ratelimit_service.go:1128`, `ratelimit_service.go:1165`, `ratelimit_service.go:1432`, `ratelimit_service.go:1571`).
- `backend/internal/service/model_rate_limit.go`: model-level rate-limit lookup by account and mapped/final model key (`model_rate_limit.go:9`, `model_rate_limit.go:30`, `model_rate_limit.go:52`, `model_rate_limit.go:80`).
- `backend/internal/service/gateway_service.go`: failover status filter, side-effect calls into rate-limit handling, successful response session-window updates, stream timeout handling, and detached contexts (`gateway_service.go:3668`, `gateway_service.go:6502`, `gateway_service.go:6651`, `gateway_service.go:6665`, `gateway_service.go:6777`, `gateway_service.go:7101`, `gateway_service.go:7347`, `gateway_service.go:7773`, `gateway_service.go:7781`).
- `backend/internal/service/account_credentials_persistence.go`: OAuth credential persistence helper used by forced refresh (`account_credentials_persistence.go:5`, `account_credentials_persistence.go:9`, `account_credentials_persistence.go:21`).
- Support files required to verify behavior: `account.go`, `account_service.go`, `temp_unsched.go`, `token_refresh_service.go`, `openai_gateway_service.go`, `gemini_messages_compat_service.go`, `antigravity_gateway_service.go`, `antigravity_quota_fetcher.go`, and `repository/account_repo.go`.

## Clean-Room Boundary

- All Sub2API references below are behavioral evidence and line attribution, not implementation instructions.
- KEEP items cite observed behavior that HUAKAI should preserve as product outcomes.
- IMPROVE items are labeled `HUAKAI-DESIGN, NOT in Sub2API` and must be implemented from HUAKAI contracts.
- AVOID items are anti-patterns or sharp edges observed in Sub2API behavior or state boundaries.
- This file intentionally does not reproduce Sub2API source code, tests, schema definitions, comments, or distinctive structures.

## Evidence Consistency Notes

- Every Sub2API behavior claim in this pass cites a source file and line from the local clone at commit `b0a2252ed19c3720e6adafde6083e64fbac2efa9` (`ratelimit_service.go:121`, `gateway_service.go:3668`).
- HUAKAI design recommendations are not cited as Sub2API behavior; where they reference a Sub2API gap, the citation points only to the observed source behavior being improved (`ratelimit_service.go:798`, `ratelimit_service.go:808`, `ratelimit_service.go:867`).
- LiteLLM labels are intentionally absent because LiteLLM source was not available in this task; taxonomy rows use only `SUB2API-VERIFIED` or `HUAKAI-DESIGN`.
- Claude's existing decomposition was not opened; this pass was built from the requested source files and direct helper dependencies only.
- The term "disable" in this document means "removed from scheduling by status/error/cooldown state" unless the row explicitly says lifecycle `disabled` (`account.go:107`, `account.go:108`, `domain/constants.go:6`).

## Entry Points and Scheduling Context

- Gateway failover treats `401`, `403`, `429`, `529`, and all `>=500` responses as account-failover candidates (`gateway_service.go:3668`, `gateway_service.go:3670`, `gateway_service.go:3674`).
- Non-retryable upstream error handling reads a capped response body and delegates to `HandleUpstreamError`; the returned disable flag affects error response handling (`gateway_service.go:6502`, `gateway_service.go:6549`, `gateway_service.go:6550`).
- Retry-exhausted side effects only call rate-limit handling for OAuth or setup-token `403`; other retry-exhausted API-key errors are logged without marking the account (`gateway_service.go:6651`, `gateway_service.go:6655`, `gateway_service.go:6657`, `gateway_service.go:6660`).
- Failover side effects always call `HandleUpstreamError` with the status, headers, and body (`gateway_service.go:6665`, `gateway_service.go:6667`).
- Successful streaming and non-streaming responses update the Anthropic session-window state from response headers (`gateway_service.go:6777`, `gateway_service.go:7347`).
- Streaming reads can trigger `HandleStreamTimeout`, which is a separate temp-unsched/error state path from HTTP status handling (`gateway_service.go:7101`, `ratelimit_service.go:1571`).
- Billing uses a context detached from client cancellation with a timeout, and streaming upstream calls detach cancellation only for streaming requests (`gateway_service.go:7773`, `gateway_service.go:7776`, `gateway_service.go:7778`, `gateway_service.go:7781`, `gateway_service.go:7788`).

## HandleUpstreamError Decision Tree

| Branch | Observed Sub2API behavior |
| --- | --- |
| Pool mode short-circuit | If an account is in pool mode and custom error codes are not enabled, local account state is not marked and the function returns false (`ratelimit_service.go:121`, `ratelimit_service.go:124`, `ratelimit_service.go:126`). Pool mode is only available for API-key or Bedrock-type accounts with a `pool_mode` credential flag (`account.go:836`, `account.go:837`, `account.go:840`). |
| Custom error-code filter | If `ShouldHandleErrorCode` is false, handling is skipped and the function returns false (`ratelimit_service.go:129`, `ratelimit_service.go:131`). `ShouldHandleErrorCode` returns true when custom error codes are disabled, true when the custom list is empty, and otherwise only true for listed codes (`account.go:923`, `account.go:924`, `account.go:928`, `account.go:931`, `account.go:936`). |
| Temp-unsched before status branch | For non-401 statuses, configured temp-unsched rules are attempted before the status switch, and a match returns true without running later disable/rate-limit logic (`ratelimit_service.go:134`, `ratelimit_service.go:135`, `ratelimit_service.go:136`, `ratelimit_service.go:137`). |
| `400` disable cases | `400` disables on messages containing organization disabled, Anthropic credit balance, or identity verification required; other `400` responses are not state-mutating in this branch (`ratelimit_service.go:147`, `ratelimit_service.go:150`, `ratelimit_service.go:154`, `ratelimit_service.go:159`, `ratelimit_service.go:164`). |
| `401` OpenAI terminal auth | OpenAI `token_invalidated` or `token_revoked` disables the account as an auth error (`ratelimit_service.go:165`, `ratelimit_service.go:167`, `ratelimit_service.go:168`, `ratelimit_service.go:173`). OpenAI `detail == Unauthorized` also disables (`ratelimit_service.go:177`, `ratelimit_service.go:178`, `ratelimit_service.go:183`). |
| `401` OAuth refresh window | OAuth accounts except Antigravity invalidate token cache, set `expires_at` to now, persist credentials, then set temp-unsched for configured minutes or 10 minutes default (`ratelimit_service.go:187`, `ratelimit_service.go:189`, `ratelimit_service.go:198`, `ratelimit_service.go:199`, `ratelimit_service.go:208`, `ratelimit_service.go:210`, `ratelimit_service.go:212`, `ratelimit_service.go:213`). |
| `401` non-OAuth or Antigravity OAuth | Non-OAuth and Antigravity OAuth accounts use auth-error disable behavior instead of the OAuth temp-unsched path (`ratelimit_service.go:217`, `ratelimit_service.go:223`, `ratelimit_service.go:224`). |
| `402` | OpenAI `deactivated_workspace` disables; all other `402` responses disable for billing/payment failure (`ratelimit_service.go:226`, `ratelimit_service.go:228`, `ratelimit_service.go:230`, `ratelimit_service.go:234`, `ratelimit_service.go:238`). |
| `403` | `403` logs raw diagnostics and delegates to platform-specific `handle403` (`ratelimit_service.go:240`, `ratelimit_service.go:252`). |
| `429` | `429` delegates to cooldown computation and does not return `shouldDisable` (`ratelimit_service.go:253`, `ratelimit_service.go:254`, `ratelimit_service.go:255`). |
| `529` | `529` delegates to overload cooldown and does not return `shouldDisable` (`ratelimit_service.go:256`, `ratelimit_service.go:257`, `ratelimit_service.go:258`). |
| Custom default branch | When custom error codes are enabled and the code reached the default branch, the account is set to custom-error disabled (`ratelimit_service.go:259`, `ratelimit_service.go:260`, `ratelimit_service.go:265`, `ratelimit_service.go:266`). |
| Uncustomized `5xx` | With no custom error-code match, unhandled `>=500` statuses are logged but do not mutate account availability (`ratelimit_service.go:267`, `ratelimit_service.go:269`, `ratelimit_service.go:270`). |

## 429 Cooldown Layers

### OpenAI `x-codex-*` Headers

- For OpenAI accounts, Sub2API first persists an OpenAI Codex usage snapshot and attempts to calculate a reset time from `x-codex-*` headers (`ratelimit_service.go:795`, `ratelimit_service.go:796`, `ratelimit_service.go:797`, `ratelimit_service.go:798`).
- The parser extracts primary and secondary used-percent, reset-after-seconds, and window-minutes headers, plus primary-over-secondary percent, and returns nil if none are present (`openai_gateway_service.go:5298`, `openai_gateway_service.go:5322`, `openai_gateway_service.go:5336`, `openai_gateway_service.go:5350`, `openai_gateway_service.go:5356`).
- Normalization maps primary/secondary windows into 5h/7d by comparing `window_minutes`; with no window metadata it assumes primary means 7d and secondary means 5h (`openai_gateway_service.go:130`, `openai_gateway_service.go:155`, `openai_gateway_service.go:157`, `openai_gateway_service.go:162`, `openai_gateway_service.go:169`, `openai_gateway_service.go:178`, `openai_gateway_service.go:179`).
- If normalized 7d used percent is at least 100 and a reset exists, the 7d reset wins; otherwise 5h at least 100 wins; otherwise the longest available reset-after-seconds is used (`ratelimit_service.go:916`, `ratelimit_service.go:917`, `ratelimit_service.go:920`, `ratelimit_service.go:925`, `ratelimit_service.go:931`, `ratelimit_service.go:939`).
- A successful OpenAI header reset writes account-level rate-limited state and returns without trying later 429 fallbacks (`ratelimit_service.go:798`, `ratelimit_service.go:799`, `ratelimit_service.go:803`, `ratelimit_service.go:804`).

### Anthropic Per-Window Headers

- Anthropic per-window cooldown reads 5h and 7d reset headers and returns nil when both are absent, allowing the aggregate-header fallback (`ratelimit_service.go:969`, `ratelimit_service.go:970`, `ratelimit_service.go:971`, `ratelimit_service.go:973`, `ratelimit_service.go:974`).
- A window is considered exceeded when its `surpassed-threshold` header is true or its utilization parses at roughly `>= 1.0` (`ratelimit_service.go:1023`, `ratelimit_service.go:1027`, `ratelimit_service.go:1032`, `ratelimit_service.go:1033`).
- If both 5h and 7d are exceeded, Sub2API prefers 7d and falls back to 5h; if exactly one is exceeded, that window is chosen; if neither is clearly exceeded, the sooner parsed reset is chosen (`ratelimit_service.go:997`, `ratelimit_service.go:1000`, `ratelimit_service.go:1002`, `ratelimit_service.go:1006`, `ratelimit_service.go:1008`, `ratelimit_service.go:1010`, `ratelimit_service.go:1012`).
- A successful Anthropic per-window result writes account-level rate-limited state, updates the 5h session window as rejected, and returns (`ratelimit_service.go:808`, `ratelimit_service.go:809`, `ratelimit_service.go:815`, `ratelimit_service.go:819`, `ratelimit_service.go:820`, `ratelimit_service.go:824`, `ratelimit_service.go:825`).

### Aggregate Header, Body Parsers, and Defaults

- If per-window parsing does not resolve, Sub2API reads `anthropic-ratelimit-unified-reset` as an aggregate Unix timestamp (`ratelimit_service.go:828`, `ratelimit_service.go:875`, `ratelimit_service.go:885`, `ratelimit_service.go:887`).
- If the aggregate header is absent, OpenAI body fallback parses `error.type` of `usage_limit_reached` or `rate_limit_exceeded`, preferring `resets_at` and then `resets_in_seconds` (`ratelimit_service.go:830`, `ratelimit_service.go:832`, `ratelimit_service.go:834`, `ratelimit_service.go:1095`, `ratelimit_service.go:1097`, `ratelimit_service.go:1101`, `ratelimit_service.go:1112`).
- Gemini and Antigravity body fallback parses Gemini-style daily quota, `metadata.quotaResetDelay`, or text matching retry-in seconds (`ratelimit_service.go:843`, `ratelimit_service.go:845`, `gemini_messages_compat_service.go:2797`, `gemini_messages_compat_service.go:2800`, `gemini_messages_compat_service.go:2808`, `gemini_messages_compat_service.go:2813`, `gemini_messages_compat_service.go:2826`, `gemini_messages_compat_service.go:2829`).
- Anthropic `429` with no reset timestamp is skipped and not locally marked rate-limited (`ratelimit_service.go:856`, `ratelimit_service.go:858`, `ratelimit_service.go:859`, `ratelimit_service.go:863`).
- Non-Anthropic `429` with no reset uses a 5-minute default account-level rate limit (`ratelimit_service.go:866`, `ratelimit_service.go:867`, `ratelimit_service.go:869`).
- Invalid aggregate reset timestamps also fall back to a 5-minute account-level rate limit (`ratelimit_service.go:875`, `ratelimit_service.go:876`, `ratelimit_service.go:878`, `ratelimit_service.go:879`).
- Aggregate-header success updates account-level rate-limited state and backfills a rejected 5h session window ending at the reset timestamp (`ratelimit_service.go:885`, `ratelimit_service.go:887`, `ratelimit_service.go:893`, `ratelimit_service.go:895`).

## 529 Overload Cooldown

- `529` is modeled separately from rate limit as an overloaded state with an `overload_until` timestamp (`ratelimit_service.go:1128`, `ratelimit_service.go:1156`, `ratelimit_service.go:1157`; `repository/account_repo.go:1112`, `repository/account_repo.go:1115`).
- Overload settings are read from setting service when available; read failure falls back to config (`ratelimit_service.go:1129`, `ratelimit_service.go:1132`, `ratelimit_service.go:1134`, `ratelimit_service.go:1138`).
- If no setting record exists, config `OverloadCooldownMinutes` is used and defaults to 10 when non-positive (`ratelimit_service.go:1139`, `ratelimit_service.go:1140`, `ratelimit_service.go:1141`, `ratelimit_service.go:1143`).
- If settings disable overload cooldown, the `529` is ignored for local cooldown state (`ratelimit_service.go:1146`, `ratelimit_service.go:1147`, `ratelimit_service.go:1148`).

## 403 Handling

- Platform dispatch sends Antigravity `403` to a classifier, OpenAI `403` to a counter-based cooldown/disable path, and all other platforms to auth-error disable (`ratelimit_service.go:680`, `ratelimit_service.go:681`, `ratelimit_service.go:684`, `ratelimit_service.go:687`, `ratelimit_service.go:693`).
- Non-Antigravity, non-OpenAI `403` is permanent auth-error state from the scheduler perspective because `handleAuthError` writes status `error` (`ratelimit_service.go:687`, `ratelimit_service.go:693`, `ratelimit_service.go:647`, `ratelimit_service.go:648`; `repository/account_repo.go:714`, `repository/account_repo.go:717`).
- OpenAI `403` without a counter cache immediately becomes auth-error disable (`ratelimit_service.go:697`, `ratelimit_service.go:705`, `ratelimit_service.go:706`, `ratelimit_service.go:707`).
- OpenAI `403` with a counter cache increments a 180-minute counter, disables at count `>=3`, and otherwise sets 10-minute temp-unsched (`ratelimit_service.go:56`, `ratelimit_service.go:57`, `ratelimit_service.go:58`, `ratelimit_service.go:710`, `ratelimit_service.go:717`, `ratelimit_service.go:723`, `ratelimit_service.go:725`).
- Antigravity `403` classifies the body as validation, violation, or generic forbidden (`ratelimit_service.go:765`, `ratelimit_service.go:766`, `ratelimit_service.go:768`; `antigravity_quota_fetcher.go:219`, `antigravity_quota_fetcher.go:223`, `antigravity_quota_fetcher.go:227`, `antigravity_quota_fetcher.go:230`).
- Antigravity validation appends an extracted validation URL when present and disables the account; violation and generic forbidden also disable (`ratelimit_service.go:769`, `ratelimit_service.go:777`, `ratelimit_service.go:780`, `ratelimit_service.go:783`, `ratelimit_service.go:791`, `ratelimit_service.go:794`, `ratelimit_service.go:802`; `antigravity_quota_fetcher.go:238`, `antigravity_quota_fetcher.go:240`).

## Temp-Unschedulable Rules

- Temp-unsched rules are enabled only when credentials contain boolean `temp_unschedulable_enabled` true (`account.go:277`, `account.go:281`, `account.go:285`, `account.go:286`).
- Rules are read from `temp_unschedulable_rules` as an array and each accepted rule has `error_code`, `keywords`, `duration_minutes`, and `description`; invalid rules are skipped (`account.go:289`, `account.go:293`, `account.go:298`, `account.go:310`, `account.go:311`, `account.go:312`, `account.go:313`, `account.go:314`, `account.go:317`).
- The runtime matcher rejects nil accounts, disabled temp-unsched, missing rules, non-positive status, and empty body (`ratelimit_service.go:1432`, `ratelimit_service.go:1433`, `ratelimit_service.go:1436`, `ratelimit_service.go:1454`, `ratelimit_service.go:1458`).
- For non-Antigravity `401`, a repeat 401 after a prior temp-unsched 401 returns false so default error handling can escalate (`ratelimit_service.go:1439`, `ratelimit_service.go:1440`, `ratelimit_service.go:1448`, `ratelimit_service.go:1451`).
- Response matching is capped to 64 KiB and keyword matching is case-insensitive substring matching (`ratelimit_service.go:1429`, `ratelimit_service.go:1462`, `ratelimit_service.go:1463`, `ratelimit_service.go:1466`, `ratelimit_service.go:1501`, `ratelimit_service.go:1510`).
- Triggered state records until time, trigger time, status code, matched keyword, rule index, and a response-message snapshot capped to 2048 bytes (`ratelimit_service.go:1430`, `ratelimit_service.go:1517`, `ratelimit_service.go:1521`, `ratelimit_service.go:1528`, `ratelimit_service.go:1534`).
- Repository persistence only updates temp-unsched when the new `until` extends the previous value, preventing shorter later matches from shortening a cooldown (`repository/account_repo.go:1126`, `repository/account_repo.go:1134`).
- Temp-unsched status can be served from cache first, falls back to DB, parses JSON reasons into structured state, and writes cache on DB fallback (`ratelimit_service.go:1371`, `ratelimit_service.go:1373`, `ratelimit_service.go:1383`, `ratelimit_service.go:1398`, `ratelimit_service.go:1400`, `ratelimit_service.go:1410`).

## OAuth 401 Force-Refresh Interaction

- The 401 path invalidates the token cache before mutating credentials when an invalidator exists (`ratelimit_service.go:187`, `ratelimit_service.go:189`, `ratelimit_service.go:190`).
- It sets credential `expires_at` to current RFC3339 time and persists via a helper that prefers `UpdateCredentials` when the repository supports it (`ratelimit_service.go:195`, `ratelimit_service.go:198`, `ratelimit_service.go:199`; `account_credentials_persistence.go:5`, `account_credentials_persistence.go:14`, `account_credentials_persistence.go:15`, `account_credentials_persistence.go:16`, `repository/account_repo.go:407`, `repository/account_repo.go:409`).
- Token providers decide refresh need from `expires_at`, so forcing `expires_at` to now makes the next request-path token provider consider refresh needed (`claude_token_provider.go:78`, `claude_token_provider.go:79`; `openai_token_provider.go:153`, `openai_token_provider.go:154`; `account.go:224`, `account.go:230`, `account.go:233`).
- Background refresh lists active accounts, including temp-unsched active accounts, and refreshes accounts whose provider says they need refresh (`token_refresh_service.go:227`, `token_refresh_service.go:229`, `token_refresh_service.go:162`, `token_refresh_service.go:173`, `token_refresh_service.go:186`).
- On refresh success, Sub2API clears temp-unsched if it is still active, deletes temp-unsched cache, and invalidates OAuth token cache (`token_refresh_service.go:340`, `token_refresh_service.go:341`, `token_refresh_service.go:368`, `token_refresh_service.go:371`, `token_refresh_service.go:380`, `token_refresh_service.go:382`).
- On retry-exhausted refresh failure, it sets temp-unsched for a retry-cooldown instead of setting account error, preserving active status for future refresh attempts (`token_refresh_service.go:298`, `token_refresh_service.go:309`, `token_refresh_service.go:311`, `token_refresh_service.go:317`).
- Non-retryable refresh errors set account error (`token_refresh_service.go:267`, `token_refresh_service.go:268`, `token_refresh_service.go:270`).

## Model-Level Rate Limit Granularity

- The model-rate-limit map key is `model_rate_limits`, stored in account extra state (`model_rate_limit.go:9`, `repository/account_repo.go:1065`, `repository/account_repo.go:1070`, `repository/account_repo.go:1082`, `repository/account_repo.go:1085`).
- The read-side scope is per account and per model key: generic accounts use `GetMappedModel(requestedModel)`, while Antigravity uses the final model key after mapping and thinking suffix (`model_rate_limit.go:30`, `model_rate_limit.go:35`, `model_rate_limit.go:37`, `model_rate_limit.go:68`, `model_rate_limit.go:73`, `model_rate_limit.go:74`, `model_rate_limit.go:75`).
- A model-level limit is active only when `rate_limit_reset_at` parses as RFC3339 and is in the future (`model_rate_limit.go:12`, `model_rate_limit.go:13`, `model_rate_limit.go:80`, `model_rate_limit.go:92`, `model_rate_limit.go:96`).
- Antigravity writes model-level limits after smart retry exhaustion or model-specific 429 handling, updates the in-cache account snapshot, and clears sticky session binding when present (`antigravity_gateway_service.go:394`, `antigravity_gateway_service.go:396`, `antigravity_gateway_service.go:401`, `antigravity_gateway_service.go:405`, `antigravity_gateway_service.go:2817`, `antigravity_gateway_service.go:2823`, `antigravity_gateway_service.go:2828`, `antigravity_gateway_service.go:2831`).
- Antigravity 429 handling resolves the final model key and falls back to account-level rate limit only when no model key can be resolved (`antigravity_gateway_service.go:2906`, `antigravity_gateway_service.go:2910`, `antigravity_gateway_service.go:2917`, `antigravity_gateway_service.go:2918`, `antigravity_gateway_service.go:2928`).
- Clearing the account-level rate limit also clears Antigravity quota scopes, model-rate-limit extra state, temp-unsched state, temp-unsched cache, and the OpenAI 403 counter (`ratelimit_service.go:1256`, `ratelimit_service.go:1257`, `ratelimit_service.go:1260`, `ratelimit_service.go:1263`, `ratelimit_service.go:1266`, `ratelimit_service.go:1269`, `ratelimit_service.go:1274`).

## UpdateSessionWindow From Successful Responses

- Session-window updates run only when `anthropic-ratelimit-unified-5h-status` is present (`ratelimit_service.go:1165`, `ratelimit_service.go:1166`, `ratelimit_service.go:1167`, `ratelimit_service.go:1168`).
- The 5h reset header is parsed as Unix seconds, with millisecond timestamps detected and divided by 1000 (`ratelimit_service.go:1175`, `ratelimit_service.go:1176`, `ratelimit_service.go:1177`, `ratelimit_service.go:1179`).
- Parsed reset times are accepted only if they are no earlier than now minus 5 hours and no later than now plus 7 days (`ratelimit_service.go:1181`, `ratelimit_service.go:1182`, `ratelimit_service.go:1183`, `ratelimit_service.go:1184`).
- On accepted reset changes, the stored 5h window start is reset minus five hours and status is recorded (`ratelimit_service.go:1186`, `ratelimit_service.go:1188`, `ratelimit_service.go:1190`, `ratelimit_service.go:1191`, `ratelimit_service.go:1216`).
- When no reset header is available and a window needs initialization, successful `allowed` or `allowed_warning` status creates a predicted window from the current hour to current hour plus five hours (`ratelimit_service.go:1198`, `ratelimit_service.go:1199`, `ratelimit_service.go:1200`, `ratelimit_service.go:1201`, `ratelimit_service.go:1203`).
- On window initialization, stale passive utilization fields are nulled before updating the window (`ratelimit_service.go:1207`, `ratelimit_service.go:1208`, `ratelimit_service.go:1209`, `ratelimit_service.go:1210`, `ratelimit_service.go:1211`, `ratelimit_service.go:1212`).
- Passive 5h/7d utilization and 7d reset headers are stored in extra state with sampled-at timestamp when present (`ratelimit_service.go:1220`, `ratelimit_service.go:1223`, `ratelimit_service.go:1228`, `ratelimit_service.go:1234`, `ratelimit_service.go:1242`, `ratelimit_service.go:1243`).
- If status becomes `allowed` while the account is currently rate-limited, Sub2API clears rate-limit state (`ratelimit_service.go:1249`, `ratelimit_service.go:1250`).

## Runtime State Machine

| State | Entered by | Scheduler effect | Recovery |
| --- | --- | --- | --- |
| `error` | Auth-error and custom-error paths call `SetError` (`ratelimit_service.go:647`, `ratelimit_service.go:648`, `ratelimit_service.go:785`, `ratelimit_service.go:787`). | `SetError` stores status `error`; `IsSchedulable` rejects non-active accounts (`repository/account_repo.go:714`, `repository/account_repo.go:717`, `account.go:107`, `account.go:108`). | `ClearError` sets status active and clears message (`repository/account_repo.go:789`, `repository/account_repo.go:792`, `repository/account_repo.go:793`). |
| `disabled` | Manual/status-level disabled exists as a non-active account state (`domain/constants.go:5`, `domain/constants.go:6`; `account.go:107`, `account.go:108`). | Non-active accounts are not schedulable (`account.go:107`, `account.go:108`). | Source inspected here does not show F-RATE automatic transition into `disabled`; it is an operator/lifecycle state at this commit. |
| `rate_limited` | `SetRateLimited` sets `rate_limited_at` and `rate_limit_reset_at` (`repository/account_repo.go:1048`, `repository/account_repo.go:1052`, `repository/account_repo.go:1053`). | `IsSchedulable` rejects future `RateLimitResetAt` (`account.go:118`, `account.go:119`); `IsRateLimited` returns true while reset is in the future (`account.go:130`, `account.go:134`). | `ClearRateLimit` clears rate limit and overload fields, and service-level clear also clears model/temp/counters (`repository/account_repo.go:1164`, `repository/account_repo.go:1167`, `repository/account_repo.go:1169`; `ratelimit_service.go:1256`, `ratelimit_service.go:1263`, `ratelimit_service.go:1266`). |
| `overloaded` | `529` path writes overload until time (`ratelimit_service.go:1156`, `ratelimit_service.go:1157`; `repository/account_repo.go:1112`, `repository/account_repo.go:1115`). | `IsSchedulable` rejects future `OverloadUntil`; `IsOverloaded` reports future overload (`account.go:115`, `account.go:116`, `account.go:137`, `account.go:141`). | Repository `ClearRateLimit` clears overload as well as rate-limit fields (`repository/account_repo.go:1164`, `repository/account_repo.go:1167`, `repository/account_repo.go:1169`). |
| `temp_unschedulable` | OAuth 401, temp-unsched rules, refresh retry exhaustion, or stream timeout can write temp-unsched (`ratelimit_service.go:213`, `ratelimit_service.go:1545`, `token_refresh_service.go:311`, `ratelimit_service.go:1644`). | `IsSchedulable` rejects future `TempUnschedulableUntil`; cache/DB status lookup returns active state while until is in the future (`account.go:121`, `account.go:122`, `ratelimit_service.go:1378`, `ratelimit_service.go:1390`). | `ClearTempUnschedulable` clears DB and cache, and also clears model-rate-limit state (`ratelimit_service.go:1324`, `ratelimit_service.go:1325`, `ratelimit_service.go:1328`, `ratelimit_service.go:1333`). |
| `model_rate_limited` | Antigravity model-specific paths write `model_rate_limits[model_key].rate_limit_reset_at` (`antigravity_gateway_service.go:2823`, `antigravity_gateway_service.go:2853`, `antigravity_gateway_service.go:2855`). | Model lookup blocks only the mapped/final model while reset is future (`model_rate_limit.go:30`, `model_rate_limit.go:37`, `model_rate_limit.go:41`, `model_rate_limit.go:80`, `model_rate_limit.go:96`). | Clearing rate limit or temp-unsched clears model-rate-limit extra (`ratelimit_service.go:1263`, `ratelimit_service.go:1333`, `repository/account_repo.go:1205`, `repository/account_repo.go:1209`). |

## Failure Modes Sub2API Handles

- Pool-mode accounts can avoid local state mutation for uncustomized upstream errors (`ratelimit_service.go:124`, `ratelimit_service.go:126`).
- Custom error-code lists can prevent unlisted statuses from changing account state (`ratelimit_service.go:129`, `ratelimit_service.go:131`; `account.go:923`, `account.go:936`).
- Specific `400`, `401`, `402`, and `403` auth/billing failures become explicit account error state (`ratelimit_service.go:150`, `ratelimit_service.go:168`, `ratelimit_service.go:228`, `ratelimit_service.go:238`, `ratelimit_service.go:693`).
- OAuth 401 can force token refresh while keeping status active through temp-unsched (`ratelimit_service.go:198`, `ratelimit_service.go:213`; `token_refresh_service.go:227`, `token_refresh_service.go:340`).
- OpenAI Codex 429 can use 5h/7d rate-limit headers and persist usage snapshots (`ratelimit_service.go:797`, `ratelimit_service.go:798`; `openai_gateway_service.go:5422`).
- Anthropic 429 can distinguish 5h and 7d windows and update rejected session-window state (`ratelimit_service.go:987`, `ratelimit_service.go:1000`, `ratelimit_service.go:820`).
- Gemini 429 can parse daily quota and retry-delay signals from body details or text (`gemini_messages_compat_service.go:2800`, `gemini_messages_compat_service.go:2808`, `gemini_messages_compat_service.go:2826`).
- Overload 529 can be operator-disabled or mapped to a separate overload state (`ratelimit_service.go:1146`, `ratelimit_service.go:1157`).
- OpenAI 403 can use a counter window before permanent error (`ratelimit_service.go:710`, `ratelimit_service.go:717`, `ratelimit_service.go:723`).
- Temp-unsched rules provide per-account keyword/status policy with structured reason state (`account.go:289`, `ratelimit_service.go:1468`, `ratelimit_service.go:1528`).
- Successful Anthropic responses can clear stale rate-limit state when status returns to allowed (`ratelimit_service.go:1249`, `ratelimit_service.go:1250`).

## Failure Modes Sub2API Does Not Handle or Leaves Thin

- Anthropic 429 without reset time is not locally cooled down, even if the provider returned HTTP 429 (`ratelimit_service.go:856`, `ratelimit_service.go:858`, `ratelimit_service.go:863`).
- OpenAI 403 counter state is unavailable when the counter cache is not configured; in that case first 403 becomes permanent error (`ratelimit_service.go:705`, `ratelimit_service.go:706`).
- Custom error-code behavior can suppress all unlisted status handling, including normal cooldown paths (`ratelimit_service.go:129`, `ratelimit_service.go:131`; `account.go:923`, `account.go:936`).
- Default non-Anthropic 429 fallback is fixed at 5 minutes when no reset is parsed (`ratelimit_service.go:866`, `ratelimit_service.go:867`).
- Temp-unsched keyword matching is substring-only and body-size capped to 64 KiB (`ratelimit_service.go:1429`, `ratelimit_service.go:1463`, `ratelimit_service.go:1510`).
- Temp-unsched persistence only extends a prior cooldown; it does not record a shorter newer reason if the existing until is later (`repository/account_repo.go:1126`, `repository/account_repo.go:1134`).
- Model-level rate limit is verified for Antigravity-triggered paths; the inspected generic 429 path remains account-level for OpenAI, Anthropic, Gemini, and generic fallback (`ratelimit_service.go:799`, `ratelimit_service.go:809`, `ratelimit_service.go:847`, `antigravity_gateway_service.go:2918`).
- Automatic transition into lifecycle `disabled` was not found in the inspected F-RATE files; disable-like failures use status `error` (`repository/account_repo.go:714`, `repository/account_repo.go:717`; `domain/constants.go:5`, `domain/constants.go:6`).

## KEEP for HUAKAI

- KEEP: Preserve pool-mode short-circuit semantics as an operator option so pooled API-key/Bedrock accounts can retry without automatically poisoning local account state (`ratelimit_service.go:124`, `ratelimit_service.go:126`; `account.go:836`, `account.go:840`).
- KEEP: Preserve custom error-code allowlist behavior, including the rule that unlisted codes skip local state mutation (`ratelimit_service.go:129`, `ratelimit_service.go:131`; `account.go:923`, `account.go:936`).
- KEEP: Preserve OAuth 401 as force-refresh plus temporary unschedulability rather than immediate permanent error for non-Antigravity OAuth accounts (`ratelimit_service.go:187`, `ratelimit_service.go:198`, `ratelimit_service.go:213`).
- KEEP: Preserve OpenAI Codex 5h/7d cooldown extraction and header snapshot capture (`ratelimit_service.go:797`, `ratelimit_service.go:798`; `openai_gateway_service.go:5298`, `openai_gateway_service.go:5422`).
- KEEP: Preserve Anthropic 5h/7d exceeded-window selection and session-window rejection update (`ratelimit_service.go:987`, `ratelimit_service.go:1000`, `ratelimit_service.go:820`).
- KEEP: Preserve 529 as a separate overload state with an operator off switch (`ratelimit_service.go:1146`, `ratelimit_service.go:1157`; `repository/account_repo.go:1112`).
- KEEP: Preserve OpenAI 403 graduated cooldown before permanent error when counter storage is available (`ratelimit_service.go:710`, `ratelimit_service.go:717`, `ratelimit_service.go:723`).
- KEEP: Preserve per-account temp-unsched rules with status code, keyword, duration, and structured reason (`account.go:310`, `account.go:317`; `ratelimit_service.go:1528`).
- KEEP: Preserve defensive session-window timestamp parsing, millisecond detection, and range validation (`ratelimit_service.go:1177`, `ratelimit_service.go:1182`, `ratelimit_service.go:1184`).
- KEEP: Preserve model-level cooldown for model-scoped upstream failures instead of always cooling an entire provider account (`model_rate_limit.go:30`, `model_rate_limit.go:80`; `antigravity_gateway_service.go:2918`).

## IMPROVE for HUAKAI

- IMPROVE: HUAKAI-DESIGN, NOT in Sub2API: define a first-class `ProviderFailureTaxonomy` enum and store it on routing events, account health history, and usage/audit records; Sub2API spreads evidence across status branches and logs (`ratelimit_service.go:147`, `ratelimit_service.go:240`, `ratelimit_service.go:253`, `ratelimit_service.go:256`).
- IMPROVE: HUAKAI-DESIGN, NOT in Sub2API: make 429 cooldown source explicit as `header_openai_codex`, `header_anthropic_window`, `header_anthropic_aggregate`, `body_openai`, `body_gemini`, or `default`, because Sub2API writes cooldown but not a normalized cooldown-source enum (`ratelimit_service.go:798`, `ratelimit_service.go:808`, `ratelimit_service.go:828`, `ratelimit_service.go:834`, `ratelimit_service.go:845`, `ratelimit_service.go:867`).
- IMPROVE: HUAKAI-DESIGN, NOT in Sub2API: record both selected reset window and all competing parsed reset candidates for operator review; Sub2API logs window analysis but persists only final runtime state (`ratelimit_service.go:990`, `ratelimit_service.go:1018`, `repository/account_repo.go:1048`).
- IMPROVE: HUAKAI-DESIGN, NOT in Sub2API: split permanent account disable, recoverable auth error, and operator-paused lifecycle states instead of overloading status `error` for many failures (`repository/account_repo.go:714`, `repository/account_repo.go:717`; `domain/constants.go:5`, `domain/constants.go:6`).
- IMPROVE: HUAKAI-DESIGN, NOT in Sub2API: add structured cooldown provenance to temp-unsched state, including policy ID/version, because Sub2API records rule index and keyword but not stable policy identity (`ratelimit_service.go:1528`, `ratelimit_service.go:1533`).
- IMPROVE: HUAKAI-DESIGN, NOT in Sub2API: make fallback cooldowns provider-policy driven rather than hardcoded 5 minutes for generic non-Anthropic 429 and 10 minutes for overload/OAuth defaults (`ratelimit_service.go:867`, `ratelimit_service.go:1141`, `ratelimit_service.go:210`).
- IMPROVE: HUAKAI-DESIGN, NOT in Sub2API: support structured provider error parsing before substring matching in temp-unsched rules so operators can target error code/type fields without brittle keyword matches (`ratelimit_service.go:1466`, `ratelimit_service.go:1510`).
- IMPROVE: HUAKAI-DESIGN, NOT in Sub2API: model-level cooldown should be provider-agnostic where provider payloads identify a model scope; Sub2API verifies this mostly through Antigravity paths (`antigravity_gateway_service.go:2918`, `ratelimit_service.go:799`, `ratelimit_service.go:809`).

## AVOID for HUAKAI

- AVOID: Do not let custom error-code filters silently bypass mandatory safety cooldowns without an explicit policy reason and audit event; Sub2API skips unlisted codes early (`ratelimit_service.go:129`, `ratelimit_service.go:131`).
- AVOID: Do not make the first OpenAI 403 permanent-error when counter storage is missing; Sub2API does that fallback (`ratelimit_service.go:705`, `ratelimit_service.go:706`).
- AVOID: Do not use a single generic status `error` for auth revocation, billing exhaustion, KYC, forbidden validation, and custom code without separate taxonomy fields; Sub2API funnels many branches through `SetError` (`ratelimit_service.go:150`, `ratelimit_service.go:168`, `ratelimit_service.go:228`, `ratelimit_service.go:238`, `ratelimit_service.go:693`).
- AVOID: Do not rely on substring-only temp-unsched matching for critical production policies; Sub2API lowercases and substring-matches truncated body text (`ratelimit_service.go:1463`, `ratelimit_service.go:1466`, `ratelimit_service.go:1510`).
- AVOID: Do not treat clearing account-level rate limit as inherently safe to clear all model-level and temp-unsched states unless policy says they share recovery semantics; Sub2API clear bundles these states together (`ratelimit_service.go:1256`, `ratelimit_service.go:1263`, `ratelimit_service.go:1266`).
- AVOID: Do not drop Anthropic 429 local cooldown solely because reset headers are absent without a separate provider-specific classification and observability record; Sub2API skips local state in that case (`ratelimit_service.go:856`, `ratelimit_service.go:858`, `ratelimit_service.go:863`).

## HUAKAI Failure Taxonomy Enum Candidate

| Enum candidate | Label | Evidence / design note |
| --- | --- | --- |
| `POOL_MODE_LOCAL_STATE_SKIPPED` | SUB2API-VERIFIED | Pool mode skips local marking without custom codes (`ratelimit_service.go:124`, `ratelimit_service.go:126`). |
| `CUSTOM_ERROR_CODE_SKIPPED` | SUB2API-VERIFIED | Unlisted custom status exits early (`ratelimit_service.go:129`, `ratelimit_service.go:131`). |
| `CUSTOM_ERROR_CODE_MATCHED` | SUB2API-VERIFIED | Listed custom status becomes custom error state (`ratelimit_service.go:260`, `ratelimit_service.go:265`). |
| `ORG_DISABLED_400` | SUB2API-VERIFIED | Organization disabled text in 400 disables (`ratelimit_service.go:150`, `ratelimit_service.go:152`). |
| `CREDIT_BALANCE_EXHAUSTED_400_402` | SUB2API-VERIFIED | Anthropic credit balance 400 and generic 402 disable (`ratelimit_service.go:154`, `ratelimit_service.go:234`, `ratelimit_service.go:238`). |
| `IDENTITY_VERIFICATION_REQUIRED_400` | SUB2API-VERIFIED | Identity verification 400 disables (`ratelimit_service.go:159`, `ratelimit_service.go:161`). |
| `TOKEN_REVOKED_401` | SUB2API-VERIFIED | OpenAI token revoked/invalidated disables (`ratelimit_service.go:168`, `ratelimit_service.go:173`). |
| `OAUTH_401_FORCE_REFRESH` | SUB2API-VERIFIED | OAuth 401 forces expiry and temp-unsched (`ratelimit_service.go:198`, `ratelimit_service.go:213`). |
| `PAYMENT_REQUIRED_402` | SUB2API-VERIFIED | 402 disables as payment/billing issue (`ratelimit_service.go:234`, `ratelimit_service.go:238`). |
| `FORBIDDEN_VALIDATION_403` | SUB2API-VERIFIED | Antigravity validation 403 disables with optional validation URL (`ratelimit_service.go:769`, `ratelimit_service.go:777`, `ratelimit_service.go:780`). |
| `FORBIDDEN_VIOLATION_403` | SUB2API-VERIFIED | Antigravity violation 403 disables (`ratelimit_service.go:783`, `ratelimit_service.go:791`). |
| `FORBIDDEN_TEMP_COOLDOWN_403` | SUB2API-VERIFIED | OpenAI 403 can temp-unsched before threshold (`ratelimit_service.go:710`, `ratelimit_service.go:723`, `ratelimit_service.go:725`). |
| `RATE_LIMIT_429_OPENAI_CODEX_WINDOW` | SUB2API-VERIFIED | OpenAI Codex window headers produce cooldown (`ratelimit_service.go:798`, `ratelimit_service.go:920`, `ratelimit_service.go:925`). |
| `RATE_LIMIT_429_ANTHROPIC_WINDOW` | SUB2API-VERIFIED | Anthropic per-window headers produce cooldown (`ratelimit_service.go:808`, `ratelimit_service.go:1000`, `ratelimit_service.go:1018`). |
| `RATE_LIMIT_429_BODY_RETRY_HINT` | SUB2API-VERIFIED | OpenAI/Gemini body parsers produce cooldown (`ratelimit_service.go:834`, `ratelimit_service.go:845`; `gemini_messages_compat_service.go:2808`). |
| `RATE_LIMIT_429_DEFAULT_COOLDOWN` | SUB2API-VERIFIED | Generic non-Anthropic 429 falls back to 5 minutes (`ratelimit_service.go:866`, `ratelimit_service.go:867`). |
| `OVERLOAD_529_COOLDOWN` | SUB2API-VERIFIED | 529 writes overload state when enabled (`ratelimit_service.go:1146`, `ratelimit_service.go:1157`). |
| `TEMP_UNSCHED_RULE_MATCH` | SUB2API-VERIFIED | Per-account status+keyword rules write temp-unsched (`ratelimit_service.go:1468`, `ratelimit_service.go:1477`, `ratelimit_service.go:1545`). |
| `MODEL_RATE_LIMITED` | SUB2API-VERIFIED | Model-specific reset timestamp is stored and checked per model key (`model_rate_limit.go:80`, `antigravity_gateway_service.go:2918`). |
| `COOLDOWN_SOURCE_STRUCTURED_AUDIT` | HUAKAI-DESIGN | Add normalized cooldown-source and parsed-candidate audit; Sub2API does not expose one enum across branches (`ratelimit_service.go:798`, `ratelimit_service.go:808`, `ratelimit_service.go:867`). |

## Acceptance Test Scenarios: Sub2API-Inheritable

| ID | Scenario |
| --- | --- |
| AT-RATE-001 | Given a pool-mode API-key account without custom error codes, when upstream returns 429/5xx, then local account state is not mutated and failover can proceed (`ratelimit_service.go:124`, `ratelimit_service.go:126`; `gateway_service.go:3670`). |
| AT-RATE-002 | Given custom error codes enabled with a non-listed status, when upstream returns that status, then no rate-limit/error/temp state is written (`ratelimit_service.go:129`, `ratelimit_service.go:131`; `account.go:923`, `account.go:936`). |
| AT-RATE-003 | Given OpenAI OAuth 401 with `token_revoked`, then account status becomes error and OAuth refresh temp-unsched path is not used (`ratelimit_service.go:168`, `ratelimit_service.go:173`, `ratelimit_service.go:175`). |
| AT-RATE-004 | Given non-Antigravity OAuth 401 with an expired token, then token cache is invalidated, `expires_at` is persisted as now, and temp-unsched is set for configured/default cooldown (`ratelimit_service.go:189`, `ratelimit_service.go:198`, `ratelimit_service.go:199`, `ratelimit_service.go:213`). |
| AT-RATE-005 | Given OpenAI 429 with Codex 7d exhausted headers, then account reset is now plus 7d reset-after seconds (`ratelimit_service.go:916`, `ratelimit_service.go:920`, `ratelimit_service.go:921`). |
| AT-RATE-006 | Given OpenAI 429 with Codex 5h exhausted and 7d not exhausted, then account reset is now plus 5h reset-after seconds (`ratelimit_service.go:917`, `ratelimit_service.go:925`, `ratelimit_service.go:926`). |
| AT-RATE-007 | Given Anthropic 429 with both 5h and 7d exceeded, then 7d reset is chosen and session-window status is rejected (`ratelimit_service.go:1000`, `ratelimit_service.go:1002`, `ratelimit_service.go:820`). |
| AT-RATE-008 | Given Anthropic 429 with no reset headers, then no local rate-limit state is written (`ratelimit_service.go:856`, `ratelimit_service.go:858`, `ratelimit_service.go:863`). |
| AT-RATE-009 | Given Gemini 429 with `metadata.quotaResetDelay`, then reset time uses ceiling seconds from that delay (`gemini_messages_compat_service.go:2808`, `gemini_messages_compat_service.go:2813`, `gemini_messages_compat_service.go:2816`). |
| AT-RATE-010 | Given generic non-Anthropic 429 with no parseable reset, then account-level cooldown is 5 minutes (`ratelimit_service.go:866`, `ratelimit_service.go:867`, `ratelimit_service.go:869`). |
| AT-RATE-011 | Given 529 and overload cooldown disabled, then no overload state is written (`ratelimit_service.go:1146`, `ratelimit_service.go:1147`, `ratelimit_service.go:1148`). |
| AT-RATE-012 | Given OpenAI 403 counter below threshold, then account becomes temp-unsched for 10 minutes; at threshold it becomes error (`ratelimit_service.go:717`, `ratelimit_service.go:723`, `ratelimit_service.go:725`). |
| AT-RATE-013 | Given Antigravity validation 403 with validation URL, then account error message includes validation URL and status becomes error (`ratelimit_service.go:769`, `ratelimit_service.go:777`, `repository/account_repo.go:717`). |
| AT-RATE-014 | Given a temp-unsched rule matching status and keyword, then temp-unsched reason includes status, keyword, rule index, and capped response message (`ratelimit_service.go:1468`, `ratelimit_service.go:1477`, `ratelimit_service.go:1528`, `ratelimit_service.go:1534`). |
| AT-RATE-015 | Given successful Anthropic response with 5h reset in milliseconds, then timestamp is divided by 1000 and accepted only within the allowed range (`ratelimit_service.go:1177`, `ratelimit_service.go:1179`, `ratelimit_service.go:1182`, `ratelimit_service.go:1184`). |
| AT-RATE-016 | Given an account is rate-limited and a later Anthropic successful response reports status `allowed`, then rate-limit state is cleared (`ratelimit_service.go:1249`, `ratelimit_service.go:1250`). |
| AT-RATE-017 | Given Antigravity model-specific 429 resolves a final model key, then only that model key is model-rate-limited (`antigravity_gateway_service.go:2910`, `antigravity_gateway_service.go:2918`; `model_rate_limit.go:30`, `model_rate_limit.go:41`). |

## Acceptance Test Scenarios: HUAKAI-Design

| ID | Scenario |
| --- | --- |
| AT-RATE-101 | HUAKAI-DESIGN: every cooldown write records `failure_taxonomy`, `cooldown_source`, parsed reset candidates, selected reset, and policy version; Sub2API has multiple branches without a single normalized enum (`ratelimit_service.go:798`, `ratelimit_service.go:808`, `ratelimit_service.go:867`). |
| AT-RATE-102 | HUAKAI-DESIGN: custom error-code skip emits an audit event with account, status, policy version, and reason so skipped cooldowns are explainable; Sub2API exits early (`ratelimit_service.go:129`, `ratelimit_service.go:131`). |
| AT-RATE-103 | HUAKAI-DESIGN: missing OpenAI 403 counter storage falls back to conservative temp cooldown plus ops alert, not first-failure permanent account error; Sub2API disables when cache is nil (`ratelimit_service.go:705`, `ratelimit_service.go:706`). |
| AT-RATE-104 | HUAKAI-DESIGN: provider-specific fallback cooldown policy is configurable by route/provider/account tier, replacing fixed 5m generic 429 fallback (`ratelimit_service.go:866`, `ratelimit_service.go:867`). |
| AT-RATE-105 | HUAKAI-DESIGN: Anthropic 429 without reset is classified into no-cooldown, short-cooldown, or manual-review by structured body classification instead of unconditional skip (`ratelimit_service.go:856`, `ratelimit_service.go:858`, `ratelimit_service.go:863`). |
| AT-RATE-106 | HUAKAI-DESIGN: clearing account-level rate limit does not automatically clear model-level and temp-unsched state unless recovery policy links those states; Sub2API clears them together (`ratelimit_service.go:1256`, `ratelimit_service.go:1263`, `ratelimit_service.go:1266`). |
| AT-RATE-107 | HUAKAI-DESIGN: temp-unsched rules can match structured provider error code/type fields before keyword fallback; Sub2API uses keyword substring matching over truncated body text (`ratelimit_service.go:1463`, `ratelimit_service.go:1510`). |
| AT-RATE-108 | HUAKAI-DESIGN: model-level cooldown exists for all providers when the provider response or route context identifies a safe model scope; Sub2API verified trigger is Antigravity-centered (`antigravity_gateway_service.go:2918`; `ratelimit_service.go:799`, `ratelimit_service.go:809`). |

## Open TODOs From This Commit Only

- TODO: Verify whether LiteLLM has equivalent taxonomy labels before marking any row `LITELLM-VERIFIED`; LiteLLM source was not cloned for this task.
- TODO: Verify the complete scheduler candidate exclusion path for `model_rate_limited`; this pass verified the account helper and Antigravity writes but did not deep-read every scheduler caller (`model_rate_limit.go:30`, `antigravity_gateway_service.go:2918`).
- TODO: Verify operator UI/API behavior for configuring temp-unsched rules; this pass verified account credential schema and runtime matcher only (`account.go:289`, `ratelimit_service.go:1432`).
- TODO: Verify whether lifecycle `disabled` can be entered by admin bulk update or other non-F-RATE code; this pass found the state constant and schedulability effect, but no automatic F-RATE transition into disabled (`domain/constants.go:6`, `account.go:107`, `repository/account_repo.go:1467`).
- TODO: Verify whether OpenAI WebSocket-specific rate-limit handling adds additional cooldown semantics outside the inspected HTTP gateway path; this pass focused on the files requested plus direct parser dependencies.
