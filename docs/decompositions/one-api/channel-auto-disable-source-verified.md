# one-api Channel Auto-Disable Source-Verified Decomposition

Metadata:

| Field | Value |
| --- | --- |
| Project | one-api |
| Feature | Channel auto-disable: permanent-error gate, rolling success-rate metric window, scheduled-test path, retry interaction |
| HUAKAI matrix row | F-CH-002 (L2) |
| Lane / round | Codex specifier-lane R3 |
| Date | 2026-04-29 |
| License posture | one-api is MIT anchor evidence. This file describes observed behavior only and avoids upstream code names, file paths, schema copying, and code-shaped implementation. |
| Truth-discipline | Observed regions: 16 / Inferences: 18 / Open questions: 9 |
| Companion critic read first | `.omc/artifacts/decomp-critic/C1-oneapi-channel-auto-disable.md` |
| Do-not-read guard | Did not read `docs/decompositions/one-api/channel-auto-disable-claude-deep.md`. |

## §1 WHY

Channel health is not just a background probe problem. The observed upstream behavior combines live relay failures, scheduled Channel tests, response-time checks, in-memory success-rate windows, status mutation, ability availability, cache refresh, retry, and best-effort notification into one operational outcome: a Channel stops being eligible for routing after enough evidence says it should not receive traffic [region-1] [region-2] [region-3] [region-4] [region-5] [region-6] [region-9].

The pressure behind the design is availability under Provider failure. A failed Provider Account should not keep receiving user traffic indefinitely; a Channel with permanent credential/account failure should be removed faster than a Channel with transient upstream instability; and a Channel that recovers should be returned only after safe evidence [region-1] [region-6]. At the same time, observed upstream behavior shows that disable decisions are not globally serialized with routing: relay error handling launches status mutation asynchronously, retry continues after the first failed attempt, and memory cache may continue selecting stale Channel entries until refresh [region-5] [region-8] [region-9]. That creates a strong HUAKAI requirement: Channel health must be modeled as a state machine with durable evidence and routing consistency, not only as a notification side effect.

The critic's central correction is confirmed: there are at least two distinct disable gates. One gate classifies certain relay/test errors as permanent-looking and can mark a Channel disabled when an operator option is enabled [region-1] [region-5] [region-6]. A separate metric gate consumes success/failure events into a rolling window and disables when the window is full and the success rate is below a configured threshold [region-3] [region-4]. Scheduled tests add a third observed path: response time above a configured threshold can disable an otherwise enabled Channel, while a clean test can re-enable a disabled Channel when the separate automatic-enable option is enabled [region-6]. These are related but not interchangeable.

## §2 WHAT

S-1. Immediate permanent-error disable is opt-in, not always on. The classifier returns no disable decision when the operator-controlled automatic-disable option is off, even if the relay/test error looks permanent [region-1] [region-4].

S-2. Immediate permanent-error disable requires a normalized upstream error object; a missing normalized error does not trigger immediate disable [region-1].

S-3. Unauthorized response status is treated as an immediate disable signal when automatic disable is enabled [region-1].

S-4. A narrow set of OpenAI-shaped error categories and codes is treated as immediate-disable evidence when automatic disable is enabled [region-1].

S-5. Error message text is also part of the immediate-disable classifier; the observed message matching covers account termination, policy violation, low credit/balance, organization disabled/restricted, permission denial, invalid Provider API credential, expired Provider API credential, and one mojibake-looking text fragment [region-1].

S-6. The immediate classifier does not show a broad provider-normalized taxonomy. Observed source handles a narrow status/type/code/message set, so quota, billing, policy, region, proxy, malformed auth, and provider-specific errors that do not match those patterns fall through to non-immediate handling [region-1]. This is an observed negative claim limited to the read classifier region.

S-7. Relay success emits a success metric event for the originally selected Channel and returns without disable or retry work [region-5] [region-3].

S-8. Relay failure starts a background error-processing path before retry selection continues [region-5].

S-9. The live relay error-processing path either disables the Channel if the permanent-error classifier says yes, or emits a failure metric event if the classifier says no [region-5] [region-1] [region-3].

S-10. Retry is separate from disable. A failed request may still retry through another Channel after the failed Channel has been handed to background error processing [region-5].

S-11. Retry is skipped when the request was pinned to a specific Channel by the caller/operator path [region-5].

S-12. Retry is allowed for rate-limit status, server-error status, and most non-2xx/non-bad-request statuses; retry is not allowed for bad-request status or 2xx statuses [region-5].

S-13. During retry, the next Channel is selected from the same User Group and original Model context, not from a newly parsed request model [region-5] [region-8].

