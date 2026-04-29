# LiteLLM Cooldown Handler + Retry Policy Hierarchy - Round 3 Source-Verified

| Field | Value |
| --- | --- |
| Reference | LiteLLM, MIT anchor E-LIC-005; enterprise-only areas not read |
| HUAKAI rows | F-CH-002 (L2) + F-GW-004 (L1) |
| Lane | Codex specifier-lane R3 |
| Date | 2026-04-29 |
| Truth-discipline | Observed regions: 16 / Inferences: 9 / Open questions: 7 |
| Clean-room posture | Behavior-only. Upstream implementation names, distinctive structure, and code-shaped descriptions are intentionally omitted. |
| Critic read first | Yes: `.omc/artifacts/decomp-critic/C4-litellm-cooldown-retry.md` |

## §1 WHY

The reference design is trying to keep a multi-Channel gateway from repeatedly sending traffic into a Provider Account or Channel that has just shown a bad failure pattern. Official routing docs describe the system as load-balancing across multiple configured candidates and adding reliability controls: cooldowns, fallbacks, timeouts, and retries across multiple providers, with Redis used in production for cooldown and usage tracking [region-10].

The pressure is not just "retry the failed call." The observed behavior combines request retry, Provider Account/Channel health suppression, fallback to other model-facing groups, and separate branches for context-window and content-policy failures [region-12][region-13][region-14]. The public issue record shows why this matters: operators have reported retry delay not being honored on rate-limit paths and a provider-side temporary outage being treated differently depending on mapped HTTP/error class [region-15][region-16]. Therefore HUAKAI should treat F-CH-002 and F-GW-004 as one operational surface: failure classification, cooldown, retry budget, fallback target, and operator evidence must be deterministic together.

## §2 WHAT

### S-1: Cooldown is scoped to individual Provider Account / Channel candidates, not the whole requested Model

Official docs state cooldowns apply to individual configured candidates and keep healthy alternatives available, while each configured entry receives a unique identity used for tracking [region-10]. For HUAKAI vocabulary, this means the reference behavior maps most closely to Provider Account or Channel health, not to global Model health.

### S-2: Cooldown can be disabled globally for a gateway instance

The source gate exits before any cooldown decision when the gateway-level disable switch is set [region-2]. Official docs expose the same control in SDK and proxy configuration [region-10].

### S-3: Missing or unresolved Channel identity suppresses cooldown

The source gate exits when the failed candidate has no stable identity or cannot be mapped back to its group [region-2]. Observed implication: failure callback paths that lose identity cannot quarantine the failing candidate [region-2].

### S-4: Provider-default candidates are exempt from cooldown logic

The source gate exits when the failed candidate is in the gateway instance's provider-default identity list [region-2]. I did not observe a user-facing doc explaining this exemption; it is source-observed only.

### S-5: Connection-class errors are explicitly excluded from cooldown

The cooldown-required classifier checks the exception text for a connection-error marker and returns "do not cooldown" when it appears [region-1]. This is not the same as "all network-like failures are safe"; only the observed connection marker is confirmed.

### S-6: Status taxonomy has immediate cooldown triggers and non-triggering client errors

The cooldown-required classifier returns cooldown for 429, 401, 408, and 404, returns no cooldown for other 4xx statuses, and treats non-4xx statuses as cooldown-eligible [region-1]. If status parsing fails, it falls back to a server-error-like status before later decision logic [region-7].

### S-7: Default cooldown decision uses failure percentage plus a traffic floor

When no explicit allowed-failure policy is active, the source reads current-minute success and failure counters, computes failure percentage, and cools down on high failure percentage [region-3]. It also has a separate rule for the "all requests failed" case that requires a minimum traffic count before cooldown [region-4].

### S-8: Single-account protection is conditional, not a blanket last-healthy rule

The source detects when the configured group has only one candidate [region-3]. Under default percentage logic, a 429 and high-failure-percentage branch avoid cooldown for that single-candidate group, while the all-failed-with-traffic-floor branch can still cooldown even for a single-candidate group [region-4]. This confirms the critic's warning that "single-account exemption" is narrower than a generic last-healthy protection.

