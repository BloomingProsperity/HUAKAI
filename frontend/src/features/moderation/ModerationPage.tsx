import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getModerationConfig, listModerationLogs, updateModerationConfig } from './api'
import { configToForm, decisionLabel, decisionTone, formatFee, validateConfig } from './moderation'
import { EMPTY_LOG_FILTERS, type LogFilters, type ModerationConfig, type ModerationLog } from './types'

/*
 * 内容审核(风控)运营台。管线第 8 站(安全审计)下的风控配置 + 命中日志。
 * 后端 /admin/v1/moderation(admin token):
 *   - 配置:开关(总开关/fail-closed)、采样率、自动封禁阈值与窗口、违规罚款(GET/PUT config)
 *   - 命中日志:只读列表,按 API Key 过滤、分页(GET logs)
 * 注意:platform_admin 角色下后端要求 tenant_id 必填,故本页先要租户 ID 再加载。
 * 不在此页做关键词/哈希规则增删,也不碰任何 pool/registry/gateway 等碰撞包模块。
 */

type ConfigForm = ReturnType<typeof configToForm>

const PAGE_SIZE = 50

export function ModerationPage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantId, setTenantId] = useState<number | null>(null)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>内容审核</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          风控:租户级审核配置(开关/采样/自动封禁/罚款)与命中日志(只读)。先指定租户 ID。
        </p>
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
        <button type="submit" style={primaryBtn}>
          加载
        </button>
      </form>

      {tenantId == null ? (
        <Empty>请输入正整数租户 ID 后点击「加载」。</Empty>
      ) : (
        <>
          <ConfigCard tenantId={tenantId} />
          <LogsCard tenantId={tenantId} />
        </>
      )}
    </div>
  )
}

function ConfigCard({ tenantId }: { tenantId: number }) {
  const [form, setForm] = useState<ConfigForm | null>(null)
  const [meta, setMeta] = useState<{ updatedBy?: string; updatedAt?: string }>({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      setNotice(null)
      getModerationConfig(tenantId, signal)
        .then((cfg: ModerationConfig) => {
          setForm(configToForm(cfg))
          setMeta({ updatedBy: cfg.updated_by, updatedAt: cfg.updated_at })
        })
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载审核配置失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [tenantId],
  )

  // tenantId 变更触发加载;用 key 重置(见 LogsCard 同理),这里用 useState 惰性 + 首次 effect。
  useFirstLoad(load)

  const setF = <K extends keyof ConfigForm>(k: K, v: ConfigForm[K]) =>
    setForm((f) => (f ? { ...f, [k]: v } : f))

  const save = async () => {
    if (!form) return
    const v = validateConfig(tenantId, form)
    if (!v.ok) {
      setError(v.error)
      return
    }
    setSaving(true)
    setError(null)
    setNotice(null)
    try {
      const cfg = await updateModerationConfig(v.value)
      setForm(configToForm(cfg))
      setMeta({ updatedBy: cfg.updated_by, updatedAt: cfg.updated_at })
      setNotice('已保存。')
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>审核配置</h2>
        {meta.updatedAt && (
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>
            最近更新 {fmt(meta.updatedAt)} {meta.updatedBy ? `· by ${meta.updatedBy}` : ''}
          </span>
        )}
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {loading || !form ? (
        <Empty>{error ? '—' : '加载中…'}</Empty>
      ) : (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 'var(--hk-space-4)', padding: 'var(--hk-space-4)' }}>
          <Toggle label="启用审核(总开关)" checked={form.enabled} onChange={(v) => setF('enabled', v)} />
          <Toggle
            label="fail-closed(审核后端异常时拦截)"
            checked={form.failClosed}
            onChange={(v) => setF('failClosed', v)}
          />
          <NumField
            label="采样率(%,0~100)"
            value={form.sampleRatePct}
            onChange={(v) => setF('sampleRatePct', v)}
          />
          <NumField
            label="自动封禁阈值(次)"
            value={form.banThreshold}
            onChange={(v) => setF('banThreshold', v)}
          />
          <NumField
            label="封禁统计窗口(秒,>0)"
            value={form.banWindowSeconds}
            onChange={(v) => setF('banWindowSeconds', v)}
          />
          <Field label="违规罚款(USD)">
            <input
              value={form.violationFeeUsd}
              onChange={(e) => setF('violationFeeUsd', e.target.value)}
              inputMode="decimal"
              placeholder="如 0 或 1.50"
              style={inp}
            />
          </Field>
          <div style={{ gridColumn: '1 / -1', display: 'flex', gap: 'var(--hk-space-2)' }}>
            <button type="button" disabled={saving} onClick={save} style={primaryBtn}>
              {saving ? '保存中…' : '保存配置'}
            </button>
            <button type="button" disabled={saving || loading} onClick={() => load()} style={ghostBtn}>
              重新加载
            </button>
          </div>
        </div>
      )}
    </section>
  )
}