S-14. Retry avoids immediately reusing the last failed Channel by comparing the newly selected Channel with the last failed Channel and skipping if they match [region-5].

S-15. Retry selection can request a lower-priority Channel set after the first retry attempt; this is done by passing a retry-aware selection flag into the Channel selector [region-5] [region-8].

S-16. A successful retry returns without emitting a success metric event in the outer relay success branch, because that branch only runs before the retry loop [region-5]. This supports the critic's concern that retry recovery may not credit the fallback Channel in the same relay controller path.

S-17. A failed retry repeats the same background error-processing path for the newly failed Channel [region-5].

S-18. The metric gate is separately controlled by an environment flag; when disabled, success/failure emission returns without recording anything [region-3] [region-14].

S-19. Metric history is an in-process map keyed by Channel identity, with a sequence of boolean success/failure observations per Channel [region-3].

S-20. Metric success/failure events are sent through bounded in-process queues, and each emit call starts a goroutine that sends into the relevant queue [region-3] [region-14].

S-21. The metric window only disables after the stored observation count reaches the configured queue size; before the window is full, failures only update the success-rate calculation [region-3] [region-14].

S-22. When the metric window is full and success rate falls below the configured threshold, the window is cleared and the Channel is disabled through the metric-disable path [region-3] [region-2].

S-23. Metric-disable status mutation uses the same auto-disabled Channel state as permanent-error disable, but its notification text is based on success-rate threshold evidence instead of a permanent-error reason [region-2].

S-24. Scheduled Channel testing builds a synthetic chat-style request, sends it through the selected Channel's adaptor, parses the response, requires usage data, records a test log asynchronously, and records response time [region-6].

S-25. Manual single-Channel testing reports success/failure and elapsed time to the caller, updates response time asynchronously, and does not by itself perform the automatic disable/enable decisions shown in the all-Channel test loop [region-6].

S-26. All-Channel testing has a process-local lock that prevents concurrent all-Channel test loops in the same process [region-6].

S-27. All-Channel testing obtains Channels by scope; the observed Channel inventory query can include all Channels, disabled Channels, or a paged default set depending on scope [region-10].

S-28. In all-Channel testing, response time above the configured threshold can disable an enabled Channel when automatic disable is enabled [region-6] [region-4].

S-29. If the response-time threshold is effectively zero, the source substitutes an unreachable sentinel value, so response-time disable is effectively suppressed in that configuration [region-6].

S-30. In all-Channel testing, an enabled Channel with a permanent-looking provider error can be disabled through the same immediate classifier used by live relay failures [region-6] [region-1].

S-31. In all-Channel testing, a disabled Channel can be re-enabled after a clean test when automatic enable is enabled, the transport/test error is nil, and the normalized upstream error is nil [region-6] [region-1].

S-32. Scheduled all-Channel testing is started only when a configured test frequency is present at process start; it sleeps for that interval in minutes between all-Channel test runs [region-7].

S-33. Channel status mutation also updates model/group ability availability for that Channel, then writes the Channel status [region-9] [region-8].

S-34. Routing eligibility uses ability records when memory cache is off, and uses a process-local Channel cache when memory cache is on [region-8] [region-9].

S-35. The process-local Channel cache is populated only with enabled Channels and refreshes on a configured interval when memory cache is enabled [region-9] [region-7].

S-36. A disable status write does not directly refresh the process-local Channel cache in the observed status mutation region; stale eligibility can therefore persist until cache refresh when memory cache is enabled [region-9] [region-7]. This claim is limited to the observed regions and does not assert behavior of code not read.

S-37. Direct Channel pinning verifies the Channel exists and is enabled before forwarding; if the pinned Channel is disabled, the request is rejected rather than routed elsewhere [region-8].

S-38. Direct Channel pinning still uses the selected Channel's credentials and context setup, so a pinned request that reaches relay failure can still invoke the shared Channel disable path even though retry is suppressed [region-8] [region-5].

S-39. Disable notification is best effort. Status mutation happens before notification; message-pusher or email failure is logged but does not roll back the disabled state [region-2] [region-13].

S-40. Metric-disable notification is also best effort and does not gate status mutation [region-2] [region-13].

S-41. Balance-update scanning can disable an enabled Channel when an observed balance check returns a non-positive balance for supported Provider types in that scan path [region-12]. This is adjacent to, not the same as, the two requested disable gates.

S-42. Startup behavior advertises metric mode in logs when metric disable is enabled, starts periodic Channel tests only under the scheduled-test frequency option, starts cache sync only when memory cache is enabled, and initializes Channel cache from database on memory-cache startup [region-7].

## §2-bis Lifecycle Traces