### S-9: Non-retryable status can still force cooldown

After percentage and status branches, the source asks the shared retryability classifier whether the status should be retried; if not, cooldown is triggered [region-4]. Observed behavior: cooldown and retryability are coupled, but not identical, because earlier branches can decide before this check.

### S-10: Explicit allowed-failure policy switches to a legacy counter path

When a gateway-level allowed-failure policy or non-default allowed-failure count is present, the default percentage decision is bypassed and a counter path is used [region-4][region-7]. The counter path increments a per-candidate failure counter with TTL and only cools down after the updated count exceeds the configured allowance [region-7].

### S-11: Cooldown duration has a short hierarchy

The write path starts from gateway-level cooldown duration or a fallback default, then allows the current failure path to supply a specific cooldown time [region-5]. The cache wrapper also uses caller-supplied cooldown duration when present, otherwise its initialized default, and writes the entry with TTL equal to that duration [region-8].

### S-12: Cooldown recovery is TTL disappearance, not proof of health

Cooldown entries are stored with TTL and active cooldown reads return only entries still present [region-8][region-9]. I did not observe a required health probe before a candidate becomes eligible again. Therefore "recovery" in the observed path is expiration-based, not probe-confirmed.

### S-13: Active cooldown reads are batch reads over all known candidates

Both async and sync read paths first ask the gateway instance for all known candidate identities, then batch-read active cooldown entries and return the identities still in cooldown [region-6]. This supports healthy selection excluding currently cooled candidates, but the exact selection call site was not fully read in this round.

### S-14: Gateway initialization uses a dual cache when Redis is configured, but one pre-cooldown counter is in-process

The gateway instance builds a cache combining distributed and in-memory layers for cooldown and usage state [region-5]. The allowed-failure counter used by the explicit policy path is initialized as an in-memory cache [region-5]. Observed consequence: some cooldown state can be shared across replicas, but the pre-cooldown counter path is not proven to be replica-shared.

### S-15: Failure handling is callback-driven

Gateway setup registers a failure callback that leads into candidate-level failure behavior [region-11]. The cooldown write path also triggers a cooldown event callback after writing a cooldown entry [region-5]. This confirms the critic's point that cooldown insertion depends on failure callback plumbing and available metadata.

### S-16: Retry configuration is layered across request/default count, exception policy, group-specific exception policy, and per-Channel override

The source and docs show multiple retry inputs: a gateway-level retry count, request-level retry count in the wrapper path, exception-specific policy, group-specific exception policy, and per-candidate maximum retry setting in candidate params [region-11][region-12][region-13]. Region 12 describes the wrapper checking request retry count before global default, then applying exception policy when present [region-12]. Region 11 shows gateway initialization parsing exception policy and group-specific policy [region-11]. Region 13 shows the type inventory includes a per-candidate max retry field [region-13].

### S-17: Exception-specific retry policies cover a fixed set of mapped error classes

The observed policy type exposes retry controls for bad request, authentication, timeout, rate limit, content-policy, and internal-server classes [region-13]. The implementation summary says the resolver maps an exception to a policy key and returns a configured count, otherwise none [region-12]. This supports deterministic exception-class policy, but it also inherits any upstream error-mapping mistakes.

### S-18: Docs promise rate-limit backoff, immediate generic retry, and minimum retry wait

Official docs state rate-limit retries use exponential backoff, generic errors retry immediately, and a minimum wait can be configured [region-10]. Issue reports show at least two operator-observed paths where configured delay or backoff was allegedly ignored [region-15]. Therefore HUAKAI should not treat the docs promise as complete runtime proof for every routing strategy.

### S-19: Fallback resolution checks exact, provider-stripped, wildcard, and string entries

The fallback resolver first checks exact group match, then a provider-stripped match, then wildcard, and can also accept a direct string fallback entry [region-14]. This is a deterministic order for generic fallback list resolution.

### S-20: Fallback execution is bounded, skips the original group, mutates the effective model target, and records attempted fallback depth

The async fallback path stops when depth reaches the configured maximum, skips fallback candidates equal to the original group, changes the effective requested model to the fallback target, increments fallback depth, calls the same fallback-capable request path, and adds fallback-attempt metadata to the successful response [region-14]. If a fallback fails, it records the latest failure and raises that latest failure after the loop [region-14].

