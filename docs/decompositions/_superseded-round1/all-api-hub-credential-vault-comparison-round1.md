# All API Hub - Multi-Account Credential Vault + Cross-Source Comparison

| Field | Value |
| --- | --- |
| Status | Draft |
| Reference | All API Hub, AGPL-3.0, E-LIC-003 |
| Feature in HUAKAI matrix | F-OPS-003 + propose F-KEY-002 + F-EXPORT-001 |
| Evidence ledger row | E-AAH-001, E-AAH-003, E-AAH-004 |
| Specifier session | Codex specifier-lane, 2026-04-29 |
| Specifier date | 2026-04-29 |
| Reviewer session | TBD |
| Reviewer date | TBD |
| Source files read | https://github.com/qixing-jk/all-api-hub<br>https://github.com/qixing-jk/all-api-hub/blob/main/README.md<br>https://github.com/qixing-jk/all-api-hub/blob/main/README_EN.md<br>https://github.com/qixing-jk/all-api-hub/pull/680<br>https://github.com/qixing-jk/all-api-hub/releases/tag/v3.26.0 |

## 1. WHY

All API Hub solves a client-side operator problem that server-side gateways do not solve directly: one human operator may hold many upstream relay-station accounts, each with its own balance, usage, model list, price multipliers, and API Keys. Logging into every source to compare available Models or copy credentials is slow and error-prone. The upstream browser-extension architecture makes sense because the operator is already authenticated in the browser, and the extension can keep management state local by default instead of requiring a central server.

This is materially different from sub2api, Portkey, or Helicone. Those references sit on the request path and optimize Route, Channel, Provider Account selection, observability, and billing. All API Hub is a personal operator console: it turns scattered Provider Account credentials into a local inventory, then lets the operator compare balances, Usage Records, Model prices, and export selected API Keys into downstream tools.

## 2. WHAT (algorithm in HUAKAI vocabulary)

The extension treats each upstream relay-station login as a Provider Account candidate. When the operator adds a source, the extension normalizes the Provider endpoint, attempts to recognize the Provider family from page and API signals, fetches basic User-facing account metadata, and warns when the same source identity already exists. If recognition fails, the operator can manually complete the Provider Account metadata instead of losing the feature.

Provider Accounts are persisted in browser-local storage as an operator-owned inventory. Updates are guarded across extension contexts so popup, options page, side panel, and background refresh do not overwrite each other. On read, stored data may be migrated to the current local schema. On write, account state is re-rendered reactively in the UI. Optional sync is separate: the default is local-first; WebDAV backup/sync is only active when configured, with selective data scope and optional encryption.

For dashboard comparison, the extension refreshes each Provider Account through the correct Provider-family adapter. It pulls balance, usage, health, available Model catalog, and per-Model pricing or multiplier information where the source supports it. The UI then aggregates by Provider Account and Model so the operator can compare effective cost and remaining balance from one panel.

For credential vault behavior, the extension maintains API Key-oriented profiles independent from the raw Provider Account. A profile is effectively a reusable `Provider endpoint + API Key + labels/notes` bundle. The list masks sensitive values by default, supports copy and verification actions, and can export selected credentials to supported downstream clients. Export flows resolve the full API Key only at the moment of use and may batch over multiple Provider Accounts and multiple API Keys.

## 3. INPUTS

Inputs include Provider endpoint URL, browser tab/page context, cookies or signed session already held by the browser, operator-entered API Key values, manually supplied Provider Account labels, selected Provider family, tag and note metadata, refresh interval preferences, WebDAV sync configuration, optional backup encryption secret, selected sync scope, selected downstream export target, selected Provider Accounts/API Keys for export, Model name, Model group or tier, pricing multiplier, balance value, usage totals, health-probe result, and timestamps from refresh or sync.

The mutated state is local operator inventory: Provider Account records, API Key profiles, Model catalog cache, pricing comparison cache, usage and balance snapshots, refresh status, sync status, export preferences, and user preferences. In HUAKAI terms, this is not gateway request-path state; it is operator-side Provider Account and upstream credential management state.

## 4. FAILURE MODES HANDLED

- Unrecognized Provider family: detected when automatic site recognition cannot classify a source; response is manual completion rather than blocking account creation.
- Duplicate Provider Account: detected by normalized source identity plus upstream User/account hints; response is an operator warning and later cleanup support.
- Disabled or stale Provider Account: surfaced in the account list and filtered in some UI actions so the operator does not accidentally treat it as healthy inventory.
- Concurrent extension writes: mitigated through storage write coordination and reactive browser storage wrappers.
- Cross-device overwrite risk: mitigated by explicit sync settings, selectable data scope, merge/upload/download modes, and optional encrypted backup.
- Missing downstream export configuration: export flows require target configuration before sending full API Keys.
- Partial batch export failure: batch integrations report per-item success/failure and aggregate result state instead of pretending the whole operation succeeded.
- Misleading key display: sensitive API Key values are masked by default, with explicit reveal/copy/export actions.