Trace A: live permanent-error disable with retry.

1. A User request has already been assigned to a Channel by User Group and Model eligibility [region-8].
2. The Provider call returns a normalized upstream error and non-success status [region-11].
3. Relay starts background Channel error processing for the failed Channel [region-5].
4. The error classifier sees automatic disable enabled and matches unauthorized status, error taxonomy, code, or message text [region-1].
5. The Channel is marked auto-disabled; ability availability is changed to unavailable; a system log and best-effort notification are emitted [region-2] [region-9].
6. The original request may still retry through another eligible Channel unless retry rules or direct Channel pinning suppress retry [region-5].

Trace B: live transient/error-rate path.

1. A User request fails with an error not classified as immediate-disable evidence [region-1] [region-5].
2. The background error processor emits a failure metric event for the failed Channel [region-5] [region-3].
3. The metric event is sent through an in-process queue into an in-process observation window [region-3].
4. If the window is not full, no disable occurs yet [region-3].
5. If the window is full and success rate is below threshold, the metric path clears the Channel's window and marks the Channel auto-disabled with metric-specific notification [region-3] [region-2].

Trace C: retry recovery with metric asymmetry.

1. Initial Channel selection chooses a Channel from the User Group and Model eligibility set [region-8].
2. The first attempt fails and emits either immediate disable or a metric failure event through background processing [region-5].
3. Retry selection chooses another Channel from the original User Group and Model context and avoids the last failed Channel if selected again [region-5].
4. If the retry succeeds, the relay returns from inside the retry loop [region-5].
5. Observed source does not show a success metric emission for that retry success in the same outer success branch, so the first failed Channel can receive a failure observation while the successful fallback Channel is not clearly credited by that controller path [region-5]. This is observed from the relay control flow only; any success accounting inside lower-level adaptors was not observed.

Trace D: scheduled all-Channel test disable and re-enable.

1. At startup, a configured scheduled-test frequency starts a background loop that runs all-Channel tests after each interval [region-7].
2. The test loop fetches Channels by scope and prevents another all-Channel loop in the same process [region-6].
3. Each Channel receives a synthetic chat-style test request, and the source records response time and test log evidence [region-6].
4. If an enabled Channel exceeds response-time threshold and automatic disable is enabled, it is disabled [region-6].
5. If an enabled Channel returns a permanent-looking normalized provider error, it is disabled through the classifier [region-6] [region-1].
6. If a disabled Channel has no transport/test error and no normalized provider error, and automatic enable is enabled, it is enabled again [region-6] [region-1].

Trace E: stale-cache routing after disable.

1. When memory cache is enabled, startup loads enabled Channels into an in-process routing cache and starts periodic cache sync [region-7] [region-9].
2. Disable status mutation changes ability availability and Channel status in the database, but the observed mutation path does not call the cache refresh routine [region-9].
3. Until periodic sync runs, a process-local cache may still hold the previously enabled Channel [region-9]. This is an observed risk from the read source regions, not a claim about every deployment configuration.
4. With memory cache disabled, routing uses live ability lookup rather than the in-process Channel cache [region-8].

## §3 INPUTS

Observed upstream inputs and state used by the feature:

| Input / state | Observed use | Source |
| --- | --- | --- |
| Operator automatic-disable option | Gates immediate permanent-error disable and response-time disable in scheduled tests. | [region-1] [region-4] [region-6] |
| Operator automatic-enable option | Gates re-enable after scheduled test success. | [region-1] [region-4] [region-6] |
| Metric enable flag | Gates collection of success/failure observations. | [region-3] [region-14] |
| Metric window size | Minimum sample count before metric disable can fire. | [region-3] [region-14] |
| Metric success-rate threshold | Success-rate cutoff below which metric disable fires. | [region-3] [region-14] |
| Metric event queue sizes | Bounds the in-process success/failure event queues. | [region-3] [region-14] |
| Retry count | Controls live relay retry loop length. | [region-5] [region-4] |
| Scheduled-test frequency | Starts periodic all-Channel testing when present. | [region-7] |
| Channel test prompt | Synthetic request payload for Channel tests. | [region-6] [region-15] |
| Response-time threshold | Scheduled all-Channel testing uses it to disable slow enabled Channels. | [region-6] [region-4] |
| Inter-test request interval | Sleep between Channel test or balance update operations. | [region-6] [region-12] [region-15] |
| Channel status | Determines routing eligibility, direct pin rejection, and enabled/disabled test branch. | [region-8] [region-9] [region-10] |
| Ability availability | Model/User Group routing eligibility mirror changed on status write. | [region-8] [region-9] |
| Channel cache | Optional process-local routing source populated from enabled Channels. | [region-9] |
| User Group and Model | Select eligible Channel for normal routing and retry. | [region-8] [region-5] |
| Direct Channel pin | Bypasses normal selection, rejects disabled pinned Channel, and suppresses retry. | [region-8] [region-5] |
| Normalized upstream error | Feeds permanent-error classification. | [region-1] [region-11] |
| Provider response status | Feeds retry and disable decisions. | [region-1] [region-5] [region-11] |
| Root notification target and message-pusher config | Used for best-effort disable/enable notification. | [region-2] [region-13] |

