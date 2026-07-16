import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { listOrphans, reconcileOrphan } from './api'
import {
  buildReconcileRequest,
  mapOrphanRows,
  needsBackChargeConfirm,
  parseTenantFilter,
  reconcileStatusLabel,
  statusLabel,
  summarizeReconcile,
  type OrphanTableRow,
} from './orphanreconcile'
import { RECONCILE_STATUSES, type OrphanItem, type ReconcileStatus } from './types'

/*
 * 媒体任务孤儿对账(运营台·系统,stage7)。**money 敏感**。
 * 后端 /admin/v1/media-task-orphans(admin token):
 *   - 列 pending 孤儿:上游已创建任务却因租约丢失未落库、可能漏计费的真亏钱线索。
 *   - 对账:把孤儿标记为 reconciled/cancelled/ignored;reconciled 可勾选 back_charge 追扣余额。
 *
 * 安全姿态(镜像后端 Manual-First):追扣只在操作员显式勾选 + 二次确认时发生;
 * 幂等防双扣由后端状态门 + billing.Capture 双闸保障(本页只负责把意图清晰呈现给操作员)。
 * 本页不碰任何 pool/registry/gateway 等碰撞包模块。
 */

const PAGE_LIMIT = 200

export function OrphanReconcilePage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantFilter, setTenantFilter] = useState<number | null>(null)
  const [rows, setRows] = useState<OrphanItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  // 正在对账的孤儿 id(禁用其行的按钮,防重复点击)。
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listOrphans(tenantFilter, PAGE_LIMIT, signal)
        .then((resp) => setRows(resp.items ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载孤儿列表失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [tenantFilter],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  // 执行一次对账。status=目标终态;backCharge=是否追扣(money)。
  const doReconcile = useCallback(
    async (item: OrphanItem, status: ReconcileStatus, backCharge: boolean) => {
      const built = buildReconcileRequest(status, backCharge, '')
      if (!built.ok) {
        setError(built.error)
        return
      }
      // money 二次确认:仅真追扣(reconciled+backCharge)弹钱确认。
      if (needsBackChargeConfirm(status, backCharge)) {
        const ok = window.confirm(
          `确认追扣孤儿 #${item.id}(任务 #${item.task_id}、用户 #${item.user_id})漏掉的费用?\n` +
            `此动作会真实从用户余额扣款(走 billing 结算),不可轻易撤销。`,
        )
        if (!ok) return
      } else {
        const ok = window.confirm(
          `确认将孤儿 #${item.id} 标记为「${statusLabel(status)}」(不追扣)?`,
        )
        if (!ok) return
      }

      setBusyId(item.id)
      setError(null)
      setNotice(null)
      try {
        const resp = await reconcileOrphan(item.id, built.value)
        setNotice(`孤儿 #${item.id}:${summarizeReconcile(resp)}`)
        // 重新加载列表:已对账/取消/忽略的孤儿会从 pending 列表消失;追扣未发生的仍在。
        load()
      } catch (e) {
        if (e instanceof ApiError) {
          // 409 = 请求追扣但未扣到(后端把孤儿保持 pending、未扣款)。注意:后端 409 响应体是
          // reconcileResponse 形态({back_charge_outcome},非 {error:{code,message}} 信封),
          // 故 lib/api 拿不到精确 outcome 码(e.code 会回退成 http_409)。这里给出准确的通用中文文案,
          // 不再把 e.code 喂给 outcomeLabel(那只会显示 "http_409")。
          if (e.status === 409) {
            setError(
              `孤儿 #${item.id} 未追扣到费用:该任务的预扣已不可追(可能 hold 已释放 / 任务已归档 / 无预估金额)。` +
                `孤儿保持待处置、未扣款;可调查后重试或改为「忽略」关闭。`,
            )
          } else {
            setError(`孤儿 #${item.id} 对账失败:${e.message}(${e.code})`)
          }
        } else {
          setError('对账失败')
        }
      } finally {
        setBusyId(null)
      }
    },
    [load],
  )
  const tableRows = mapOrphanRows(rows)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>媒体任务孤儿对账</h1>
          <p className="hk-sub">
            孤儿 = 上游已创建任务却因租约丢失未落库、可能漏计费的真亏钱线索。对账走 Manual-First:
            标记终态;勾选「追扣」时<strong style={{ color: 'var(--hk-danger)' }}>真实从用户余额扣款</strong>(money,需二次确认),
            幂等防双扣由后端保障。
          </p>
        </div>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          setTenantFilter(parseTenantFilter(tenantInput))
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="按租户 ID 过滤(可选,留空=全部)">
          <input
            value={tenantInput}
            onChange={(e) => setTenantInput(e.target.value)}
            inputMode="numeric"
            placeholder="留空=跨租户全部"
            style={{ ...inp, width: 200 }}
          />
        </Field>
        <button type="submit" className="hk-btn hk-btn--green">
          应用过滤
        </button>
        <button type="button" onClick={() => load()} className="hk-btn">
          刷新
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      <section className="hk-card">
        <div className="hk-card__head">
          <h3>待处置孤儿(pending)</h3>
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>
            {loading ? '加载中…' : `共 ${rows.length} 条`}
          </span>
        </div>

        {loading && rows.length === 0 ? (
          <EmptyState title="正在加载待处置孤儿" hint="请稍候。" />
        ) : rows.length === 0 ? (
          <EmptyState title="暂无待处置孤儿" hint="当前过滤范围内没有需要人工对账的孤儿记录。" />
        ) : (
          <DataListTable
            label="待处置孤儿"
            rows={tableRows}
            rowKey={(row) => row.id}
            columns={orphanColumns(busyId, doReconcile)}
          />
        )}
      </section>
    </div>
  )
}

