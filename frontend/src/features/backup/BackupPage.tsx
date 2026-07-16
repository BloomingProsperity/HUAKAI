import { useEffect, useState } from 'react'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { StatCard } from '../../ui/StatCard'
import { getBackupManifest } from './api'
import { mapBackupTableRows, totalEstimatedRows, type BackupTableRow } from './backup'
import type { BackupManifest } from './types'

/*
 * 备份与恢复(运营台 · 系统)。只读展示"可备份清单":schema/迁移版本、表清单 + 行数估算、
 * 脱敏策略声明。**本页不导出任何业务数据、不触发备份、零写入**——只点亮备份能力的地基与边界。
 * 真正的数据导出 bundle 与恢复(写入)是后续 Owner-gated 切片。数据走 GET /v1/admin/backup/manifest
 * (platform_admin)。真码:backend/internal/backuphttp、backend/cmd/gateway/routes_backup.go。
 */

export function BackupPage() {
  const [data, setData] = useState<BackupManifest | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    setLoading(true)
    setError(null)
    getBackupManifest(ac.signal)
      .then((d) => setData(d))
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setError(e instanceof Error ? e.message : '加载备份 manifest 失败')
        setData(null)
      })
      .finally(() => {
        if (!ac.signal.aborted) setLoading(false)
      })
    return () => ac.abort()
  }, [])

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>备份与恢复</h1>
          <p className="hk-sub">
            运营台 · 只读「可备份清单」(表名 / 行数估算 / schema 版本 / 脱敏边界)。本页不导出任何业务数据、
            不触发备份;真正的数据导出与恢复为后续受控切片。
          </p>
        </div>
      </header>

      {loading && <p style={{ color: 'var(--hk-ink-500)' }}>加载中…</p>}
      {error && (
        <p style={{ color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)' }}>
          {error}
        </p>
      )}

      {!loading && !error && data && (
        <>
          <div style={{ display: 'flex', gap: 'var(--hk-space-4)', flexWrap: 'wrap' }}>
            <StatCard label="迁移版本" value={`#${data.schema_version}${data.schema_dirty ? ' (dirty)' : ''}`} tone={data.schema_dirty ? 'danger' : 'default'} />
            <StatCard label="表数量" value={String(data.table_count)} />
            <StatCard label="行数估算合计" value={totalEstimatedRows(data).toLocaleString()} />
          </div>
          <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }}>行数基准:{data.estimate_basis}</p>

          <section className="hk-card">
            <div className="hk-card__head"><h3>表清单</h3></div>
            <div style={{ maxHeight: 360, overflow: 'auto' }}>
              <DataListTable label="备份表清单" rows={mapBackupTableRows(data.tables)} rowKey={(row) => row.name} columns={backupColumns} />
            </div>
          </section>

          {/* 脱敏策略卡:左侧警示描边为风险语义,刻意保留以提示脱敏边界 */}
          <section className="hk-card" style={{ borderLeft: '4px solid var(--hk-warn)' }}>
            <div className="hk-card__head"><h3>脱敏策略</h3></div>
            <div className="hk-card__body">
              <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', marginTop: 0 }}>{data.redaction_policy.note}</p>
              <ul style={{ fontSize: 12, margin: 0, paddingLeft: 18, color: 'var(--hk-ink-700)' }}>
                {data.redaction_policy.redacted_columns.map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
            </div>
          </section>
        </>
      )}
    </div>
  )
}

const backupColumns: DataListColumn<BackupTableRow>[] = [
  { key: 'name', label: '表名', render: (row) => row.name },
  { key: 'rows', label: '行数估算', render: (row) => <span className="hk-mono">{row.estimatedRows}</span> },
]