## §4 FAILURE MODES

Only observed failure modes are listed.

| Failure mode | Observed behavior | Source |
| --- | --- | --- |
| Automatic disable option off | Permanent-looking relay/test errors do not immediately disable; live relay failures fall to metric emission when metric mode is enabled. | [region-1] [region-5] [region-3] |
| Metric mode off | Success/failure events are ignored; rolling success-rate disable cannot fire. | [region-3] |
| Metric event queue saturation | Emit launches a goroutine that sends to a bounded queue; source does not show a non-blocking send or drop policy, so goroutines can block if consumers lag. | [region-3] [region-14] |
| In-process metric volatility | Rolling health history is stored in an in-process map; source does not show persistence or cross-node merge for this window. | [region-3] |
| Retry success not credited in observed relay branch | A successful retry returns from inside the retry loop, outside the initial success metric branch. | [region-5] |
| Direct Channel pin failure | Pinned request is not retried, but once it reaches relay failure the shared Channel error-processing path can still disable or metric-fail that Channel. | [region-8] [region-5] |
| Stale memory cache | Status write changes database state and ability availability, while observed cache refresh happens on periodic sync; stale Channel objects can remain process-local until sync. | [region-9] [region-7] |
| Scheduled test false recovery | A disabled Channel can be re-enabled by one clean scheduled test when automatic enable is on; observed source does not require cooldown, repeated probes, model-specific coverage, or operator approval. | [region-6] [region-1] |
| Response-time sentinel | If response-time threshold calculates to zero, source substitutes a very high sentinel, effectively preventing response-time disable. | [region-6] |
| Notification failure | Disable/enable state changes are not rolled back when message-pusher or email delivery fails; errors are logged. | [region-2] [region-13] |
| Non-JSON upstream error body | Error handler initializes a generic upstream error and only replaces it if parsing succeeds; malformed/non-JSON bodies remain generic status-code errors. | [region-11] |
| All-Channel test overlap in one process | A local lock rejects concurrent all-Channel test loops in the same process; this does not prove cross-process exclusion. | [region-6] |
| Balance scan zero balance | In the balance update path, a non-positive balance can disable supported enabled Channels. | [region-12] |

## §5 INTERFACES TO HUAKAI

Personal Edition:

- Keep the user-visible capability: HUAKAI Personal Edition should periodically probe Channel health, stop routing to unhealthy Channels, and surface the reason in local Admin/Ops UI.
- Use a simple but durable local PostgreSQL-backed Channel health table, because HUAKAI DR-006 already standardizes PostgreSQL. Even for Personal Edition, in-memory-only windows would make restarts erase health evidence.
- Default policy can be conservative: mark ambiguous failures as `degraded` or `under-investigation`, reserve full disable for confirmed credential/account errors or repeated probe failure.
- Direct Channel pinning, if exposed, must be local-admin scoped and must not globally punish a shared Channel without independent confirmation.
- Re-enable can be manual-first in Personal Edition: one clean test can move a Channel from disabled to candidate/under-investigation, but final resume should be explicit unless Owner chooses fully automatic recovery.

SaaS Edition:

- Tenant blast radius must be explicit. A User or tenant-triggered request failure should not globally disable a shared Channel unless independent evidence confirms a Provider Account problem that affects all tenants.
- Channel health observations need tenant_id, Channel, Provider Account, Model, Route, request id, failure class, retry attempt, and source (`relay`, `scheduled-test`, `balance-check`, `operator`) so Admin can distinguish tenant-specific quota/auth failures from shared upstream failures.
- Routing must fail closed against disabled/quarantined Channels. Cache invalidation or short TTL must be part of the contract, not an implementation afterthought.
- Retry accounting must be attempt-aware. Failed first attempts and successful fallbacks must both be represented so success-rate windows do not overstate failure or hide recovery.
- Recovery should require cooldown, Model/Provider Account-specific probes, and optional operator approval for shared Channels.
- Alert delivery must be separate from durable Audit Event and incident state. Email or message-pusher failure cannot be the only evidence path.

## §6 RISKS

