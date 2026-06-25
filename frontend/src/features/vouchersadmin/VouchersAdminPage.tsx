import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { createVoucher, createVoucherBatch, getBatch, listVouchers, revokeVoucher } from './api'
import {
  batchStatusLabel,
  buildBatchRequest,
  buildCreateRequest,
  centsToYuan,
  EMPTY_BATCH_FORM,
  EMPTY_CREATE_FORM,
  filterByStatus,
  grantKindLabel,
  parseListTenantId,
  statusLabel,
  statusTone,
  type BatchForm,
  type CreateForm,
} from './vouchersadmin'
import type { Batch, BatchCreateResult, CreateResult, GetBatchResult, Voucher } from './types'

/*
 * 兑换码管理(运营台,admin 壳)。运维链周边站:面向卖额度场景批量发码 / 吊销 / 查批次。
 * 全部端点 adminGate(/v1/admin/vouchers,platform_admin RBAC)。money 相关只读展示,
 * 不在此动账(发码/吊销由后端服务层落账与审计)。后端绝不回明文 code,只在创建那一刻回一次,
 * 故创建成功后用专门弹窗当场展示明文供运营者保存。
 */
const STATUS_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '可用' },
  { value: 'expired', label: '已过期' },
  { value: 'exhausted', label: '已用尽' },
  { value: 'revoked', label: '已吊销' },
]

