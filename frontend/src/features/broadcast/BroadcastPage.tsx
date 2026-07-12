import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getWorkerStats, sendBroadcast } from './api'
import {
  DEFAULT_TENANT_ID,
  SEVERITIES,
  failureRate,
  severityLabel,
  severityTone,
  validateBroadcast,
} from './broadcast'
import type { Severity, WorkerStats } from './types'

/*
 * 站内信广播 + 通知 worker 统计运营台(运营台 · 内容与公告)。
 * 后端两端点(routes_notifications.go,admin 鉴权):
 *   - POST /v1/admin/notifications/broadcast:向目标租户全体用户群发站内信(标题/正文/级别)。
 *     这是高影响改动型动作(一次写入即触达全体),发送前做 window.confirm 二次确认。
 *   - GET  /v1/admin/notifications/worker-stats:只读,展示订阅提醒/到期 worker 的进程内计数器。
 * platform_admin 必须显式指定 tenant_id;单租户部署默认 1,顶栏可改。
 * 不碰任何 pool/registry/gateway 等碰撞包模块。
 */

export function BroadcastPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>站内信广播</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            运营台 · 向目标租户全体用户群发站内信(标题/正文/级别),并查看订阅提醒/到期 worker 运行统计。
            广播一次即触达全体,发送前会二次确认。
          </p>
        </div>
        <Field label="租户 ID(tenant_id)">
          <input
            value={tenantId}
            inputMode="numeric"
            onChange={(e) => {
              const v = Number.parseInt(e.target.value, 10)
              setTenantId(Number.isInteger(v) && v > 0 ? v : DEFAULT_TENANT_ID)
            }}
            style={{ ...inp, width: 96 }}
          />
        </Field>
      </header>

      <BroadcastForm tenantId={tenantId} />
      <WorkerStatsCard />
    </div>
  )
}

// ── 广播发送表单 ───────────────────────────────────────────────────────────────
function BroadcastForm({ tenantId }: { tenantId: number }) {
  const [title, setTitle] = useState('')
  const [body, setBody] = useState('')
  const [severity, setSeverity] = useState<Severity>('info')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const submit = () => {
    const result = validateBroadcast(tenantId, { title, body, severity })
    if (!result.ok) {
      setError(result.error)
      setNotice(null)
      return
    }
    // 高影响改动型动作:广播一次即写入目标租户全体用户的站内信箱,发送前二次确认。
    if (
      !window.confirm(
        `确认向租户 #${tenantId} 的【全体用户】群发站内信「${result.value.title}」(级别:${severityLabel(severity)})?` +
          '\n该操作会写入所有用户的站内信箱,无法批量撤回。',
      )
    ) {
      return
    }
    setBusy(true)
    setError(null)
    setNotice(null)
    sendBroadcast(result.value)
      .then((res) => {
        setNotice(`已群发:收信用户 ${res.inserted} 人(租户 #${res.tenant_id})。`)
        setTitle('')
        setBody('')
        setSeverity('info')
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? `${e.message}(${e.code})` : '群发失败'))
      .finally(() => setBusy(false))
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>群发站内信</h2>
        <StatusBadge tone={severityTone(severity)}>{severityLabel(severity)}</StatusBadge>
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-4)' }}>
        <Field label="标题(title)">
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="如:系统维护通知"
            maxLength={200}
            style={inp}
          />
        </Field>
        <Field label="正文(body)">
          <textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={5}
            placeholder="站内信正文(纯文本)"
            style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', lineHeight: 1.6 }}
          />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
          <Field label="严重级别(severity)">
            <select value={severity} onChange={(e) => setSeverity(e.target.value as Severity)} style={{ ...inp, width: 200 }}>
              {SEVERITIES.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </select>
          </Field>
          <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
            {busy ? '群发中…' : '群发到全体用户'}
          </button>
        </div>
        <p style={{ margin: 0, fontSize: 11, color: 'var(--hk-ink-300)' }}>
          目标范围 = 租户 #{tenantId} 全体用户。platform_admin 须指定 tenant_id;tenant_operator 仅可对自身租户群发。
        </p>
      </div>
    </section>
  )
}

// ── worker 统计只读卡 ─────────────────────────────────────────────────────────
function WorkerStatsCard() {
  const [stats, setStats] = useState<WorkerStats | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback((signal?: AbortSignal) => {
    setLoading(true)
    setError(null)
    getWorkerStats(signal)
      .then((s) => setStats(s))
      .catch((e: unknown) => {
        if (signal?.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载 worker 统计失败')
      })
      .finally(() => {
        if (!signal?.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>通知 Worker 统计(只读)</h2>
        <button type="button" disabled={loading} onClick={() => load()} style={ghostBtn}>
          {loading ? '刷新中…' : '刷新'}
        </button>
      </div>

      {error && <Banner kind="error">{error}</Banner>}

      {loading && !stats ? (
        <Empty>加载中…</Empty>
      ) : !stats ? (
        <Empty>暂无统计数据。</Empty>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(260px, 1fr))', gap: 'var(--hk-space-4)', padding: 'var(--hk-space-4)' }}>
          <StatBlock
            title="订阅提醒 Worker(reminder)"
            metrics={[
              { label: '调度轮次', value: stats.reminder.tick_count },
              { label: '累计发出提醒', value: stats.reminder.sent_total },
              { label: '失败轮次', value: stats.reminder.failed_ticks, danger: stats.reminder.failed_ticks > 0 },
            ]}
            rate={failureRate(stats.reminder.failed_ticks, stats.reminder.tick_count)}
          />
          <StatBlock
            title="到期处理 Worker(expiry)"
            metrics={[
              { label: '调度轮次', value: stats.expiry.tick_count },
              { label: '累计处理到期', value: stats.expiry.expired_total },
              { label: '失败轮次', value: stats.expiry.failed_ticks, danger: stats.expiry.failed_ticks > 0 },
            ]}
            rate={failureRate(stats.expiry.failed_ticks, stats.expiry.tick_count)}
          />
        </div>
      )}
      <p style={{ margin: 0, padding: '0 var(--hk-space-4) var(--hk-space-4)', fontSize: 11, color: 'var(--hk-ink-300)' }}>
        计数为当前网关进程内累计值,进程重启后清零;非持久化历史。
      </p>
    </section>
  )
}

function StatBlock({
  title,
  metrics,
  rate,
}: {
  title: string
  metrics: ReadonlyArray<{ label: string; value: number; danger?: boolean }>
  rate: string
}) {
  return (
    <div style={{ border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>{title}</span>
        <StatusBadge tone={rate === '—' || rate === '0%' ? 'ok' : 'warn'}>失败率 {rate}</StatusBadge>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        {metrics.map((m) => (
          <div key={m.label} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13 }}>
            <span style={{ color: 'var(--hk-ink-500)' }}>{m.label}</span>
            <span style={{ fontFamily: 'var(--hk-font-mono)', color: m.danger ? 'var(--hk-danger)' : 'var(--hk-ink-900)', fontWeight: 600 }}>
              {m.value}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

/* ——— 本文件私有小组件 / 样式 ——— */
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
      : { color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