R-1. (inference, not observed) HUAKAI DR-001 multi-tenancy makes upstream-style global disable too broad. Observed source mutates a shared Channel status after relay/test failure [region-2] [region-5] [region-6]. In HUAKAI SaaS Edition, the same failure may be tenant-scoped, User Group-scoped, Provider Account-scoped, Model-scoped, or global. Risk: one tenant's bad pinned request or quota state disables service for other tenants.

R-2. (inference, not observed) HUAKAI DR-002 edition split needs different defaults. Observed source exposes automatic disable/enable as broad options [region-4]. Personal Edition can tolerate manual-first recovery; SaaS Edition needs stricter audit, blast-radius control, and fail-closed routing.

R-3. (inference, not observed) HUAKAI DR-006 PostgreSQL makes process-memory metric windows unnecessary and risky. Observed source stores rolling health in process memory [region-3]. PostgreSQL-backed health evidence gives restart survival, cross-node consistency, and auditability.

R-4. (inference, not observed) The immediate classifier is too provider-specific for HUAKAI's Provider-neutral Gateway. Observed classifier uses a narrow status/type/code/message set [region-1]. HUAKAI needs normalized categories such as auth, credential-expired, account-disabled, quota-exhausted, billing-blocked, policy-blocked, safety-blocked, regional-blocked, transient-upstream, timeout, malformed-response, and proxy/transport.

R-5. (inference, not observed) Retry metric asymmetry can distort Channel health. Observed relay flow emits success only before retry and failure during background error processing [region-5]. HUAKAI should record per-attempt outcomes and also request-level outcome.

R-6. (inference, not observed) Async disable can race routing. Observed relay launches error processing in the background and continues retry [region-5]. HUAKAI should make health transition durable and quickly visible to route selection.

R-7. (inference, not observed) Scheduled-test recovery is too weak for shared SaaS Channels. Observed source can re-enable after one clean test when automatic enable is on [region-6]. HUAKAI should require enough successful probes to cover affected Model and Provider Account conditions.

R-8. (inference, not observed) Best-effort notification is not enough for regulated operations. Observed source logs notification failures but keeps status changes [region-2] [region-13]. HUAKAI needs durable Audit Event and incident rows independent of delivery success.

R-9. (inference, not observed) Stale cache semantics can violate fail-closed routing. Observed memory cache refreshes periodically and status mutation does not directly refresh the cache in the read region [region-9] [region-7]. HUAKAI should make disabled/quarantined state transactionally visible or use cache invalidation with strict TTL.

R-10. (inference, not observed) Response-time disable alone can misclassify Provider slowness. Observed scheduled tests can disable on slow response when automatic disable is on [region-6]. HUAKAI should separate latency degradation, hard disable, and Provider incident states.

R-11. (inference, not observed) Non-JSON upstream errors can hide useful classification. Observed error normalization falls back to generic status errors when body parsing fails [region-11]. HUAKAI should classify malformed/non-JSON response as its own category, not only generic upstream error.

R-12. (inference, not observed) Balance scans that disable shared Channels need Provider Account scoping. Observed balance scan can disable a Channel on non-positive balance [region-12]. HUAKAI must attach this to Provider Account lifecycle and not mix it with Channel-only health.

## §7 SAFE ADAPTATION

1. Implement `Implemented Better` for F-CH-002: Channel health probe + live failure classification + rolling success-rate policy + retry-aware accounting + durable incident/audit workflow.
2. Use HUAKAI terminology: Provider, Provider Account, Channel, Route, User Group, API Key, Usage Record, Audit Event.
3. Represent Channel health as explicit states: `enabled`, `degraded`, `under-investigation`, `quarantined`, `disabled`, plus reason class and source. Avoid a single boolean-like auto-disabled state as the only operational truth.
4. Store every health observation in PostgreSQL with request id, Route, Channel, Provider Account, Model, User Group, tenant, source, status class, latency, retry attempt, and raw-safe reason summary.
5. Store state transitions separately from observations. Each transition should include previous state, next state, reason code, policy version, actor/source, evidence ids, cooldown deadline, and notification state.
6. Replace narrow message matching with provider-normalized classification. Message text may be supporting evidence, not the policy boundary.
7. Make retry accounting explicit: record attempt-level failure for the failed Channel and attempt-level success for the fallback Channel; separately record final request outcome.
8. Make routing fail closed: disabled/quarantined Channels are not eligible even if a cache is stale. Cache entries must carry health version or expire quickly.
9. Treat direct Channel pinning as an override with guardrails. Pinned failures can mark a tenant-scoped incident immediately, but shared Channel disable requires independent confirmation unless the failure is cryptographically/account-level obvious.
10. Separate scheduled test outcomes by Model and Provider Account where possible. One generic probe must not re-enable all model/provider-account combinations.
11. Make re-enable a policy: Personal Edition may default to manual confirmation; SaaS Edition should require cooldown plus repeated clean probes and operator approval for shared Channels.
12. Separate alert delivery from audit. Email/message push can fail; the Audit Event and incident row must still exist.
13. Model response-time threshold as degradation first. Disable only after latency evidence combines with failure or policy thresholds.
14. Preserve balance/quota disable as Provider Account lifecycle evidence, not only Channel health.