### S-21: Typed fallback categories exist outside generic fallback

Official reliability docs describe content-policy fallback, context-window fallback, generic fallback for remaining errors, and default fallback [region-12]. Context-window fallback requires pre-call checks for pre-call enforcement [region-12]. This means a HUAKAI retry/fallback hierarchy must separate "fallback because the input cannot fit" from "fallback because a Provider Account failed."

### S-22: Specific Channel fallback can bypass cooldown checks

Official docs state that if all candidates in a group are in cooldown, a configured fallback to a specific model identity will skip the cooldown check for that fallback target [region-14]. This is an observed user-facing behavior and a high-risk divergence point for HUAKAI.

## §2-bis Lifecycle Traces

### Trace A: Default multi-Channel 429 path

Request enters a Route with multiple Provider Account candidates. One candidate receives 429. The cooldown classifier marks 429 as cooldown-eligible [region-1]. The default decision branch cools down 429 only when the group is not single-candidate [region-4]. If cooled, the write path stores the cooldown entry with the selected duration and emits a cooldown event [region-5]. Later active reads return that candidate as cooled while TTL is present [region-6][region-8].

### Trace B: Low-volume noisy failure path

Request traffic is sparse and a candidate has failures. The default branch computes failure percentage from current-minute counters [region-3]. The all-failed branch requires a minimum traffic count before cooldown [region-4]. If the floor is not reached and no other immediate branch applies, the candidate is not cooled by that branch [region-4].

### Trace C: Explicit allowed-failure policy path

Operator configures per-error or gateway-level allowed failures. The default percentage branch is bypassed [region-4]. The counter path gets the allowed count for the observed exception or gateway default, increments an in-memory failure counter, and cools down only when the updated count is greater than the allowance [region-7]. If not exceeded, the counter is stored with TTL equal to cooldown duration [region-7].

### Trace D: Retry then fallback path

A request fails and the retry wrapper decides the effective retry count from request/default count and exception policy [region-12]. If retries exhaust and fallbacks are configured, the fallback resolver chooses candidate fallback groups by exact, provider-stripped, wildcard, or direct string order [region-14]. The fallback executor skips the original group, increments depth, invokes the fallback-capable path, and adds attempted fallback count to successful response metadata [region-14].

### Trace E: Specific Channel emergency path

All candidates in one group are in cooldown. Docs describe fallback to a specific configured identity and explicitly say this skips cooldown check [region-14]. The request can therefore be sent to a cooled candidate through this specific fallback mechanism [region-14]. HUAKAI should treat this as an emergency override pattern, not as default routing.

## §3 INPUTS

Observed input inventory:

| Input | Observed role | Region |
| --- | --- | --- |
| Requested Model / model-facing group | Groups candidate Provider Accounts / Channels for selection and fallback. | [region-10][region-14] |
| Candidate identity | Unit tracked for cooldown and fallback-to-specific behavior. | [region-10][region-14] |
| Current exception class | Selects retry count and allowed-failure policy. | [region-12][region-13] |
| HTTP status | Drives cooldown-required taxonomy and retryability branch. | [region-1][region-4] |
| Exception text | Used to exclude connection-class failures from cooldown. | [region-1] |
| Current-minute success/failure counters | Used to compute failure percentage and traffic floor. | [region-3][region-4] |
| Cooldown duration | Stored as TTL on cooldown entry. | [region-5][region-8] |
| Disable cooldown flag | Stops cooldown logic. | [region-2][region-10] |
| Allowed-failure policy/count | Switches from percentage branch to counter branch. | [region-4][region-7][region-13] |
| Retry count and retry wait | Controls retry attempts and minimum delay according to docs. | [region-10][region-12] |
| Exception retry policy | Overrides retry counts per mapped error class. | [region-12][region-13] |
| Group-specific retry policy | Overrides general retry policy for selected group. | [region-11][region-12] |
| Fallback lists | Determine model/group fallback targets and order. | [region-12][region-14] |
| Fallback depth limit | Stops recursive fallback. | [region-14] |
| Pre-call check flag | Required for context-window enforcement before upstream call. | [region-12] |

