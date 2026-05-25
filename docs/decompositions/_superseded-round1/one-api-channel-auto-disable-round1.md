# one-api - Channel Auto-Disable on Permanent-Error Pattern

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | one-api, MIT, E-LIC-004 |
| Feature in HUAKAI matrix | F-CH-002 |
| Evidence ledger row | E-OAI-DEEP-006 |
| Specifier session | Codex specifier-lane, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | TBD |
| Reviewer date | TBD |
| Source files read | https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/controller/relay.go<br>https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/monitor/manage.go<br>https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/monitor/metric.go<br>https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/monitor/channel.go<br>https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/controller/channel-test.go<br>https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/common/config/config.go<br>https://github.com/songquanpeng/one-api/blob/8df4a2670b98266bd287c698243fff327d9748cf/model/channel.go |

## 1. WHY

The upstream design is trying to protect live traffic from a Channel that has stopped being useful for reasons an immediate retry will not fix. The operator pain point is practical: a Provider Account can lose authorization, run out of upstream balance, lose permission to call a Model, become restricted by Provider policy, or start returning persistent account-level denial messages. If such a Channel stays eligible, each User request wastes one attempt, adds latency, consumes retry budget, and may hide the real problem behind repeated fallback.

Verified behavior does not exactly match a pure "N consecutive permanent errors of class X" rule. Source review shows three disable routes: recognized permanent-ish relay errors disable immediately when the operator enables automatic disable; rolling success-rate metrics can disable after enough recent failures and a configured rate threshold; scheduled or manual health tests can disable for slow response, permanent-ish Provider errors, or exhausted upstream balance. This matters for HUAKAI because F-CH-002 should not oversimplify the reference into one threshold counter.

## 2. WHAT (algorithm in HUAKAI vocabulary)

For normal gateway traffic, a request is routed to a Channel. If the upstream call succeeds, the Channel receives a success signal for health metrics. If the upstream call fails, the gateway first decides whether the response belongs to a permanent-error class. The permanent classes observed are authorization failure, exhausted upstream quota or balance, Provider Account deactivation or restriction, missing permission, invalid or expired upstream credential, and text patterns indicating policy termination, disabled organization, low credit, or denied permission.

When automatic disable is enabled and the failure matches that permanent class, the Channel is marked auto-disabled. That status removes it from the set of Channels eligible for later Route selection. The system records an operator-visible log and attempts an operator notification through configured notification channels.

When the failure is not classified as permanent, it is emitted as a health-metric failure rather than immediately disabling the Channel. If metric collection is enabled, the gateway keeps a per-Channel rolling window of recent success/failure booleans. The window must reach its configured size before a low success rate can trigger a disable. Once the success rate falls below the configured threshold, the Channel is auto-disabled and the window for that Channel is cleared.

The scheduled health-test path is separate. A background or manual run probes Channels with a small synthetic request. Only one all-Channel test run is allowed at a time. If a currently enabled Channel exceeds the configured response-time threshold, it can be auto-disabled when automatic disable is enabled; otherwise an operator notification is sent without status mutation. If the test returns a permanent Provider error, the Channel is auto-disabled. If a Channel is currently disabled and automatic enable is enabled, a clean test response can mark it enabled again.

Retry interaction is intentionally asymmetric. A failed request can retry through another eligible Channel, depending on gateway retry settings and response status. The failed Channel is processed for health or disable in the background. The retry loop is not waiting for the disable to finish; therefore the disable primarily affects future selection, not necessarily the same retry loop. Forced-specific Channel routing bypasses normal retry behavior, so auto-disable can still be recorded while fallback may not happen for that request.

## 3. INPUTS

The feature consumes the upstream HTTP status, the normalized Provider error type and code, the Provider error message, the selected Channel id/name/status, the User id for logging, gateway retry configuration, automatic disable and automatic enable toggles, rolling metric enablement, metric queue size, metric success-rate threshold, scheduled test frequency, per-test response-time threshold, request interval between health probes, configured root operator contact, and optional message-pusher settings.

It mutates Channel lifecycle state from enabled to auto-disabled, and in the health-test path may mutate an auto-disabled or manually disabled Channel back to enabled if automatic enable is active and the test succeeds. It also updates health-test timing metadata, stores health-test logs, emits system logs, and sends operator notifications.

## 4. FAILURE MODES HANDLED

- Provider Account credential revoked or invalid: detected through authorization status, normalized error class/code, or credential-related message; response is auto-disable plus operator log/notification.
- Provider Account quota or balance exhausted: detected through normalized quota class, balance/credit message, or balance polling; response is auto-disable.
- Provider-side permission or policy restriction: detected through permission class, forbidden class, or restriction/termination messages; response is auto-disable.
- Persistent low success rate: detected through a full rolling metric window below threshold; response is auto-disable and reset of the local metric window.
- Slow Channel during probe: detected by scheduled/manual test exceeding the configured response-time threshold; response is auto-disable if enabled, otherwise notify only.
- Disabled Channel recovery: detected by a clean scheduled/manual test when automatic enable is configured; response is status restoration and operator notification.

