import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { getModerationConfig, listModerationLogs, updateModerationConfig } from './api'
import { configToForm, mapModerationLogRows, validateConfig } from './moderation'
import type { ModerationLogTableRow } from './moderation'
import { BannedKeysCard, HashesCard, KeywordsCard } from './ModerationRules'
import { EMPTY_LOG_FILTERS, type LogFilters, type ModerationConfig, type ModerationLog } from './types'

/*
 * 内容审核(风控)运营台。管线第 8 站(安全审计)下的风控配置 + 命中日志。
 * 后端 /admin/v1/moderation(admin token):
 *   - 配置:开关(总开关/fail-closed)、采样率、自动封禁阈值与窗口(GET/PUT config)
 *   - 命中日志:只读列表,按 API Key 过滤、分页(GET logs)
 * 注意:platform_admin 角色下后端要求 tenant_id 必填,故本页先要租户 ID 再加载。
 * 关键词/哈希黑名单的增删/批量导入 + 被封 Key 解封见 ./ModerationRules;
 * 本页不碰任何 pool/registry/gateway 等碰撞包模块。
 */

type ConfigForm = ReturnType<typeof configToForm>

const PAGE_SIZE = 50

export function ModerationPage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantId, setTenantId] = useState<number | null>(null)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>内容审核</h1>
          <p className="hk-sub">
            风控:租户级审核配置(开关/采样/自动封禁/罚款)与命中日志(只读)。先指定租户 ID。
          </p>
        </div>
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
        <button type="submit" className="hk-btn hk-btn--green">
          加载
        </button>
      </form>

      {tenantId == null ? (
        <EmptyState title="请先选择租户" hint="请输入正整数租户 ID 后点击「加载」。" />
      ) : (
        <>
          <ConfigCard tenantId={tenantId} />
          <KeywordsCard tenantId={tenantId} />
          <HashesCard tenantId={tenantId} />
          <BannedKeysCard tenantId={tenantId} />
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
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>审核配置</h3>
        {meta.updatedAt && (
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>
            最近更新 {fmt(meta.updatedAt)} {meta.updatedBy ? `· by ${meta.updatedBy}` : ''}
          </span>
        )}
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {loading || !form ? (
        <EmptyState title={error ? '配置暂不可用' : '正在加载审核配置'} hint={error ? undefined : '请稍候。'} />
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
          <div style={{ gridColumn: '1 / -1', display: 'flex', gap: 'var(--hk-space-2)' }}>
            <button type="button" disabled={saving} onClick={save} className="hk-btn hk-btn--green">
              {saving ? '保存中…' : '保存配置'}
            </button>
            <button type="button" disabled={saving || loading} onClick={() => load()} className="hk-btn">
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
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>命中日志(只读)</h3>
        <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>已载 {rows.length} 条</span>
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
        <button type="submit" className="hk-btn hk-btn--green">
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraft(EMPTY_LOG_FILTERS)
            setFilters(EMPTY_LOG_FILTERS)
          }}
          className="hk-btn"
        >
          重置
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}

      {loading && rows.length === 0 ? (
        <EmptyState title="正在加载命中记录" hint="请稍候。" />
      ) : rows.length === 0 ? (
        <EmptyState title="该租户暂无命中记录" />
      ) : (
        <DataListTable label="审核命中日志" rows={mapModerationLogRows(rows)} rowKey={(row) => row.id} columns={moderationLogColumns} />
      )}

      {hasMore && (
        <div style={{ padding: 'var(--hk-space-4)', display: 'flex', justifyContent: 'center' }}>
          <button type="button" disabled={loading} onClick={() => void fetchPage(offset, true)} className="hk-btn">
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
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return (
    <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>
      {children}
    </div>
  )
}

function fmt(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }

const moderationLogColumns: DataListColumn<ModerationLogTableRow>[] = [
  { key: 'time', label: '时间', render: (row) => <span className="hk-mono">{row.occurredAt}</span> },
  { key: 'decision', label: '判定', badge: true, render: (row) => <StatusBadge tone={row.decisionTone}>{row.decision}</StatusBadge> },
  { key: 'reason', label: '原因码', render: (row) => <span style={{ color: 'var(--hk-ink-700)' }}>{row.reasonCode}</span> },
  { key: 'api-key', label: 'API Key', render: (row) => <span className="hk-mono">{row.apiKey}</span> },
  { key: 'user', label: '用户', render: (row) => <span className="hk-mono">{row.user}</span> },
  { key: 'hash', label: 'Payload Hash', render: (row) => <span className="hk-mono" style={{ color: 'var(--hk-ink-300)' }}>{row.payloadHash}</span> },
]