## §4 FAILURE MODES

Only source-observed failure modes are listed.

| Failure mode | Observed behavior | HUAKAI concern | Region |
| --- | --- | --- | --- |
| Missing candidate identity | Cooldown logic exits. | Failed Provider Account may remain eligible. | [region-2] |
| Cooldowns disabled | Cooldown logic exits. | Operator may unintentionally remove health protection. | [region-2][region-10] |
| Connection-class error | Cooldown is skipped. | Good for network flaps; dangerous if provider outage is mapped as connection-like. | [region-1] |
| Other 4xx client errors | Cooldown is skipped by status taxonomy. | Temporary provider outage encoded as 400 can fail without Channel fallback. | [region-1][region-16] |
| Single-candidate 429/high-rate branch | Some default cooldown branches avoid single-candidate groups. | Availability is preserved, but failing single Provider Account may continue receiving traffic. | [region-4] |
| TTL expiry recovery | Candidate returns when cache entry expires. | Recovery is not proof of health. | [region-8][region-9] |
| Per-error allowed-failure counter in process memory | Counter path uses in-memory cache. | Multi-replica gateways can disagree before cooldown entry is written. | [region-5][region-7] |
| Specific identity fallback | Docs say cooldown check is skipped. | Cooled Channel can receive traffic without explicit operator audit. | [region-14] |
| Retry delay drift | Operators reported immediate retry despite configured delay/backoff. | F-GW-004 tests must verify runtime delay per strategy. | [region-15] |
| Error taxonomy drift | Same temporary outage can be mapped to non-fallback class. | Provider normalization must happen before retry/cooldown. | [region-16] |

## §5 INTERFACES TO HUAKAI

Personal Edition should expose the minimal reliable behavior: Route-level retry count, normalized Gateway Error class policy, Channel cooldown policy, per-Channel cooldown state, and an operator-visible Health page showing reason, first seen, last seen, expiry, and whether recovery was TTL-only or probe-confirmed.

SaaS Edition must add tenant and Provider Account dimensions. A cooldown event must be scoped at least by tenant, Route, Channel, Provider Account, logical Model, error class, and attempt id. SaaS should not share a cooldown state only by public model name or provider endpoint, because one tenant's upstream credential failure must not poison another tenant's Channel.

Both editions should connect F-CH-002 and F-GW-004 through a single Gateway Attempt ledger. Retry/fallback should consume a request-level budget, while cooldown decisions should consume attempt-level evidence. Billing and Quota must settle once per logical Gateway Request, not once per failed attempt.

## §6 RISKS

| Risk | Type | Reasoning |
| --- | --- | --- |
| Tenant blast radius | Inference, not observed | Source regions show candidate-level health, not HUAKAI tenant/account/channel scoping. DR-001 requires tenant isolation. |
| Split-brain cooldown threshold | Inference, not observed | Cooldown entries can use shared cache, but explicit allowed-failure pre-threshold counter is in-memory [region-5][region-7]. Two HUAKAI replicas could undercount unless PostgreSQL/Redis owns the threshold. |
| Recovery ambiguity | Observed + inference | TTL expiry is observed [region-8][region-9]. HUAKAI ops needs to distinguish expired, probe-passed, manual-restored, and forced-enabled. |
| Retry cost amplification | Inference, not observed | Retry/fallback layers are observed [region-12][region-14]. HUAKAI billing needs one logical request settlement and per-attempt cost attribution. |
| Unsafe emergency bypass | Observed + inference | Specific identity fallback skips cooldown [region-14]. HUAKAI should require audited emergency override. |
| Error class drift | Observed issue | Issue #12503 shows temporary provider outage can be classified as a non-fallback 400 path [region-16]. HUAKAI needs provider-normalized error reasons. |
| Docs/runtime drift | Observed issue | Docs promise retry wait/backoff [region-10], issues report ignored wait/backoff [region-15]. HUAKAI acceptance tests must verify behavior, not just config presence. |
| Dual Edition configuration drift | Inference, not observed | Personal Edition may tolerate local state; SaaS Edition cannot. Config must make storage backend and isolation guarantees explicit. |
| Callback dependency | Observed + inference | Failure registration and cooldown event callbacks are observed [region-11][region-5]. HUAKAI should not depend on mutable process-global callbacks for health correctness. |

