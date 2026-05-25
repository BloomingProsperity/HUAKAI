# all-api-hub credential vault / comparison decomposition - source verified R3

## Metadata

| Field | Value |
| --- | --- |
| Project | all-api-hub |
| Feature | Multi-account credential vault, cross-source price comparison, site-recognition heuristics, secure-storage primitives |
| HUAKAI rows | F-OPS-003 + F-KEY-002 + F-EXPORT-001 |
| Lane | Codex specifier-lane, Round 3 |
| Date | 2026-04-29 |
| Clean-room mode | Behavior-only decomposition from observed public docs, tests, and source regions. No upstream file paths, function names, schemas, distinctive structure, or implementation names are carried forward. |
| Truth-discipline | Observed regions: 15 / Inferences: 9 / Open questions: 9 |
| Critic handled | Yes, see §11 |

## §1 WHY - pressures driving the design

The reference exists because operators accumulate relay-station accounts, standalone upstream credentials, managed backend targets, model catalogs, and export destinations faster than a single browser tab can safely manage. The public feature overview presents one surface for balances, usage, model price comparison, automatic check-in, key management, standalone credential profiles, downstream export, self-hosted management, Cloudflare-assisted access, backup/sync, and local-first storage [region-1].

That pressure is not only convenience. The same source family shows that the vault can move secrets across boundaries: local JSON backup/import, WebDAV upload/download, encrypted WebDAV envelopes, direct downstream tool export, and management-interface writes into a downstream system [region-2][region-3][region-6][region-15]. For HUAKAI, this means the feature cannot be treated as a small UI helper; it is a tenant-scoped credential data plane.

The price-comparison side is also operationally loaded. The reference compares sources, billing modes, groups, exchange/recharge context, and account-vs-profile sources, and tests that "cheapest" can change when group filters or real-price conversion are toggled [region-11]. For HUAKAI, price comparison must be evidence-bearing and audit-friendly, not a routing oracle unless the source and normalization status are known.

The critic's main correction is confirmed: "local-first" is not "sealed." Observed docs and tests show export, sync, downstream writes, and backup encryption/retry paths [region-2][region-3][region-6][region-15]. HUAKAI must preserve the feature outcome while replacing casual secret movement with governed, tenant-aware workflows.

## §2 WHAT - source-grounded sub-behaviors

S-1. The reference presents a unified operator surface for multiple relay accounts, balances, usage, health, model price comparison, automatic check-in, site key management, standalone credential profiles, downstream export, and compatibility verification [region-1].

S-2. The credential vault is broader than saved upstream credentials: observed backup/import behavior covers relay accounts, pinned account order, preferences, managed-site/channel configuration, standalone credential profiles, and tag associations [region-2][region-10].

S-3. Local JSON export produces a backup for accounts and preferences, and local JSON import can overwrite current accounts and preferences; observed docs recommend taking a backup before import [region-2].

S-4. Backup import has more than one shape: tests observe legacy account-only import, full modern import, profile-only import, preferences-only import, unknown-version fallback, and partial success when one section fails [region-10].

S-5. WebDAV sync is an optional cross-device path that uses remote credentials, can upload, download, or merge, and runs manually or on a periodic background interval when enabled [region-3].

S-6. WebDAV merge is timestamp-oriented and can create duplicate accounts, because the public docs explicitly warn that merge duplicates may require manual deletion and re-upload [region-3].

S-7. WebDAV backup encryption is observed as a password-derived encrypted envelope with randomized salt and IV, authenticated encryption, envelope detection, backwards compatibility for plaintext backups, and failure on missing/wrong password or tampered ciphertext [region-15].

S-8. Auto-identification depends on browser-side target-site login state; the operator must be logged into the target site in the same browser before identification can read account data [region-4].

S-9. Auto-identification can use different authentication modes, and one supported site family requires cookie authentication and cannot represent multiple accounts on the same site through that cookie mode [region-4].

S-10. Auto-identification has a manual fallback when required fields cannot be inferred or the detection flow repeatedly fails [region-4].

S-11. Site compatibility is not uniform: the public supported-site page separates daily manageable relay sites, limited-information compatible variants, and self-built backend targets; it also marks at least one backend family as discontinued [region-5].

S-12. Automatic refresh can run in the browser background, refresh on popup open, enforce a minimum interval for non-forced refresh, and broadcast success/failure updates to open UI surfaces [region-7].

