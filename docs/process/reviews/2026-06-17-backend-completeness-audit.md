# 后端功能完整性审计（2026-06-17）

> 8 功能域 agent 扫 stub/TODO/not-implemented + 对照三镜像(sub2api/new-api/CLIProxyAPI)缺 mode + 测试缺口；声称 S1 的逐条对抗复核。基线 `feat/frontend-portal` @ 1eeaa71d。
> 运行规模：8 agent · ~867k subagent tokens · 350 次工具调用(读真码) · ~9.5 分钟。

## ⚠️ 审计范围与局限（务必先读）

这是一遍 **breadth-first 侦察**，不是 exhaustive 完整性证明：

- **覆盖密度**：181 个内部包分给 8 个 agent，每个 ~9 分钟扫 ~22 个包 → 是"找明显问题"的侦察深度，**不是逐行/逐函数验证**。
- **"0 S1" 的真实含义**：是"**本遍未发现**致命洞"，**不等于**"证明无洞"。absence-of-finding ≠ proof-of-completeness。
- **能查到的**：未接线脚手架、stub/mock、TODO、对照镜像**缺整条 mode**、money/security 路径**无测试**——都给了 file:line，确凿可复核。
- **查不到的**：函数在边界条件下是否正确、并发竞态、难复现的 silent corruption——需 exhaustive 测试 + 形式化审查，本遍不保证。
- 要"真敢说全做完"，需对 money/security/quota 域做**逐包深挖第二遍**（更慢、对抗复核多遍）。

## 总结论

**0 个确认致命洞（S1）** —— 即本遍侦察下，后端核心路径未见 silent-fake/缺整条 mode 的致命缺陷（非完整性证明，见上局限）。

计数：S1 claimed 0 / S1 confirmed **0** / S2 **13** / S3 11（含 15 条 "complete" 确认）。

## 域裁决

| 功能域 | 裁决 | 发现数 |
|---|---|---|
| payment-billing-money | 🟡 minor-gaps | 4 |
| subscription-quota-budget | 🟡 minor-gaps | 5 |
| auth-identity-keys | ✅ complete | 5 |
| provider-channel-pool-credential | 🟡 minor-gaps | 6 |
| gateway-router-relay-proto | 🟡 minor-gaps | 4 |
| alerting-obs-audit-dlq-cache | 🟡 minor-gaps | 6 |
| comms-notify-moderation-referral | 🟡 minor-gaps | 5 |
| media-hermes-misc-features | 🟡 minor-gaps | 5 |

## 🟡 S2 —— 次要 mode 缺失 / 测试缺口（建议 roadmap）

