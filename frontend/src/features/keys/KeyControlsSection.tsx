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
  validateGroup,
  validateQuota,
  type GroupForm,
  type QuotaForm,
  type QuotaMetric,
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

      {/* 配额(money 敏感)*/}
      <QuotaRow apiKeyId={apiKeyId} form={quota} onChange={setQuota} />
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
}: {
  apiKeyId: number
  form: QuotaForm
  onChange: (f: QuotaForm) => void
}) {
  const [state, setState] = useState<RowState>(idleState)
  const save = async () => {
    const v = validateQuota(form)
    if (!v.ok) {
      setState({ kind: 'error', msg: v.error })
      return
    }
    // money 敏感:设定单 key 用量上限。"0"=不限,提示用户确认其影响。
    // 窗口/模式为 round-trip(保存限额不改窗口口径),确认文案里如实带上当前窗口与模式。
    const isUnlimited = v.value.limit_usd === '0'
    const windowSuffix = form.windowKind ? `窗口口径:${windowKindLabel(form.windowKind)} · 模式:${modeLabel(form.mode)}。` : ''
    const confirmMsg = isUnlimited
      ? `将该 Key 的用量上限设为「不限」(无限额)?${windowSuffix}`
      : `将该 Key 的用量上限设为 ${v.value.limit_usd}(${metricLabel(form.metric)})?超出后该 Key 将被限额。${windowSuffix}`
    if (!window.confirm(confirmMsg)) return
    setState(busyState)
    try {
      const out = await putQuota(apiKeyId, v.value)
      onChange(quotaToForm(out))
      setState({ kind: 'ok', msg: '已保存' })
    } catch (e) {
      setState({ kind: 'error', msg: errMsg(e) })
    }
  }
  const metrics: Array<{ value: QuotaMetric; label: string }> = [
    { value: 'cost-usd', label: '消费金额(USD)' },
    { value: 'request-count', label: '请求次数' },
  ]
  return (
    <Card title="用量上限(配额)" badge="计费敏感" save={save} state={state}>
      <p style={help}>
        设定该 Key 在当前窗口的用量上限。0 或留空 = 不限额。
        {form.windowKind ? `当前窗口口径:${windowKindLabel(form.windowKind)} · 模式:${modeLabel(form.mode)}(保存限额不会改变窗口口径)。` : ''}
      </p>
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
            style={{ ...inp, width: 180 }}
          >
            {metrics.map((m) => (
              <option key={m.value} value={m.value}>
                {m.label}
              </option>
            ))}
          </select>
        </label>
      </div>
    </Card>
  )
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