S-13. Disabled accounts are a first-class state: E2E tests observe disable and re-enable actions persisting to storage, and unit tests observe enabled account slices excluding disabled entries [region-9][region-13].

S-14. Disabled accounts are excluded from automatic refresh and scheduled analysis flows according to changelog evidence, while separate changelog entries show UI filtering and disabled-account action-menu changes evolved over time [region-14].

S-15. Account ordering is persisted: E2E tests observe pin/unpin changing both display order and persisted pinned-order state [region-9].

S-16. Duplicate cleanup is operational, not cosmetic: E2E tests observe duplicate scanning, preview, explicit keep/delete selection, pinned-account deletion warning, final confirmation, and pruning of stale pinned/order references [region-9].

S-17. Duplicate identity is not merely display name: changelog evidence says later changes added configurable duplicate warnings, duplicate cleanup by origin plus user id, global duplicate-name disambiguation, and managed-target duplicate checks [region-14].

S-18. Standalone upstream credential profiles are independent from site accounts: E2E tests observe creating, verifying, opening model management with a profile-scoped source, editing, and deleting a profile persisted in extension storage [region-8].

S-19. Profile-backed model management can load models and suppress account-key actions that only apply to site accounts [region-8].

S-20. Price comparison is source-sensitive: tests observe an all-accounts view where the same model appears from multiple accounts and only the lowest row is marked as lowest [region-11].

S-21. Price comparison is group-sensitive: tests observe each account choosing its cheapest eligible group, and the result changes when an account-specific group is excluded [region-11].

S-22. Same-named groups on different accounts are treated independently; account-specific group filters do not globally remove the same group name from other accounts [region-11].

S-23. Price comparison is billing-mode-sensitive: tests observe token-based and per-call models filtered separately, kept in separate price-sorting groups, and sorted using different price inputs [region-11].

S-24. Real-price comparison can change the cheapest result when account balance/recharge context is considered; tests observe a row losing cheapest status after real-price mode is enabled [region-11].

S-25. When pricing support is unavailable through the selected source, model management downgrades source capabilities and resets price-specific sorting/filtering state instead of pretending pricing still applies [region-12].

S-26. Quick export supports multiple destination classes: local app launch/population, clipboard/JSON export, and management-interface writes into self-hosted targets [region-6].

S-27. Exported content includes source/account naming, Provider endpoint, upstream credential, model list, recharge ratio, and group/priority context where the destination supports it [region-6].

S-28. CLIProxyAPI integration reads a configured management target and management credential, derives an OpenAI-compatible Provider endpoint from the source account, and updates or creates downstream provider configuration [region-6].

S-29. Downstream export attempts deduplication by matching target provider name or Provider endpoint, and repeated import is documented as updating credential entries rather than creating many provider records [region-6].

S-30. Downstream export has observed failure paths: missing destination configuration, 401/403 or HTTP errors from the target management surface, and model-list fetch gaps [region-6].

S-31. Cloudflare/challenge handling is an explicit browser-assisted path: docs observe automatic challenge detection, temporary same-origin window usage, cookie reuse, degraded replay after failed regular fetch, and manual completion within a timeout [region-7][region-6].

S-32. Managed target access has a separate admin credential/session problem: tests observe stored login-assist credentials, automatic and manual two-factor/verification paths, cached verified session reuse until expiry, passkey manual requirement, unexpected probe failure propagation, and secret redaction from verification failure messages [region-15].

## §2-bis Lifecycle traces

Trace A - account discovery to vault entry:
1. Operator is logged into a relay site in the browser and starts auto-identification [region-4].
2. If the site blocks normal access through Cloudflare or similar controls, a temporary same-origin window may be opened and the operator may need to complete the challenge [region-7].
3. Detection fills account data when possible; if required fields remain missing, the operator can fall back to manual entry [region-4].
4. The saved account becomes part of the multi-account dashboard and can later be disabled, pinned, exported, refreshed, or included in backup/sync [region-1][region-9].

