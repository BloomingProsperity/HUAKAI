# All API Hub reference delta

## Repo snapshot

- Repo: `.omc/reference-src/all-api-hub`
- Branch: `main`
- Commit: `9f397c95c211`
- Tag: `nightly-2-g9f397c95`
- File count: `1956`
- State: clean.

## Source areas read

- Credential profile and telemetry: `.omc/reference-src/all-api-hub/src/types/apiCredentialProfiles.ts`, `.omc/reference-src/all-api-hub/src/services/apiCredentialProfiles/*`
- Account operations/defaults/dedupe/key repair: `.omc/reference-src/all-api-hub/src/services/accounts/*`
- Auto check-in: `.omc/reference-src/all-api-hub/src/types/autoCheckin.ts`, `.omc/reference-src/all-api-hub/src/services/checkin/autoCheckin/*`, `.omc/reference-src/all-api-hub/src/features/AutoCheckin/*`
- WebDAV import/export/sync: `.omc/reference-src/all-api-hub/src/services/webdav/*`, `.omc/reference-src/all-api-hub/src/features/ImportExport/components/WebDAVSettings.tsx`
- Managed site/model sync and token export: `.omc/reference-src/all-api-hub/src/features/ManagedSiteModelSync/*`, `.omc/reference-src/all-api-hub/src/services/managedSites/*`, `.omc/reference-src/all-api-hub/src/types/managedSiteTokenBatchExport.ts`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | Credential profiles define telemetry modes, read-only custom endpoints, JSON path maps, snapshots, attempts, and persisted profiles. | `.omc/reference-src/all-api-hub/src/types/apiCredentialProfiles.ts:9`, `:17`, `:30`, `:75`, `:101` |
| source-confirmed | Telemetry config restricts custom endpoint URLs to the profile base URL origin and validates JSON path maps. | `.omc/reference-src/all-api-hub/src/services/apiCredentialProfiles/telemetryConfig.ts:24`, `:44`, `:65`, `:106` |
| source-confirmed | Telemetry reads OpenAI-compatible, New API, Sub2API, and custom JSON-path usage/balance modes with redacted errors. | `.omc/reference-src/all-api-hub/src/services/apiCredentialProfiles/telemetry.ts:171`, `:239`, `:285`, `:327`, `:456`, `:547` |
| source-confirmed | Account operations can auto-detect account auth, support Sub2API access-token mode, cookie/access-token/no-auth types, token info, refresh token, and validation. | `.omc/reference-src/all-api-hub/src/services/accounts/accountOperations.ts:121`, `:163`, `:181`, `:201`, `:236`, `:377` |
| source-confirmed | Account defaults normalize quota, today's stats, site type, auth type, persisted account, and updates. | `.omc/reference-src/all-api-hub/src/services/accounts/accountDefaults.ts:17`, `:82`, `:152`, `:253`, `:281` |
| source-confirmed | Duplicate account scan/delete exists. | `.omc/reference-src/all-api-hub/src/services/accounts/accountDedupe.ts:14`, `:148`, `:193` |
| source-confirmed | Auto check-in has statuses, skip reasons, retry state, account snapshots, schedule mode, retry strategy, storage, daily alarm, manual run, retry account, and UI flows. | `.omc/reference-src/all-api-hub/src/types/autoCheckin.ts:13`, `:216`, `:289`, `.omc/reference-src/all-api-hub/src/services/checkin/autoCheckin/scheduler.ts:921`, `:1264`, `:1512`, `:2415` |
| source-confirmed | WebDAV sync supports Basic auth, backup directory creation, encrypted upload/download envelopes, selected-section merge, retry decrypt, and settings UI. | `.omc/reference-src/all-api-hub/src/services/webdav/webdavService.ts:12`, `:235`, `:320`, `:351`, `.omc/reference-src/all-api-hub/src/services/webdav/webdavSelectiveSync.ts:486`, `.omc/reference-src/all-api-hub/src/features/ImportExport/components/WebDAVSettings.tsx:598` |
| source-confirmed | Managed site/model sync has selected/manual run flows, failed-channel retry, history, stats, and route preselection tests. | `.omc/reference-src/all-api-hub/src/features/ManagedSiteModelSync/ManagedSiteModelSync.tsx:258`, `:412`, `:450`, `:630`, `.omc/reference-src/all-api-hub/tests/features/ManagedSiteModelSync/ManagedSiteModelSync.test.tsx:191`, `:381`, `:491` |
| source-confirmed | Managed site providers implement search/create/update/delete channel, fetch secret key, fetch models, import, and auto-config for several target products. | `.omc/reference-src/all-api-hub/src/services/managedSites/providers/newApi.ts:62`, `:88`, `:158`, `:296`, `:476`, `.omc/reference-src/all-api-hub/src/services/managedSites/providers/veloera.ts:46` |
| source-confirmed | Token batch export defines preview/execution statuses, warning codes, blocked reasons, and execution results. | `.omc/reference-src/all-api-hub/src/types/managedSiteTokenBatchExport.ts:6`, `:16`, `:27`, `.omc/reference-src/all-api-hub/src/services/managedSites/tokenBatchExport.ts:353`, `:399` |

## Inferred features

- inferred: All API Hub is mostly operator tooling around other gateways, not a core gateway. It is useful for admin workflows, telemetry probes, model sync, and import/export UX.
- inferred: Its client-side credential model is an anti-pattern for HUAKAI server core. HUAKAI should use KMS/envelope encryption and server-side audit instead of browser-local secret storage.

## Open questions

- open-question: Need deeper reading of encryption primitives before using WebDAV as inspiration.
- open-question: Auto check-in legality/product fit depends on provider terms and should be product-reviewed before being added to HUAKAI core.

## HUAKAI delta

- `F-OPS-003`, `F-OPS-004`, `F-EXPORT-001`, and `F-SYNC-001` exist in `docs/03_FEATURE_PARITY_MATRIX.md`, but should be framed as admin/plugin layer, not core gateway L1.
- Custom telemetry JSON paths are useful for external account health, but must be same-origin and SSRF-guarded if server-side.
- Managed site sync is more useful as a migration/operator tool than as runtime routing logic.

## Suggested Feature IDs

| Feature ID | Name | Level | Delta |
| --- | --- | --- | --- |
| `F-OPS-TELEMETRY-001` | External account telemetry profile | L3 | Same-origin read-only endpoint, JSON path map, redacted attempt history, balance/usage snapshots. |
| `F-OPS-AUTOCHECK-001` | Provider account check-in workflow | L3 | Manual/daily schedule, retry state, skip reasons, snapshots; keep outside L1 core. |
| `F-SYNC-SEC-001` | Encrypted config import/export/sync | L4 | Server-side envelope encryption, selected-section merge, audit, and no plaintext browser secret dependency. |
| `F-MANAGED-SITE-001` | Managed gateway/channel sync tool | L3/L4 | Search/create/update channels, fetch models, retry failed sync, preview before write. |
| `F-TOKEN-EXPORT-001` | Token batch export/probe | L3 | Preview/execution warnings, blocked reasons, rate limits, and audit. |
