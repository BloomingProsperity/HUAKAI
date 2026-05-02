# All API Hub operator tooling / anti-pattern deep dive

## Snapshot

- Reference repo: `.omc/reference-src/all-api-hub`
- Branch: `main`
- Commit: `9f397c95c211`
- Tag / describe: `nightly-2-g9f397c95`
- Tracked file count: `2100`
- State: clean
- Review mode: clean-room behavior extraction only. This project is useful for operator workflows, but its browser/local secret model is not appropriate for HUAKAI server core.

## Source areas read

- Credential telemetry:
  - `.omc/reference-src/all-api-hub/src/types/apiCredentialProfiles.ts`
  - `.omc/reference-src/all-api-hub/src/services/apiCredentialProfiles/telemetryConfig.ts`
  - `.omc/reference-src/all-api-hub/src/services/apiCredentialProfiles/telemetry.ts`
- Account operations and defaults:
  - `.omc/reference-src/all-api-hub/src/services/accounts/accountOperations.ts`
  - `.omc/reference-src/all-api-hub/src/services/accounts/accountDefaults.ts`
  - `.omc/reference-src/all-api-hub/src/services/accounts/accountDedupe.ts`
- Auto check-in:
  - `.omc/reference-src/all-api-hub/src/types/autoCheckin.ts`
  - `.omc/reference-src/all-api-hub/src/services/checkin/autoCheckin/scheduler.ts`
- WebDAV sync:
  - `.omc/reference-src/all-api-hub/src/services/webdav/webdavService.ts`
  - `.omc/reference-src/all-api-hub/src/services/webdav/webdavSelectiveSync.ts`
  - `.omc/reference-src/all-api-hub/src/features/ImportExport/components/WebDAVSettings.tsx`
- Managed site/model sync and token batch export:
  - `.omc/reference-src/all-api-hub/src/features/ManagedSiteModelSync/ManagedSiteModelSync.tsx`
  - `.omc/reference-src/all-api-hub/src/services/managedSites/providers/newApi.ts`
  - `.omc/reference-src/all-api-hub/src/services/managedSites/providers/veloera.ts`
  - `.omc/reference-src/all-api-hub/src/types/managedSiteTokenBatchExport.ts`
  - `.omc/reference-src/all-api-hub/src/services/managedSites/tokenBatchExport.ts`
  - `.omc/reference-src/all-api-hub/tests/features/ManagedSiteModelSync/ManagedSiteModelSync.test.tsx`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | API credential profiles store telemetry mode, custom endpoint, JSON path map, attempts, snapshots, balance/usage/model telemetry, base URL, and secret API key. | `.omc/reference-src/all-api-hub/src/types/apiCredentialProfiles.ts:9`, `:17`, `:30`, `:38`, `:63`, `:75`, `:81`, `:90`, `:108`, `:110`, `:112`, `:118`, `:119` |
