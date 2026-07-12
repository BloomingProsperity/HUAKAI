import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  channelHealthOverride,
  getChannelHealthSummary,
  listChannelHealth,
} from './api'
import {
  actionLabel,
  actionNeedsConfirm,
  buildOverride,
  canOverride,
  confidenceLabel,
  signalLabel,
  stateLabel,
  stateTone,
} from './channelHealth'
import type {
  ChannelHealthItem,
  ChannelHealthSummary,
  OverrideAction,
} from './types'

/*
 * 渠道健康台 —— 上游渠道(账号凭证)的健康状态机总览 + 人工干预。
 * 后端 /v1/admin/channel-health(读)+ /v1/admin/provider-accounts/{id}/channel-health/*(写),
 * 均 admin token。读侧展示 crash/cooling_down/ramping 等状态、信号类别、置信层级、冷却/爬坡进度;
 * 写侧 pause(人工封停)/resume(恢复)/force-active(强制上线)直接复用列表项坐标。
 * platform_admin 下 tenant_id 必填,故先指定租户 ID 再加载。
 * 本页仅调既有 channel-health 端点,不触碰任何 pool/channel/gateway 写路径。
 */

const PAGE_SIZE = 100

export function ChannelHealthPage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantId, setTenantId] = useState<number | null>(null)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>渠道健康台</h1>
          <p className="hk-sub">
            上游渠道(账号凭证维度)的健康状态机:自动冷却 / 爬坡恢复 / 受损,及人工暂停 / 恢复 / 强制上线。先指定租户 ID。
          </p>
        </div>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          const v = Number(tenantInput.trim())
          setTenantId(Number.isInteger(v) && v > 0 ? v : null)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="租户 ID(tenant_id)">
          <input
            value={tenantInput}
            onChange={(e) => setTenantInput(e.target.value)}
            inputMode="numeric"
            placeholder="如 1"
            style={{ ...inp, width: 160 }}
          />
        </Field>
        <button type="submit" className="hk-btn hk-btn--green">
          加载
        </button>
      </form>

      {tenantId == null ? (
        <Empty>请输入正整数租户 ID 后点击「加载」。</Empty>
      ) : (
        <ChannelHealthBoard tenantId={tenantId} />
      )}
    </div>
  )
}

