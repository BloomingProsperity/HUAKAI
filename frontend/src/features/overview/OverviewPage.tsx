import { useEffect, useState } from 'react'
import { StatusBadge } from '../../ui/StatusBadge'
import { AnnouncementBanner } from './AnnouncementBanner'
import { getKeyUsageSummary, getQuota, listApiKeys } from './api'
import {
  buildUsageBars,
  formatCount,
  formatUsd,
  metricLabel,
  pickHeadlineQuota,
  quotaProgress,
  summarizeKeys,
  usageBarRatios,
  windowLabel,
} from './overview'
import type { ApiKeyView, KeyUsageSummary, QuotaWindow } from './types'

/*
 * 概览(user 壳首页)。把"我这账号现在怎么样"压成一屏:配额窗口 + 用量简图 + Key 数 + 快捷入口。
 * 每张卡各自独立加载、各自降级:任一端点失败/不可用只在该卡显示 "—",不连累其它卡。
 *
 * 数据全部 session 可达(已核 backend 真码,见 types.ts 顶注):
 *   - /v1/me/quota、/v1/api-keys、/v1/me/keys/{id}/usage-summary
 * 不用 /v1/me/analytics/time-series:它挂的是 API-key(hk_key)鉴权,session token 不可达;
 * 故"用量时序简图"改由 session 可达的 per-key usage-summary 折叠成内联 SVG 简图。
 */
export function OverviewPage() {
  return (
    <div className="hk-page">
      {/* 用户公告横幅:拉 /v1/announcements,有生效公告才展示;无/失败则整体不渲染,不打扰主内容。 */}
      <AnnouncementBanner />

      <header className="hk-pagehead">
        <div>
          <h1>概览</h1>
          <p className="hk-sub">账户配额、近段用量与快捷入口的一屏速览。</p>
        </div>
      </header>

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(280px, 1fr))',
          gap: 'var(--hk-space-4)',
          alignItems: 'start',
        }}
      >
        <QuotaCard />
        <KeyCountCard />
      </div>

      <UsageSparkCard />
      <QuickLinksCard />
    </div>
  )
}

// ───────────────────────── 配额窗口卡 ─────────────────────────

function QuotaCard() {
  const [items, setItems] = useState<QuotaWindow[] | null>(null)
  const [state, setState] = useState<'loading' | 'ok' | 'fail'>('loading')

  useEffect(() => {
    const ctrl = new AbortController()
    getQuota(ctrl.signal)
      .then((r) => {
        if (ctrl.signal.aborted) return
        setItems(r.items)
        setState('ok')
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setState('fail')
      })
    return () => ctrl.abort()
  }, [])

  const headline = items ? pickHeadlineQuota(items) : null

  return (
    <Card title="配额窗口">
      {state === 'loading' ? (
        <Muted>加载中…</Muted>
      ) : state === 'fail' ? (
        <Dash hint="配额暂不可用" />
      ) : !headline ? (
        <Muted>当前账户未配置配额限制(无上限)。</Muted>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <QuotaBar w={headline} />
          {items && items.length > 1 && (
            <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>另有 {items.length - 1} 个配额窗口,见用量页。</span>
          )}
        </div>
      )}
    </Card>
  )
}

function QuotaBar({ w }: { w: QuotaWindow }) {
  const p = quotaProgress(w.consumed, w.cap)
  const barColor = p.tone === 'danger' ? 'var(--hk-danger)' : p.tone === 'warn' ? 'var(--hk-warn)' : 'var(--hk-primary-500)'
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 13 }}>
        <span style={{ color: 'var(--hk-ink-700)' }}>
          {metricLabel(w.metric)} · {windowLabel(w.window_kind)}
        </span>
        {p.over ? <StatusBadge tone="danger">已超额</StatusBadge> : p.tone === 'warn' ? <StatusBadge tone="warn">接近上限</StatusBadge> : <StatusBadge tone="ok">正常</StatusBadge>}
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12 }}>
        <span className="hk-mono" style={{ color: p.over ? 'var(--hk-danger)' : 'var(--hk-ink-500)' }}>
          {w.consumed} / {p.unlimited ? '无上限' : w.cap}
        </span>
        {w.request_count > 0 && <span style={{ color: 'var(--hk-ink-300)' }}>{w.request_count} 次请求</span>}
      </div>
      {!p.unlimited && (
        <div style={{ height: 8, background: 'var(--hk-surface-sunken)', borderRadius: 'var(--hk-radius-pill)', overflow: 'hidden' }}>
          <div style={{ width: `${p.pct}%`, height: '100%', background: barColor, transition: 'width .2s' }} />
        </div>
      )}
    </div>
  )
}

// ───────────────────────── Key 数卡 ─────────────────────────

function KeyCountCard() {
  const [counts, setCounts] = useState<{ total: number; active: number } | null>(null)
  const [state, setState] = useState<'loading' | 'ok' | 'fail'>('loading')

  useEffect(() => {
    const ctrl = new AbortController()
    listApiKeys(0, 100, ctrl.signal)
      .then((r) => {
        if (ctrl.signal.aborted) return
        setCounts(summarizeKeys(r.api_keys, r.count))
        setState('ok')
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setState('fail')
      })
    return () => ctrl.abort()
  }, [])

  return (
    <Card title="我的密钥">
      {state === 'loading' ? (
        <Muted>加载中…</Muted>
      ) : state === 'fail' || !counts ? (
        <Dash hint="密钥数暂不可用" />
      ) : (
        <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--hk-space-4)' }}>
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            <span style={{ fontSize: 30, fontWeight: 700, color: 'var(--hk-ink-900)', lineHeight: 1 }} className="hk-mono">
              {formatCount(counts.total)}
            </span>
            <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>密钥总数</span>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            <span style={{ fontSize: 18, fontWeight: 600, color: 'var(--hk-primary-600)', lineHeight: 1 }} className="hk-mono">
              {formatCount(counts.active)}
            </span>
            <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>活跃</span>
          </div>
        </div>
      )}
    </Card>
  )
}