Trace B - backup/import and WebDAV sync:
1. Operator exports local data to JSON, which includes accounts and preferences and may include additional vault-adjacent sections in modern backup flows [region-2][region-10].
2. Operator imports a backup; import can overwrite accounts/preferences or import only selected sections depending on backup shape [region-2][region-10].
3. With WebDAV enabled, the system can test remote credentials, download remote backup, merge/upload/download based on strategy, write local state, and upload the resulting JSON [region-3].
4. If encrypted backup is enabled, the uploaded backup is an encrypted envelope and download needs the configured password; wrong or missing password is an observed failure path [region-15].

Trace C - price comparison:
1. Operator opens model management for all accounts or one source [region-1][region-12].
2. Pricing rows are filtered by source, account group, and billing mode; same-named groups remain account-local [region-11].
3. Cheapest markers are computed per model across eligible rows; toggling real-price mode can change which row is cheapest because recharge/exchange context changes effective cost [region-11].
4. If the selected source cannot support pricing, the price-specific mode is reset and source capabilities are downgraded [region-12].

Trace D - downstream export:
1. Operator selects a key from Key Management and chooses an export target [region-6].
2. For desktop/clipboard targets, the export either launches a local app or emits target-specific JSON [region-6].
3. For self-hosted/management targets, the operation reads destination management configuration and writes Provider/Channel-style configuration using the source Provider endpoint, upstream credential, model list, ratio, and group context where supported [region-6].
4. Failure is surfaced when destination configuration is missing, target auth fails, target HTTP support is absent, or model list data is empty [region-6].

Trace E - duplicate and disabled recovery:
1. Operator disables an account; disabled state is persisted and disabled entries are excluded from enabled slices [region-9][region-13].
2. Automatic refresh and scheduled analysis flows skip disabled accounts according to changelog evidence [region-14].
3. Operator scans for duplicates; the dialog groups candidates, lets the operator choose the kept record, previews deletion, warns when pinned accounts are affected, and then updates account plus ordering references [region-9].

## §3 INPUTS - data structure inventory

Observed input categories, redacted into HUAKAI vocabulary:

| Input category | Observed contents | HUAKAI meaning |
| --- | --- | --- |
| Provider Account record | Source URL/origin, display label, external user identity, auth mode, browser/session-derived credential, local state, disabled flag, ordering metadata [region-4][region-9][region-14] | Tenant-scoped Provider Account with lifecycle state and external identity evidence. |
| Upstream credential inventory | API keys associated with a relay site, standalone profile keys, exportable key entries [region-1][region-6][region-8] | Upstream credentials held by Provider Accounts or standalone verification profiles. |
| Standalone credential profile | Name, Provider endpoint, API type, upstream credential, tags, notes, timestamps as observed by profile tests [region-8][region-10] | Non-routable credential profile unless promoted into a Provider Account or Channel. |
| Backup package | Account data, preferences, pinned/order state, managed target config, tags, profile data, timestamp/version indicators [region-2][region-10] | Tenant data package requiring preview, validation, and import policy. |
| WebDAV configuration | Remote URL, username/password, strategy, interval, sync-data selection, optional encryption password [region-3][region-15] | External sync connector secret plus policy. |
| Pricing source context | Source kind, account/profile source, model rows, group ratios, billing mode, recharge/exchange context, effective group [region-11][region-12] | Price evidence row with provenance and normalization context. |
| Export destination config | Desktop target, clipboard/JSON target, self-hosted management target, management credential, Provider endpoint derivation [region-6] | Secret release target and action policy. |
| Browser challenge/session context | Login state, cookie mode, optional permissions, temporary window state, verification timeout [region-4][region-7] | Manual recovery and browser-assisted source-access state. |

## §4 FAILURE MODES - observed only

