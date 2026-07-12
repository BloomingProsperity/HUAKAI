import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { batchRevokeApiKeys, listApiKeys, revokeApiKey } from './api'
import { buildBatchRevoke, isSelectable, summarizeBatchResult, toggleSelected } from './batch'
import { CreateKeyModal } from './CreateKeyModal'
import { EditKeyModal } from './EditKeyModal'
import type { ApiKeyView } from './types'

/*
 * API Key · 我的密钥(P0)。/v1/api-keys 列表 + 创建(一次性明文)+ 撤销。
 * 仅展示 key_prefix(脱敏),绝不回显明文(后端存 hash,物理不可逆)。
 */
export function KeysPage() {
  const [keys, setKeys] = useState<ApiKeyView[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<ApiKeyView | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)
  const [flash, setFlash] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listApiKeys(0, 100, signal)
      .then((resp) => setKeys(resp.api_keys))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载密钥列表失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const revoke = async (k: ApiKeyView) => {
    if (!window.confirm(`确认撤销 Key「${k.name}」(${k.key_prefix})?撤销后不可恢复。`)) return
    setBusyId(k.api_key_id)
    setError(null)
    try {
      await revokeApiKey(k.api_key_id, '')
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '撤销失败')
    } finally {
      setBusyId(null)
    }
  }

  // 仅活跃 Key 可被批量选中;勾选集随之过滤,避免对已撤销项操作。
  const selectableIds = keys.filter(isSelectable).map((k) => k.api_key_id)
  const allSelected = selectableIds.length > 0 && selectableIds.every((id) => selected.has(id))

  const batchRevoke = async () => {
    const ids = [...selected]
    const built = buildBatchRevoke(ids, '')
    if ('error' in built) {
      setError(built.error)
      return
    }
    if (!window.confirm(`确认批量撤销 ${ids.length} 个 Key?撤销后不可恢复。`)) return
    setBatchBusy(true)
    setError(null)
    setFlash(null)
    try {
      const resp = await batchRevokeApiKeys(built.ids, built.reason)
      setFlash(summarizeBatchResult(resp))
      setSelected(new Set())
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '批量撤销失败')
    } finally {
      setBatchBusy(false)
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>我的密钥</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            管线第 3 站 · 把账号池签发成可用密钥。共 {keys.length} 个。
          </p>
        </div>
        <button type="button" onClick={() => setCreateOpen(true)} style={newBtn}>
          ＋ 新建 Key
        </button>
      </header>

      {createOpen && (
        <CreateKeyModal onClose={() => setCreateOpen(false)} onCreated={() => setRefreshNonce((n) => n + 1)} />
      )}
      {editing && (
        <EditKeyModal
          apiKey={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            setRefreshNonce((n) => n + 1)
          }}
        />
      )}

      {error && (
        <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
          {error}
        </div>
      )}
      {flash && (
        <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }}>
          {flash}
        </div>
      )}
      {selected.size > 0 && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-2) var(--hk-space-4)', background: 'var(--hk-surface-sunken)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }}>
          <span style={{ fontSize: 13, color: 'var(--hk-ink-700)' }}>已选 {selected.size} 个</span>
          <button type="button" disabled={batchBusy} onClick={batchRevoke} style={revokeBtn}>
            {batchBusy ? '撤销中…' : '批量撤销'}
          </button>
          <button type="button" onClick={() => setSelected(new Set())} style={{ border: 'none', background: 'transparent', color: 'var(--hk-ink-500)', fontSize: 13, cursor: 'pointer' }}>
            清空选择
          </button>
        </div>
      )}

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
        {loading && keys.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : keys.length === 0 ? (
          <Empty>还没有密钥。点击右上角「新建 Key」创建第一个。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  <th style={{ ...th, width: 36 }}>
                    <input
                      type="checkbox"
                      checked={allSelected}
                      aria-label="全选活跃 Key"
                      onChange={(e) => setSelected(e.target.checked ? new Set(selectableIds) : new Set())}
                    />
                  </th>
                  {['名称', '前缀', '状态', '过期', '最近使用', '创建时间', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {keys.map((k) => (
                  <tr key={k.api_key_id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={{ ...td, textAlign: 'center' }}>
                      {isSelectable(k) && (
                        <input
                          type="checkbox"
                          checked={selected.has(k.api_key_id)}
                          aria-label={`选择 ${k.name}`}
                          onChange={() => setSelected((s) => toggleSelected(s, k.api_key_id))}
                        />
                      )}
                    </td>
                    <td style={td}>
                      <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{k.name}</span>
                    </td>
                    <td style={td}>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{k.key_prefix}</code>
                    </td>
                    <td style={td}>
                      <StatusBadge tone={statusTone(k.status)}>{statusLabel(k.status)}</StatusBadge>
                    </td>
                    <td style={td}>{fmt(k.expires_at) || '永不'}</td>
                    <td style={td}>{fmt(k.last_used_at) || '从未'}</td>
                    <td style={td}>{fmt(k.created_at)}</td>
                    <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                      {k.status === 'active' && (
                        <>
                          <button type="button" disabled={busyId === k.api_key_id} onClick={() => setEditing(k)} style={editBtn}>
                            编辑
                          </button>
                          <button type="button" disabled={busyId === k.api_key_id} onClick={() => revoke(k)} style={revokeBtn}>
                            撤销
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'active':
      return 'ok'
    case 'revoked':
      return 'muted'
    case 'expired':
      return 'danger'
    default:
      return 'muted'
  }
}
function statusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '活跃'
    case 'revoked':
      return '已撤销'
    case 'expired':
      return '已过期'
    default:
      return status
  }
}

function fmt(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('zh-CN', { hour12: false })
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle', whiteSpace: 'nowrap', color: 'var(--hk-ink-700)' }
const newBtn: React.CSSProperties = { height: 36, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const revokeBtn: React.CSSProperties = { height: 28, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-danger)', fontSize: 12, cursor: 'pointer' }
const editBtn: React.CSSProperties = { height: 28, padding: '0 var(--hk-space-3)', marginRight: 'var(--hk-space-2)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-primary-700)', fontSize: 12, cursor: 'pointer' }
