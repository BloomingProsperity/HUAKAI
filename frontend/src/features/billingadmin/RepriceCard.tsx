import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { repriceBilling } from './api'
import {
  canStartReprice,
  executeRepriceGuarded,
  repriceScopeSummary,
  sumRepriceCostDelta,
  validateRepriceForm,
} from './billingadmin'
import type { RepriceForm, RepriceResponse } from './types'

const INITIAL_FORM: RepriceForm = {
  scope: 'record',
  usageRecordId: '',
  tenantId: '',
  from: '',
  to: '',
  limit: '100',
  reason: '',
  acknowledged: false,
}

interface RepriceRun {
  response: RepriceResponse
  scope: string
  reason: string
}

/** money 入口独立成卡片，避免台账页继续膨胀；仍属于同一 billing feature。 */
export function RepriceCard() {
  const [form, setForm] = useState<RepriceForm>(INITIAL_FORM)
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState<'preview' | 'apply' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [run, setRun] = useState<RepriceRun | null>(null)

  const change = <K extends keyof RepriceForm>(key: K, value: RepriceForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
    setConfirming(false)
    setError(null)
  }

  const openConfirmation = () => {
    const invalid = validateRepriceForm(form)
    if (invalid) {
      setError(invalid)
      return
    }
    if (!form.acknowledged) {
      setError('请先勾选已了解重算会改写计费记录')
      return
    }
    setError(null)
    setConfirming(true)
  }

  const execute = async (intent: 'preview' | 'apply', confirmed: boolean) => {
    setBusy(intent)
    setError(null)
    try {
      const scope = repriceScopeSummary(form)
      const response = await executeRepriceGuarded(form, intent, confirmed, repriceBilling)
      setRun({ response, scope, reason: form.reason.trim() })
      setConfirming(false)
    } catch (cause) {
      setError(cause instanceof ApiError ? `${cause.message}(${cause.code})` : cause instanceof Error ? cause.message : '重算请求失败')
    } finally {
      setBusy(null)
    }
  }

  const ready = canStartReprice(form)
  return (
    <section className="hk-card hk-danger-card">
      <div className="hk-card__head">
        <h3>按当前价表重算</h3>
        <span className="hk-pill hk-pill--crit">触钱操作</span>
      </div>
      <div className="hk-card__body hk-col">
        <p className="hk-section-copy">
          仅处理待对账记录，并按当前价表重新计算权威成本。服务会按记录阻止重复重算；实际执行会新增重算事件并改变计费结论。
        </p>
        <div className="hk-confirmbox">
          后端当前没有审计原因或客户端幂等键字段：下方原因用于本页强制确认，<strong>不会写入后端审计</strong>；请求体不会伪造这两个字段。
        </div>

        <div className="hk-seg" aria-label="重算范围">
          <button type="button" className={form.scope === 'record' ? 'is-on' : ''} onClick={() => change('scope', 'record')}>单条记录</button>
          <button type="button" className={form.scope === 'window' ? 'is-on' : ''} onClick={() => change('scope', 'window')}>租户时间窗</button>
        </div>

        <div className="hk-form-grid">
          {form.scope === 'record' ? (
            <label className="hk-field">
              <span>用量记录 ID</span>
              <input className="hk-input hk-mono" inputMode="numeric" value={form.usageRecordId} onChange={(event) => change('usageRecordId', event.target.value)} placeholder="usage_record_id" />
            </label>
          ) : (
            <>
              <label className="hk-field">
                <span>租户 ID</span>
                <input className="hk-input hk-mono" inputMode="numeric" value={form.tenantId} onChange={(event) => change('tenantId', event.target.value)} placeholder="tenant_id" />
              </label>
              <label className="hk-field">
                <span>开始时间</span>
                <input className="hk-input" type="datetime-local" value={form.from} onChange={(event) => change('from', event.target.value)} />
              </label>
              <label className="hk-field">
                <span>结束时间</span>
                <input className="hk-input" type="datetime-local" value={form.to} onChange={(event) => change('to', event.target.value)} />
              </label>
              <label className="hk-field">
                <span>记录上限（1–100）</span>
                <input className="hk-input hk-mono" inputMode="numeric" value={form.limit} onChange={(event) => change('limit', event.target.value)} />
              </label>
            </>
          )}
          <label className="hk-field" style={{ gridColumn: '1 / -1' }}>
            <span>操作原因（必填）</span>
            <textarea className="hk-input hk-textarea" rows={3} value={form.reason} onChange={(event) => change('reason', event.target.value)} placeholder="说明为什么需要重算，便于人工复核本次操作意图" />
          </label>
        </div>

        <label className="hk-inline-actions" style={{ color: 'var(--hk-ink-700)', fontSize: 13 }}>
          <input type="checkbox" checked={form.acknowledged} onChange={(event) => change('acknowledged', event.target.checked)} />
          我已了解将改写计费记录
        </label>

        {error && <div className="hk-errorbox" role="alert">{error}</div>}
        <div className="hk-inline-actions">
          <button type="button" className="hk-btn" disabled={!ready || busy !== null} onClick={() => execute('preview', false)}>
            {busy === 'preview' ? '预演中…' : '仅预演，不写入'}
          </button>
          <button type="button" className="hk-btn hk-btn--danger" disabled={!ready || busy !== null} onClick={openConfirmation}>
            按当前价表重算
          </button>
        </div>

        {confirming && (
          <div className="hk-confirmbox" role="alertdialog" aria-labelledby="reprice-confirm-title">
            <strong id="reprice-confirm-title">确认实际改写计费记录？</strong>
            <div>影响范围：{repriceScopeSummary(form)}</div>
            <div>操作原因：{form.reason.trim()}</div>
            <div>提交后将发送 <code>dry_run=false</code>；已重算记录会返回 already_repriced，其余符合条件的记录会写入重算事件。</div>
            <div className="hk-inline-actions" style={{ marginTop: 'var(--hk-space-2)' }}>
              <button type="button" className="hk-btn hk-btn--danger" disabled={busy !== null} onClick={() => execute('apply', true)}>
                {busy === 'apply' ? '重算中…' : '确认改写并重算'}
              </button>
              <button type="button" className="hk-btn" disabled={busy !== null} onClick={() => setConfirming(false)}>取消</button>
            </div>
          </div>
        )}

        {run && <RepriceResult run={run} />}
      </div>
    </section>
  )
}

