import { Fragment, useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listDlq, replayDlq } from './api'
import {
  canReplay,
  eventKindLabel,
  formatPayload,
  formatTs,
  isMoneySensitiveKind,
  laneTone,
  LIMIT_DEFAULT,
  shortReason,
  statusLabel,
  statusTone,
  validateLimit,
} from './dlq'
import { EVENT_KINDS, STATUS_FILTERS, type DlqRecord } from './types'

/*
 * 死信队列(DLQ)运营台。管线第 7 站(系统)下的可靠性运维。
 * 后端 /admin/v1(admin token,platform_admin 角色):
 *   - GET  /admin/v1/dlq/{handler}        按 event_kind 列死信(routes.go:1116)
 *   - POST /admin/v1/dlq/{id}/replay      重放一条(routes.go:1117)
 * 重放触发 settle 重入 / 计费恢复(money 敏感):重放走完整 idempotency 路径(幂等),
 * 但仍对该动作做二次确认;money 类 event_kind 的确认措辞更强烈地提示「会触发计费恢复」。
 * 本页不碰任何 pool/registry/gateway 等碰撞包模块。
 */

const STATUS_LABELS: Record<string, string> = {
  '': '全部状态',
  pending: '待处理',
  inflight: '处理中',
  delivered: '已投递',
  operator_review: '待人工审阅',
  dlq: '死信',
  quarantined: '已隔离',
}