## §7 SAFE ADAPTATION

1. Implement cooldown as a HUAKAI Health state machine, not as a direct copy of upstream cache behavior.
2. Store Health evidence in PostgreSQL first for correctness under DR-006, with Redis allowed as a serving cache.
3. Scope Health by tenant, Route, Channel, Provider Account, logical Model, and provider-normalized error class.
4. Normalize provider errors before retry/cooldown: `rate_limited`, `auth_invalid`, `quota_exhausted`, `transient_upstream`, `context_too_large`, `content_policy_blocked`, `bad_request_user`, `gateway_overload`, `network_unknown`.
5. Keep the traffic-volume floor pattern for failure-rate cooldown, but make the floor and percentage policy explicit per Channel class.
6. Preserve single-Provider Account availability by an explicit `last_resort` policy. Do not silently continue to send traffic to a failing Account without marking it degraded.
7. Replace specific-Channel cooldown bypass with an audited emergency override requiring actor, reason, expiry, and incident id.
8. Define retry precedence as: request override, tenant policy, Route policy, Channel/Provider Account policy, platform default, then provider response wait hint as a timing input, not as a retry-count override.
9. Separate request retry budget from fallback depth budget and Channel health accounting.
10. Emit one Gateway Attempt record per try and one Usage Record/Billing Ledger settlement per logical request.
11. Treat TTL expiry as "eligible by expiry", not "healthy"; optional probe can promote to "probe-passed."
12. Add acceptance tests for two gateway replicas alternating failures, usage-based routing retry delay, streaming partial failure, context-window pre-call fallback, content-policy fallback, and emergency override audit.

## §8 EVIDENCE LEDGER ROWS

| Evidence ID | Project | Source type | Behavior | HUAKAI directive |
| --- | --- | --- | --- | --- |
| E-LM-DEEP-R3-001 | LiteLLM | Source code deep read | Cooldown taxonomy combines status class, connection-error exclusion, disable gates, identity presence, provider-default exemption, failure percentage, traffic floor, single-candidate handling, and allowed-failure counter path. | IMPROVE: implement as typed, tenant-scoped Health policy. |
| E-LM-DEEP-R3-002 | LiteLLM | Source code deep read | Cooldown state is TTL-backed and active reads return only currently present entries. | IMPROVE: TTL expiry is not recovery proof; add Health recovery source. |
| E-LM-DEEP-R3-003 | LiteLLM | Source code deep read | The gateway instance uses shared cache for cooldown entries but in-memory cache for explicit allowed-failure pre-threshold counter. | AVOID: use durable/distributed threshold counting for SaaS. |
| E-LM-DEEP-R3-004 | LiteLLM | Source code + docs | Retry policy has multiple layers: request/default count, exception policy, group-specific exception policy, per-candidate max retry, retry wait docs, and fallback depth. | IMPROVE: publish deterministic HUAKAI precedence and tests. |
| E-LM-DEEP-R3-005 | LiteLLM | Source code + docs | Fallback resolution includes exact, provider-stripped, wildcard, direct string, typed fallback categories, default fallback, and bounded fallback execution. | KEEP: typed fallback categories; IMPROVE: audit model/Channel mutation. |
| E-LM-DEEP-R3-006 | LiteLLM | Official docs | Specific identity fallback can skip cooldown check. | AVOID default; reframe as audited emergency override. |
| E-LM-DEEP-R3-007 | LiteLLM | Public issues | Operators report retry wait/backoff and error taxonomy drift. | IMPROVE: acceptance-test runtime behavior by strategy and provider error class. |

## §9 OPEN QUESTIONS

