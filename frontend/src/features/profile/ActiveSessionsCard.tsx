import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listSessions, revokeSessionFamily } from './sessionsApi'
import { canRevoke, deviceSummary, familyStatusLabel, familyStatusTone, sortFamilies } from './sessions'
import type { SessionFamily } from './sessionsTypes'

/*
 * 活跃会话卡(user 壳,挂在个人资料页·安全区)。列出当前账号的全部登录设备族,
 * 可逐个撤销(强制登出该设备)。破坏性动作 → 二次确认。
 * 端点 POST /v1/sessions/list + POST /v1/sessions/revoke(session 鉴权,归属后端强校验)。
 * 注意:撤销当前会话所在的设备族会把自己登出 —— 二次确认文案已提示。
 */
export function ActiveSessionsCard() {
  const [families, setFamilies] = useState<SessionFamily[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [busyId, setBusyId] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listSessions(signal)
      .then((r) => setFamilies(sortFamilies(r.families ?? [])))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载登录会话失败')
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

  const revoke = async (f: SessionFamily) => {
    if (
      !window.confirm(
        '确认撤销该登录会话?该设备/谱系将被强制登出。如果这是你当前正在使用的设备,你会被立即登出。',
      )
    ) {
      return
    }
    setBusyId(f.id)
    setError(null)
    setFlash(null)
    try {
      const r = await revokeSessionFamily(f.id)
      setFlash(`已撤销 ${r.revoked} 个会话`)
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '撤销失败')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <Card title="登录会话与设备">
      <p style={hint}>
        你账号当前的全部登录设备。撤销某个会话会强制该设备登出;若撤销的是当前设备,你将被立即登出。
      </p>
      {error && <ErrBox>{error}</ErrBox>}
      {flash && <OkBox>{flash}</OkBox>}
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button type="button" onClick={() => setRefreshNonce((n) => n + 1)} disabled={loading} style={ghostBtn}>
          刷新
        </button>
      </div>
      {loading && families.length === 0 ? (
        <Muted>加载中…</Muted>
      ) : families.length === 0 ? (
        <Muted>没有登录会话记录。</Muted>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          {families.map((f) => (
            <div key={f.id} style={listRow}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
                <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
                  <StatusBadge tone={familyStatusTone(f.status)}>{familyStatusLabel(f.status)}</StatusBadge>
                  <span style={{ fontWeight: 600, fontSize: 13, color: 'var(--hk-ink-900)' }}>{deviceSummary(f)}</span>
                </div>
                <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
                  最近活跃 {fmt(f.last_active_at)} · 创建于 {fmt(f.created_at)}
                  {f.revoked_reason ? ` · 撤销原因 ${f.revoked_reason}` : ''}
                </span>
              </div>
              {canRevoke(f) ? (
                <button type="button" disabled={busyId === f.id} onClick={() => revoke(f)} style={dangerLinkBtn}>
                  {busyId === f.id ? '撤销中…' : '撤销'}
                </button>
              ) : (
                <span style={{ fontSize: 12, color: 'var(--hk-ink-300)', flexShrink: 0 }}>—</span>
              )}
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

/* ---------------- 本卡自有的小组件与样式 ---------------- */

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section
      style={{
        background: 'var(--hk-surface)',
        border: '1px solid var(--hk-line)',
        borderRadius: 'var(--hk-radius-lg)',
        boxShadow: 'var(--hk-shadow-1)',
        padding: 'var(--hk-space-5)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
      }}
    >
      <h2 style={{ fontSize: 16, margin: 0, color: 'var(--hk-ink-900)' }}>{title}</h2>
      {children}
    </section>
  )
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>{children}</div>
}
function ErrBox({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
}
function OkBox({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }}>{children}</div>
}

function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}

const hint: React.CSSProperties = { margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }
const listRow: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-3) var(--hk-space-4)', background: 'var(--hk-surface-sunken)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }
const ghostBtn: React.CSSProperties = { height: 30, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const dangerLinkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-danger)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)', flexShrink: 0 }