| Failure mode | Observed source evidence | HUAKAI implication |
| --- | --- | --- |
| Import overwrites current accounts/preferences | Data-management docs explicitly warn import overwrites current accounts and preferences [region-2] | Restore must be previewed and reversible. |
| Backup format invalid | Import tests throw when timestamp/format is missing or nothing importable exists [region-10] | Import validation must fail before mutation. |
| Partial import success | Tests observe accounts importing while preferences fail, returning partial success [region-10] | Import must produce section-level results and audit. |
| WebDAV auth/connection failure | WebDAV docs list 401/403 and unsupported methods as connection problems [region-3] | Sync connector health must be visible and tenant-scoped. |
| WebDAV duplicate accounts after merge | WebDAV docs list duplicates after merge as a common issue [region-3] | Merge must have conflict review rather than silent write. |
| Missing/wrong WebDAV encryption password | Encryption source throws on missing/wrong password/tamper; docs mention decryption retry [region-15] | Encrypted restore needs retry and lockout policy. |
| Auto-identification stuck or failed | Auto-identification docs list Cloudflare/firewall, slow network, permission/browser anomaly, 401/403, and required-field failures [region-4] | Source onboarding needs explicit recovery states. |
| Cookie mode cannot represent multiple same-site accounts for one family | Auto-identification docs state the cookie mode limitation [region-4] | Multi-account identity must be adapter-declared. |
| Disabled account stale operations | Changelog says disabled accounts were later excluded from refresh/UI/scheduled operations [region-14] | Disabled-state semantics must be centralized. |
| Duplicate cleanup can delete pinned records | E2E duplicate cleanup warns when a pinned account will be deleted [region-9] | Cleanup needs preview and reference pruning. |
| Price capability unavailable | Model-list tests reset pricing state when selected source cannot price [region-12] | Comparisons must carry capability status. |
| Export target missing or unauthorized | Quick export and CLIProxyAPI docs list missing config and 401/403/HTTP errors [region-6] | Secret-release failure must be audited. |
| Cloudflare challenge blocks server-like refresh | Cloudflare docs require browser temporary window/manual challenge completion [region-7] | Scheduled server refresh may need manual recovery. |
| Managed target verification needs manual steps | Managed-session tests observe passkey/manual verification and propagated probe failures [region-15] | Admin-target export cannot assume one-shot auth. |

## §5 INTERFACES TO HUAKAI - Personal vs SaaS Edition

Personal Edition:

- Provide a local/operator-first credential vault view for Provider Accounts, upstream credentials, standalone profiles, and comparison-only sources.
- Allow JSON import/export only with explicit preview, backup-before-restore, and section-level results.
- Support optional sync plugins, but keep encrypted backup mandatory if the sync target can store upstream credentials.
- Allow price comparison as an operator decision aid. It may suggest cheaper sources but must not directly change Route policy without explicit promotion.
- Allow downstream export plugins only after the operator confirms exact destination, upstream credential, Provider endpoint, and model/group scope.

SaaS Edition:

- Every Provider Account, upstream credential, profile, sync connector, export target, and comparison result is tenant-scoped under DR-001.
- PostgreSQL is the authoritative store under DR-006; browser storage is not the system of record.
- Secret storage uses envelope encryption, rotation metadata, reveal/export policy, and Audit Events.
- Backup/import is an Admin data-plane operation with dry-run, diff, idempotency key, versioned restore, rollback or compensating action, and cross-tenant isolation tests.
- Price comparison rows carry provenance, source timestamp, adapter capability, billing mode, currency/ratio context, group scope, disabled-state treatment, and stale/partial status.
- Downstream export is a governed Plugin action with target allowlists, role checks, dry-run, reconciliation, and immutable Audit Events.

## §6 RISKS - HUAKAI-fit reasoning

R-1. (inference, not observed) Browser-local storage is acceptable for the reference's local-first extension posture, but HUAKAI SaaS would create cross-tenant blast radius if credentials, profile keys, and managed-target credentials were stored as generic preferences instead of tenant-scoped encrypted secrets.

R-2. (inference, not observed) Import overwrite semantics are dangerous under DR-001 because one wrong tenant context could replace many Provider Accounts and preferences; HUAKAI needs restore previews and tenant locks before mutation.

R-3. (inference, not observed) WebDAV merge-by-freshness is too weak for HUAKAI because conflict resolution must account for tenant, source origin, external identity, credential fingerprint, disabled state, and audit history.

R-4. (inference, not observed) A "cheapest" badge can mislead routing if it omits billing mode, group, recharge ratio, source timestamp, and manual/verified status; HUAKAI must keep comparison separate from automatic Route decisions unless verified.

R-5. (inference, not observed) The observed browser-assisted Cloudflare path cannot be reproduced reliably by a server scheduler; HUAKAI needs stale-source and manual-recovery states rather than treating refresh failure as simple downtime.

R-6. (inference, not observed) Downstream export is a secret-release workflow. In SaaS, writing one tenant's upstream credential into a shared target would be a data leak unless destination ownership and tenant boundary are verified.