function ChannelHealthBoard({ tenantId }: { tenantId: number }) {
  const [summary, setSummary] = useState<ChannelHealthSummary | null>(null)
  const [rows, setRows] = useState<ChannelHealthItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  // busy 记录正在执行干预的渠道(provider_account_id:credential_version 复合键)+ 动作。
  const [busyKey, setBusyKey] = useState<string | null>(null)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      Promise.all([
        getChannelHealthSummary(tenantId, signal),
        listChannelHealth(tenantId, PAGE_SIZE, 0, signal),
      ])
        .then(([s, list]) => {
          setSummary(s)
          setRows(list.items ?? [])
        })
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载渠道健康失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [tenantId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const rowKey = (item: ChannelHealthItem) => `${item.provider_account_id}:${item.credential_version}:${item.account_credential_id}`

  const override = (item: ChannelHealthItem, action: OverrideAction) => {
    // 高影响动作(pause 封停 / force-active 绕过冷却)二次确认。
    if (actionNeedsConfirm(action)) {
      if (!window.confirm(`确认对渠道「${item.channel_id}」执行「${actionLabel(action)}」?`)) return
    }
    const reason = window.prompt(`「${actionLabel(action)}」渠道「${item.channel_id}」。请填写操作原因(供审计):`, '')
    if (reason === null) return // 取消
    const built = buildOverride(item, reason)
    if (!built.ok) {
      setError(built.error)
      return
    }
    const key = rowKey(item)
    setBusyKey(key)
    setError(null)
    setNotice(null)
    channelHealthOverride(built.providerAccountId, action, built.body)
      .then((rec) => {
        setNotice(`已对「${item.channel_id}」执行「${actionLabel(action)}」,现态:${stateLabel(rec.state)}`)
        load()
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败'))
      .finally(() => setBusyKey(null))
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {/* 状态聚合卡 */}
      <SummaryCard summary={summary} loading={loading} onReload={() => load()} />

      {/* 渠道列表 */}
      <section className="hk-card">
        <div className="hk-card__head">
          <h3>渠道明细</h3>
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
        </div>
        {loading && rows.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : rows.length === 0 ? (
          <Empty>该租户暂无渠道健康记录。</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['渠道', '厂商', '状态', '健康分', '信号 / 置信', '冷却 / 爬坡', '更新', '人工干预'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((item) => {
                  const key = rowKey(item)
                  const writable = canOverride(item)
                  const busy = busyKey === key
                  return (
                    <tr key={key}>
                      <td className="hk-mono">
                        <div>{item.channel_id}</div>
                        <div style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>
                          acct #{item.provider_account_id} · cred #{item.account_credential_id} v{item.credential_version}
                        </div>
                      </td>
                      <td>{item.vendor || '—'}</td>
                      <td>
                        <StatusBadge tone={stateTone(item.state)}>{stateLabel(item.state)}</StatusBadge>
                      </td>
                      <td className="hk-mono">{fmtNum(item.score)}</td>
                      <td style={{ color: 'var(--hk-ink-700)' }}>
                        <div>{signalLabel(item.reason_class)}</div>
                        <div style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{confidenceLabel(item.confidence_tier)}</div>
                      </td>
                      <td style={{ color: 'var(--hk-ink-700)' }}>
                        {item.cooldown_until ? (
                          <div title="冷却到期">冷却至 {fmt(item.cooldown_until)}</div>
                        ) : null}
                        {item.state === 'ramping' || item.ramp_stage_pct > 0 ? (
                          <div style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>
                            爬坡 {item.ramp_stage_pct}% · 失败 {item.ramp_failure_count}
                          </div>
                        ) : null}
                        {!item.cooldown_until && item.state !== 'ramping' && item.ramp_stage_pct === 0 ? '—' : null}
                      </td>
                      <td className="hk-mono">{fmt(item.updated_at)}</td>
                      <td style={{ whiteSpace: 'nowrap' }}>
                        {writable ? (
                          <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
                            <button type="button" disabled={busy} onClick={() => override(item, 'pause')} className="hk-btn hk-btn--sm hk-btn--danger">
                              {busy ? '…' : '暂停'}
                            </button>
                            <button type="button" disabled={busy} onClick={() => override(item, 'resume')} className="hk-btn hk-btn--sm">
                              恢复
                            </button>
                            <button type="button" disabled={busy} onClick={() => override(item, 'force-active')} style={warnBtn}>
                              强制上线
                            </button>
                          </div>
                        ) : (
                          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>坐标不全</span>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}

function SummaryCard({
  summary,
  loading,
  onReload,
}: {
  summary: ChannelHealthSummary | null
  loading: boolean
  onReload: () => void
}) {
  // 把 by_state 按状态机顺序排展示,缺失态补 0,便于一眼看分布。
  const order: string[] = ['active', 'ramping', 'degraded', 'cooling_down', 'disabled', 'manual_paused']
  const byState = summary?.by_state ?? {}
  // 把后端返回但不在固定顺序里的状态也带上(防漏)。
  const extras = Object.keys(byState).filter((s) => !order.includes(s))
  const states = [...order, ...extras]

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>状态聚合</h3>
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
          {summary?.oldest_cooldown_at && (
            <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>最早冷却 {fmt(summary.oldest_cooldown_at)}</span>
          )}
          <button type="button" disabled={loading} onClick={onReload} className="hk-btn hk-btn--sm">
            {loading ? '加载中…' : '刷新'}
          </button>
        </div>
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-4)' }}>
        <Stat label="总渠道" value={summary?.total ?? 0} tone="info" />
        {states.map((s) => (
          <Stat key={s} label={stateLabel(s)} value={byState[s] ?? 0} tone={stateTone(s)} />
        ))}
      </div>
    </section>
  )
}

/* ——— 本页私有小组件 / 样式 ——— */
function Stat({ label, value, tone }: { label: string; value: number; tone: ReturnType<typeof stateTone> }) {
  return (
    <div style={{ minWidth: 96, padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface-sunken)' }}>
      <div style={{ marginBottom: 4 }}>
        <StatusBadge tone={tone}>{label}</StatusBadge>
      </div>
      <div style={{ fontSize: 20, fontWeight: 700, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-900)' }}>{value}</div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

function Banner({ kind, children }: { kind: 'error' | 'ok'; children: React.ReactNode }) {
  const palette =
    kind === 'error'
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

function fmt(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

function fmtNum(n: number): string {
  if (typeof n !== 'number' || Number.isNaN(n)) return '—'
  // 保留 2 位小数但裁掉无意义尾随 0。
  return String(Math.round(n * 100) / 100)
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
// 强制上线为警示类操作(绕过冷却),保留独立琥珀底样式以突出风险语义;共享按钮体系暂无 warn 变体。
const warnBtn: React.CSSProperties = { border: '1px solid var(--hk-warn-soft)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-warn-soft)', color: 'var(--hk-warn)', fontSize: 12, fontWeight: 600, cursor: 'pointer', padding: '4px 10px' }