1. I did not fully source-read the exact healthy-selection method that subtracts active cooldown candidates from candidate lists in current `main`; docs and cooldown read helpers support the claim, but exact call-site behavior remains open.
2. I did not fully source-read streaming retry/fallback in this R3 pass; any streaming-specific cooldown/retry behavior should be decomposed separately.
3. I did not observe how provider response wait headers are merged with configured retry wait in current `main`; issue evidence shows drift, not final implementation truth.
4. I did not observe whether group-specific retry policy always has priority over global exception policy in all call paths; available source summaries indicate override, but exact current call order needs a direct source region.
5. I did not observe whether provider-default cooldown exemption is user-configurable or only internal.
6. I did not observe full sync fallback behavior; async fallback path was read.
7. I did not observe all endpoint families (embeddings, image, pass-through, batch) for parity with chat completion retry/cooldown behavior.

## §10 SOURCE COVERAGE PROOF

| Region | Source region read | What it contributed |
| --- | --- | --- |
| region-1 | Public source mirror of cooldown classifier, lines 109-191 | Connection-error exclusion and status-code cooldown taxonomy. |
| region-2 | Public source mirror of cooldown gate, lines 193-284 | Disable switch, missing identity, unresolved group, and provider-default exemption. |
| region-3 | Public source mirror of cooldown decision, lines 286-379 | Single-candidate detection and current-minute success/failure counter calculation. |
| region-4 | Public source mirror of cooldown decision, lines 381-433 | 429 branch, all-failed traffic floor, high-failure-percentage branch, non-retryable branch, policy-path switch. |
| region-5 | Public source mirror of cooldown write path, lines 437-529; official source gateway init lines 3134-3178 and callback lines 3366-3379 | Cooldown duration selection, cache write, cooldown event callback, shared cache init, in-memory failed-call counter, failure callback registration. |
| region-6 | Public source mirror of active cooldown read paths, lines 533-649 | Async/sync active cooldown read behavior over known candidate identities. |
| region-7 | Public source mirror of allowed-failure counter and status cast, lines 651-752 | Per-error/gateway allowed-failure counter, TTL on pre-threshold count, parse fallback to server-error-like status. |
| region-8 | Official raw source cooldown cache, lines 0-5 from fetched raw view | Cooldown entry contains masked exception/status/timestamp/duration and is written with TTL. |
| region-9 | Official raw source cooldown cache, lines 3-5 from fetched raw view | Active cooldown reads batch cache keys and return only present dict entries; min cooldown returns stored duration or default. |
| region-10 | Official routing docs, lines 45-52, 836-901, 963-1031 | Reliability feature set, Redis production note, cooldown docs, per-candidate cooldown docs, retry/backoff/min wait docs, custom retry/cooldown policy docs. |
| region-11 | Official source gateway init, lines 3401-3451 | Gateway initialization parses exception retry policy, group retry policy, and allowed-failure policy. |
| region-12 | Official reliability docs, lines 135-144, 361-455, 568-753 | Typed fallbacks, context-window requirement, content-policy fallback, default fallback, retries/fallbacks/cooldowns config. |
| region-13 | Official source type definitions mirror, lines 609-675 and 826-875 | Per-candidate retry input, allowed-failure policy classes, retry policy classes. |
| region-14 | Official source fallback handler, lines 777-883 and 885-1020; official reliability docs lines 485-531 | Exact/stripped/wildcard/direct fallback resolution, bounded async fallback execution, fallback metadata, specific identity cooldown bypass. |
| region-15 | GitHub issues #6011 and #7669, issue body lines 178-207 and 210-249 | Operator reports of retry delay/backoff not being honored, especially usage-based routing path. |
| region-16 | GitHub issue #12503 search/open result | Operator report that 400 temporary upstream error did not trigger fallback while 401 did, showing taxonomy drift. |

## §11 ROUND-2 CRITIC FINDINGS