export function VouchersAdminPage() {
  const [tenantInput, setTenantInput] = useState('1')
  const [tenantId, setTenantId] = useState(1)
  const [statusFilter, setStatusFilter] = useState('')
  const [vouchers, setVouchers] = useState<Voucher[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [createKind, setCreateKind] = useState<'single' | 'batch' | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [batchView, setBatchView] = useState<number | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listVouchers(tenantId, 200, signal)
        .then((resp) => setVouchers(resp.vouchers ?? []))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载兑换码列表失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [tenantId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const applyTenant = () => {
    const parsed = parseListTenantId(tenantInput)
    if (parsed === null) {
      setError('租户 ID 必须为正整数')
      return
    }
    setError(null)
    setTenantId(parsed)
  }

  const onRevoke = async (v: Voucher) => {
    const reason = window.prompt(`吊销券 #${v.id}?可填写吊销原因(可选):`, '')
    if (reason === null) return // 取消
    setBusyId(v.id)
    setError(null)
    try {
      await revokeVoucher(v.id, { tenant_id: v.tenant_id, reason: reason.trim() || undefined })
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '吊销失败')
    } finally {
      setBusyId(null)
    }
  }

  const visible = filterByStatus(vouchers, statusFilter) as Voucher[]

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>兑换码管理</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            运营台 · 批量发码 / 吊销 / 批次查看。当前租户 {tenantId},共 {visible.length} 张。
          </p>
        </div>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', flexShrink: 0 }}>
          <button type="button" onClick={() => setCreateKind('single')} style={ghostBtn}>
            ＋ 单张
          </button>
          <button type="button" onClick={() => setCreateKind('batch')} style={newBtn}>
            ＋ 批量生成
          </button>
        </div>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          applyTenant()
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <label style={lbl}>
          租户 ID
          <input value={tenantInput} onChange={(e) => setTenantInput(e.target.value)} style={{ ...inp, width: 120 }} inputMode="numeric" />
        </label>
        <button type="submit" style={primaryBtn}>
          查询
        </button>
        <label style={lbl}>
          状态筛选
          <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} style={{ ...inp, width: 140 }}>
            {STATUS_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <button type="button" onClick={refresh} style={ghostBtn}>
          刷新
        </button>
      </form>

      {error && <div style={errBox}>{error}</div>}

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
        {loading && vouchers.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : visible.length === 0 ? (
          <Empty>没有兑换码。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['ID', '指纹', '面额', '种类', '兑换', '状态', '批次', '有效期', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {visible.map((v) => (
                  <tr key={v.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={tdNum}>{v.id}</td>
                    <td style={{ ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }}>{v.code_fingerprint}</td>
                    <td style={tdNum}>
                      {centsToYuan(v.amount_cents)} {v.currency_code}
                    </td>
                    <td style={td}>{grantKindLabel(v.grant_kind)}</td>
                    <td style={tdNum}>
                      {v.redeemed_count}/{v.max_redemptions}
                    </td>
                    <td style={td}>
                      <StatusBadge tone={statusTone(v.status)}>{statusLabel(v.status)}</StatusBadge>
                    </td>
                    <td style={td}>
                      {v.batch_id ? (
                        <button type="button" onClick={() => setBatchView(v.batch_id ?? null)} style={linkBtn}>
                          #{v.batch_id}
                        </button>
                      ) : (
                        <span style={{ color: 'var(--hk-ink-300)' }}>—</span>
                      )}
                    </td>
                    <td style={{ ...td, whiteSpace: 'nowrap', color: 'var(--hk-ink-500)', fontSize: 12 }}>
                      {fmt(v.valid_from)} ~ {fmt(v.valid_until)}
                    </td>
                    <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                      {v.status === 'active' && (
                        <button type="button" disabled={busyId === v.id} onClick={() => onRevoke(v)} style={dangerLinkBtn}>
                          吊销
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {createKind === 'single' && <CreateSingleModal onClose={() => setCreateKind(null)} onCreated={refresh} />}
      {createKind === 'batch' && <CreateBatchModal onClose={() => setCreateKind(null)} onCreated={refresh} />}
      {batchView !== null && <BatchDrawer tenantId={tenantId} batchId={batchView} onClose={() => setBatchView(null)} />}
    </div>
  )
}

/* ---------------- 单张创建 ---------------- */
function CreateSingleModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState<CreateForm>(EMPTY_CREATE_FORM)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<CreateResult | null>(null)
  const set = <K extends keyof CreateForm>(k: K, val: CreateForm[K]) => setForm((f) => ({ ...f, [k]: val }))

  const submit = async () => {
    const built = buildCreateRequest(form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setBusy(true)
    setError(null)
    try {
      const res = await createVoucher(built)
      setResult(res)
      onCreated()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onClose} title="新建兑换码(单张)">
      {result ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <div style={successBox}>创建成功。明文兑换码仅此一次显示,请立即保存:</div>
          {result.code && <CodeReveal code={result.code} />}
          <Row label="券 ID">{result.voucher.id}</Row>
          <Row label="面额">
            {centsToYuan(result.voucher.amount_cents)} {result.voucher.currency_code}
          </Row>
          <Row label="指纹">{result.voucher.code_fingerprint}</Row>
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <button type="button" onClick={onClose} style={primaryBtn}>
              完成
            </button>
          </div>
        </div>
      ) : (
        <>
          <Field label="租户 ID">
            <input value={form.tenantId} onChange={(e) => set('tenantId', e.target.value)} style={inp} inputMode="numeric" />
          </Field>
          <Field label="面额(元)">
            <input value={form.amountYuan} onChange={(e) => set('amountYuan', e.target.value)} style={inp} inputMode="decimal" placeholder="如 10.00" />
          </Field>
          <Field label="币种">
            <input value={form.currencyCode} onChange={(e) => set('currencyCode', e.target.value)} style={inp} />
          </Field>
          <Field label="自定义码(留空则后端随机生成)">
            <input value={form.code} onChange={(e) => set('code', e.target.value)} style={inp} placeholder="可选" />
          </Field>
          <Field label="生效时间">
            <input type="datetime-local" value={form.validFrom} onChange={(e) => set('validFrom', e.target.value)} style={inp} />
          </Field>
          <Field label="失效时间">
            <input type="datetime-local" value={form.validUntil} onChange={(e) => set('validUntil', e.target.value)} style={inp} />
          </Field>
          <Field label="最大兑换次数">
            <input value={form.maxRedemptions} onChange={(e) => set('maxRedemptions', e.target.value)} style={inp} inputMode="numeric" />
          </Field>
          <CheckRow checked={form.singleUsePerUser} onChange={(c) => set('singleUsePerUser', c)} label="每用户仅可兑换一次" />
          {error && <div style={errBox}>{error}</div>}
          <Actions busy={busy} onCancel={onClose} onSubmit={submit} submitLabel="创建" />
        </>
      )}
    </Modal>
  )
}

/* ---------------- 批量创建 ---------------- */
function CreateBatchModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState<BatchForm>(EMPTY_BATCH_FORM)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<BatchCreateResult | null>(null)
  const set = <K extends keyof BatchForm>(k: K, val: BatchForm[K]) => setForm((f) => ({ ...f, [k]: val }))

  const submit = async () => {
    const built = buildBatchRequest(form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setBusy(true)
    setError(null)
    try {
      const res = await createVoucherBatch(built)
      setResult(res)
      onCreated()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '批量创建失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onClose} title="批量生成兑换码">
      {result ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <div style={successBox}>
            批次 #{result.batch.id} 已生成 {result.batch.created_count}/{result.batch.requested_count} 张。明文码仅此一次显示,请导出保存:
          </div>
          <CodesReveal codes={result.codes.map((c) => c.code)} />
          <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
            <button type="button" onClick={onClose} style={primaryBtn}>
              完成
            </button>
          </div>
        </div>
      ) : (
        <>
          <Field label="租户 ID">
            <input value={form.tenantId} onChange={(e) => set('tenantId', e.target.value)} style={inp} inputMode="numeric" />
          </Field>
          <Field label="生成数量(1 ~ 1000)">
            <input value={form.count} onChange={(e) => set('count', e.target.value)} style={inp} inputMode="numeric" />
          </Field>
          <Field label="面额(元)">
            <input value={form.amountYuan} onChange={(e) => set('amountYuan', e.target.value)} style={inp} inputMode="decimal" placeholder="如 10.00" />
          </Field>
          <Field label="币种">
            <input value={form.currencyCode} onChange={(e) => set('currencyCode', e.target.value)} style={inp} />
          </Field>
          <Field label="生效时间">
            <input type="datetime-local" value={form.validFrom} onChange={(e) => set('validFrom', e.target.value)} style={inp} />
          </Field>
          <Field label="失效时间">
            <input type="datetime-local" value={form.validUntil} onChange={(e) => set('validUntil', e.target.value)} style={inp} />
          </Field>
          <Field label="每张最大兑换次数">
            <input value={form.maxRedemptions} onChange={(e) => set('maxRedemptions', e.target.value)} style={inp} inputMode="numeric" />
          </Field>
          <CheckRow checked={form.singleUsePerUser} onChange={(c) => set('singleUsePerUser', c)} label="每用户仅可兑换一次" />
          {error && <div style={errBox}>{error}</div>}
          <Actions busy={busy} onCancel={onClose} onSubmit={submit} submitLabel="生成" />
        </>
      )}
    </Modal>
  )
}

/* ---------------- 批次查看抽屉 ---------------- */
function BatchDrawer({ tenantId, batchId, onClose }: { tenantId: number; batchId: number; onClose: () => void }) {
  const [data, setData] = useState<GetBatchResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    getBatch(tenantId, batchId, ctrl.signal)
      .then(setData)
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载批次失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [tenantId, batchId])

  return (
    <div onClick={onClose} style={overlay}>
      <div
        onClick={(e) => e.stopPropagation()}
        style={{ marginLeft: 'auto', height: '100%', width: 'min(560px,100%)', background: 'var(--hk-surface)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', overflowY: 'auto' }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <h2 style={{ fontSize: 18 }}>批次 #{batchId}</h2>
          <button type="button" onClick={onClose} style={ghostBtn}>
            关闭
          </button>
        </div>
        {loading ? (
          <Empty>加载中…</Empty>
        ) : error ? (
          <div style={errBox}>{error}</div>
        ) : data ? (
          <>
            <BatchSummary batch={data.batch} />
            <div style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>该批次券 {data.vouchers.length} 张</div>
            <div style={{ border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', overflow: 'hidden' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                <thead>
                  <tr>
                    {['ID', '指纹', '兑换', '状态'].map((h) => (
                      <th key={h} style={th}>
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {data.vouchers.map((v) => (
                    <tr key={v.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                      <td style={tdNum}>{v.id}</td>
                      <td style={{ ...td, fontFamily: 'var(--hk-font-mono)' }}>{v.code_fingerprint}</td>
                      <td style={tdNum}>
                        {v.redeemed_count}/{v.max_redemptions}
                      </td>
                      <td style={td}>
                        <StatusBadge tone={statusTone(v.status)}>{statusLabel(v.status)}</StatusBadge>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        ) : null}
      </div>
    </div>
  )
}

function BatchSummary({ batch }: { batch: Batch }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--hk-space-2)', background: 'var(--hk-surface-sunken)', borderRadius: 'var(--hk-radius-md)', padding: 'var(--hk-space-3)' }}>
      <Row label="状态">
        <StatusBadge tone={statusTone(batch.status)}>{batchStatusLabel(batch.status)}</StatusBadge>
      </Row>
      <Row label="数量">
        {batch.created_count}/{batch.requested_count}
      </Row>
      <Row label="单张面额">
        {centsToYuan(batch.amount_cents)} {batch.currency_code}
      </Row>
      <Row label="有效期">
        {fmt(batch.valid_from)} ~ {fmt(batch.valid_until)}
      </Row>
    </div>
  )
}

/* ---------------- 明文码展示 ---------------- */
function CodeReveal({ code }: { code: string }) {
  return (
    <div style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 15, fontWeight: 600, padding: 'var(--hk-space-3)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)', borderRadius: 'var(--hk-radius-md)', wordBreak: 'break-all', color: 'var(--hk-ink-900)' }}>
      {code}
    </div>
  )
}

function CodesReveal({ codes }: { codes: string[] }) {
  // 多行明文,便于运营者一次性复制全部。只读 textarea,不落任何持久化。
  return (
    <textarea
      readOnly
      value={codes.join('\n')}
      rows={Math.min(12, Math.max(4, codes.length))}
      style={{ width: '100%', fontFamily: 'var(--hk-font-mono)', fontSize: 13, padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface-sunken)', color: 'var(--hk-ink-900)', resize: 'vertical' }}
    />
  )
}

/* ---------------- 通用小件 ---------------- */
function Modal({ title, children, onClose }: { title: string; children: React.ReactNode; onClose: () => void }) {
  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(460px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={{ fontSize: 18 }}>{title}</h2>
        {children}
      </div>
    </div>
  )
}

function Actions({ busy, onCancel, onSubmit, submitLabel }: { busy: boolean; onCancel: () => void; onSubmit: () => void; submitLabel: string }) {
  return (
    <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
      <button type="button" onClick={onCancel} style={ghostBtn}>
        取消
      </button>
      <button type="button" disabled={busy} onClick={onSubmit} style={primaryBtn}>
        {busy ? '提交中…' : submitLabel}
      </button>
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

function CheckRow({ checked, onChange, label }: { checked: boolean; onChange: (c: boolean) => void; label: string }) {
  return (
    <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-700)' }}>
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--hk-space-3)', fontSize: 13 }}>
      <span style={{ color: 'var(--hk-ink-500)' }}>{label}</span>
      <span style={{ color: 'var(--hk-ink-900)', textAlign: 'right' }}>{children}</span>
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}

/* ---------------- 样式 token(仅引 var(--hk-*),禁硬编码新色) ---------------- */
const lbl: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const newBtn: React.CSSProperties = { height: 36, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
const dangerLinkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: '#8f322a', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
const successBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', zIndex: 'var(--hk-z-overlay)' as unknown as number }