## §8 EVIDENCE LEDGER ROWS

Suggested clean-room evidence rows to add or update in `docs/07_REFERENCE_EVIDENCE_LEDGER.md`:

| Evidence ID | Reference | Source type | Observed behavior | HUAKAI implication | Clean-room note |
| --- | --- | --- | --- | --- | --- |
| E-OAI-CH-002-R3-001 | one-api | Source code deep read | Live relay failure can auto-disable a Channel when an operator-enabled permanent-error classifier matches status, normalized error category/code, or selected message text. | Keep capability, replace classifier with provider-normalized taxonomy and durable transition log. | Behavior only; no source identifiers copied. |
| E-OAI-CH-002-R3-002 | one-api | Source code deep read | Non-immediate relay failures emit failure observations into an optional in-process rolling success-rate window that can auto-disable below threshold. | Keep rolling health concept, implement in PostgreSQL with retry-aware attempt accounting. | Behavior only. |
| E-OAI-CH-002-R3-003 | one-api | Source code deep read | Relay retry continues separately from background Channel error processing; pinned Channel requests are not retried. | HUAKAI must specify retry/disable ordering and tenant blast radius for pinned requests. | Behavior only. |
| E-OAI-CH-002-R3-004 | one-api | Source code deep read | Scheduled all-Channel tests can disable slow or permanent-error Channels and can re-enable disabled Channels after a clean test when automatic enable is on. | HUAKAI should require cooldown, repeated probes, Model/Provider Account scope, and optional operator confirmation. | Behavior only. |
| E-OAI-CH-002-R3-005 | one-api | Source code deep read | Disable changes Channel status and ability availability, while memory-cache routing refresh is periodic. | HUAKAI must enforce fail-closed routing with cache invalidation/version checks. | Behavior only. |
| E-OAI-CH-002-R3-006 | one-api | Source code deep read | Disable/enable notification is best effort; state mutation does not depend on email/message delivery success. | HUAKAI needs durable Audit Event and incident state independent of alert delivery. | Behavior only. |

Suggested parity matrix update for F-CH-002:

| Field | Proposed value |
| --- | --- |
| Disposition | Implemented Better |
| Capability | Health probe + live error classifier + rolling success-rate policy + scheduled test + retry-aware accounting + fail-closed routing |
| Risk note | Prevent silent/global disable by requiring tenant/provider-account blast-radius logic, durable audit, provider-normalized classification, and recovery policy. |
| Acceptance-test anchor | AT-CH-002 should cover immediate disable, metric disable, scheduled disable, scheduled recovery, retry accounting, pinned Channel blast radius, stale cache fail-closed behavior, and notification failure. |

## §9 OPEN QUESTIONS

OQ-1. Does any lower-level adaptor emit metric success during retry success? I observed the relay controller path and did not observe fallback success emission there [region-5].

OQ-2. Are there deployments where memory cache is disabled by default often enough that stale-cache risk is rare? Source shows both modes but not operator prevalence [region-7] [region-9].

OQ-3. Does any external process or admin action refresh Channel cache immediately after status mutation? The observed status mutation path does not do so [region-9].

OQ-4. Does all-Channel testing run on every node in a multi-node deployment, or only master nodes in common deployments? Startup code starts it based on local environment, but I did not observe cluster coordination [region-7].

OQ-5. Are Channel test logs durable enough for operator audit? I observed async test log recording, but did not read the log storage and retention implementation [region-6].

OQ-6. Does the UI clearly distinguish manually disabled vs auto-disabled vs metric-disabled vs response-time-disabled? I did not read UI source for this R3 artifact.

OQ-7. Does metric disable check current Channel status before writing auto-disabled? Observed metric-disable writes the status directly; I did not observe a compare-and-set or current-status guard [region-2].

OQ-8. How does balance-update disable interact with automatic-disable option? Observed balance-update disable call does not show the same option gate in the read region [region-12], but broader scheduling/admin gating was not fully read.

OQ-9. Does the malformed mojibake text fragment in the classifier correspond to a specific provider/user-visible phrase? I observed the fragment but cannot honestly identify it from source reading [region-1].