## 5. INTERFACES TO HUAKAI

HUAKAI should connect this behavior to Route selection, Channel lifecycle state, Provider Account health, operator notification, Audit Event creation, Usage Record attribution, and gateway retry policy. The local state model should keep `enabled`, `paused`, `degraded`, and an explicit `under-investigation` or auto-disabled reason code separate from manual operator disable. Route selection must exclude auto-disabled Channels immediately after a committed state transition. Retry logic should treat a just-failed Channel as excluded for the current request regardless of whether the background disable write has completed.

## 6. RISKS

The upstream behavior can over-disable because keyword matching on Provider messages is broad and language-dependent. It can under-disable because the rolling metric window is in-process and may not represent all instances in a horizontal deployment. Immediate auto-enable after a single clean probe can cause flapping. Manual disable and auto-disable are not clearly separated in recovery semantics if automatic enable is allowed for any disabled Channel. Background handling creates a race where the same request path may continue retrying before the failed Channel is globally removed. The design also lacks a strong Audit Event trail for status changes, which HUAKAI requires for operator accountability.

## 7. SAFE ADAPTATION FOR HUAKAI

- KEEP: Disable Channels on verified permanent Provider Account failures so live traffic routes around unusable inventory.
- KEEP: Maintain a rolling health signal for non-permanent failures so transient network errors do not immediately remove capacity.
- IMPROVE: Replace broad message substring matching with a versioned error taxonomy: credential, quota-exhausted, permission, policy, balance, Provider outage, network, malformed response, and unknown.
- IMPROVE: Use distributed counters or database-backed state for multi-node deployments, with a minimum traffic floor before success-rate disable.
- IMPROVE: Require an Audit Event and operator alert for every automatic lifecycle transition.
- IMPROVE: Use operator-confirmed resume by default, or an explicit cooldown-based resume policy with flap dampening and reason-specific rules.
- IMPROVE: Current-request retry exclusion should be synchronous and independent from the global auto-disable write.
- AVOID: Do not auto-enable manually disabled Channels after one successful probe.
- AVOID: Do not classify Cloudflare HTML, parser failures, or network timeouts as credential death without a separate evidence class.

## 8. EVIDENCE LEDGER ROWS

- E-LIC-004: one-api is MIT and is the safe-anchor reference for source verification.
- E-OAI-DEEP-006: source-verified Channel auto-disable on permanent-error pattern.
- E-OAI-DEEP-002: retry triggers on rate-limit, 5xx, and most non-success responses.
- E-OAI-DEEP-003: failed Channel exclusion is per request during retry.
- E-OAI-DEEP-010: eligible Channel selection depends on User Group and Model.
- E-OAI-DEEP-011: forced-specific Channel path bypasses normal selection and most retry behavior.
- E-OAI-DEEP-012: retry loop rewinds the request body and selects another eligible Channel without meaningful backoff.
- E-OAI-DEEP-016: manual/scheduled Channel tests can disable or re-enable Channels.
- E-OAI-009: README-level evidence for periodic availability checks and auto-disable below success threshold.
- E-OAI-013: README-level evidence for operator dashboard metrics and configurable auto-disable threshold.

## 9. OPEN QUESTIONS

- Should HUAKAI model permanent-error threshold as immediate disable for severe classes and N-of-M for softer classes, rather than one global count?
- Should quota-exhausted Provider Accounts become Channel `paused`, Provider Account `quota-exhausted`, or both?
- What minimum sample count and time window should be required before success-rate disable in SaaS Edition?
- Should auto-enable be allowed only after cooldown plus multiple clean probes, or always require operator confirmation for production?
- How should HUAKAI present a Channel disabled because one Provider Account failed when the Channel contains a Provider Account pool?
- Should retry budget be reduced when the first failure is permanent, or should the gateway immediately choose another Channel without charging a retry slot?

## Owner Summary

本文件拆解了 one-api 在 F-CH-002 下的 Channel 自动禁用行为：它不是单一的「N 次连续永久错误」规则，而是由永久错误即时禁用、低成功率滚动窗口禁用、健康测试慢响应/余额耗尽禁用，以及可选自动恢复共同组成；与已有 sub2api 拆解的关键差异是 sub2api 更偏向监控调度与人工恢复语义，而 one-api 更强调请求路径中的错误分类和可配置自动开关。HUAKAI 应吸收“永久错误绕路”和“非永久错误滚动观测”，但要强化为可审计的错误分类、分布式计数、当前请求同步排除、默认 operator-confirm-resume，并避免 broad message matching 与单次探测自动恢复造成误判或抖动。