function RepriceResult({ run }: { run: RepriceRun }) {
  const { response } = run
  const changed = response.dry_run ? response.summary.would_apply : response.summary.repriced
  const delta = sumRepriceCostDelta(response.items)
  return (
    <div className="hk-codebox" aria-live="polite">
      <div className="hk-inline-actions">
        <strong>{response.dry_run ? '重算预演结果' : '实际重算结果'}</strong>
        <span className={`hk-pill ${response.summary.failed > 0 ? 'hk-pill--crit' : 'hk-pill--ok'}`}>
          {response.summary.failed > 0 ? '含失败项' : '请求完成'}
        </span>
      </div>
      <p className="hk-section-copy">范围：{run.scope}；本页确认原因：{run.reason}</p>
      <div className="hk-metric-grid">
        <Metric label={response.dry_run ? '预计重算' : '已重算'} value={String(changed)} />
        <Metric label="响应总条数" value={String(response.summary.total)} />
        <Metric label="已重算过" value={String(response.summary.already_repriced)} />
        <Metric label="跳过 / 失败" value={`${response.summary.skipped} / ${response.summary.failed}`} />
        <Metric label="逐条差额合计" value={delta ?? '无法汇总'} mono />
      </div>
      <div className="hk-tablewrap">
        <table className="hk-table">
          <thead>
            <tr>{['记录', '租户', '状态', '原成本', '当前价成本', '差额', '定价来源 / 原因'].map((title) => <th key={title}>{title}</th>)}</tr>
          </thead>
          <tbody>
            {response.items.map((item) => (
              <tr key={`${item.tenant_id}-${item.usage_record_id}`}>
                <td className="hk-mono">{item.usage_record_id}</td>
                <td className="hk-mono">{item.tenant_id}</td>
                <td><span className={`hk-pill ${statusClass(item.status)}`}>{item.status}</span></td>
                <td className="hk-mono">{item.original_cost}</td>
                <td className="hk-mono">{item.authoritative_cost}</td>
                <td className="hk-mono">{item.cost_delta}</td>
                <td>{item.error_message || item.skipped_reason || item.pricing_source || '—'}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {response.items.length === 0 && <div className="hk-empty">该范围没有可重算记录。</div>}
      </div>
    </div>
  )
}

function Metric({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="hk-metric">
      <div className="hk-metric__label">{label}</div>
      <div className={`hk-metric__v ${mono ? 'hk-mono' : ''}`} style={{ fontSize: 17 }}>{value}</div>
    </div>
  )
}

function statusClass(status: string): string {
  if (status === 'repriced' || status === 'would_apply') return 'hk-pill--ok'
  if (status === 'error') return 'hk-pill--crit'
  if (status === 'skipped') return 'hk-pill--warn'
  return 'hk-pill--info'
}
