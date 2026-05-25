# LiteLLM - Cooldown Handler + Retry Policy Hierarchy

| Field | Value |
| --- | --- |
| Status | Draft source-verified decomposition |
| Reference | LiteLLM (MIT, E-LIC-005; `enterprise/` not read) |
| Feature in HUAKAI matrix | F-CH-002 (L2) + F-GW-004 (L1) |
| Evidence ledger row | E-LM-DEEP-001..006, E-LM-DEEP-007, E-LM-DEEP-014 |
| Specifier session | Codex specifier-lane |
| Specifier date | 2026-04-29 |
| Reviewer session | Pending |
| Reviewer date | Pending |
| Verified source URLs | https://github.com/BerriAI/litellm/blob/62920a0cb29f11912edb5bacee470f1b1c044def/litellm/router.py<br>https://github.com/BerriAI/litellm/blob/62920a0cb29f11912edb5bacee470f1b1c044def/litellm/router_utils/cooldown_handlers.py<br>https://github.com/BerriAI/litellm/blob/62920a0cb29f11912edb5bacee470f1b1c044def/litellm/router_utils/get_retry_from_policy.py<br>https://github.com/BerriAI/litellm/blob/62920a0cb29f11912edb5bacee470f1b1c044def/litellm/constants.py |

## 1. WHY

This feature exists because Provider Account failure handling has two competing risks. If the gateway keeps routing traffic to a failing Provider Account, Users see repeated errors and retries waste quota, latency, and upstream spend. If the gateway quarantines too aggressively, one transient miss can remove a healthy Provider Account and create an avoidable outage. The source balances those risks with error class, observed failure rate, minimum traffic volume, and Pool size before mutating selection state. This supports F-CH-002 because live request failures become Channel health signals.

The retry hierarchy solves a separate operator problem: different Users, User Groups, Models, Provider Accounts, and error classes need different retry budgets. A global retry count is too blunt during incidents. Rate-limit, auth-fail, timeout, and bad request classes may need different budgets; a specific Provider Account may need a different budget from the Router default; a per-request override may need to preserve caller intent. LiteLLM applies these layers before fallback, supporting F-GW-004 deterministic retry behavior.

Inference: the minimum traffic floor is meant to prevent one failed request from becoming a false 100% failure-rate quarantine.

## 2. WHAT

For cooldown, HUAKAI should model each Provider Account Deployment as carrying short-window success and failure counters. On each failed upstream attempt, the gateway classifies the error into a HUAKAI failure class: connection failure, rate-limit, auth-fail, timeout, not-found, bad request, content-policy, context-window, or generic upstream failure.

Connection failure is treated differently from a provider API rejection: it does not by itself make the Provider Account unhealthy for cooldown. Rate-limit, auth-fail, timeout, and not-found are cooldown-eligible. Most other User-caused 4xx responses are not; non-4xx provider failures are cooldown-eligible.

If cooldown is enabled and the Provider Account Deployment maps to a logical Model group, the gateway increments the current-minute failure counter. In the default path, cooldown applies when:

- the failure is rate-limit and the logical Model group has more than one configured Provider Account Deployment;
- all recent requests for that Provider Account Deployment failed and the traffic volume reaches a high-volume floor;
- the failure ratio is above the default threshold and the minimum request floor has been reached, excluding the single-configured-deployment case;
- the failure class is not retryable.

The single-account exemption is precise: it protects a logical Model group with exactly one configured Provider Account Deployment from ordinary rate-limit and failure-rate cooldown. It is not a general "last remaining healthy Provider Account" exemption. High-volume all-fail behavior, non-retryable failures, and explicit fail-count policy can still quarantine it.

Cooldown duration is selected by deterministic precedence: Provider Account Deployment cooldown setting first, provider retry-after signal second, Router default last. A zero cooldown value is a disable signal and should not be replaced with the default.

For retry hierarchy, HUAKAI should resolve retry count in this order of effect. Start with the Router default or request-provided retry count already attached to the attempt. If the selected Provider Account Deployment adds a retry count to the raised attempt error and no earlier request-level count already exists on that error, use the Provider Account Deployment value. Then evaluate retry policy by logical Model group and exception class. If a per-request Model-group policy is provided, it replaces the Router-level Model-group policy for matching Models. If the selected policy has an exception-class-specific retry budget, that value becomes final and bypasses the generic retryability gate. If no retry policy applies, the generic retryability check still controls retry.

Retries are exhausted within the same logical Model group before fallback. If another healthy Provider Account Deployment in the same group exists, retry sleep is zero; otherwise the gateway uses provider retry-after or calculated backoff.

## 3. INPUTS

Inputs consumed: request-level retry count, timeout, fallback settings, optional per-request Model-group retry policy, Router retry/cooldown settings, Provider Account Deployment metadata, current-minute counters, cooldown registry entries, error status/class, response headers, candidate Provider Account Deployments, and time.

State mutated: per-Provider Account Deployment failure counter, cooldown registry with expiry, retry metadata on the request/response path, and the selection candidate set.

## 4. FAILURE MODES HANDLED

