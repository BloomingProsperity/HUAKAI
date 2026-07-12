import { apiGet } from '../../lib/api'
import type { BackupManifest } from './types'

/*
 * 备份 manifest 数据访问层。端点 /v1/admin/backup/manifest 走 apiGet
 * → authHeaders(path) 按 /v1/admin/ 前缀自动注入 admin token(不裸 fetch,避 #143 漏鉴权坑)。
 * 真码:backend/internal/backuphttp、backend/cmd/gateway/routes_backup.go(adminGate platform_admin)。
 */

const MANIFEST = '/v1/admin/backup/manifest'

/** 取只读备份 manifest(元数据,零业务数据)。 */
export async function getBackupManifest(signal?: AbortSignal): Promise<BackupManifest> {
  return apiGet<BackupManifest>(MANIFEST, { signal })
}