R-7. (inference, not observed) Disabled account semantics must cover comparison, export, backup, refresh, and recovery consistently; partial exclusion would produce stale price rows or accidental secret export from disabled Provider Accounts.

R-8. (inference, not observed) Standalone credential profiles should not become routable Provider Accounts by accident; HUAKAI needs an explicit promote-to-Provider-Account workflow with validation and Audit Event.

R-9. (inference, not observed) Managed-target admin credentials should be separated by purpose from upstream Provider credentials; both are secrets, but their release scopes and rotation owners differ.

## §7 SAFE ADAPTATION - concrete divergences

1. Replace browser storage as authority with PostgreSQL-backed, tenant-scoped vault tables and encrypted secret material. Browser/local state may cache non-secret display state only.
2. Model Provider Account identity as tenant + adapter/source type + canonical origin + verified external user identity + credential fingerprint, with disabled-state and conflict-review metadata.
3. Treat standalone profiles as comparison/verification inputs until explicitly promoted.
4. Make backup/import a privileged Admin workflow: validate, preview diff, classify sections, obtain confirmation, apply idempotently, and record Audit Events.
5. Make sync connectors plugins. WebDAV is one plugin, not the hardcoded sync model; all sync plugins must support encryption, revocation, and tenant scoping.
6. Make price comparison rows immutable evidence snapshots with adapter capability, source timestamp, group, billing mode, currency/ratio, raw value, normalized value, and stale/partial flags.
7. Exclude disabled Provider Accounts from automated refresh, export, and routing by default; allow read-only inclusion in backup/restore previews with clear state labels.
8. Convert quick export into a governed secret-release action with dry-run, target allowlist, role check, idempotency key, reconciliation, and rollback/compensating record.
9. Treat Cloudflare/manual challenge recovery as an operator-assisted adapter state, not a background-server guarantee.
10. Keep all adapter-specific compatibility claims behind capability probes and certification status, especially for limited-information or discontinued source families.

## §8 EVIDENCE LEDGER ROWS

| Proposed ID | Source | Source type | Observed behavior | HUAKAI feature implication | Risk / mitigation | Clean-room note |
| --- | --- | --- | --- | --- | --- | --- |
| E-AAH-DEEP-001 | all-api-hub | Source-verified behavior read | Unified dashboard spans Provider Accounts, key management, standalone profiles, model comparison, export, backup/sync, and browser-assisted recovery. | F-OPS-003 dashboard must include vault + comparison + health context. | High-value credential aggregation; require encrypted tenant vault and Audit Events. | Behavior only. |
| E-AAH-DEEP-002 | all-api-hub | Source-verified behavior read | Backup/import includes accounts, order/pin metadata, preferences, managed target config, profiles, tags, and partial/legacy import cases. | F-KEY-002 must cover credential-vault data plane, not only upstream keys. | Overwrite and partial import; require preview, section-level result, rollback. | Behavior only. |
| E-AAH-DEEP-003 | all-api-hub | Source-verified behavior read | Price comparison depends on source, account/profile context, group, billing mode, recharge ratio, and capability availability. | F-OPS-003 price comparison must carry provenance and normalization data. | Misleading cheapest row; mark stale/partial/unverified and keep separate from routing. | Behavior only. |
| E-AAH-DEEP-004 | all-api-hub | Source-verified behavior read | Downstream export writes upstream credentials into external tools or management targets and deduplicates target records by destination identity. | F-EXPORT-001 becomes governed secret-release plugin. | Secret leakage and partial target writes; require allowlists, idempotency, reconciliation, audit. | Behavior only. |
| E-AAH-DEEP-005 | all-api-hub | Source-verified behavior read | Cloudflare/browser challenges and managed target verification can require manual browser/session steps. | F-KEY-002/F-EXPORT-001 need manual recovery states. | Server-side refresh cannot guarantee browser-assisted access; require stale-source state. | Behavior only. |

## §9 OPEN QUESTIONS