| source-confirmed | Profile security note explicitly says `apiKey` is stored in extension local storage and must be masked/log-safe. | `.omc/reference-src/all-api-hub/src/types/apiCredentialProfiles.ts:95`, `:98`, `:110` |
| source-confirmed | Custom telemetry endpoints are resolved under the profile origin; cross-origin endpoints are rejected. | `.omc/reference-src/all-api-hub/src/services/apiCredentialProfiles/telemetryConfig.ts:65`, `:74`, `:79`, `:80`, `:91`, `:98`, `:106`, `:123` |
| source-confirmed | Telemetry config trims and validates supported JSON path mappings before persistence. | `.omc/reference-src/all-api-hub/src/services/apiCredentialProfiles/telemetryConfig.ts:24`, `:34`, `:42`, `:44`, `:55`, `:56`, `:118` |
| source-confirmed | Telemetry fetches read-only endpoints with bearer auth, sanitizes endpoint errors, records attempts, and supports OpenAI-compatible, New API, Sub2API, and custom JSON path usage/balance modes. | `.omc/reference-src/all-api-hub/src/services/apiCredentialProfiles/telemetry.ts:148`, `:150`, `:185`, `:188`, `:239`, `:250`, `:257`, `:285`, `:324`, `:362`, `:407`, `:456`, `:516`, `:632`, `:713`, `:727`, `:730` |
| source-confirmed | Account operations auto-detect site/user/auth, force Sub2API into access-token mode, validate cookie/access-token/no-auth paths, normalize Sub2API refresh-token state, and persist quota/today stats. | `.omc/reference-src/all-api-hub/src/services/accounts/accountOperations.ts:119`, `:146`, `:161`, `:163`, `:181`, `:217`, `:236`, `:240`, `:309`, `:340`, `:368`, `:377`, `:390`, `:496`, `:620`, `:621`, `:624` |
| source-confirmed | Account defaulting normalizes quota, daily prompt/completion tokens, daily consumption, request count, income, auth type, and persisted account shape. | `.omc/reference-src/all-api-hub/src/services/accounts/accountDefaults.ts:19`, `:20`, `:21`, `:22`, `:23`, `:24`, `:95`, `:119`, `:154`, `:209`, `:240`, `:248` |
| source-confirmed | Duplicate account scan exists by origin URL and user ID. | `.omc/reference-src/all-api-hub/src/services/accounts/accountDedupe.ts:146` |
| source-confirmed | Auto check-in has status/result enums, skip reasons, run summaries, manual run messages, per-day retry state, account snapshots, daily/retry target days, schedule mode, and retry strategy. | `.omc/reference-src/all-api-hub/src/types/autoCheckin.ts:11`, `:26`, `:95`, `:112`, `:140`, `:175`, `:203`, `:216`, `:225`, `:241`, `:249`, `:260`, `:273`, `:293`, `:299`, `:305` |
| source-confirmed | Scheduler separates daily and retry alarms, uses local day boundary, guards in-flight daily runs, skips stale alarms, builds runnable/skip snapshots, and retries only today's failed accounts. | `.omc/reference-src/all-api-hub/src/services/checkin/autoCheckin/scheduler.ts:119`, `:188`, `:204`, `:254`, `:451`, `:465`, `:1758`, `:1770`, `:1800`, `:1902`, `:1912`, `:2023`, `:2031`, `:2052`, `:2077`, `:2237`, `:2415` |
| source-confirmed | WebDAV sync builds Basic auth, prepares backup directory, reads encrypted backup settings, tests connectivity, downloads raw/plain/encrypted content, uploads encrypted envelope when enabled, and preserves unselected remote sections during selective sync. | `.omc/reference-src/all-api-hub/src/services/webdav/webdavService.ts:12`, `:14`, `:75`, `:113`, `:183`, `:235`, `:237`, `:257`, `:276`, `:312`, `:320`, `:348`, `:351`, `:364`, `:371`, `.omc/reference-src/all-api-hub/src/services/webdav/webdavSelectiveSync.ts:486`, `:493`, `:613`, `:637`, `:704`, `:833` |
| source-confirmed | WebDAV UI supports save/test/upload/download/import, decrypt retry modal, optional persistence of decrypt password, and selected-section merge. | `.omc/reference-src/all-api-hub/src/features/ImportExport/components/WebDAVSettings.tsx:104`, `:206`, `:230`, `:246`, `:258`, `:299`, `:309`, `:315`, `:341`, `:343`, `:355`, `:370`, `:405`, `:441`, `:598`, `:689`, `:703` |
| source-confirmed | Managed site model sync UI has history/manual tabs, search/filter, route-based preselection, selected-row execution, single-channel execution, retry-failed action, stats/progress, and tests for failed-channel retry and preselection. | `.omc/reference-src/all-api-hub/src/features/ManagedSiteModelSync/ManagedSiteModelSync.tsx:52`, `:88`, `:97`, `:258`, `:306`, `:347`, `:359`, `:376`, `:412`, `:450`, `:530`, `:630`, `.omc/reference-src/all-api-hub/tests/features/ManagedSiteModelSync/ManagedSiteModelSync.test.tsx:191`, `:381`, `:491`, `:500` |
| source-confirmed | Managed-site token batch export has non-mutating preview, status/warning/blocking reason codes, concurrency mapping, secret resolution warnings, executable filtering, execution results, and skipped rows for non-executable items. | `.omc/reference-src/all-api-hub/src/types/managedSiteTokenBatchExport.ts:8`, `:43`, `:55`, `:58`, `:70`, `.omc/reference-src/all-api-hub/src/services/managedSites/tokenBatchExport.ts:38`, `:49`, `:99`, `:104`, `:111`, `:186`, `:211`, `:238`, `:260`, `:287`, `:350`, `:396`, `:399`, `:406`, `:448`, `:489` |

