# All API Hub missed pass

## Version

- Branch: `main`
- Commit: `9f397c95c211`
- Tag: `nightly-2-g9f397c95`
- Files: 2100

## Source areas read

- Account/key auto-provisioning and repair.
- Sub2API client integration.
- Model sync and metadata.
- API credential profiles.
- Managed-site channel matching.
- WebDAV encrypted selective sync.
- Usage/balance history.

## Behavior-confirmed capabilities

- Sub2API integration hydrates auth from stored account state, refreshes near expiry, persists refreshed token data, serializes auth mutation by origin/token lock, and falls back to a full resync on 401 responses.
- Sub2API key management fetches tokens, user groups, and available models, and supports create, update, and delete operations as distinct API calls.
- Shared storage write lock uses the Web Locks API where available and falls back to an in-memory queue for single-context environments.
- API credential profile storage deduplicates profiles, supports import, merge, list, create, update, and delete, and stores telemetry snapshots alongside credentials.
- API credential model catalog fetches provider model IDs and builds a fallback pricing response from token-declared models combined with upstream-discovered models.
- Managed-site channel matching records URL, key, and model assessment signals with reasons including exact, contained, and similar model matches.
- Model metadata service caches remote metadata, builds lookup maps, detects vendor identity, and falls back to bundled defaults when remote fetch fails.
- WebDAV sync supports encrypted backup, selective section merge/import, conflict-preserving section handling, background alarm scheduling, best-effort upload, rollback on partial import failure, and status notification.
- Duplicate account scanning compares origin and user identity and supports configurable keep strategies.

## HUAKAI gap

All API Hub is not a gateway-core reference. Its value is product operations: account repair, key creation UX, model discovery, profile telemetry, backup/restore, duplicate cleanup, and human-facing health signals. HUAKAI's admin/frontend will be hard if these are treated as afterthoughts.

## Upgrade design

- Add admin workflows as first-class traces: "repair key", "resync credential", "duplicate account cleanup", "model catalog refresh", "export/backup".
- Server-side replacement for browser storage locks: use DB transaction/advisory lock/outbox for every read-modify-write workflow.
- Treat model metadata as a cached, versioned catalog with fallback and preview before routing.
- Build account health signals from URL/key/model/credential/capacity/usage axes, not one status string.

## Suggested Feature IDs

- `F-ADMIN-KEY-REPAIR-001` L2: admin key repair/resync flow.
- `F-ADMIN-ACCOUNT-DEDUPE-001` L3: duplicate account detection and safe merge/delete workflow.
- `F-MODEL-METADATA-CACHE-001` L2: model metadata cache with fallback defaults.
- `F-CONFIG-BACKUP-001` L3: encrypted export/import with section selection and rollback.
- `F-ADMIN-HEALTH-SIGNALS-001` L2: account/channel assessment signals.

## Acceptance test direction

- Resync credential under concurrent calls and assert only one mutation wins while others reuse result.
- Import backup with failure in the middle and assert rollback restores previous state.
- Model catalog refresh failure uses previous cache and emits visible stale status.

## Open questions

- Whether HUAKAI should include backup/export in Personal Edition L2 or postpone to L3.
- Whether account dedupe is needed before multi-tenant SaaS or only when imports are supported.

---
Source files read: all-api-hub src/services/apiService/sub2api/index, src/services/core/storageWriteLock, src/services/apiCredentialProfiles/apiCredentialProfilesStorage, src/services/apiCredentialProfiles/modelCatalog, src/services/managedSites/channelMatch, src/services/managedSites/channelAssessmentSignals, src/services/models/modelMetadata/ModelMetadataService, src/services/webdav/webdavBackupEncryption, src/services/webdav/webdavSelectiveSync, src/services/webdav/webdavAutoSyncService, src/services/accounts/accountDedupe
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
