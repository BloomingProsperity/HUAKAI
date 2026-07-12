import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import {
  buildIPAllowlist,
  buildIPBlacklist,
  buildModelAllowlist,
  emptyGroupForm,
  emptyQuotaForm,
  modeLabel,
  windowKindLabel,
  firstInvalidIP,
  groupToForm,
  ipAllowlistFromView,
  ipBlacklistFromView,
  listToText,
  metricLabel,
  modelAllowlistFromView,
  parseList,
  quotaToForm,
  quotaToUsage,
  usagePercent,
  validateGroup,
  validateQuota,
  QUOTA_MODE_OPTIONS,
  WINDOW_KIND_OPTIONS,
  type GroupForm,
  type QuotaForm,
  type QuotaMetric,
  type QuotaUsageView,
} from './controls'
import {
  getGroupOrNull,
  getIPAllowlist,
  getIPBlacklist,
  getModelAllowlist,
  getQuotaOrNull,
  putGroup,
  putIPAllowlist,
  putIPBlacklist,
  putModelAllowlist,
  putQuota,
} from './controlsApi'

/*
 * API Key「高级控制」分区(挂在 EditKeyModal 内)。
 * 逐项 GET 回填 + PUT 保存,共 5 项:配额(money 敏感)/分组/IP 白名单/IP 黑名单/模型白名单。
 * 全部走 /v1/api-keys/{id}/...(session 鉴权,经 lib/api 自动带 session token)。
 * 每项独立保存、独立 busy/错误/成功提示,互不阻塞。配额是 money 敏感项,明确标注「0=不限」。
 */
export function KeyControlsSection({ apiKeyId }: { apiKeyId: number }) {
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)

  // 各项表单态。
  const [quota, setQuota] = useState<QuotaForm>(emptyQuotaForm())
  // 配额用量(只读):当前窗口已用/剩余/窗口结束时刻。未配置配额时为 null。
  const [quotaUsage, setQuotaUsage] = useState<QuotaUsageView | null>(null)
  const [group, setGroup] = useState<GroupForm>(emptyGroupForm())
  const [allowText, setAllowText] = useState('')
  const [blockText, setBlockText] = useState('')
  const [modelText, setModelText] = useState('')

  const loadAll = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setLoadError(null)
      // 并行拉取五项;配额/分组的「未配置 404」已在数据层吞成 null。
      Promise.all([
        getQuotaOrNull(apiKeyId, signal),
        getGroupOrNull(apiKeyId, signal),
        getIPAllowlist(apiKeyId, signal),
        getIPBlacklist(apiKeyId, signal),
        getModelAllowlist(apiKeyId, signal),
      ])
        .then(([q, g, allow, block, models]) => {
          if (signal.aborted) return
          setQuota(q ? quotaToForm(q) : emptyQuotaForm())
          setQuotaUsage(q ? quotaToUsage(q) : null)
          setGroup(g ? groupToForm(g) : emptyGroupForm())
          setAllowText(listToText(ipAllowlistFromView(allow)))
          setBlockText(listToText(ipBlacklistFromView(block)))
          setModelText(listToText(modelAllowlistFromView(models)))
        })
        .catch((e: unknown) => {
          if (signal.aborted) return
          setLoadError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载高级控制失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [apiKeyId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadAll(ctrl.signal)
    return () => ctrl.abort()
  }, [loadAll])

  if (loading) {
    return <div style={hint}>加载高级控制…</div>
  }
  if (loadError) {
    return <div style={errBox}>{loadError}</div>
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <h3 style={{ fontSize: 14, margin: 0, color: 'var(--hk-ink-900)' }}>高级控制</h3>

      {/* 配额(money 敏感):只读用量展示 + 上限/度量/窗口口径/模式可编辑 */}
      <QuotaRow apiKeyId={apiKeyId} form={quota} onChange={setQuota} usage={quotaUsage} onUsageChange={setQuotaUsage} />
      {/* 分组 */}
      <GroupRow apiKeyId={apiKeyId} form={group} onChange={setGroup} />
      {/* IP 白名单 */}
      <ListRow
        title="IP 白名单"
        help="每行一个 IP 或 CIDR(如 203.0.113.4 或 10.0.0.0/8)。留空=放行所有来源 IP。"
        value={allowText}
        onChange={setAllowText}
        validateIP
        save={(entries) => putIPAllowlist(apiKeyId, buildIPAllowlist(entries))}
      />
      {/* IP 黑名单 */}
      <ListRow
        title="IP 黑名单"
        help="每行一个 IP 或 CIDR。命中即拒绝。留空=不拦截任何 IP。"
        value={blockText}
        onChange={setBlockText}
        validateIP
        save={(entries) => putIPBlacklist(apiKeyId, buildIPBlacklist(entries))}
      />
      {/* 模型白名单 */}
      <ListRow
        title="模型白名单"
        help="每行一个模型 ID(如 gpt-4o、claude-3-5-sonnet)。留空=放行所有模型。"
        value={modelText}
        onChange={setModelText}
        save={(entries) => putModelAllowlist(apiKeyId, buildModelAllowlist(entries))}
      />
    </div>
  )
}

