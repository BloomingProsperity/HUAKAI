import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../../lib/api'
import { StatusBadge, healthTone } from '../../ui/StatusBadge'
import { clearAccountRateLimit, getProviderAccount, setAccountEnabled } from './api'
import { accountAvailableActions } from './detail'
import type { ProviderAccount } from './types'

/*
 * 账号详情页(P0)。GET /admin/v1/provider-accounts/{id} 展示账号全貌(分组字段)+ 运维动作:
 * 启用/停用(PATCH /{id}/enabled)、清除限流(POST /{id}/clear-rate-limit,仅限流态可用)。
 * 所有 mutation 带审计原因输入(reason 进 admin 审计)。
 */
export function AccountDetailPage() {
  const params = useParams()
  const id = Number(params.id)
  const [account, setAccount] = useState<ProviderAccount | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [flash, setFlash] = useState<string | null>(null)

  const fetchAccount = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      getProviderAccount(id, signal)
        .then((a) => setAccount(a))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载账号详情失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [id],
  )

  useEffect(() => {
    if (!Number.isInteger(id) || id <= 0) {
      setError('非法账号 ID')
      setLoading(false)
      return
    }
    const ctrl = new AbortController()
    fetchAccount(ctrl.signal)
    return () => ctrl.abort()
  }, [id, fetchAccount])

  const runAction = async (fn: () => Promise<ProviderAccount>, ok: string) => {
    setBusy(true)
    setFlash(null)
    setError(null)
    try {
      const updated = await fn()
      setAccount(updated)
      setReason('')
      setFlash(ok)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  if (loading) return <Center>加载中…</Center>
  if (error && !account) return <Center tone="danger">{error}</Center>
  if (!account) return <Center tone="danger">账号不存在</Center>

  const actions = accountAvailableActions(account)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)', maxWidth: 920 }}>
      <Link to="/accounts" style={{ fontSize: 13 }}>
        ← 返回账号列表
      </Link>
      <header style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <h1 style={{ fontSize: 22 }}>{account.name}</h1>
        <StatusBadge tone={account.enabled ? 'ok' : 'muted'}>{account.enabled ? '已启用' : '已停用'}</StatusBadge>
        <StatusBadge tone={healthTone(account.health_state)}>{account.health_state || '—'}</StatusBadge>
        <span className="hk-mono" style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>#{account.id}</span>
      </header>

      {flash && <Banner tone="ok">{flash}</Banner>}
      {error && <Banner tone="danger">{error}</Banner>}

      <Card title="运维动作">
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <label style={{ fontSize: 12, color: 'var(--hk-ink-500)', display: 'flex', flexDirection: 'column', gap: 4 }}>
            审计原因(可选,记入 admin 审计)
            <input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="如:疑似异常,临时停用排查"
              style={inputStyle}
            />
          </label>
          <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
            <button
              type="button"
              disabled={busy}
              onClick={() =>
                runAction(
                  () => setAccountEnabled(account.id, actions.toggleTo === 'enable', reason),
                  actions.toggleTo === 'enable' ? '已启用账号' : '已停用账号',
                )
              }
              style={actions.toggleTo === 'enable' ? primaryBtn : dangerBtn}
            >
              {actions.toggleTo === 'enable' ? '启用账号' : '停用账号'}
            </button>
            {actions.isRateLimited && (
              <button
                type="button"
                disabled={busy}
                onClick={() => runAction(() => clearAccountRateLimit(account.id, reason), '已清除限流态')}
                style={ghostBtn}
              >
                清除限流态
              </button>
            )}
            {account.rate_limit_reason && (
              <span style={{ fontSize: 12, color: 'var(--hk-warn)', alignSelf: 'center' }}>
                限流原因:{account.rate_limit_reason}
              </span>
            )}
          </div>
        </div>
      </Card>

      <Card title="调度与池">
        <Grid
          rows={[
            ['账号类型', account.account_type],
            ['优先级', String(account.priority)],
            ['静态权重', String(account.static_weight)],
            ['并发上限', String(account.cap_concurrency)],
            ['在途请求', String(account.in_flight_count)],
            ['探测模型', account.probe_model || '—'],
            ['标签', account.tags.length ? account.tags.join(' · ') : '—'],
            ['模型白名单', account.model_allow_list.length ? account.model_allow_list.join(' · ') : '不限'],
            ['能力标记', account.capability_flags.length ? account.capability_flags.join(' · ') : '—'],
          ]}
        />
      </Card>

      <Card title="健康与凭据">
        <Grid
          rows={[
            ['健康态', account.health_state || '—'],
            ['凭据态', account.credential_state || '—'],
            ['最近派发', fmt(account.last_dispatch_at)],
            ['最近探测', fmt(account.last_probe_at)],
            ['探测延迟(ms)', account.last_probe_latency_ms != null ? String(account.last_probe_latency_ms) : '—'],
            ['Token 版本', String(account.token_version)],
            ['最近刷新', fmt(account.last_refresh_at)],
            ['刷新结果', account.last_refresh_outcome || '—'],
            ['OAuth 端点健康', account.oauth_endpoint_health || '—'],
          ]}
        />
      </Card>

      <Card title="限流与过载">
        <Grid
          rows={[
            ['限流起始', fmt(account.rate_limited_at)],
            ['限流重置', fmt(account.rate_limit_reset_at)],
            ['限流原因', account.rate_limit_reason || '—'],
            ['过载至', fmt(account.overload_until)],
            ['临时停调至', fmt(account.temp_unschedulable_until)],
            ['过期时间', fmt(account.expires_at)],
          ]}
        />
      </Card>
    </div>
  )
}