1. Exact upstream adapter capability matrix by site family is not fully proven from the regions read; only broad public supported-site categories and selected tests were observed.
2. Exact persistence encryption for ordinary local profile keys was not proven; only WebDAV backup encryption and local-storage persistence behavior were observed.
3. Whether disabled accounts are included in every export path was not fully proven; docs/tests prove exclusion from enabled slices, refresh/scheduled analysis, and UI operations only.
4. Whether price comparison rows store timestamps per source or are computed transiently was not proven.
5. Whether downstream export has rollback or reconciliation beyond success/failure notification was not observed.
6. Whether duplicate cleanup considers credential fingerprint was not observed; observed evidence supports origin plus external user identity and operator preview.
7. Whether WebDAV sync locks against simultaneous device writes was not proven; docs mention in-progress prompt and strategy, not full concurrency control.
8. Whether standalone profiles support rotation history was not observed.
9. Whether managed-target admin credentials are encrypted at rest locally was not proven.

## §10 SOURCE COVERAGE PROOF

| Region | Region read | Contribution |
| --- | --- | --- |
| region-1 | English README feature overview | Established top-level product scope: multi-account dashboard, model comparison, key management, standalone profiles, export, WebDAV, Cloudflare helper, local-first posture. |
| region-2 | English data import/export docs | Proved JSON export/import behavior, included data categories, overwrite warning, interoperability, and import failure/common issue language. |
| region-3 | English WebDAV sync docs | Proved remote credential setup, manual and automatic sync, merge/upload/download strategies, timestamp merge, duplicate-after-merge issue, and WebDAV auth failures. |
| region-4 | English auto-identification troubleshooting docs | Proved browser login dependency, Cloudflare/firewall slowness, optional-permission issue, auth-mode switching, cookie-mode multi-account limitation, required fields, and manual fallback. |
| region-5 | English supported-site docs | Proved site categories, limited-information variants, self-built backend targets, and discontinued-backend caveat. |
| region-6 | English quick export + CLIProxyAPI docs | Proved export target classes, exported content categories, management-interface writes, deduplication by destination identity, model-list dependency, and 401/403/missing-config failures. |
| region-7 | English auto-refresh + Cloudflare helper docs | Proved background/popup refresh, minimum interval, refresh failure causes, temporary same-origin window, cookie reuse, replay/degradation, manual challenge, and rate-limit guidance. |
| region-8 | Popup credential-profile E2E tests | Proved standalone profile create/verify/model-management/edit/delete workflows and storage persistence. |
| region-9 | Account-management E2E tests | Proved disable/re-enable persistence, pin/unpin persisted order, duplicate scan/preview/confirmation, pinned deletion warning, and stale reference pruning. |
| region-10 | Import/export unit tests | Proved legacy and modern import variants, full backup contents, profile/tag merge, preferences-only import, unknown-version fallback, preserve-WebDAV option, partial success, and no-importable-data failure. |
| region-11 | Model-list filtering/pricing tests | Proved all-account cheapest marking, group-specific cheapest calculation, account-local group names, billing-mode filtering, real-price conversion effect, per-call fallback/default behavior, and separate price-sorting groups. |
| region-12 | Model-list data tests | Proved capability downgrade and reset of price-specific modes when the selected source cannot provide pricing. |
| region-13 | Account data hook tests | Proved enabled account/display slices exclude disabled entries. |
| region-14 | Changelog regions on duplicate/disabled/pricing drift | Proved feature drift around disabled filtering, duplicate warnings, origin+user cleanup, duplicate-name disambiguation, price comparison, temporary-window prompting, and auto-refresh interval adjustments. |
| region-15 | WebDAV backup encryption and managed-target session tests/source | Proved encrypted backup envelope primitives, missing/wrong password failure, and managed target login/verification/session/manual/passkey/redaction behavior. |

## §11 ROUND-2 CRITIC FINDINGS