| 域 | 包 | 类型 | 证据 | 详情 |
|---|---|---|---|---|
| payment-billing-money | `payment` | missing-mode | backend/internal/payment/store_postgres_refund.go:71-78 (refundOrderTx accepts rec.AmountC… | The admin refund endpoint (paymenthttp/refund.go:14,55) accepts a caller-supplied amount_cents, and service.RefundOrder (payment/service.go:240-251) o… |
| payment-billing-money | `windowcost` | no-test | backend/internal/windowcost/postgres.go:72-95 PostgresAggregator.SumWindowCost converts us… | The cents-conversion in PostgresAggregator.SumWindowCost feeds a quota/cost gate (window_cost_limit_cents enforcement). A float-truncation or numeric-… |
| subscription-quota-budget | `adminquotahttp` | missing-mode | backend/internal/adminquotahttp/quota_policy_crud.go:28-30 (validWindowKinds = {none, fixe… | The admin quota-policy CRUD HTTP allowlist omits 'calendar_month' even though the quota engine, the DB CHECK constraint, the subscription monthly-cap … |
| subscription-quota-budget | `adminquotahttp` | weak-test | backend/internal/adminquotahttp/quota_policy_crud_test.go:244 (windowKinds := []string{"no… | The CRUD allowlist test enumerates the same incomplete window-kind set as the production allowlist, so it cannot catch the missing 'calendar_month' mo… |
| provider-channel-pool-credential | `proxyadmin / proxyhealth (proxies schema)` | missing-mode | sub2api ent/schema/proxy.go:55-67 defines expires_at, fallback_mode (none\|proxy\|direct),… | HUAKAI is missing the entire proxy-expiry + fallback-on-expiry mode that sub2api implements. A proxy in HUAKAI has no expiration concept and no operat… |
| provider-channel-pool-credential | `channelprobe / channelhealth` | missing-mode | new-api controller/channel-test.go:896 testAllChannels runs a scheduled ACTIVE test that i… | HUAKAI's active channel-probe scaffolding (channelprobe.ChannelHealthScheduler + ActiveProbe func type) exists but is inert: never wired into cmd/gate… |
| gateway-router-relay-proto | `completionshttp` | wiring-bug | backend/internal/completionshttp/attempt.go:117 (Settle), attempt.go:171 (Settle), complet… | The /v1/completions handler is the ONLY money path in scope that settles and aborts billing on the raw, request-scoped (cancellable) context instead o… |
| gateway-router-relay-proto | `completionshttp` | no-test | completionshttp/handler_test.go contains only TestCompletionsReserveThenSettle_HappyPath, … | No discriminating test guards the settle-survives-client-disconnect behavior on the completions money path. Because production uses the cancellable ct… |
| alerting-obs-audit-dlq-cache | `systemhealthhttp` | wiring-bug | backend/cmd/gateway/routes_systemhealth.go:42-51 (buildSystemHealthSource leaves alertSvc … | Confirmed 'known nil bug': the gatewaySystemHealthSource.alertSvc field is never populated because the deps struct holds no alerting.Service reference… |
| alerting-obs-audit-dlq-cache | `systemhealthhttp` | no-test | No backend/cmd/gateway/routes_systemhealth_test.go exists; backend/internal/systemhealthht… | The systemhealthhttp unit test validates handler/derivation logic against a fake source but the production adapter buildSystemHealthSource (which cont… |
| comms-notify-moderation-referral | `community/invitation` | not-implemented | backend/internal/community/invitation/referral_reward_store.go:34 qualifyPendingReferralWi… | The referral TIER-PROGRESSION feature (silver/gold/platinum, tier_progress table, tierForQualifiedReferralCount, upsertReferralTierProgressTx) exists … |
| comms-notify-moderation-referral | `announcement / announcementhttp` | missing-mode | HUAKAI announcement/types.go:17-29 Announcement struct has NO targeting field and NO notif… | Versus the maturest mirror (sub2api, the rule-16 tiebreaker), HUAKAI is missing two whole announcement modes: (1) per-audience TARGETING (show an anno… |
| media-hermes-misc-features | `mjclient,sunoclient,videoclient` | weak-test | videoclient/router.go:200-201 + sunoclient/router.go:149-150 + mjclient/router.go map bill… | All three media-relay clients translate service errors to HTTP status via writeServiceError (insufficient_balance->402, ErrProviderUnavailable->400, E… |

## ⚪ S3 —— 轻微 / 卫生项

| 域 | 包 | 类型 | 证据 | 详情 |
|---|---|---|---|---|
| payment-billing-money | `paymenthttp` | wiring-bug | backend/internal/paymenthttp/refund_request_postgres.go:97-130 ApproveRefundRequest calls … | Approve is a documented split-transaction: money moves (RefundOrder commits) then the request row is marked approved in the outer tx. If the process d… |
| subscription-quota-budget | `windowcost` | no-test | backend/internal/windowcost/postgres.go:67-86 (PostgresAggregator.SumWindowCost numeric-te… | The only production money conversion in windowcost (USD numeric column -> integer cents, truncating not rounding, via big.Float) is untested except be… |
| auth-identity-keys | `apikeyipallow` | no-test | backend/internal/apikeyipallow/ contains only allowlist.go with no *_test.go file (apikeyi… | The IP-allowlist normalizer/matcher (AllowsCSV, Normalize, normalizeEntry) has no dedicated unit test, asymmetric with its sibling apikeyipdeny which … |
| auth-identity-keys | `userkeycontrolshttp` | no-test | userkeycontrolshttp/ip_blacklist_handlers.go defines newSetIPBlacklistHandler/newGetIPBlac… | The IP blacklist admin HTTP handlers (set/get) have no end-to-end route test asserting session-scope propagation and body parsing, unlike the symmetri… |
| provider-channel-pool-credential | `proxyadminhttp / proxyhealth` | missing-mode | sub2api internal/handler/admin/proxy_handler.go:251-253 exposes POST /proxies/:id/test (Te… | HUAKAI has no on-demand proxy connectivity/latency test endpoint and no quality-check (latency tier / egress IP / country). An operator cannot verify … |
| provider-channel-pool-credential | `credentialworker` | stub | backend/internal/credentialworker/mode_refresh.go:100 registers AuthModeAzure with mockTok… | The Azure auth mode's refresh adapter is named 'mock' and treats the credential as static (ErrNoRefreshRequired) unless an operator wires an arbitrary… |
| provider-channel-pool-credential | `proxysecret` | no-test | backend/internal/proxysecret/secret.go has prod=1 test=0 (no _test.go). It builds a tenant… | proxysecret encrypt/decrypt round-trip is exercised indirectly through proxyadmin and the resolver, so the path is not wholly untested — but the secur… |
| alerting-obs-audit-dlq-cache | `obs` | stub | backend/internal/obs/dlq/refund_worker.go defines NewRefundHandler/NewRefundEvent/RefundSi… | obs/dlq's refund handler is dead/unwired production code: the actual mismatch-refund money path is implemented and wired via the audit/auditreceipt Mi… |
| alerting-obs-audit-dlq-cache | `alerting` | missing-mode | HUAKAI silenceMatches/silenceScopeMatches at backend/internal/alerting/service.go:456-482 … | Secondary-path gap, not a hole: HUAKAI's DB-backed silence records already cover sub2api's per-rule and dimension silencing (a nil-RuleID record = glo… |
| alerting-obs-audit-dlq-cache | `userauditlog` | no-test | backend/internal/userauditlog/store.go has real validation (TenantID/UserID/Action/Outcome… | Minor test gap: the core write/read path of the PostgresStore is covered by the userauditloghttp pg integration test (inserts issue+revoke events, rea… |
| media-hermes-misc-features | `tlsfphealth` | weak-test | tlsfphealth/pg.go:19-29 ListActive uses `ORDER BY id LIMIT $1` with maxPerTick=500 (worker… | The drift-health worker only ever validates the first 500 active TLS-fingerprint profiles by ascending id every tick. With more than 500 active profil… |

## ✅ "complete" 确认（域稳固证据）

- **payment-billing-money** `payment`：Every mode the mirror-comparison hint asked for is present and production-grade: auto webhook AND manual admin credit BOTH present; refund e…
- **subscription-quota-budget** `subscription`：Subscription quota model is feature-complete vs the sub2api mirror and in some areas stronger. Reset strategy diverges intentionally and is …
- **subscription-quota-budget** `quotaenforce`：The enforcement/settlement wiring (quota Reserve scopes include ScopeUser so subscription cost_usd policies bind; billing-then-quota orderin…
- **auth-identity-keys** `twofa + controlhttp + gatewayhttp`：The hint flagged a possible '2FA bind-init known gap'. It is NOT present. HUAKAI implements the full verify-before-enable bind flow: Setup p…
- **auth-identity-keys** `passkey + passkeyhttp`：Full WebAuthn passkey lifecycle is present and wired: registration ceremony with session consumption, discoverable login, credential listing…
- **auth-identity-keys** `apikeyipallow + apikeyipdeny + apikeymodelallow`：Hint asked to confirm key ip/model allow/deny enforcement is actually wired, not just present. It is. IP deny+allow run inside the credentia…
- **provider-channel-pool-credential** `provider/registrydefault (session adapters)`：The numerous TODO(OCAW)/placeholder markers in the provider tree are confined to the unverified session-reversal adapters, which are env-gat…
- **gateway-router-relay-proto** `gateway`：Mid-stream fallback is intentionally NOT implemented: once deliveryStarted (tracker.started()) is true a forward error is terminal, no retry…
- **alerting-obs-audit-dlq-cache** `alerting`：Alert evaluation scheduler genuinely runs (config-gated, with clean stop fn and Postgres leader-lock to prevent duplicate alerts across repl…
- **comms-notify-moderation-referral** `notify`：Notify domain is genuinely complete and well-tested; no stubs or missing channel modes. Recorded as a confirming complete finding.
- **comms-notify-moderation-referral** `moderation / moderationhttp`：Moderation keyword/hash/external/auto-ban/unban paths are all implemented, wired, and discriminatingly tested. The 429 handling flagged in m…
- **comms-notify-moderation-referral** `checkin / checkinhttp / invitevalidatehttp`：Checkin and invite-validate paths are complete with idempotency on the money/identity paths. Confirming complete.
- **media-hermes-misc-features** `modelsync`：Confirming modelsync is NOT trigger-only despite the audit hint. It has a full fetch->validate->apply pipeline with an atomic batch store pa…
- **media-hermes-misc-features** `tlsfpresolve`：Confirming the TLS-fingerprint resolve feature is fully wired end-to-end (not a dormant sidecar): the account-bound / rotation-pool profile …
- **media-hermes-misc-features** `mediatask`：The media-task async money lifecycle (reserve->capture/release, timeout expiry, lease fencing, idempotent replay) is complete and the integr…

## 重点（值得优先 roadmap 的真 gap）

- **payment 部分退款一次性锁死**：partial refund 把订单永久翻成 `refunded`，剩余金额无法再退（缺 `partially_refunded` 态 + 退款累加器；sub2api 有）。money 正确性次要 mode。
- **adminquotahttp 漏 `calendar_month`**：admin quota-policy CRUD 白名单缺月度窗，虽引擎/DB/订阅都支持 → admin API 建不了月度 USD 上限策略（注释谎称"暴露完整超集"）。**直接影响已合并的配额策略前端 #15**。且其测试枚举同一残缺集=非判别。
- **completionshttp 计费用可取消 ctx**：/v1/completions 是唯一在请求级(可取消)ctx 上结算+中止计费的 money 路径，客户端断连可能中止计费；且无测试守护。
- **systemhealthhttp alertSvc nil**：`AlertingFiringCount` 永远返回 0（alertSvc 从未注入 source builder）——覆盖审计也独立发现此 wiring bug。
- **channelprobe 空转**：主动探测脚手架(ChannelHealthScheduler)存在但从未接进 cmd/gateway。
- **referral tier-progression 未接线**：silver/gold/platinum 分级 + tier_progress 表逻辑只在 community/invitation 孤儿文件里，生产其余处零引用。
- **announcement 缺 targeting + 已读追踪**：对照 sub2api 缺受众定向与按用户已读（已在前端 #19 roadmap 登记）。

> 注：覆盖审计(端点×前端)与本审计(后端功能完整性)互补——前者说"前端接没接"，后者说"后端做没做对/全"。两者的 systemhealth alertSvc nil 互相印证。
