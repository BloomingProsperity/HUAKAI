# `all-api-hub` — Multi-Account Credential Vault + Cross-Source Comparison (Claude deep decomposition)

| Field | Value |
| --- | --- |
| Status | Deep decomposition (Claude lane, peer to Codex R3 specifier output) |
| Reference | All API Hub (AGPL-3.0, [E-LIC-003](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | F-OPS-003 + propose F-KEY-002 + F-EXPORT-001 |
| Specifier session | Claude PM-Orchestrator (Opus), 2026-04-29 |
| Source-reading delegate | Sonnet Explore agent — read 9+ source files (~30min) |
| Companion artifacts | docs/decompositions/all-api-hub/credential-vault-comparison-source-verified.md (Codex R3), .omc/artifacts/decomp-critic/C6-aah-credential-vault.md (Codex critic) |
| **Truth-discipline** | **Observed regions: 9** / **Inferences: 1** / **Open questions: 5** / **Critical finding: credentials stored plaintext** |

> **Headline truth-first finding**: Sonnet's source read confirmed **NO at-rest encryption** of credentials — API keys persist as plaintext JSON in `browser.storage.local`. This is a foundational security divergence HUAKAI must NOT inherit.

> **License**: AGPL-3.0 — Safe Equivalent only.

> **Scope note**: All API Hub is a CLIENT-SIDE browser extension, not a server gateway. Its relevance to HUAKAI is operator-UX patterns (Personal Edition admin), not gateway core.

---

## 1. WHY (motivation)

All API Hub solves a problem orthogonal to gateway core: an end user juggling multiple relay-station accounts (operator on platform A, operator on platform B, etc.) needs a unified view of balance / usage / pricing across them. The browser extension architecture is chosen because:

**Pressure 1 — local-first by privacy posture**: Users don't trust a centralized service to hold all their relay-station API keys. A local extension keeps credentials on-device `[region-2, region-9]`.

**Pressure 2 — site-detection automation**: Manually re-entering credentials per site is tedious; the extension auto-recognizes known relay-station UIs and offers in-context credential capture `[region-3]`.

**Pressure 3 — cross-source comparison without server-side aggregation**: Calling each registered site's billing/usage API directly from the browser produces a unified view with no central server intermediary `[region-5]`.

The trade-off accepted: **plaintext storage** in browser.storage.local. The product treats OS-level browser-data protection (Chrome's per-user encryption at OS level) as sufficient. HUAKAI's multi-tenant SaaS context cannot accept this — see §6 R-1.

---

## 2. WHAT (architecture in HUAKAI vocabulary)

### Sub-behaviors S-1..S-12 (observed-only)

**S-1: Manifest V3 with broad permissions** `[region-1]`. Extension manifest declares: storage, tabs, alarms, contextMenus, sidePanel; optional cookies + declarativeNetRequest; `host_permissions: ["<all_urls>"]`. The all-URLs permission is over-broad — strictly the extension only needs API endpoints of registered relay sites + content-script execution on detected sites.

**S-2: Plaintext credential profile in browser.storage.local** `[region-2]`. Each profile shape:
- id (UUID) + name + apiType (openai / google / anthropic / ...) + baseUrl + apiKey (plaintext) + tagIds + notes + telemetryConfig + telemetrySnapshot.

Storage backend is `browser.storage.local` via `@plasmohq/storage` wrapper. Stored as JSON. NO encryption layer between profile object and storage.

**S-3: Profile config versioning** `[region-2]`. Schema migrations supported via `API_CREDENTIAL_PROFILES_CONFIG_VERSION` constant. When the schema upgrades, an in-place migration runs on first read.

**S-4: Site recognition by URL + model-list match** `[region-3]`. Two-signal heuristic:
- URL match: `findManagedSiteChannelsByBaseUrl()` normalizes URLs and compares.
- Model list match: `inspectManagedSiteChannelModelsMatch()` fuzzy-matches the page's model catalog against registered profiles (similarity threshold 0.5).
- Match levels: EXACT / SECONDARY / FUZZY / NONE.

The dual-signal design tolerates URL changes (e.g., site rename) by falling back to model-list correlation.

**S-5: Cross-source data collection by direct API calls** `[region-5]`. For each profile, the extension calls the relay site's backend via Bearer-token-authenticated HTTP requests. Endpoint paths varied per site type:
- OpenAI-compat / one-api: `/v1/dashboard/billing/subscription`, `/v1/dashboard/billing/usage`, `/v1/models`
- new-api: `/api/usage/token/`
- Sub2API: `/v1/usage`
- Custom: user-defined JSON-path mapping for non-standard endpoints

NO DOM scraping observed. Pure API requests.

**S-6: Custom endpoint with JSON path mapping** `[region-2, region-5]`. For sites without a standard billing API, the user can declare:
- `endpoint`: a relative path or same-origin URL
- `jsonPaths`: a map of `{field: "path.to.value"}` extracting from response

This makes the vault extensible to new sites without code changes.

**S-7: USD normalization + envelope unwrapping** `[region-5]`. Aggregation steps:
- Quota units divided by per-site exchange rate constant
- `.data` envelope unwrapping for sites that wrap responses
- Timestamp normalization (seconds → milliseconds)
- Soft fallback on 404/405 (mark endpoint unsupported, continue)

**S-8: Refresh cadence (alarm + on-demand)** `[region-2, region-1]`. Background service worker uses Chrome `alarms` API for periodic polling (probably hourly/daily — exact period not surfaced in source read). UI also offers manual refresh.

**S-9: Telemetry snapshot persistence** `[region-2]`. Each profile carries a `telemetrySnapshot` with: balanceUsd, todayCostUsd, todayRequests, todayTokens, unlimitedQuota, expiresAt, attempts (per-source attempt log with sanitized endpoint + status + message).

The snapshot is the cached display surface; UI reads from snapshots, not from live API on every render.

**S-10: Endpoint sanitization for telemetry logs** `[region-2]`. Before persisting an attempt, `sanitizeTelemetryEndpoint()` redacts query parameters and trims base URLs from error messages. Secrets never logged in production code.

**S-11: Backup / export in JSON v2.0 format** `[region-6]`. User can export a backup containing accounts + preferences + channelConfigs + apiCredentialProfiles. Selective inclusion: user can exclude credentials from export (UI preference). Import handlers for V1 (legacy) and V2.

**S-12: WebDAV sync (opt-in, scope-limited)** `[region-6]`. Optional cloud sync via WebDAV. Source read explicitly notes: credentials NOT sent to WebDAV during export (excluded from cloud backup) — local-only for keys; sync covers config and preferences.

### 2-bis Lifecycle traces (2 observed, 1 marked open)

**L-1 New profile registration**: User enters base URL + API key in extension UI → profile saved to browser.storage.local → background fetches initial telemetry → snapshot populated → UI updates with first balance/usage data.

**L-2 Cross-source dashboard**: User opens sidepanel/popup → all profiles' snapshots loaded from local storage → tabular display with USD-normalized balance + today's cost/requests/tokens. Background timer triggers next refresh.

**L-3 Encryption-at-rest** — moved to §9 Q-1 (NOT observed; doesn't exist).

---

## 3. INPUTS

**Per-Profile state**: id, name, apiType, baseUrl, apiKey (PLAINTEXT), telemetryConfig, telemetrySnapshot, tagIds, notes, lastRefreshAt.

**Per-Process state**: in-memory copies of profiles (loaded from storage at extension start), alarm schedule, current refresh in-flight set.

**Per-Site recognition state**: URL fingerprint registry, model-catalog cache.

**Persistent state**: browser.storage.local (key: `api_credential_profiles`); optional WebDAV mirror for non-credential config.

**Configuration inputs**: telemetry mode (automatic / custom / manual-disabled), custom endpoint config, refresh cadence, UI preferences.

---

## 4. FAILURE MODES

| FM-id | Trigger | Observable outcome | Operator signal | Recovery | Blast radius |
|---|---|---|---|---|---|
| FM-1 | Browser data leaked / device compromised | All API keys readable as plaintext from extension storage | none until external incident | rotate all keys | all profiles on device |
| FM-2 | Site changes its billing API shape | Default extractor returns 0; user sees stale balance | UI shows N/A or last-good | manual config update or new release | one profile |
| FM-3 | Extension upgraded across config schema versions | Profile migration must run on first read; failure → orphan profiles | log only | manual recovery | per-user installation |
| FM-4 | WebDAV sync conflict (mid-edit on two devices) | Last-writer-wins; one device's edits clobbered | no UI conflict resolution | user notice + re-edit | per-config |
| FM-5 | Custom JSON-path mapping incorrect | Wrong field extracted (e.g., quota vs balance swap) | UI displays wrong number | user fix | per-profile |
| FM-6 | Site requires cookie-based auth not Bearer | API call fails 401; profile marked degraded | UI status indicator | switch to cookie auth flow | per-profile |
| FM-7 | Rate-limit on relay site's billing API | Repeated 429 from polling; UI may show stale | metric attempt log | reduce poll cadence | per-profile |
| FM-8 | host_permissions over-broad (all URLs) | Extension can read every site's content; potential supply-chain risk if extension itself is compromised | none preventive | scope down permissions | all browser sessions |

---

## 5. INTERFACES TO HUAKAI

**Personal Edition (HIGH relevance)**:
- HUAKAI's Personal Edition operator manages multiple upstream provider accounts. The all-api-hub UX patterns (per-account vault, per-account telemetry snapshot, custom endpoint mapping) inform HUAKAI's admin UI design (Phase 7+).
- The dual-signal site recognition (URL + model-list) is irrelevant to HUAKAI Personal (operator manually adds providers; no auto-detect).

**SaaS Edition (LOW relevance)**:
- A SaaS tenant operator manages their own provider accounts; tenant-isolation pushes vault server-side (PostgreSQL with at-rest encryption), not client-side.
- All-api-hub's local-first model is structurally INCOMPATIBLE with multi-tenant SaaS — credentials must live on the platform with proper KMS, not on operator's browser.

**Cross-feature**:
- F-AUTH-005: HUAKAI's `provider_accounts.credentials_encrypted` column (in observability-billing.sql) is the SERVER-SIDE analog. Encryption: at-rest KMS (DR-006 PostgreSQL column-level encryption); never plaintext.
- F-EXPORT-001 backup: HUAKAI's admin export must NEVER include plaintext credentials in export blobs. Exclude or redact analogous to all-api-hub's "exclude from WebDAV" pattern.

---

## 6. RISKS HUAKAI MUST GUARD AGAINST

**R-1 [Plaintext storage IS UNACCEPTABLE for HUAKAI]**: All-api-hub's choice to store API keys plaintext is acceptable for a single-user browser extension under OS-level data protection. HUAKAI's SaaS Edition cannot inherit this — credentials MUST be encrypted at rest with envelope encryption (DEK + KMS-managed KEK). HUAKAI's `provider_accounts.credentials_encrypted` column is bytea; encryption layer wraps before INSERT.

**R-2 [DR-001 multi-tenant — vault scope]**: All-api-hub's vault is per-user (one user → many profiles). HUAKAI multi-tenant: each tenant has their own vault scoped by tenant_id; cross-tenant read is impossible at row-level filter (already in HUAKAI schema).

**R-3 [Custom JSON-path mapping (S-6) attack surface]**: All-api-hub allows users to declare arbitrary JSON paths against arbitrary endpoints. In HUAKAI multi-tenant, an operator declaring a custom endpoint that points to a colleague's tenant's API would be a privilege-escalation. HUAKAI MUST: (a) constrain custom endpoints to operator's own tenant_id scope; (b) audit endpoint declarations; (c) deny same-origin to platform admin endpoints.

**R-4 [host_permissions over-broad (S-1, FM-8)]**: All-api-hub's `<all_urls>` permission is excessive. If HUAKAI ever ships a browser extension companion (Phase 9+ idea), permissions MUST be scoped to declared relay-station hosts only.

**R-5 [Plaintext export (FM-1, S-11)]**: All-api-hub allows users to opt-in to credential export. HUAKAI MUST default-EXCLUDE credentials from any export blob; require explicit operator confirmation + audit row for any export including credentials; even with confirmation, export should encrypt with operator-supplied passphrase.

**R-6 [Telemetry sanitization completeness (S-10)]**: All-api-hub sanitizes endpoint logs but stores attempts in plain JSON. Sanitization is the only barrier between an attacker reading logs and seeing endpoint structures. HUAKAI's audit-grade billing_event row uses redaction policy (F-AUTH-005 sanitizer pattern); apply same to operator-UI logs.

**R-7 [WebDAV plaintext sync (S-12, FM-4)]**: HUAKAI's any cross-device sync (if implemented Phase 9+) MUST encrypt the payload before transmission, not just exclude credentials. Sub-config + preferences may also be sensitive (e.g., custom endpoint declarations reveal infrastructure).

---

## 7. SAFE ADAPTATION (concrete divergences)

1. **Server-side vault with KMS-encrypted credentials** (`provider_accounts.credentials_encrypted` bytea + envelope encryption).
2. **Tenant-scoped vault** — every read/write filtered by tenant_id; no cross-tenant access at row level.
3. **Constrained custom endpoint** — restricted to declared upstream domains, scope-checked at admin API.
4. **Default-exclude credentials from export** + operator-passphrase encrypted export blob.
5. **Audit row for every credential CRUD + export** event.
6. **Scoped browser extension permissions** if HUAKAI ships one (Phase 9+ idea).
7. **Adopt UX patterns**: per-profile telemetry snapshot, USD normalization, custom endpoint flexibility — at the UI layer only, with HUAKAI server enforcing all security boundaries.
8. **Adopt site-recognition UX (Personal Edition only)** — operator UI suggests known provider patterns; the dual-signal heuristic is overkill for HUAKAI's manually-declared providers.

---

## 8. EVIDENCE LEDGER ROWS

- **E-AAH-001 (existing — promote)**: Multi-account dashboard pattern.
- **E-AAH-DEEP-NEW-1**: Plaintext browser.storage.local credential storage `[region-2]` — **counter-evidence** for HUAKAI's encrypted-at-rest requirement.
- **E-AAH-DEEP-NEW-2**: Custom JSON-path mapping for site-specific extractors `[region-2, region-5]` — UX pattern relevant to HUAKAI Personal admin.
- **E-AAH-DEEP-NEW-3**: Per-profile telemetry snapshot model `[region-2]` — UX pattern.
- **E-AAH-DEEP-NEW-4**: WebDAV sync excluding credentials by default `[region-6]` — confirms "credentials never in cross-device sync" principle.

---

## 9. OPEN QUESTIONS

1. **Q-1 At-rest encryption**: Sonnet's read found NO encryption layer. Confirm definitively — is this a documented design choice or oversight? (HUAKAI's adaptation is the same regardless: server-side encryption.)
2. **Q-2 Cookie-based auth flow**: source mentions `cookieAuthSessionCookie` and `cookieInterceptor.ts` but full mechanism not traced. Affects HUAKAI's understanding of how extension captures session for sites that don't expose Bearer auth.
3. **Q-3 host_permissions justification**: is `<all_urls>` truly necessary or over-privileged? (HUAKAI's extension if any will start narrow.)
4. **Q-4 Refresh cadence default**: source did not surface exact poll interval. Affects expectation of telemetry freshness.
5. **Q-5 WebDAV credential exclusion guarantee**: source asserts credentials excluded from WebDAV; source-level confirmation of sync-payload structure not fully traced. (HUAKAI's approach: server-side, no extension-WebDAV pattern.)

---

## 10. SOURCE COVERAGE PROOF (Sonnet Explore agent, ~30min, 9+ files)

| Region | URL | Contribution |
|---|---|---|
| region-1 | github.com/qixing-jk/all-api-hub/main/wxt.config.ts | Manifest V3 + permissions + alarms |
| region-2 | .../src/services/apiCredentialProfiles/apiCredentialProfilesStorage.ts | Profile CRUD; storage subscription; config versioning |
| region-3 | .../src/services/managedSites/channelMatch.ts + channelMatchResolver.ts | Site recognition heuristic |
| region-4 | .../src/services/apiCredentialProfiles/modelCatalog.ts | Model catalog normalization |
| region-5 | .../src/services/apiCredentialProfiles/telemetry.ts | Cross-source aggregation; per-site endpoint variants |
| region-6 | .../src/services/importExport/importExportService.ts | Backup format + WebDAV sync |
| region-7 | .../src/entrypoints/content/index.ts | Content script + feature toggles |
| region-8 | .../src/entrypoints/sidepanel + popup + options | UI dispatch entrypoints |
| region-9 | .../src/services/apiCredentialProfiles/telemetryConfig.ts | Telemetry mode configuration |

---

## 11. ROUND-2 CRITIC FINDINGS (C6 all-api-hub)

> Codex critic file at `.omc/artifacts/decomp-critic/C6-aah-credential-vault.md`. This Claude-deep is independent. Synthesis stage merges Codex specifier-deep + C6 critic + this Claude-deep.

---

## Owner Chinese summary

本 deep 拆解依据 Sonnet Explore agent 真读 9+ 个 all-api-hub 源文件（30min），由我（Claude Opus）合成 12 个 sub-behavior + 3 个 lifecycle + 8 个 failure 模式 + 7 个 HUAKAI-fit 风险 + 8 项 safe adaptation。**头号发现 — 安全反例**：所有 API key **明文存在 browser.storage.local**，**没有任何加密层**——这是单用户浏览器扩展在 OS 级数据保护下的合理选择，但 HUAKAI 多租户 SaaS **绝不能继承**。HUAKAI MUST：(1) 服务端 KMS 加密凭证（envelope encryption + provider_accounts.credentials_encrypted bytea，R-1）；(2) 租户隔离 row-level filter（R-2）；(3) custom JSON path 限制在租户自有 endpoint 内（R-3）；(4) export 默认排除凭证 + operator passphrase 加密（R-5）。可借鉴的 UX：per-profile 遥测快照、USD 标准化、custom endpoint 灵活性——但安全边界全在服务端，不在扩展。本文件未读 codex specifier 或 critic 输出。
