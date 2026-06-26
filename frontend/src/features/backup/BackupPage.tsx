import { useEffect, useState } from 'react'
import { getBackupManifest } from './api'
import { totalEstimatedRows } from './backup'
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
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>备份与恢复</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          运营台 · 只读「可备份清单」(表名 / 行数估算 / schema 版本 / 脱敏边界)。本页不导出任何业务数据、
          不触发备份;真正的数据导出与恢复为后续受控切片。
        </p>
      </header>

      {loading && <p style={{ color: 'var(--hk-ink-500)' }}>加载中…</p>}
      {error && (
        <p style={{ color: 'var(--hk-danger, #c0392b)', background: 'var(--hk-danger-bg, #fdecea)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-2)' }}>
          {error}
        </p>
      )}

      {!loading && !error && data && (
        <>
          <div style={{ display: 'flex', gap: 'var(--hk-space-4)', flexWrap: 'wrap' }}>
            <Stat label="迁移版本" value={`#${data.schema_version}${data.schema_dirty ? ' (dirty)' : ''}`} warn={data.schema_dirty} />
            <Stat label="表数量" value={String(data.table_count)} />
            <Stat label="行数估算合计" value={totalEstimatedRows(data).toLocaleString()} />
          </div>
          <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }}>行数基准:{data.estimate_basis}</p>

          <section>
            <h2 style={{ fontSize: 15, marginBottom: 'var(--hk-space-2)' }}>表清单</h2>
            <div style={{ border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', overflow: 'auto', maxHeight: 360 }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                <thead>
                  <tr style={{ position: 'sticky', top: 0, background: 'var(--hk-surface, #fff)', textAlign: 'left' }}>
                    <th style={th}>表名</th>
                    <th style={{ ...th, textAlign: 'right' }}>行数估算</th>
                  </tr>
                </thead>
                <tbody>
                  {data.tables.map((t) => (
                    <tr key={t.name} style={{ borderTop: '1px solid var(--hk-line)' }}>
                      <td style={td}>{t.name}</td>
                      <td style={{ ...td, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>{t.estimated_rows.toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          <section style={{ border: '1px solid var(--hk-line)', borderLeft: '4px solid var(--hk-warn, #b8860b)', borderRadius: 'var(--hk-radius-2)', padding: 'var(--hk-space-4)' }}>
            <h2 style={{ fontSize: 15, marginTop: 0, marginBottom: 'var(--hk-space-2)' }}>脱敏策略</h2>
            <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', marginTop: 0 }}>{data.redaction_policy.note}</p>
            <ul style={{ fontSize: 12, margin: 0, paddingLeft: 18, color: 'var(--hk-ink-700)' }}>
              {data.redaction_policy.redacted_columns.map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
          </section>
        </>
      )}
    </div>
  )
}

function Stat({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div style={{ border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-3)', padding: 'var(--hk-space-3) var(--hk-space-4)', minWidth: 140 }}>
      <div style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}</div>
      <div style={{ fontSize: 22, fontWeight: 600, color: warn ? 'var(--hk-danger, #c0392b)' : 'var(--hk-ink-900)' }}>{value}</div>
    </div>
  )
}

const th: React.CSSProperties = { padding: '8px 12px', fontWeight: 600, color: 'var(--hk-ink-700)' }
const td: React.CSSProperties = { padding: '6px 12px' }