| Finding | Disposition |
| --- | --- |
| C-001 mixed cooldown decision tree | CONFIRM-from-source: §2 S-1..S-12 cite region-1..region-8. |
| C-002 multi-process split-brain | CONFIRM-from-source + HUAKAI inference: shared cooldown cache and in-memory pre-threshold counter observed in region-5/7; SaaS risk in §6. |
| C-003 endpoint-path retry delay sensitivity | CONFIRM-from-source for docs/issues drift: region-10 and region-15. Exact endpoint source path remains open in §9. |
| C-004 wider retry hierarchy | CONFIRM-from-source: §2 S-16 cites region-11..region-13. |
| C-005 callback and metadata dependency | CONFIRM-from-source: §2 S-3 and S-15 cite region-2, region-5, region-11. |
| C-006 TTL recovery, not health proof | CONFIRM-from-source: §2 S-12 cites region-8/9. |
| C-007 status-code taxonomy blind spots | CONFIRM-from-source: §2 S-6 and §4 cite region-1/16. |
| C-008 specific identity skips cooldown | CONFIRM-from-source: §2 S-22 cites region-14. |
| F-001 production behavior depends on many dimensions | CONFIRM-from-source: §2 enumerates status, traffic, policy, group cardinality, cache, fallback. |
| F-002 retry-after complexity | CONFIRM-from-source for docs/issues drift; provider-header merge remains OPEN (§9). |
| F-003 fallback mutates effective group | CONFIRM-from-source: §2 S-20 cites region-14. |
| F-004 noisy-neighbor risk | CONFIRM as HUAKAI inference: §6 tenant blast radius. |
| F-005 automatic recovery hides ops gap | CONFIRM-from-source: §2 S-12 and §6 recovery ambiguity. |
| D-001 docs vs retry-delay issues | CONFIRM-from-source: region-10 vs region-15. |
| D-002 cooldown not simple count | CONFIRM-from-source: §2 S-6..S-10. |
| D-003 docs recovery vs TTL | CONFIRM-from-source for TTL; active health confirmation remains unobserved. |
| D-004 fallback classification drift | CONFIRM-from-source: region-16. |
| D-005 specific identity bypass contradiction | CONFIRM-from-source: region-14. |
| N-001 avoid local in-memory authoritative gate | CONFIRM as HUAKAI adaptation: §7 item 2. |
| N-002 avoid cooldown bypass default | CONFIRM as HUAKAI adaptation: §7 item 7. |
| N-003 avoid mixed SDK/proxy retry semantics | CONFIRM as HUAKAI adaptation: §7 items 8-10. |
| N-004 avoid status-only retryability | CONFIRM as HUAKAI adaptation: §7 item 4. |
| N-005 avoid TTL as recovery proof | CONFIRM as HUAKAI adaptation: §7 item 11. |
| N-006 avoid callback as health pipeline | CONFIRM as HUAKAI risk/adaptation: §6 and §7 item 10. |
| N-007 avoid one default cooldown | CONFIRM as HUAKAI adaptation: §7 item 5. |
| S-001 local-only cooldown state risk | CONFIRM partially: pre-threshold counter local observed; cooldown cache shared when Redis configured. |
| S-002 hidden global callback state | CONFIRM-from-source: failure callback registration observed in region-11. |
| S-003 inconsistent taxonomy | CONFIRM-from-source: region-1 and region-16. |
| S-004 magic constants / operator policy | CONFIRM as HUAKAI risk: source constants observed, HUAKAI policy recommendation in §7. |
| S-005 fail-open specific fallback | CONFIRM-from-source: region-14. |
| S-006 tenant leakage potential | CONFIRM as HUAKAI inference, not observed upstream claim: §6. |
| S-007 recovery ambiguity | CONFIRM-from-source: region-8/9. |

中文总结：本轮把 LiteLLM 的 cooldown handler 与 retry/fallback hierarchy 拆成 22 个可追溯子行为，真实观察包括冷却状态分类、连接错误排除、失败率阈值、流量地板、单候选保护边界、TTL 恢复、回调触发、重试/回退配置层级、特定 Channel 回退跳过 cooldown，以及 retry delay / 错误分类漂移的公开 issue；合理推断主要集中在 HUAKAI 的 DR-001/DR-002/DR-006 适配风险，例如租户隔离、PostgreSQL 持久化、双实例一致性和审计化 emergency override；critic 的 C/F/D/N/S finding 均已逐项 CONFIRM 或标为部分确认/开放问题；当前保留 7 个 open questions，重点是当前 main 的精确健康选择调用点、streaming 路径、provider retry header 合并顺序、以及全 endpoint family 的一致性。