// ── 配额行(money 敏感)─────────────────────────────────────────────────────────
function QuotaRow({
  apiKeyId,
  form,
  onChange,
  usage,
  onUsageChange,
}: {
  apiKeyId: number
  form: QuotaForm
  onChange: (f: QuotaForm) => void
  usage: QuotaUsageView | null
  onUsageChange: (u: QuotaUsageView | null) => void
}) {
  const [state, setState] = useState<RowState>(idleState)
  const save = async () => {
    const v = validateQuota(form)
    if (!v.ok) {
      setState({ kind: 'error', msg: v.error })
      return
    }
    // money 敏感:设定单 key 用量上限 + 窗口口径 + 模式,均直接影响计费/限流行为。
    // 窗口/模式现为可编辑,确认文案如实带上将保存的窗口与模式(空值取选项默认标签),避免静默改动计费口径。
    const isUnlimited = v.value.limit_usd === '0'
    const windowText = WINDOW_KIND_OPTIONS.find((o) => o.value === form.windowKind)?.label ?? windowKindLabel(form.windowKind)
    const modeText = QUOTA_MODE_OPTIONS.find((o) => o.value === form.mode)?.label ?? modeLabel(form.mode)
    const windowSuffix = `\n窗口口径:${windowText}${form.windowKind === 'fixed' ? `(${form.windowSeconds} 秒)` : ''} · 模式:${modeText}。`
    const confirmMsg = isUnlimited
      ? `将该 Key 的用量上限设为「不限」(无限额)?${windowSuffix}`
      : `将该 Key 的用量上限设为 ${v.value.limit_usd}(${metricLabel(form.metric)})?超出后按所选模式处理。${windowSuffix}`
    if (!window.confirm(confirmMsg)) return
    setState(busyState)
    try {
      const out = await putQuota(apiKeyId, v.value)
      onChange(quotaToForm(out))
      onUsageChange(quotaToUsage(out))
      setState({ kind: 'ok', msg: '已保存' })
    } catch (e) {
      setState({ kind: 'error', msg: errMsg(e) })
    }
  }
  const metrics: Array<{ value: QuotaMetric; label: string }> = [
    { value: 'cost-usd', label: '消费金额(USD)' },
    { value: 'request-count', label: '请求次数' },
  ]
  // 切换窗口口径时,离开固定窗口需清零 window_seconds(日历窗口后端要求为 0)。
  const onWindowKindChange = (kind: string) => {
    onChange({ ...form, windowKind: kind, windowSeconds: kind === 'fixed' ? form.windowSeconds : 0 })
  }
  return (
    <Card title="用量上限(配额)" badge="计费敏感" save={save} state={state}>
      {/* 只读用量展示(KEY-007):已用 / 剩余 / 窗口结束时刻 */}
      <QuotaUsagePanel usage={usage} />
      <p style={help}>设定该 Key 在当前窗口的用量上限。0 或留空 = 不限额。窗口口径与模式为计费敏感项,保存前会二次确认。</p>
      <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <label style={fieldLabel}>
          上限
          <input
            value={form.limitUsd}
            onChange={(e) => onChange({ ...form, limitUsd: e.target.value })}
            placeholder="0 = 不限"
            style={{ ...inp, width: 140 }}
          />
        </label>
        <label style={fieldLabel}>
          度量
          <select
            value={form.metric}
            onChange={(e) => onChange({ ...form, metric: e.target.value as QuotaMetric })}
            style={{ ...inp, width: 160 }}
          >
            {metrics.map((m) => (
              <option key={m.value} value={m.value}>
                {m.label}
              </option>
            ))}
          </select>
        </label>
        {/* 窗口口径(money 敏感):每日/每周/每月/固定窗 */}
        <label style={fieldLabel}>
          窗口口径
          <select value={form.windowKind} onChange={(e) => onWindowKindChange(e.target.value)} style={{ ...inp, width: 200 }}>
            {WINDOW_KIND_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        {/* 固定窗口专属:窗口秒数(后端要求 >0) */}
        {form.windowKind === 'fixed' && (
          <label style={fieldLabel}>
            窗口秒数
            <input
              type="number"
              min={1}
              value={form.windowSeconds || ''}
              onChange={(e) => onChange({ ...form, windowSeconds: Math.max(0, Math.floor(Number(e.target.value) || 0)) })}
              placeholder="如 3600"
              style={{ ...inp, width: 140 }}
            />
          </label>
        )}
        {/* 消费上限模式(money 敏感):阻断/仅观测/… */}
        <label style={fieldLabel}>
          超限模式
          <select value={form.mode} onChange={(e) => onChange({ ...form, mode: e.target.value })} style={{ ...inp, width: 200 }}>
            {QUOTA_MODE_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
      </div>
    </Card>
  )
}

// ── 配额用量只读面板 ───────────────────────────────────────────────────────────
function QuotaUsagePanel({ usage }: { usage: QuotaUsageView | null }) {
  if (!usage) {
    return <p style={help}>当前未配置配额(无用量上限)。设置下方上限后可在此查看已用/剩余。</p>
  }
  const unlimited = Number(usage.limitUsd) <= 0 || !Number.isFinite(Number(usage.limitUsd))
  const pct = usagePercent(usage.usedUsd, usage.limitUsd)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, padding: 'var(--hk-space-2) var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)' }}>
      <div style={{ display: 'flex', gap: 'var(--hk-space-4)', flexWrap: 'wrap', fontSize: 12, color: 'var(--hk-ink-700)' }}>
        <span>
          已用 <strong style={{ color: 'var(--hk-ink-900)' }}>{usage.usedUsd}</strong>
        </span>
        <span>
          上限 <strong style={{ color: 'var(--hk-ink-900)' }}>{unlimited ? '不限' : usage.limitUsd}</strong>
        </span>
        {!unlimited && usage.remainingUsd != null && (
          <span>
            剩余 <strong style={{ color: 'var(--hk-primary-600)' }}>{usage.remainingUsd}</strong>
          </span>
        )}
        {usage.windowEnd && <span>窗口结束 {fmtTime(usage.windowEnd)}</span>}
      </div>
      {pct != null && (
        <div style={{ height: 6, borderRadius: 3, background: 'var(--hk-surface-sunken)', overflow: 'hidden' }} aria-label="用量进度">
          <div style={{ width: `${pct}%`, height: '100%', background: pct >= 100 ? 'var(--hk-danger)' : 'var(--hk-primary-500)', transition: 'width .2s' }} />
        </div>
      )}
    </div>
  )
}

