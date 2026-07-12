import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getMyGroups } from './api'
import { ratioDisplay, ratioTone, userGroupLabel } from './megroups'
import type { MeGroupItem } from './types'

/*
 * 我的分组与倍率(用户门户 · 用量与配额)。只读展示当前用户等级(user_group)及其可达的模型分组,
 * 含计费倍率(仅运维标记公开者显示,否则「未公开」、绝不臆造默认值)。session 鉴权,身份后端派生。
 * 真码端点:backend/internal/megroupshttp/handler.go:66、backend/cmd/gateway/routes.go:208。
 */
export function MyGroupsPage() {
  const [userGroup, setUserGroup] = useState('')
  const [items, setItems] = useState<MeGroupItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    getMyGroups(ctrl.signal)
      .then((resp) => {
        setUserGroup(resp.user_group)
        setItems(resp.items)
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载分组信息失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [refreshNonce])

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>分组与倍率</h1>
          <p className="hk-sub">你的等级可调度的模型分组及计费倍率。倍率仅在运营方公开时展示。</p>
        </div>
        <button type="button" onClick={() => setRefreshNonce((n) => n + 1)} className="hk-btn" disabled={loading}>
          刷新
        </button>
      </header>

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-700)' }}>
        <span style={{ color: 'var(--hk-ink-500)' }}>当前等级</span>
        {loading && !userGroup ? <span style={{ color: 'var(--hk-ink-300)' }}>…</span> : <StatusBadge tone="ok">{userGroupLabel(userGroup)}</StatusBadge>}
      </div>

      {error && <div style={errBox}>{error}</div>}

      <div className="hk-card">
        {loading && items.length === 0 ? (
          <div className="hk-empty">加载中…</div>
        ) : items.length === 0 ? (
          <div className="hk-empty">当前等级暂无可调度的模型分组。如需更高权益,请联系运营方或升级套餐。</div>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['分组名称', '分组 ID', '计费倍率'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {items.map((g) => (
                  <tr key={g.pool_group_id}>
                    <td>
                      <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{g.name || `分组 #${g.pool_group_id}`}</span>
                    </td>
                    <td className="hk-mono">
                      <code>{g.pool_group_id}</code>
                    </td>
                    <td>
                      <StatusBadge tone={ratioTone(g)}>{ratioDisplay(g)}</StatusBadge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <p style={{ color: 'var(--hk-ink-300)', fontSize: 12, margin: 0 }}>
        计费倍率作用于该分组的基础价上;「未公开」表示运营方未对外披露该分组倍率。
      </p>
    </div>
  )
}

const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