function OrphanActions({
  item,
  busy,
  onReconcile,
}: {
  item: OrphanItem
  busy: boolean
  onReconcile: (item: OrphanItem, status: ReconcileStatus, backCharge: boolean) => void
}) {
  const [status, setStatus] = useState<ReconcileStatus>('reconciled')
  const [backCharge, setBackCharge] = useState(false)
  // money 守卫(镜像后端):追扣仅 reconciled 合法;切到取消/忽略时强制取消勾选。
  const backChargeAllowed = status === 'reconciled'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, whiteSpace: 'nowrap' }}>
          <select
            value={status}
            disabled={busy}
            onChange={(e) => {
              const next = e.target.value as ReconcileStatus
              setStatus(next)
              if (next !== 'reconciled') setBackCharge(false)
            }}
            style={selBox}
          >
            {RECONCILE_STATUSES.map((s) => (
              <option key={s} value={s}>
                {reconcileStatusLabel(s)}
              </option>
            ))}
          </select>
          <label
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 6,
              fontSize: 12,
              color: backChargeAllowed ? 'var(--hk-danger)' : 'var(--hk-ink-300)',
              cursor: backChargeAllowed && !busy ? 'pointer' : 'not-allowed',
            }}
            title={backChargeAllowed ? '勾选后将真实从用户余额追扣(money)' : '仅「对账」终态可追扣'}
          >
            <input
              type="checkbox"
              checked={backCharge}
              disabled={busy || !backChargeAllowed}
              onChange={(e) => setBackCharge(e.target.checked)}
            />
            追扣余额(money)
          </label>
          <button
            type="button"
            disabled={busy}
            onClick={() => onReconcile(item, status, backCharge)}
            className={backCharge ? 'hk-btn hk-btn--sm hk-btn--danger' : 'hk-btn hk-btn--sm hk-btn--green'}
          >
            {busy ? '处理中…' : backCharge ? '对账并追扣' : '提交处置'}
          </button>
    </div>
  )
}

function orphanColumns(
  busyId: number | null,
  onReconcile: (item: OrphanItem, status: ReconcileStatus, backCharge: boolean) => void,
): DataListColumn<OrphanTableRow>[] {
  return [
    { key: 'id', label: '孤儿', render: (row) => <span className="hk-mono">#{row.id}</span> },
    { key: 'task', label: '任务', render: (row) => <span className="hk-mono">{row.task}</span> },
    { key: 'tenant', label: '租户', render: (row) => <span className="hk-mono">{row.tenant}</span> },
    { key: 'user', label: '用户', render: (row) => <span className="hk-mono">{row.user}</span> },
    { key: 'provider', label: '厂商', render: (row) => row.provider },
    { key: 'provider-task', label: '上游任务 ID', render: (row) => <span className="hk-mono" style={{ color: 'var(--hk-ink-500)' }}>{row.providerTaskId}</span> },
    { key: 'estimated-charge', label: '预估漏扣', render: (row) => <span className="hk-mono">{row.estimatedCharge}</span> },
    { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
    { key: 'observed-at', label: '上报时间', render: (row) => <span className="hk-mono">{row.observedAt}</span> },
    { key: 'actions', label: '处置', render: (row) => <OrphanActions item={row.item} busy={busyId === row.id} onReconcile={onReconcile} /> },
  ]
}

/* ——— 小工具组件 / 样式(本页私有) ——— */

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
  return (
    <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>
      {children}
    </div>
  )
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const selBox: React.CSSProperties = { height: 30, padding: '0 var(--hk-space-2)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 12, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)' }