## §10 SOURCE COVERAGE PROOF

The following regions were read directly from the local MIT one-api checkout. Region labels intentionally avoid upstream file paths and function names to preserve clean-room separation.

| Region | Source region read | Contribution |
| --- | --- | --- |
| region-1 | Permanent-error and auto-enable classifier source block. | Proved automatic-disable gate, nil-error behavior, unauthorized status handling, narrow error taxonomy, message matching, and clean-test enable condition. |
| region-2 | Channel status transition and notification source block. | Proved disable/metric-disable/enable status writes, log side effects, and best-effort notification after state mutation. |
| region-3 | Rolling metric consumer/emitter source block. | Proved in-process map, bounded event queues, success/failure event emission, full-window threshold behavior, window clear on disable, and goroutine send behavior. |
| region-4 | Runtime option/default source blocks for automatic disable/enable, retry count, response threshold, and option loading. | Proved default automatic-disable/enable off, retry default, response-time threshold option, and admin/runtime option mapping. |
| region-5 | Relay entry, retry, and Channel error-processing source block. | Proved success metric on initial success, background failure processing, retry rules, pinned no-retry rule, retry Channel selection context, failed-Channel skip, retry success return, and race note. |
| region-6 | Manual and all-Channel test source block. | Proved synthetic test request, response parsing, usage requirement, test log, response-time update, all-test lock, response-time disable, permanent-error disable in tests, auto-enable after clean test, and scheduled test loop body. |
| region-7 | Startup orchestration source block. | Proved cache startup/sync, scheduled-test activation by frequency option, metric-mode startup log, and process-local startup behavior. |
| region-8 | Request distribution and selected-Channel context source block. | Proved direct Channel pin behavior, disabled pin rejection, normal User Group/Model selection, credential/context setup, and original Model preservation for retry. |
| region-9 | Channel cache source block. | Proved memory cache contains enabled Channels, periodic sync refresh, priority-aware cached selection, and no direct cache refresh in the observed status mutation path. |
| region-10 | Channel model/status source block. | Proved Channel statuses, Channel inventory scope behavior, response-time/balance fields, and status write updating ability availability plus Channel status. |
| region-11 | Upstream error normalization source block. | Proved generic fallback error construction, JSON body parsing, OpenAI-shaped override when present, and generic message fallback for malformed/non-JSON bodies. |
| region-12 | Balance update scan source block. | Proved enabled supported Channels can be disabled on non-positive balance in that scan path and that scan sleeps between requests. |
| region-13 | Message/email notification source blocks. | Proved message-pusher and email send can fail independently, notification helpers return/log errors, and no rollback path was observed. |
| region-14 | Environment metric option declarations and README environment descriptions. | Proved metric enable flag, queue size, threshold defaults, bounded queue sizes, and public documentation of metric disable concept. |
| region-15 | Test prompt and polling interval option declarations plus README environment descriptions. | Proved configurable test prompt and inter-request test interval. |
| region-16 | README multi-node/cache configuration descriptions. | Proved public docs describe master/slave node type and periodic database configuration sync; source region 9 supplies the more specific cache-health implication. |

Traceability check:

- §2 S-1..S-42 each has at least one `[region-N]` citation.
- §2-bis traces cite only observed regions.
- §4 failure modes cite only observed regions.
- §6 risks are explicitly marked as inference and tied to observed regions plus HUAKAI DR constraints.

## §11 ROUND-2 CRITIC FINDINGS