export function DlqPage() {
  const [handler, setHandler] = useState<string>(EVENT_KINDS[0])
  const [status, setStatus] = useState<string>('')
  const [limitInput, setLimitInput] = useState<string>(String(LIMIT_DEFAULT))
  const [rows, setRows] = useState<DlqRecord[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [replayingId, setReplayingId] = useState<number | null>(null)
  const [expandedId, setExpandedId] = useState<number | null>(null)

  // 当前生效的查询参数(点击「查询」时定格,避免输入即触发请求)。
  const [query, setQuery] = useState<{ handler: string; status: string; limit: number }>({
    handler: EVENT_KINDS[0],
    status: '',
    limit: LIMIT_DEFAULT,
  })

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listDlq(query.handler, { status: query.status, limit: query.limit, signal })
        .then((resp) => setRows(resp.items ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载死信列表失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [query],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const submitQuery = (e: React.FormEvent) => {
    e.preventDefault()
    const n = Number(limitInput.trim())
    const v = validateLimit(n)
    if (!v.ok) {
      setError(v.error)
      return
    }
    setNotice(null)
    setExpandedId(null)
    setQuery({ handler, status, limit: v.value })
  }

  const onReplay = (record: DlqRecord) => {
    const money = isMoneySensitiveKind(record.event_kind)
    const head = `确认重放死信 #${record.id}(${eventKindLabel(record.event_kind)})?`
    const body = money
      ? '\n\n该类型触及计费/结算:重放会触发 settle 重入与计费恢复(money 敏感)。' +
        '重放是幂等的(走 claim/usage/billing_event 三证 proof 防重复扣费),但仍会影响余额相关账目。'
      : '\n\n重放是幂等的,可安全重试。'
    if (!window.confirm(head + body)) return
    setReplayingId(record.id)
    setError(null)
    setNotice(null)
    replayDlq(record.id)
      .then((resp) => {
        setNotice(`已重放 #${record.id},当前状态:${statusLabel(resp.item.status)}`)
        load()
      })
      .catch((e: unknown) =>
        setError(e instanceof ApiError ? `重放失败:${e.message}(${e.code})` : '重放失败'),
      )
      .finally(() => setReplayingId(null))
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>死信队列</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          可靠性运维:按事件类型(handler)查看投递失败的死信,并对单条执行重放。
          重放触及计费/结算的类型(money 敏感)前会强提示;重放本身幂等。
        </p>
      </header>

      {/* 查询条件 */}
      <form onSubmit={submitQuery} style={filterBar}>
        <Field label="事件类型(handler)">
          <select value={handler} onChange={(e) => setHandler(e.target.value)} style={{ ...inp, width: 220 }}>
            {EVENT_KINDS.map((k) => (
              <option key={k} value={k}>
                {eventKindLabel(k)}（{k}）
              </option>
            ))}
          </select>
        </Field>
        <Field label="状态筛选">
          <select value={status} onChange={(e) => setStatus(e.target.value)} style={{ ...inp, width: 160 }}>
            {STATUS_FILTERS.map((s) => (
              <option key={s || 'all'} value={s}>
                {STATUS_LABELS[s] ?? s}
              </option>
            ))}
          </select>
        </Field>
        <Field label="条数(1~200)">
          <input
            value={limitInput}
            onChange={(e) => setLimitInput(e.target.value)}
            inputMode="numeric"
            placeholder={String(LIMIT_DEFAULT)}
            style={{ ...inp, width: 120 }}
          />
        </Field>
        <button type="submit" disabled={loading} style={primaryBtn}>
          {loading ? '查询中…' : '查询'}
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      <section style={card}>
        <div style={cardHead}>
          <h2 style={{ fontSize: 15, margin: 0 }}>
            {eventKindLabel(query.handler)}
            {query.status ? ` · ${STATUS_LABELS[query.status] ?? query.status}` : ''}
          </h2>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
        </div>

        {loading && rows.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : rows.length === 0 ? (
          <Empty>当前条件下暂无死信记录。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['ID', '租户', '泳道', '状态', '重试', '失败原因', '失败时间', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const expanded = expandedId === r.id
                  const replayable = canReplay(r)
                  return (
                    <Fragment key={r.id}>
                      <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
                        <td style={tdMono}>
                          <button
                            type="button"
                            onClick={() => setExpandedId(expanded ? null : r.id)}
                            style={linkBtn}
                            title="展开/收起详情"
                          >
                            #{r.id} {expanded ? '▾' : '▸'}
                          </button>
                        </td>
                        <td style={tdMono}>#{r.tenant_id}</td>
                        <td style={td}>
                          <StatusBadge tone={laneTone(r.lane)}>{r.lane || '—'}</StatusBadge>
                        </td>
                        <td style={td}>
                          <StatusBadge tone={statusTone(r.status)}>{statusLabel(r.status)}</StatusBadge>
                        </td>
                        <td style={tdMono}>{r.replay_attempts}</td>
                        <td style={{ ...td, maxWidth: 320 }}>{shortReason(r.failure_reason)}</td>
                        <td style={tdMono}>{formatTs(r.failure_at)}</td>
                        <td style={{ ...td, textAlign: 'right' }}>
                          <button
                            type="button"
                            disabled={!replayable || replayingId === r.id}
                            onClick={() => onReplay(r)}
                            style={replayable ? primaryBtn : disabledBtn}
                            title={replayable ? '重放此死信' : '已投递,无需重放'}
                          >
                            {replayingId === r.id ? '重放中…' : '重放'}
                          </button>
                        </td>
                      </tr>
                      {expanded && (
                        <tr style={{ borderTop: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }}>
                          <td colSpan={8} style={{ padding: 'var(--hk-space-4)' }}>
                            <DetailGrid record={r} />
                          </td>
                        </tr>
                      )}
                    </Fragment>
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

/* ——— 详情区:键值对 + payload ——— */
function DetailGrid({ record }: { record: DlqRecord }) {
  const kv: Array<[string, string]> = [
    ['idempotency_key', record.idempotency_key || '—'],
    ['source_table', record.source_table || '—'],
    ['source_id', record.source_id == null ? '—' : String(record.source_id)],
    ['claim_id', record.claim_id == null ? '—' : String(record.claim_id)],
    ['replica_status', record.replica_status || '—'],
    ['replica_target', record.replica_target || '—'],
    ['lease_owner', record.lease_owner ?? '—'],
    ['next_retry_at', formatTs(record.next_retry_at)],
    ['last_replay_at', formatTs(record.last_replay_at)],
    ['replayed_at', formatTs(record.replayed_at)],
    ['replay_failure_reason', record.replay_failure_reason ?? '—'],
    ['operator_review_at', formatTs(record.operator_review_at)],
  ]
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 'var(--hk-space-2) var(--hk-space-4)' }}>
        {kv.map(([k, v]) => (
          <div key={k} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{k}</span>
            <span style={{ fontSize: 12, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', wordBreak: 'break-all' }}>{v}</span>
          </div>
        ))}
      </div>
      <details>
        <summary style={{ fontSize: 12, color: 'var(--hk-ink-500)', cursor: 'pointer' }}>payload(原始载荷)</summary>
        <pre style={{ margin: 'var(--hk-space-2) 0 0', padding: 'var(--hk-space-3)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 12, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', overflowX: 'auto', maxHeight: 320 }}>
          {formatPayload(record.payload)}
        </pre>
      </details>
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
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const filterBar: React.CSSProperties = { display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }
const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const disabledBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface-sunken)', color: 'var(--hk-ink-300)', fontSize: 13, cursor: 'not-allowed' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-600)', fontSize: 13, fontFamily: 'var(--hk-font-mono)', cursor: 'pointer', padding: 0 }
