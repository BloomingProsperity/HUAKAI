/*
 * 备份只读 manifest 类型。对应后端 GET /v1/admin/backup/manifest
 * (backend/internal/backuphttp/handler.go manifestResponse)。纯元数据,零业务数据。
 */

export interface BackupTable {
  name: string
  estimated_rows: number
}

export interface RedactionPolicy {
  note: string
  redacted_columns: string[]
}

export interface BackupManifest {
  object: string
  schema_version: number
  schema_dirty: boolean
  estimate_basis: string
  table_count: number
  tables: BackupTable[]
  redaction_policy: RedactionPolicy
}