## Inferred items

- inferred: All API Hub is closer to an operator console / migration assistant / browser automation companion than a production gateway core.
- inferred: Its best transferable ideas are admin workflows: telemetry profiles, duplicate detection, selective sync, managed-site channel sync, and non-mutating previews before bulk writes.
- inferred: Its worst transferable pattern is browser/local secret custody. HUAKAI must keep secrets server-side with envelope encryption, role-gated reveal, and audit.

## Open questions

- open-question: Need deeper read of crypto helpers before judging WebDAV encryption strength.
- open-question: Auto check-in may violate upstream terms depending on provider. Product/legal review required before any HUAKAI feature spec.
- open-question: Need storage-layer read for how extension-local credentials are persisted and migrated.
- open-question: Need managed-site provider read beyond New API/Veloera before claiming full gateway sync breadth.

## HUAKAI delta

| HUAKAI area | Current status from plan files | Delta |
| --- | --- | --- |
| Credential custody | Architecture already flags All API Hub plaintext credential mode as forbidden and says KMS envelope encryption required. | Keep this as a hard production rule. Do not adopt browser/local secret storage or default export of credentials. |
| Admin incident workflow | `docs/17_FEATURE_LEVEL_MATRIX.md` wants investigation path at L3; project plan still lacks admin UI. | All API Hub shows the missing UX grain: selected rows, retry failed, search/filter, status snapshots, non-mutating preview before bulk write. |
| External account telemetry | Feature matrix has provider/account health and observability, but not custom read-only telemetry profile. | Add L3 profile model for same-origin telemetry endpoint + JSON-path extraction + redacted attempts. |
| Import/export/sync | Existing plan references export/sync but not selective merge semantics. | Need selected-section merge, remote-preserve behavior, encrypted envelope, and audit. |
| Managed gateway migration | HUAKAI focuses on running itself, not helping operators migrate from other gateways. | Add as L3/L4 operator tool after core admin is stable. |

## Recommended HUAKAI insertions

| Feature ID | Name | Level | Recommendation |
| --- | --- | --- | --- |
| `F-OPS-TELEMETRY-001` | External account telemetry profile | L3 | Same-origin read-only endpoint, JSON path map, balance/usage/model snapshot, redacted attempt history, SSRF guard if server-side. |
| `F-OPS-AUTOCHECK-001` | Provider account check-in workflow | L3 | Manual/daily schedule, skip reasons, per-day retry state, account snapshots. Gate by provider terms and keep outside L1 core. |
| `F-SYNC-SEC-001` | Encrypted selective import/export/sync | L4 | Server-side envelope encryption, selected-section merge, remote section preservation, decrypt retry workflow, audit. No plaintext browser secret dependency. |
| `F-MANAGED-SITE-001` | Managed gateway/channel sync | L3/L4 | Operator can preview, select, run, retry failed, and inspect history for gateway/channel/model sync. Useful for migrations and admin ops. |
| `F-TOKEN-EXPORT-001` | Token batch export/probe | L3 | Non-mutating preview, warning/blocking reasons, concurrency limits, per-row results, and audit trail. |
| `F-OPS-DEDUPE-001` | Account duplicate detection and repair | L2 | Detect same origin/user/provider accounts, show operator-safe merge/delete candidates, require confirmation and audit. |

## Anti-patterns HUAKAI should explicitly reject

- Browser/local storage secret custody for provider keys, refresh tokens, session cookies, or WebDAV passwords.
- Auto check-in or browser automation as a default core gateway capability without provider terms review.
- Bulk channel/token export that writes before previewing blocked/warning rows.
- Custom telemetry endpoint support without same-origin restriction, SSRF checks, timeout, redaction, and bounded response body.

## Production reviewer critique

All API Hub is valuable because it is full of operator details that core gateway plans often miss: "retry failed rows", "manual preselect by route param", "do not wipe remote sections during selective sync", "persist an attempt list when telemetry fails", "show blocked reasons before batch export".

But it should not be treated as a server architecture reference. For HUAKAI, borrow the workflow ergonomics and evidence fields; reject the browser secret custody model.