function fmt(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section
      style={{
        background: 'var(--hk-surface)',
        border: '1px solid var(--hk-line)',
        borderRadius: 'var(--hk-radius-lg)',
        boxShadow: 'var(--hk-shadow-1)',
        padding: 'var(--hk-space-4)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
      }}
    >
      <h2 style={{ fontSize: 14, color: 'var(--hk-ink-500)' }}>{title}</h2>
      {children}
    </section>
  )
}

function Grid({ rows }: { rows: [string, string][] }) {
  return (
    <dl
      style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))',
        gap: 'var(--hk-space-3)',
        margin: 0,
      }}
    >
      {rows.map(([k, v]) => (
        <div key={k} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          <dt style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>{k}</dt>
          <dd style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-900)', wordBreak: 'break-word' }}>{v}</dd>
        </div>
      ))}
    </dl>
  )
}

function Center({ children, tone }: { children: React.ReactNode; tone?: 'danger' }) {
  return (
    <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: tone === 'danger' ? 'var(--hk-danger)' : 'var(--hk-ink-500)' }}>
      {children}
    </div>
  )
}

function Banner({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const ok = tone === 'ok'
  return (
    <div
      style={{
        padding: 'var(--hk-space-3) var(--hk-space-4)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: ok ? '#0b6553' : '#8f322a',
        background: ok ? 'var(--hk-primary-50)' : '#fbe9e7',
        border: `1px solid ${ok ? 'var(--hk-primary-100)' : '#f2cdc8'}`,
      }}
    >
      {children}
    </div>
  )
}

const inputStyle: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
const baseBtn: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-4)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  fontWeight: 600,
  cursor: 'pointer',
}
const primaryBtn: React.CSSProperties = { ...baseBtn, border: '1px solid var(--hk-primary-600)', background: 'var(--hk-primary-500)', color: '#fff' }
const dangerBtn: React.CSSProperties = { ...baseBtn, border: '1px solid #c0463a', background: '#c0463a', color: '#fff' }
const ghostBtn: React.CSSProperties = { ...baseBtn, border: '1px solid var(--hk-line)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontWeight: 400 }
