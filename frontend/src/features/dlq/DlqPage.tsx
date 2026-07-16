import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { listDlq, replayDlq, replayUsageRecordDlq } from './api'
import {
  eventKindLabel,
  formatPayload,
  formatTs,
  isMoneySensitiveKind,
  LIMIT_DEFAULT,
  mapDlqRows,
  statusLabel,
  type DlqTableRow,
  validateLimit,
} from './dlq'
import { EVENT_KINDS, STATUS_FILTERS, type DlqRecord } from './types'
import { ObsDlqTab } from './ObsDlqTab'

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

type DlqTab = 'traditional' | 'usage_record' | 'observability'
const TRADITIONAL_EVENT_KINDS = EVENT_KINDS.filter((kind) => kind !== 'usage_record')

export function DlqPage() {
  const [tab, setTab] = useState<DlqTab>('traditional')
  const [handler, setHandler] = useState<string>(TRADITIONAL_EVENT_KINDS[0])
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
    handler: TRADITIONAL_EVENT_KINDS[0],
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
    if (tab === 'observability') {
      setLoading(false)
      return
    }
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, tab])

  const selectTab = (next: DlqTab) => {
    setTab(next)
    setError(null)
    setNotice(null)
    setExpandedId(null)
    if (next === 'usage_record') {
      setQuery((current) => ({ ...current, handler: 'usage_record' }))
    } else if (next === 'traditional') {
      setQuery((current) => ({ ...current, handler }))
    }
  }

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
    setQuery({ handler: tab === 'usage_record' ? 'usage_record' : handler, status, limit: v.value })
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
    const replayRequest = tab === 'usage_record' ? replayUsageRecordDlq : replayDlq
    replayRequest(record.id)
      .then((resp) => {
        setNotice(`已重放 #${record.id},当前状态:${statusLabel(resp.item.status)}`)
        load()
      })
      .catch((e: unknown) =>
        setError(e instanceof ApiError ? `重放失败:${e.message}(${e.code})` : '重放失败'),
      )
      .finally(() => setReplayingId(null))
  }

  const tableRows = mapDlqRows(rows)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>死信队列</h1>
          <p className="hk-sub">
            可靠性运维:按事件类型(handler)查看投递失败的死信,并对单条执行重放。
            重放触及计费/结算的类型(money 敏感)前会强提示;重放本身幂等。
          </p>
        </div>
      </header>

      <div className="hk-seg" role="tablist" aria-label="死信类型" style={{ alignSelf: 'flex-start' }}>
        <button type="button" role="tab" aria-selected={tab === 'traditional'} className={tab === 'traditional' ? 'is-on' : ''} onClick={() => selectTab('traditional')}>
          传统死信
        </button>
        <button type="button" role="tab" aria-selected={tab === 'usage_record'} className={tab === 'usage_record' ? 'is-on' : ''} onClick={() => selectTab('usage_record')}>
          用量记录死信
        </button>
        <button type="button" role="tab" aria-selected={tab === 'observability'} className={tab === 'observability' ? 'is-on' : ''} onClick={() => selectTab('observability')}>
          观测死信
        </button>
      </div>

      {tab === 'observability' ? (
        <ObsDlqTab />
      ) : (
        <>
      <form onSubmit={submitQuery} className="hk-card" style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap', padding: 'var(--hk-space-4)' }}>
        {tab === 'traditional' ? (
          <Field label="事件类型(handler)">
            <select value={handler} onChange={(e) => setHandler(e.target.value)} style={{ ...inp, width: 220 }}>
              {TRADITIONAL_EVENT_KINDS.map((k) => (
                <option key={k} value={k}>
                  {eventKindLabel(k)}（{k}）
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <Field label="事件类型(handler)">
            <input value="usage_record" readOnly style={{ ...inp, width: 220, color: 'var(--hk-ink-300)' }} />
          </Field>
        )}
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
        <button type="submit" disabled={loading} className="hk-btn hk-btn--green">
          {loading ? '查询中…' : '查询'}
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      <section className="hk-card">
        <div className="hk-card__head">
          <h3>
            {eventKindLabel(query.handler)}
            {query.status ? ` · ${STATUS_LABELS[query.status] ?? query.status}` : ''}
          </h3>
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
        </div>

        {loading && rows.length === 0 ? (
          <EmptyState title="正在加载死信记录" hint="请稍候。" />
        ) : rows.length === 0 ? (
          <EmptyState title="暂无死信记录" hint="当前查询条件下没有需要处理的死信。" />
        ) : (
          <>
            <DataListTable
              label="死信记录"
              rows={tableRows}
              rowKey={(row) => row.id}
              columns={dlqColumns(expandedId, (id) => setExpandedId(expandedId === id ? null : id))}
              actions={[{
                label: (row) => replayingId === row.id ? '重放中…' : '重放',
                disabled: (row) => !row.replayable || replayingId === row.id,
                onClick: (row) => onReplay(row.record),
              }]}
            />
            {expandedId != null && rows.find((row) => row.id === expandedId) && (
              <div style={{ padding: 'var(--hk-space-4)', background: 'var(--hk-surface-sunken)', borderTop: '1px solid var(--hk-line)' }}>
                <DetailGrid record={rows.find((row) => row.id === expandedId)!} />
              </div>
            )}
          </>
        )}
      </section>
        </>
      )}
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
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function dlqColumns(expandedId: number | null, onToggle: (id: number) => void): DataListColumn<DlqTableRow>[] {
  return [
    { key: 'id', label: 'ID', render: (row) => <button type="button" onClick={() => onToggle(row.id)} style={linkBtn} title="展开/收起详情">#{row.id} {expandedId === row.id ? '▾' : '▸'}</button> },
    { key: 'tenant', label: '租户', render: (row) => <span className="hk-mono">{row.tenant}</span> },
    { key: 'lane', label: '泳道', badge: true, render: (row) => <StatusBadge tone={row.laneTone}>{row.lane}</StatusBadge> },
    { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
    { key: 'attempts', label: '重试', render: (row) => <span className="hk-mono">{row.attempts}</span> },
    { key: 'reason', label: '失败原因', render: (row) => <span style={{ maxWidth: 320 }}>{row.reason}</span> },
    { key: 'failed-at', label: '失败时间', render: (row) => <span className="hk-mono">{row.failedAt}</span> },
  ]
}

// 查询表单输入框:共享层无表单 input 类,保留本地 token 化样式。
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
// 展开/收起详情的链接式按钮(非标准 hk-btn,保留内联样式)。
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-600)', fontSize: 13, fontFamily: 'var(--hk-font-mono)', cursor: 'pointer', padding: 0 }