| Critic ID | Disposition | R3 handling |
| --- | --- | --- |
| C-001 | CONFIRM-from-source | Separated immediate permanent-error disable, metric disable, and response-time scheduled-test disable in §1, §2 S-1/S-18/S-28, and traces A/B/D. |
| C-002 | CONFIRM-from-source | §2 S-4..S-6 documents narrow classifier and fallthrough; §7 requires normalized taxonomy. |
| C-003 | CONFIRM-from-source | §2 S-8..S-10 and trace A show background disable with retry continuing; §6 R-6 addresses race risk. |
| C-004 | CONFIRM-from-source | §2 S-11/S-37/S-38 and §6 R-1 address pinned no-retry plus shared disable blast radius. |
| C-005 | CONFIRM-from-source | §2 S-16 and trace C document retry metric asymmetry from observed relay flow. |
| C-006 | CONFIRM-from-source | §2 S-19 and §4 in-process volatility row document memory window. |
| C-007 | CONFIRM-from-source | §2 S-20 and §4 queue saturation row document bounded queues and goroutine sends. |
| C-008 | CONFIRM-from-source | §2 S-33..S-36 and trace E cover status, ability availability, and cache refresh separation. |
| C-009 | CONFIRM-from-source | §2 S-24..S-32 and trace D cover scheduled/manual tests, disable, response-time, and re-enable. |
| C-010 | CONFIRM-from-source | §2 S-39/S-40 and §4 notification failure row separate state mutation from alert delivery. |
| F-001 | CONFIRM-from-source | §2 decomposes classifier, retry, pinning, cache, and async mutation rather than a boolean rule. |
| F-002 | CONFIRM-from-source | §2 S-21/S-22 and §6 R-5 cover window threshold and retry skew. |
| F-003 | CONFIRM-from-source | §2 S-34..S-36 and §10 region-16 cover multi-node/cache drift; §9 OQ-4 remains open for cluster scheduling. |
| F-004 | CONFIRM-from-source | §2 S-31 and §6 R-7 cover one-clean-test recovery weakness. |
| F-005 | CONFIRM-from-source | §6 R-4/R-10 recommends quarantine/normalized taxonomy instead of trusting one broad response. |
| D-001 | CONFIRM-from-source | §1 and §2 separate operator option for immediate disable from metric environment gate. |
| D-002 | CONFIRM-from-source | §10 region-16 plus §2 S-35/S-36 document doc/source tension around sync and local health evidence. |
| D-003 | CONFIRM-from-source | §1 and §2 include three mechanisms: permanent-error, metric, response-time scheduled test. |
| D-004 | OPEN-question-because-source-ambiguous | R3 does not rely on upstream database recommendation. HUAKAI design in §6/§7 follows DR-006 PostgreSQL. |
| D-005 | CONFIRM-from-source | §2 S-8..S-17 and §4 retry rows include the observed race/asynchrony issue. |
| N-001 | CONFIRM-from-source | §7 requires PostgreSQL-backed health evidence, not memory windows. |
| N-002 | CONFIRM-from-source | §5 and §6 R-1 require tenant blast-radius control. |
| N-003 | CONFIRM-from-source | §7 requires provider-normalized taxonomy instead of narrow matching. |
| N-004 | CONFIRM-from-source | §7 requires durable transition records and idempotent state transitions. |
| N-005 | CONFIRM-from-source | §7 requires Audit Event/incident state separate from notification. |
| N-006 | CONFIRM-from-source | §7 requires cooldown, scoped probes, and approval instead of one generic clean test. |
| N-007 | CONFIRM-from-source | §7 requires fail-closed routing and cache invalidation/versioning. |
| N-008 | CONFIRM-from-source | §7 requires guardrails for direct Channel pinning. |
| S-001 | CONFIRM-from-source | §4 documents in-memory health volatility. |
| S-002 | CONFIRM-from-source | §6 R-1/R-8 cover global shared state and root notification blast radius. |
| S-003 | CONFIRM-from-source | §1 and §2 distinguish immediate classifier, metric rule, and response-time rule. |
| S-004 | CONFIRM-from-source | §3 lists magic-policy inputs; §7 converts them into explicit policy requirements. |
| S-005 | CONFIRM-from-source | §4 automatic-disable-off row covers fail-open behavior. |
| S-006 | CONFIRM-from-source | §6 R-1/R-8 cover tenant exposure and durable scoped incident needs; UI/log redaction remains outside observed source and should be handled in implementation specs. |
| S-007 | CONFIRM-from-source | §2 S-8..S-17 and trace E cover async/routing/cache concurrency smell. |
| S-008 | CONFIRM-from-source | §2 S-31 and §7 recovery policy cover missing durable reason/cooldown. |
| Synthesis recommendations | CONFIRM-from-source | §5, §6, §7, and §8 convert the critic’s top recommendations into HUAKAI-safe adaptation: provider taxonomy, durable audit, retry accounting, tenant blast radius, PostgreSQL health evidence, fail-closed routing, and recovery workflow. |

Owner 中文总结：本文件拆解 one-api 的 Channel 自动禁用能力，真实观察包括 16 个源码区域里的永久错误禁用、成功率窗口禁用、定时测试禁用/恢复、重试交互、缓存一致性和通知副作用；合理推断集中在 §6，均标注为 inference，用于把观察到的上游行为对照 HUAKAI 的 DR-001 多租户、DR-002 双版本、DR-006 PostgreSQL 约束；critic 的 C/F/D/N/S 发现已逐项 CONFIRM/OPEN 处置，未用无来源内容补深度；当前 open question 为 9 个，最高优先级是确认重试成功是否在更底层另有成功指标、缓存失效是否有未读路径、以及多节点定时测试是否有集群协调。