// ───────────────────────── 用量简图卡 ─────────────────────────

function UsageSparkCard() {
  const [bars, setBars] = useState<ReturnType<typeof buildUsageBars> | null>(null)
  const [state, setState] = useState<'loading' | 'ok' | 'fail'>('loading')

  useEffect(() => {
    const ctrl = new AbortController()
    ;(async () => {
      try {
        const list = await listApiKeys(0, 100, ctrl.signal)
        if (ctrl.signal.aborted) return
        const active = list.api_keys.filter((k) => k.status === 'active')
        const rows = await Promise.all(
          active.map((k: ApiKeyView) =>
            getKeyUsageSummary(k.api_key_id, ctrl.signal)
              .then((s: KeyUsageSummary) => ({ key: k, summary: s }))
              .catch(() => ({ key: k, summary: null as KeyUsageSummary | null })),
          ),
        )
        if (ctrl.signal.aborted) return
        setBars(buildUsageBars(rows))
        setState('ok')
      } catch {
        if (!ctrl.signal.aborted) setState('fail')
      }
    })()
    return () => ctrl.abort()
  }, [])

  return (
    <Card title="近段用量(按密钥花费)">
      {state === 'loading' ? (
        <Muted>加载中…</Muted>
      ) : state === 'fail' || !bars ? (
        <Dash hint="用量暂不可用" />
      ) : bars.length === 0 ? (
        <Muted>暂无可统计的用量(活跃密钥尚无花费)。</Muted>
      ) : (
        <UsageSpark bars={bars} />
      )}
    </Card>
  )
}

function UsageSpark({ bars }: { bars: ReturnType<typeof buildUsageBars> }) {
  const ratios = usageBarRatios(bars)
  const rowH = 28
  const gap = 8
  const labelW = 120
  const trackW = 240
  const height = ratios.length * rowH + (ratios.length - 1) * gap
  return (
    <div style={{ overflowX: 'auto' }}>
      <svg
        role="img"
        aria-label="按密钥花费的用量简图"
        width={labelW + trackW + 80}
        height={height}
        style={{ display: 'block', maxWidth: '100%' }}
      >
        {ratios.map(({ bar, ratio }, i) => {
          const y = i * (rowH + gap)
          const barW = Math.max(2, ratio * trackW)
          return (
            <g key={bar.keyId}>
              <text x={0} y={y + rowH / 2} dominantBaseline="middle" fontSize={12} fill="var(--hk-ink-700)">
                {bar.label.length > 14 ? `${bar.label.slice(0, 13)}…` : bar.label}
              </text>
              <rect x={labelW} y={y + 6} width={trackW} height={rowH - 12} rx={4} fill="var(--hk-surface-sunken)" />
              <rect x={labelW} y={y + 6} width={barW} height={rowH - 12} rx={4} fill="var(--hk-primary-500)" />
              <text
                x={labelW + trackW + 8}
                y={y + rowH / 2}
                dominantBaseline="middle"
                fontSize={11}
                fill="var(--hk-ink-500)"
                fontFamily="var(--hk-font-mono)"
              >
                ${formatUsd(bar.cost)}
              </text>
            </g>
          )
        })}
      </svg>
      <p style={{ margin: 'var(--hk-space-2) 0 0', fontSize: 12, color: 'var(--hk-ink-300)' }}>
        条长按各密钥花费归一化;明细见用量页。
      </p>
    </div>
  )
}

// ───────────────────────── 快捷入口卡 ─────────────────────────

const QUICK_LINKS: ReadonlyArray<{ href: string; label: string; hint: string }> = [
  { href: '/keys', label: '我的密钥', hint: '创建 / 撤销 API Key' },
  { href: '/usage', label: '用量与日志', hint: '配额窗口与各密钥用量明细' },
  { href: '/accounts', label: '账号中心', hint: '上游账号池' },
  { href: '/models', label: '模型与定价', hint: '可用模型与价目' },
]

function QuickLinksCard() {
  return (
    <Card title="快捷入口">
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 'var(--hk-space-3)' }}>
        {QUICK_LINKS.map((l) => (
          <a
            key={l.href}
            href={l.href}
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 2,
              padding: 'var(--hk-space-3) var(--hk-space-4)',
              border: '1px solid var(--hk-line)',
              borderRadius: 'var(--hk-radius-md)',
              background: 'var(--hk-surface)',
              textDecoration: 'none',
            }}
          >
            <span style={{ fontWeight: 600, color: 'var(--hk-primary-700)', fontSize: 14 }}>{l.label}</span>
            <span style={{ color: 'var(--hk-ink-500)', fontSize: 12 }}>{l.hint}</span>
          </a>
        ))}
      </div>
    </Card>
  )
}

// ───────────────────────── 通用小件 ─────────────────────────

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>{title}</h3>
      </div>
      <div className="hk-card__body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        {children}
      </div>
    </section>
  )
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-3)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

/** 降级占位:端点不可用时显示醒目的 "—" + 说明,而非报错条。 */
function Dash({ hint }: { hint: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
      <span style={{ fontSize: 28, fontWeight: 700, color: 'var(--hk-ink-300)', lineHeight: 1 }}>—</span>
      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{hint}</span>
    </div>
  )
}