function LogsCard({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<ModerationLog[]>([])
  const [offset, setOffset] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<LogFilters>(EMPTY_LOG_FILTERS)
  const [filters, setFilters] = useState<LogFilters>(EMPTY_LOG_FILTERS)

  const fetchPage = useCallback(
    async (off: number, append: boolean, signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      try {
        const resp = await listModerationLogs(tenantId, filters, PAGE_SIZE, off, signal)
        const items = resp.items ?? []
        setRows((prev) => (append ? [...prev, ...items] : items))
        setOffset(off + items.length)
        setHasMore(items.length === PAGE_SIZE)
      } catch (e) {
        if (signal?.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载命中日志失败')
      } finally {
        if (!signal?.aborted) setLoading(false)
      }
    },
    [tenantId, filters],
  )

  // filters / tenantId 变更:从头加载。
  useReload(() => {
    const ctrl = new AbortController()
    setRows([])
    setOffset(0)
    void fetchPage(0, false, ctrl.signal)
    return () => ctrl.abort()
  }, [fetchPage])

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>命中日志(只读)</h2>
        <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>已载 {rows.length} 条</span>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          setFilters(draft)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)' }}
      >
        <Field label="按 API Key ID 过滤(可选)">
          <input
            value={draft.apiKeyId}
            onChange={(e) => setDraft({ apiKeyId: e.target.value })}
            inputMode="numeric"
            placeholder="留空=全部"
            style={{ ...inp, width: 180 }}
          />
        </Field>
        <button type="submit" style={primaryBtn}>
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraft(EMPTY_LOG_FILTERS)
            setFilters(EMPTY_LOG_FILTERS)
          }}
          style={ghostBtn}
        >
          重置
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}

      {loading && rows.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : rows.length === 0 ? (
        <Empty>该租户暂无命中记录。</Empty>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr>
                {['时间', '判定', '原因码', 'API Key', '用户', '罚款(USD)', 'Payload Hash'].map((h) => (
                  <th key={h} style={th}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                  <td style={tdMono}>{fmt(row.occurred_at)}</td>
                  <td style={td}>
                    <StatusBadge tone={decisionTone(row.decision)}>{decisionLabel(row.decision)}</StatusBadge>
                  </td>
                  <td style={{ ...td, color: 'var(--hk-ink-700)' }}>{row.reason_code || '—'}</td>
                  <td style={tdMono}>#{row.api_key_id}</td>
                  <td style={tdMono}>#{row.user_id}</td>
                  <td style={tdMono}>{formatFee(row.violation_fee_usd)}</td>
                  <td style={{ ...tdMono, color: 'var(--hk-ink-300)' }}>{short(row.payload_hash)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {hasMore && (
        <div style={{ padding: 'var(--hk-space-4)', display: 'flex', justifyContent: 'center' }}>
          <button type="button" disabled={loading} onClick={() => void fetchPage(offset, true)} style={ghostBtn}>
            {loading ? '加载中…' : '加载更多'}
          </button>
        </div>
      )}
    </section>
  )
}

/* ——— 小工具组件 / 样式(本页私有) ——— */

// useFirstLoad / useReload:把 effect 收敛成显式命名,避免重复样板。
function useFirstLoad(load: (signal?: AbortSignal) => void) {
  useReload(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])
}

// 极薄包装:语义化的"依赖变就重跑"。等价 useEffect,但读起来更贴近意图。
function useReload(run: () => void | (() => void), deps: React.DependencyList) {
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(run, deps)
}

function Toggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-700)', cursor: 'pointer' }}>
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  )
}

function NumField({ label, value, onChange }: { label: string; value: number; onChange: (v: number) => void }) {
  return (
    <Field label={label}>
      <input
        type="number"
        value={Number.isFinite(value) ? value : ''}
        onChange={(e) => onChange(e.target.value === '' ? NaN : Number(e.target.value))}
        style={inp}
      />
    </Field>
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
      ? { color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
      : { color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return (
    <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>
      {children}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

function short(s: string): string {
  if (!s) return '—'
  return s.length > 14 ? `${s.slice(0, 8)}…${s.slice(-4)}` : s
}
function fmt(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'top' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