/** ISO 时间 → 本地可读串(展示窗口结束时刻)。 */
function fmtTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

// ── 分组行 ─────────────────────────────────────────────────────────────────────
function GroupRow({
  apiKeyId,
  form,
  onChange,
}: {
  apiKeyId: number
  form: GroupForm
  onChange: (f: GroupForm) => void
}) {
  const [state, setState] = useState<RowState>(idleState)
  const save = async () => {
    const v = validateGroup(form)
    if (!v.ok) {
      setState({ kind: 'error', msg: v.error })
      return
    }
    setState(busyState)
    try {
      const out = await putGroup(apiKeyId, v.value)
      onChange(groupToForm(out))
      setState({ kind: 'ok', msg: v.value.group_id == null ? '已清除分组绑定' : '已保存' })
    } catch (e) {
      setState({ kind: 'error', msg: errMsg(e) })
    }
  }
  return (
    <Card title="分组绑定" save={save} state={state}>
      <p style={help}>把该 Key 绑定到一个分组(倍率/策略随分组)。留空 = 不绑定。</p>
      <label style={fieldLabel}>
        分组 ID
        <input
          value={form.groupId}
          onChange={(e) => onChange({ groupId: e.target.value })}
          placeholder="留空 = 不绑定"
          style={{ ...inp, width: 160 }}
        />
      </label>
    </Card>
  )
}

