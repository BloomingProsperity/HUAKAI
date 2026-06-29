import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import {
  exportUsageCSV,
  getCostReceipt,
  listMyDisputes,
  listUsageRecords,
  verifyCostReceipt,
} from './api'
import {
  defaultExportRange,
  disputeStatusLabel,
  disputeStatusTone,
  formatCost,
  formatMicroUSD,
  hasMore,
  modelDisplay,
  statusLabel,
  statusTone,
  tokensSummary,
  validateExportRange,
  verifyLabel,
  verifyStatusLabel,
  verifyTone,
} from './usagerecords'
import type { Dispute, ReceiptVerifyResponse, UsageRecord, UserCostReceipt } from './types'

const PAGE_LIMIT = 50

/*
 * 用量明细 / 请求日志(用户门户 · 用量与配额)。只读列出当前用户跨全部 API Key 的逐请求用量
 * (模型/状态/费用/token/时间/请求 ID),游标分页「加载更多」。session 鉴权,身份后端从会话派生。
 * 区别于 /usage(聚合配额视图):这里是行级逐请求日志。
 * 真码端点:backend/internal/meusagehttp/session_handler.go:19、backend/cmd/gateway/routes.go:192。
 */
export function UsageRecordsPage() {
  const [items, setItems] = useState<UsageRecord[]>([])
  const [cursor, setCursor] = useState('')
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  // 当前展开「成本详情/收据」下钻的行键(同一时刻只展开一行,避免一次性拉太多详情)。
  const [expandedKey, setExpandedKey] = useState<string | null>(null)

  const loadFirst = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listUsageRecords({ limit: PAGE_LIMIT }, signal)
        .then((resp) => {
          setItems(resp.items)
          setCursor(resp.next_cursor)
          setMore(hasMore(resp.next_cursor))
        })
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用量明细失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [refreshNonce],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadFirst(ctrl.signal)
    return () => ctrl.abort()
  }, [loadFirst])

  const loadMore = async () => {
    if (!cursor) return
    setLoadingMore(true)
    setError(null)
    try {
      const resp = await listUsageRecords({ limit: PAGE_LIMIT, cursor })
      setItems((prev) => [...prev, ...resp.items])
      setCursor(resp.next_cursor)
      setMore(hasMore(resp.next_cursor))
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载更多失败')
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>用量明细</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            你账户跨全部 API Key 的逐请求记录(模型 / 状态 / 费用 / token)。已加载 {items.length} 条。
          </p>
        </div>
        <button type="button" onClick={() => setRefreshNonce((n) => n + 1)} style={ghostBtn} disabled={loading}>
          刷新
        </button>
      </header>

      <ExportToolbar />

      {error && <div style={errBox}>{error}</div>}

      <div style={card}>
        {loading && items.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : items.length === 0 ? (
          <Empty>暂无请求记录。发起 API 调用后这里会显示逐请求用量。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['时间', '模型', '状态', '费用', 'Token', '请求 ID', ''].map((h, hi) => (
                    <th key={h || `act-${hi}`} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {items.map((r, i) => {
                  const rowKey = r.request_id || r.ledger_id || `${r.created_at}-${i}`
                  const expanded = expandedKey === rowKey
                  return (
                    <RecordRow
                      key={rowKey}
                      record={r}
                      expanded={expanded}
                      onToggle={() => setExpandedKey((cur) => (cur === rowKey ? null : rowKey))}
                    />
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {more && (
        <div style={{ display: 'flex', justifyContent: 'center' }}>
          {/* 刷新进行中也禁用,避免与首屏重拉并发追加导致列表瞬时错位 */}
          <button type="button" onClick={loadMore} disabled={loadingMore || loading} style={ghostBtn}>
            {loadingMore ? '加载中…' : '加载更多'}
          </button>
        </div>
      )}

      <MyDisputes />
    </div>
  )
}

/**
 * 单条用量记录行 + 可展开的「成本详情/收据」下钻。
 * 展开时按需拉取:
 *   GET  /v1/generation?id=<request_id>      逐请求成本/用量明细(成本下钻)
 *   GET  /v1/receipts/<request_id>           单次签名成本收据
 *   POST /v1/receipts/<request_id>/verify    收据验签(只读密码学校验,空 body)
 * 无 request_id 的记录不提供下钻(收据/验签以 request_id 为取证锚点)。
 */
function RecordRow({ record, expanded, onToggle }: { record: UsageRecord; expanded: boolean; onToggle: () => void }) {
  const reqID = record.request_id?.trim() ?? ''
  return (
    <>
      <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
        <td style={tdTime}>{fmt(record.created_at)}</td>
        <td style={td}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <span style={{ color: 'var(--hk-ink-900)' }}>{modelDisplay(record)}</span>
            {record.stream && <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>流式</span>}
          </div>
        </td>
        <td style={td}>
          <StatusBadge tone={statusTone(record.status) as BadgeTone}>{statusLabel(record.status)}</StatusBadge>
        </td>
        <td style={{ ...td, whiteSpace: 'nowrap' }}>
          <code style={{ fontSize: 12, color: 'var(--hk-ink-700)' }}>{formatCost(record.actual_cost)}</code>
        </td>
        <td style={{ ...td, color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }}>{tokensSummary(record.tokens)}</td>
        <td style={td}>
          {reqID ? (
            <code style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{reqID}</code>
          ) : (
            <span style={{ color: 'var(--hk-ink-300)' }}>—</span>
          )}
        </td>
        <td style={{ ...td, whiteSpace: 'nowrap', textAlign: 'right' }}>
          {reqID ? (
            <button type="button" onClick={onToggle} style={ghostBtnSm} aria-expanded={expanded}>
              {expanded ? '收起' : '成本详情'}
            </button>
          ) : (
            <span style={{ color: 'var(--hk-ink-300)', fontSize: 11 }}>无请求 ID</span>
          )}
        </td>
      </tr>
      {expanded && reqID && (
        <tr>
          <td colSpan={7} style={{ padding: 0, borderTop: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }}>
            <RecordDrilldown record={record} requestID={reqID} />
          </td>
        </tr>
      )}
    </>
  )
}

/**
 * 成本下钻面板:成本/用量明细直接用列表行自带数据(record,已 session 鉴权拉过),
 * 再 session 只读拉签名收据(/v1/receipts/{id})+ 提供「验签」按钮(只读密码学校验,不动钱)。
 * 注:逐请求成本端点 /v1/generation 走的是 API-key(inboundAuth)鉴权、非 session 不可达
 * (routes.go:130 顶层 d.inboundAuth),故不调它,改用行内已有的 actual_cost/tokens/model。
 */
function RecordDrilldown({ record, requestID }: { record: UsageRecord; requestID: string }) {
  const [receipt, setReceipt] = useState<UserCostReceipt | null>(null)
  const [receiptErr, setReceiptErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const [verifyResult, setVerifyResult] = useState<ReceiptVerifyResponse | null>(null)
  const [verifyErr, setVerifyErr] = useState<string | null>(null)
  const [verifying, setVerifying] = useState(false)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setReceipt(null)
    setReceiptErr(null)
    setVerifyResult(null)
    setVerifyErr(null)
    getCostReceipt(requestID, ctrl.signal)
      .then((rc) => {
        if (ctrl.signal.aborted) return
        setReceipt(rc)
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setReceiptErr(errText(e, '收据加载失败'))
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [requestID])

  const doVerify = async () => {
    setVerifying(true)
    setVerifyErr(null)
    setVerifyResult(null)
    try {
      const res = await verifyCostReceipt(requestID)
      setVerifyResult(res)
    } catch (e) {
      setVerifyErr(errText(e, '验签失败'))
    } finally {
      setVerifying(false)
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      {loading ? (
        <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>加载成本详情…</div>
      ) : (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-4)' }}>
          {/* 成本 / 用量明细:直接取列表行自带数据(无需再打 API-key-only 的 /v1/generation) */}
          <section style={panel}>
            <h3 style={panelTitle}>成本 / 用量明细</h3>
            <dl style={dl}>
              <Field k="模型" v={modelDisplay(record)} />
              <Field k="实际费用" v={formatCost(record.actual_cost)} mono />
              <Field k="Token" v={tokensSummary(record.tokens)} />
              <Field k="状态" v={statusLabel(record.status)} />
              <Field k="Provider" v={record.provider || '—'} />
              <Field k="账号 ID" v={record.provider_account_id != null ? String(record.provider_account_id) : '—'} mono />
              <Field k="Ledger ID" v={record.ledger_id || '—'} mono />
              <Field k="请求时间" v={record.requested_at ? fmt(record.requested_at) : '—'} />
            </dl>
          </section>

          {/* 签名成本收据(receipt) */}
          <section style={panel}>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--hk-space-3)' }}>
              <h3 style={panelTitle}>签名成本收据</h3>
              {receipt && (
                <button type="button" onClick={doVerify} disabled={verifying} style={ghostBtnSm}>
                  {verifying ? '验签中…' : '验签'}
                </button>
              )}
            </div>
            {receiptErr ? (
              <div style={subtle}>{receiptErr}</div>
            ) : receipt ? (
              <dl style={dl}>
                <Field k="收据序号" v={String(receipt.receipt_sequence)} mono />
                <Field k="模型" v={receipt.cost.model || '—'} />
                <Field k="收据成本" v={formatMicroUSD(receipt.cost.cost_total_micro_usd)} mono />
                <Field k="输入 / 输出 Token" v={`${receipt.cost.input_tokens} / ${receipt.cost.output_tokens}`} />
                <Field k="缓存 Token" v={String(receipt.cost.cached_tokens)} />
                <Field k="校验态" v={receipt.validation_state || '—'} />
                <Field k="裁决" v={receipt.verdict || '—'} />
                <Field k="费率快照" v={receipt.cost.rate_table_snapshot_id ? String(receipt.cost.rate_table_snapshot_id) : '—'} mono />
                <Field k="发生时间" v={receipt.occurred_at ? fmt(receipt.occurred_at) : '—'} />
                <Field k="签名指纹" v={receipt.pubkey_fingerprint || '(未签名)'} mono />
                <Field k="Canonical Hash" v={receipt.canonical_hash || '—'} mono />
              </dl>
            ) : (
              <div style={subtle}>收据暂不可用(可能尚未最终化)</div>
            )}

            {/* 验签结果(只读密码学校验,不动钱) */}
            {verifyErr && <div style={{ ...subtle, color: '#8f322a', marginTop: 'var(--hk-space-3)' }}>{verifyErr}</div>}
            {verifyResult && (
              <div style={{ marginTop: 'var(--hk-space-3)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
                  <StatusBadge tone={verifyTone(verifyResult) as BadgeTone}>{verifyLabel(verifyResult)}</StatusBadge>
                  <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>状态:{verifyStatusLabel(verifyResult.status)}</span>
                </div>
                <dl style={dl}>
                  <Field k="签名有效" v={verifyResult.signature_valid ? '是' : '否'} />
                  <Field k="密钥状态" v={verifyResult.key_status || '—'} />
                  {verifyResult.reason ? <Field k="原因" v={verifyResult.reason} mono /> : null}
                  {verifyResult.fields_mismatch && verifyResult.fields_mismatch.length > 0 ? (
                    <Field k="不一致字段" v={verifyResult.fields_mismatch.join(', ')} mono />
                  ) : null}
                </dl>
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  )
}

/**
 * 我的争议(只读列表)。GET /v1/me/disputes(dispute_handler.go:116)。
 * 只读列本人争议;发起争议(POST /v1/receipts/{id}/disputes)触发退款流程=money,Owner-gated,不在此提供。
 */
function MyDisputes() {
  const [disputes, setDisputes] = useState<Dispute[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listMyDisputes(undefined, ctrl.signal)
      .then((resp) => setDisputes(resp.disputes ?? []))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(errText(e, '加载争议列表失败'))
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [nonce])

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h2 style={{ fontSize: 18 }}>我的争议</h2>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            你对计费收据提起的争议进度(只读)。如需对某条请求发起争议,请联系运营。
          </p>
        </div>
        <button type="button" onClick={() => setNonce((n) => n + 1)} style={ghostBtn} disabled={loading}>
          刷新
        </button>
      </header>

      {error && <div style={errBox}>{error}</div>}

      <div style={card}>
        {loading && disputes.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : disputes.length === 0 ? (
          <Empty>暂无争议记录。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['争议 ID', '请求 ID', '原因', '状态', '提交时间', '处理时间', '运营备注'].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {disputes.map((d) => (
                  <tr key={d.id || d.dispute_id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>
                      <code style={{ fontSize: 11, color: 'var(--hk-ink-700)' }}>{d.dispute_id || d.id}</code>
                    </td>
                    <td style={td}>
                      <code style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{d.request_id || '—'}</code>
                    </td>
                    <td style={{ ...td, maxWidth: 280, color: 'var(--hk-ink-700)' }}>{d.reason || '—'}</td>
                    <td style={td}>
                      <StatusBadge tone={disputeStatusTone(d.status) as BadgeTone}>{disputeStatusLabel(d.status)}</StatusBadge>
                    </td>
                    <td style={tdTime}>{fmt(d.created_at)}</td>
                    <td style={tdTime}>{d.resolved_at ? fmt(d.resolved_at) : '—'}</td>
                    <td style={{ ...td, maxWidth: 240, color: 'var(--hk-ink-500)' }}>{d.operator_note || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </section>
  )
}

/** 详情字段行(键值对)。mono 用于 ID/hash 等等宽展示。 */
function Field({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'baseline' }}>
      <dt style={{ fontSize: 12, color: 'var(--hk-ink-500)', minWidth: 96, flexShrink: 0 }}>{k}</dt>
      <dd
        style={{
          margin: 0,
          fontSize: 12,
          color: 'var(--hk-ink-900)',
          wordBreak: 'break-all',
          fontFamily: mono ? 'var(--hk-font-mono, monospace)' : undefined,
        }}
      >
        {v}
      </dd>
    </div>
  )
}

/** 把未知异常归一化成中文提示(ApiError 带 code,其余用兜底文案)。 */
function errText(e: unknown, fallback: string): string {
  return e instanceof ApiError ? `${e.message}(${e.code})` : fallback
}

/**
 * 用量 CSV 导出工具条:选起止日期(默认最近 30 天)→ 校验范围 → 下载 export.csv。
 * 范围校验在前端先行(对齐后端 from/to 必填 + ≤366 天),走带 session 头的 blob 下载。
 */
function ExportToolbar() {
  const init = defaultExportRange(30)
  const [fromDay, setFromDay] = useState(init.fromDay)
  const [toDay, setToDay] = useState(init.toDay)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const doExport = async () => {
    const invalid = validateExportRange(fromDay, toDay)
    if (invalid) {
      setError(invalid)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await exportUsageCSV(fromDay, toDay)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '导出失败,请稍后再试')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={exportBar}>
      <span style={{ fontSize: 13, color: 'var(--hk-ink-700)', fontWeight: 600 }}>导出 CSV</span>
      <label style={dateLabel}>
        从
        <input type="date" value={fromDay} max={toDay} onChange={(e) => setFromDay(e.target.value)} style={dateInput} aria-label="导出开始日期" />
      </label>
      <label style={dateLabel}>
        到
        <input type="date" value={toDay} min={fromDay} onChange={(e) => setToDay(e.target.value)} style={dateInput} aria-label="导出结束日期" />
      </label>
      <button type="button" onClick={doExport} disabled={busy} style={ghostBtn}>
        {busy ? '导出中…' : '下载 CSV'}
      </button>
      {error && <span style={{ fontSize: 12, color: '#8f322a' }}>{error}</span>}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

/** RFC3339(Nano)→ 本地可读串(24 小时制)。非法/空原样或占位。 */
function fmt(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdTime: React.CSSProperties = { ...td, color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const ghostBtnSm: React.CSSProperties = { height: 26, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 12, cursor: 'pointer', whiteSpace: 'nowrap' }
const panel: React.CSSProperties = { flex: '1 1 320px', minWidth: 280, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)', padding: 'var(--hk-space-4)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }
const panelTitle: React.CSSProperties = { fontSize: 14, margin: 0, color: 'var(--hk-ink-900)' }
const dl: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)', margin: 0 }
const subtle: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)' }
const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
const exportBar: React.CSSProperties = { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-3) var(--hk-space-4)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)' }
const dateLabel: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-500)' }
const dateInput: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-2)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontSize: 13 }