| Finding | Disposition | R3 handling |
| --- | --- | --- |
| C-001 vault spans more than saved keys | CONFIRM-from-source | §2 S-2, §3, §5 broaden vault to accounts, order, preferences, profiles, tags, managed target config, backup metadata. |
| C-002 price comparison is adapter-dependent | CONFIRM-from-source | §2 S-20..S-25 and §6 R-4 require provenance/capability/billing/group context. |
| C-003 duplicate handling is operationally deep | CONFIRM-from-source | §2 S-16..S-17 and lifecycle Trace E cover preview, identity ambiguity, warnings, cleanup. |
| C-004 disabled behavior affects vault/comparison | CONFIRM-from-source | §2 S-13..S-14, §4, §7 define disabled-state implications; export inclusion remains open in §9. |
| C-005 refresh/sync race paths are real | CONFIRM-from-source | §2 S-5..S-7/S-12 and §9 note lack of full lock proof. |
| C-006 price numbers require normalization | CONFIRM-from-source | §2 S-20..S-25 and §7 require billing mode, group, ratio, provenance, stale/partial flags. |
| C-007 secret export hazard | CONFIRM-from-source | §2 S-26..S-30, §5, §7 convert export to governed secret release. |
| C-008 partial downstream export recovery underspecified | CONFIRM-from-source | §4 and §9 mark rollback/reconciliation unobserved; §7 requires idempotency and reconciliation for HUAKAI. |
| C-009 Cloudflare/session blocks refresh | CONFIRM-from-source | §2 S-31 and Trace A cover browser/manual recovery; §6 R-5 maps HUAKAI risk. |
| F-001 local storage is not sealed | CONFIRM-from-source | §1 and §2 include JSON, WebDAV, export, encryption, downstream writes. |
| F-002 comparison is not passive catalog | CONFIRM-from-source | §2 S-20..S-25 and §6 R-4 mark it billing-adjacent evidence. |
| F-003 broad site support hides compatibility uncertainty | CONFIRM-from-source | §2 S-11 and §7 require adapter certification. |
| F-004 one-click export is admin-power | CONFIRM-from-source | §2 S-26..S-30 and §7 governed export. |
| F-005 duplicate prevention hides identity ambiguity | CONFIRM-from-source | §2 S-16..S-17, §9 open fingerprint question. |
| D-001 local-first drift with external movement | CONFIRM-from-source | §1 and §5 use "local-first with external secret movement." |
| D-002 supported-site uncertainty | CONFIRM-from-source | §2 S-11 and §9 keep uncertainty. |
| D-003 base-url/name matching insufficient | CONFIRM-from-source | §2 S-17/S-29 and §7 require stronger HUAKAI identity. |
| D-004 stale upstream family risk | CONFIRM-from-source | §2 S-11 and §7 certification. |
| D-005 destructive restore behavior | CONFIRM-from-source | §2 S-3 and §7 preview/versioned restore. |
| N-001 do not copy browser-local vault | CONFIRM-from-source | §5 and §7 replace with PostgreSQL encrypted tenant vault. |
| N-002 do not copy casual plaintext export | CONFIRM-from-source | §7 secret-release workflow. |
| N-003 do not copy compatibility-bucket dispatch | CONFIRM-from-source | §7 adapter registry/certification. |
| N-004 do not copy name/base-url duplicate matching | CONFIRM-from-source | §7 stronger identity; §9 marks fingerprint proof open. |
| N-005 do not copy JSON overwrite restore | CONFIRM-from-source | §7 preview/diff/version restore. |
| N-006 manual fallback must be unverified | CONFIRM-from-source | §7 excludes unverified/manual values from automation by default. |
| N-007 admin target credentials are secrets | CONFIRM-from-source | §5/§7 separate managed-target secrets. |
| S-001 weak persistence boundary | CONFIRM-from-source | §6 R-1/R-2 and §7. |
| S-002 tenant data leakage potential | CONFIRM-from-source | §5 SaaS and §7 tenant-scoped secret release. |
| S-003 hidden global state | CONFIRM-from-source | §3 inventories preferences/order/sync/export target state. |
| S-004 magic constants/operator override | CONFIRM-from-source | §2 S-12 and §5 require operator-visible policy. |
| S-005 fail-open manual values | CONFIRM-from-source | §7 says manual/unverified values cannot drive routing/billing by default. |
| S-006 inconsistent errors | CONFIRM-from-source | §4 consolidates observed errors; HUAKAI implication requires taxonomy. |

Owner 中文总结：本轮拆解的是 all-api-hub 的多 Provider Account 凭据库、跨来源价格比较、站点识别/浏览器挑战恢复、WebDAV/导入导出/下游导出等安全存储与 secret release 行为；真观察来自 15 个实际读过的公开文档、测试和源区域，合理推断集中在 §6 的 HUAKAI 适配风险并已逐条标注为 inference，未观察清楚的内容放入 §9 共 9 个 open questions；critic 的 C/F/D/N/S 全部逐项 CONFIRM-from-source 处置，没有为了凑深度编造行为，也没有功能缩水，clean-room 风险通过行为化描述和不携带上游路径/函数/结构来控制。