// ── 通用列表行(IP 白/黑名单、模型白名单)──────────────────────────────────────
function ListRow({
  title,
  help: helpText,
  value,
  onChange,
  save,
  validateIP,
}: {
  title: string
  help: string
  value: string
  onChange: (v: string) => void
  save: (entries: string[]) => Promise<unknown>
  validateIP?: boolean
}) {
  const [state, setState] = useState<RowState>(idleState)
  const doSave = async () => {
    const entries = parseList(value)
    if (validateIP) {
      // 前端预校验:拦明显非法 IP/CIDR,避免无谓 400(后端仍权威)。
      const bad = firstInvalidIP(entries)
      if (bad !== null) {
        setState({ kind: 'error', msg: `「${bad}」不是合法的 IP 或 CIDR` })
        return
      }
    }
    setState(busyState)
    try {
      await save(entries)
      // 规范化回填(去重后的结果),并提示清空语义。
      onChange(entries.join('\n'))
      setState({ kind: 'ok', msg: entries.length === 0 ? '已清空' : '已保存' })
    } catch (e) {
      setState({ kind: 'error', msg: errMsg(e) })
    }
  }
  return (
    <Card title={title} save={doSave} state={state}>
      <p style={help}>{helpText}</p>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={3}
        style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', fontFamily: 'var(--hk-font-mono)', resize: 'vertical', width: '100%' }}
      />
    </Card>
  )
}

// ── 卡片外壳 + 行内状态 ────────────────────────────────────────────────────────
type RowState =
  | { kind: 'idle' }
  | { kind: 'busy' }
  | { kind: 'ok'; msg: string }
  | { kind: 'error'; msg: string }
const idleState: RowState = { kind: 'idle' }
const busyState: RowState = { kind: 'busy' }

function Card({
  title,
  badge,
  save,
  state,
  children,
}: {
  title: string
  badge?: string
  save: () => void
  state: RowState
  children: React.ReactNode
}) {
  return (
    <div style={card}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
          <strong style={{ fontSize: 13, color: 'var(--hk-ink-900)' }}>{title}</strong>
          {badge && <span style={badgeStyle}>{badge}</span>}
        </div>
        <button type="button" disabled={state.kind === 'busy'} onClick={save} style={saveBtn}>
          {state.kind === 'busy' ? '保存中…' : '保存'}
        </button>
      </div>
      {children}
      {state.kind === 'ok' && <div style={okMsg}>{state.msg}</div>}
      {state.kind === 'error' && <div style={errMsgBox}>{state.msg}</div>}
    </div>
  )
}

function errMsg(e: unknown): string {
  return e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败'
}

// ── 样式(inline + var(--hk-*) token)─────────────────────────────────────────
const card: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)', padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface-sunken)' }
const help: React.CSSProperties = { margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }
const hint: React.CSSProperties = { fontSize: 13, color: 'var(--hk-ink-500)', padding: 'var(--hk-space-2)' }
const fieldLabel: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)' }
const saveBtn: React.CSSProperties = { height: 28, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-primary-700)', fontSize: 12, cursor: 'pointer', flexShrink: 0 }
const badgeStyle: React.CSSProperties = { fontSize: 11, padding: '1px 6px', borderRadius: 'var(--hk-radius-sm)', background: '#fff3e0', color: '#9a5b13', border: '1px solid #f3d9b5' }
const okMsg: React.CSSProperties = { fontSize: 12, color: 'var(--hk-primary-600)' }
const errMsgBox: React.CSSProperties = { fontSize: 12, color: 'var(--hk-danger)' }
const errBox: React.CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