## 5. INTERFACES TO HUAKAI

HUAKAI should connect this pattern to Personal Edition and Admin Ops UI, not to the core gateway request path. The direct interfaces are Provider Account inventory, upstream credential profile storage, Model Registry comparison, Usage Record summary display, health-probe summary display, operator-only export plugins, Audit Event creation for reveal/export/sync actions, and optional backup/sync plugins.

F-OPS-003 should absorb the multi-source dashboard: balance, usage, health, and price comparison across Provider Accounts. Proposed F-KEY-002 should cover operator-managed upstream credential profiles that are separate from platform-issued User API Keys. F-EXPORT-001 should cover explicit export plugins with confirmation and target-specific validation.

## 6. RISKS

The largest risk is credential concentration. A browser extension vault containing many upstream credentials becomes a high-value target, especially if WebDAV sync copies it across devices. Local-first storage is privacy-preserving but not automatically encrypted at rest beyond browser guarantees. Export flows are intentionally convenient and can bypass operator pause-points if HUAKAI copies them too literally.

Price comparison can mislead operators if it compares only visible Model multipliers and ignores hidden fees, rate limits, group eligibility, latency, or Provider Account quota exhaustion. Site recognition can also drift because relay-station variants evolve. Browser-session-based refresh is useful for personal UX but should not become a SaaS control-plane dependency.

## 7. SAFE ADAPTATION FOR HUAKAI

- KEEP: Local-first Personal Edition credential inventory with a single comparison panel for Provider Account balance, usage, health, and Model pricing.
- KEEP: Manual fallback when Provider family recognition fails, because operators still need to record the Provider Account.
- KEEP: Separate upstream credential profiles from platform-issued User API Keys.
- IMPROVE: Encrypt upstream credentials before persistence with an operator-controlled key or OS/browser secure storage where available.
- IMPROVE: Add Audit Events for reveal, copy, export, import, sync enablement, and sync restore.
- IMPROVE: Treat WebDAV or any external sync as a plugin with revocation guidance, selective scope, and backup integrity checks.
- IMPROVE: Price comparison should show confidence and dimensions: price, quota, eligibility, latency, health, and last refresh time.
- IMPROVE: Export should require explicit target, credential preview, and confirmation; batch export must show per-target failure details.
- AVOID: Do not place browser-extension session scraping or Cloudflare-assist behavior into HUAKAI gateway core.
- AVOID: Do not copy AGPL implementation structure, adapter names, UI composition, or storage schema.

## 8. EVIDENCE LEDGER ROWS

- E-LIC-003: All API Hub is AGPL-3.0 and must remain specifier-lane evidence only.
- E-AAH-001: README-level evidence that operators need one dashboard for balances, usage, and health across many relay-station accounts.
- E-AAH-003: README-level evidence for cross-source Model price and token multiplier comparison.
- E-AAH-004: README-level evidence for one-click export into downstream tools.
- Related inventory rows: Site Account credential vault proposes F-KEY-002; independent API credential profiles map to F-EXPORT-001; local-first browser storage is a security/sync design input.

## 9. OPEN QUESTIONS

- Should F-KEY-002 be a Personal Edition-only feature first, or also appear in SaaS Admin Ops as an operator vault?
- What secure storage backend is acceptable for desktop, mobile, and server-admin browser contexts?
- Should HUAKAI allow external sync of upstream credentials at all, or require export/import with manual passphrase only?
- What minimum data is needed for fair price comparison: Model price, group multiplier, quota, latency, failure rate, and last verified time?
- Should export plugins ever send full upstream credentials directly to a third-party tool, or only generate a local file/copy action with explicit warning?
- How should credential profile tags map to HUAKAI User Groups, Channels, or operator-only labels without conflating concepts?

## Owner Summary

本文件拆解了 All API Hub 的浏览器扩展侧“多 Provider Account 凭证库 + 跨来源余额/用量/价格比较”模式；它与已有 sub2api 拆解的关键差异是：sub2api 是服务端网关请求路径能力，关注 Route/Channel/Provider Account 调度与请求执行，而 All API Hub 是客户端 Personal Edition/Operator UX，关注本地保存多个上游账号、识别来源、比较价格与余额、并把 API Key 导出到工具。HUAKAI 应吸收的是 F-OPS-003 的统一运营看板、拟新增 F-KEY-002 的上游凭证档案、以及 F-EXPORT-001 的显式导出插件；不能吸收的是 AGPL 代码结构、浏览器会话抓取细节或无确认的快捷导出行为。