- **Single transient failure flapping**: detected below the minimum request floor; response is no cooldown.
- **Low-traffic 100% failure illusion**: detected below traffic threshold; response is no failure-rate quarantine.
- **Rate-limited Provider Account in a Pool**: detected by rate-limit class plus multiple configured Provider Account Deployments; response is temporary cooldown and routing to remaining candidates.
- **Only configured Provider Account**: detected by logical Model group size of one; response is bypass of ordinary rate-limit and failure-rate cooldown, while preserving explicit policy and hard-failure paths.
- **Connection failure noise**: detected by connection-error class marker; response is no cooldown, avoiding blacklist from network blips.
- **Auth-fail, timeout, and not-found**: detected by taxonomy; response is cooldown eligibility because these often indicate unusable Provider Account state or stale config.
- **Caller-specific retry policy**: detected by per-request override payload; response is to preserve request-level settings over User Group or API Key settings.
- **Exception-specific retry need**: detected by normalized failure class; response is exception-type retry count with deterministic precedence.

HUAKAI should record `routing_reason`, `cooldown_reason`, `retry_source`, and `attempt_index` on Usage Record and operator logs.

## 5. INTERFACES TO HUAKAI

This feeds Route execution, Channel health, Provider Account selection, Usage Record, Audit Event, and Admin Ops UI. Retry count and retry sleep must resolve before fallback for F-GW-004. Cooldown state must remove Provider Account Deployments from the selection set for F-CH-002. The Admin Ops UI should expose current cooldown entries, expiry time, reason class, short-window counters, and manual resume. API responses must not leak upstream credentials or internal Provider Account identifiers.

## 6. RISKS

- **Clean-room risk**: implementation must not reuse LiteLLM symbols, source layout, or test fixture structure.
- **Operational risk**: a single-account exemption can hide real outage if treated as absolute.
- **Security risk**: auth-fail cooldown may mean compromise, expired upstream credential, or provider-side outage.
- **Cost risk**: exception-specific retry policy can amplify spend when high retry counts combine with fallback.
- **Distributed risk**: in-memory counters are insufficient for SaaS Edition where money or quota is affected.
- **Explainability risk**: too many override layers can make production behavior surprising unless final resolved policy is logged.

## 7. SAFE ADAPTATION FOR HUAKAI

- **KEEP**: failure-rate cooldown with a minimum request floor; this directly prevents false quarantine from one failed request.
- **KEEP**: connection failure is not the same as rate-limit, auth-fail, timeout, or not-found for cooldown.
- **KEEP**: cooldown duration precedence of Provider Account Deployment setting, provider retry-after, then Router default.
- **KEEP**: retry current logical Model group before fallback; fallback should not start until retry budget is exhausted or bypassed by non-retryable failure.
- **IMPROVE**: split single-account handling into only configured Provider Account, last healthy Provider Account after filters, high-volume all-fail, and operator-forced fail-closed states.
- **IMPROVE**: make retry precedence explicit in a HUAKAI policy resolver that returns both value and source.
- **IMPROVE**: add structured reason codes and Admin Ops override workflow for cooldown re-entry.
- **AVOID**: silent fallback or retry that lacks Usage Record attempt attribution.
- **AVOID**: treating process-local cooldown counters as sufficient for quota, billing, or SaaS-wide incident response.

## 8. EVIDENCE LEDGER ROWS

- **E-LIC-005**: LiteLLM is the MIT safe anchor; `enterprise/` remained unread.
- **E-LM-DEEP-001..004**: status/class cooldown, failure-rate threshold, traffic floor, configurable duration, expiry, and selection exclusion.
- **E-LM-DEEP-005**: use with corrected wording: single-configured-deployment guard, not broad last-healthy Provider Account guarantee.
- **E-LM-DEEP-006**: connection-error class distinction for cooldown bypass.
- **E-LM-DEEP-007**: retry policy hierarchy and retry-before-fallback behavior.
- **E-LM-DEEP-014**: cooldown duration ladder and healthy selection exclusion.

## 9. OPEN QUESTIONS

1. Should HUAKAI auto re-enter after cooldown expiry, or require operator-confirm-resume for auth-fail and repeated not-found?
2. What is the L1 minimum traffic floor per Provider Account Deployment, and should it vary by Model or Provider?
3. Should connection failure ever trigger cooldown after a cluster-wide floor, or only affect latency/routing score?

Owner 总结：本文件拆解了 LiteLLM 的 cooldown handler 与 retry policy hierarchy，重点补齐了 failure-rate threshold、traffic-volume floor、connection-error class detection、single-configured-deployment guard，以及 global / request / Provider Account Deployment / exception-type retry override 的确定性优先级；与已有 sub2api 拆解的关键差异是，sub2api 更偏 Provider Account 选择、粘性与等待计划，而 LiteLLM 在错误分类、短窗口失败率、单账号防误伤和异常类型 retry policy 上更细，HUAKAI 应吸收其最小流量门槛、连接错误不降级为 cooldown、retry-before-fallback、可解释 retry source，并在 SaaS 场景中用共享状态、Usage Record、Audit Event 和 Admin Ops 手动恢复能力强化它。
