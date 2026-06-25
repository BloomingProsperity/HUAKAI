import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { fetchPricing, fetchSiteConfig } from './api'
import {
  brandName,
  brandSubtitle,
  docLinkOrNull,
  ownerLabel,
  pricePerMillion,
  pricingHighlights,
} from './landing'
import type { PricingItem, SiteConfig } from './types'

/*
 * 落地首页(对外营销门面,public 壳,无需鉴权)。
 * 站点品牌/简介(GET /v1/site/config)+ 模型价目亮点预览(GET /v1/pricing/page)+ 登录/注册 CTA。
 * 纯只读,无任何写动作。两个端点都是公开的,失败时降级展示默认品牌,不阻断 CTA。
 */
export function LandingPage() {
  const [cfg, setCfg] = useState<SiteConfig | null>(null)
  const [pricing, setPricing] = useState<PricingItem[]>([])
  const [pricingError, setPricingError] = useState<string | null>(null)
  const [pricingLoading, setPricingLoading] = useState(true)

  useEffect(() => {
    const ctrl = new AbortController()
    // 站点配置失败不阻断页面:降级到默认品牌(brandName 内置兜底)。
    fetchSiteConfig(ctrl.signal)
      .then(setCfg)
      .catch(() => {
        /* 静默降级:页面用默认品牌渲染 */
      })
    return () => ctrl.abort()
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    setPricingLoading(true)
    setPricingError(null)
    fetchPricing(ctrl.signal)
      .then((items) => setPricing(pricingHighlights(items, 6)))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setPricingError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载价目失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setPricingLoading(false)
      })
    return () => ctrl.abort()
  }, [])

  const name = brandName(cfg)
  const subtitle = brandSubtitle(cfg)
  const homeContent = (cfg?.site_home_content ?? '').trim()
  const docURL = docLinkOrNull(cfg)
  const registrationEnabled = cfg?.registration_enabled ?? false
  const apiBase = (cfg?.site_api_base_url ?? '').trim()
  const contact = (cfg?.site_contact_info ?? '').trim()
  const footer = (cfg?.site_footer ?? '').trim()

  return (
    <div style={page}>
      <main style={main}>
        {/* 顶部品牌条 */}
        <header style={topbar}>
          <span style={wordmark}>{name}</span>
          <nav style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'center' }}>
            {docURL && (
              <a href={docURL} target="_blank" rel="noreferrer noopener" style={ghostLink}>
                文档
              </a>
            )}
            <Link to="/rankings" style={ghostLink}>
              模型排行
            </Link>
            <Link to="/login" style={primaryLink}>
              登录
            </Link>
          </nav>
        </header>

        {/* 主视觉 */}
        <section style={hero}>
          <h1 style={heroTitle}>{name}</h1>
          <p style={heroSub}>{subtitle}</p>
          {homeContent && <p style={heroBody}>{homeContent}</p>}
          <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap', marginTop: 'var(--hk-space-2)' }}>
            <Link to="/login" style={ctaPrimary}>
              立即登录
            </Link>
            {registrationEnabled ? (
              <Link to="/login" style={ctaGhost}>
                注册账号
              </Link>
            ) : (
              <span style={{ ...ctaGhost, opacity: 0.6, cursor: 'default' }} title="当前未开放自助注册,请联系站点管理员">
                注册暂未开放
              </span>
            )}
          </div>
          {apiBase && (
            <p style={{ marginTop: 'var(--hk-space-3)', fontSize: 12, color: 'var(--hk-ink-500)' }}>
              API 入口:<code style={code}>{apiBase}</code>
            </p>
          )}
        </section>

        {/* 价目亮点预览 */}
        <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline' }}>
            <h2 style={{ fontSize: 18 }}>模型与定价</h2>
            <Link to="/login" style={ghostLink}>
              登录查看全部 →
            </Link>
          </div>
          <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>
            价格按每百万 token 美元计,以下为公开价目亮点。
          </p>

          {pricingError && (
            <div style={errorBox}>{pricingError}</div>
          )}

          <div style={cardWrap}>
            {pricingLoading && pricing.length === 0 ? (
              <Empty>加载中…</Empty>
            ) : pricing.length === 0 ? (
              <Empty>{pricingError ? '价目暂不可用。' : '暂无公开价目。'}</Empty>
            ) : (
              <div style={grid}>
                {pricing.map((it) => (
                  <article key={it.model} style={card}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-2)' }}>
                      <span style={{ fontWeight: 600, fontSize: 14, color: 'var(--hk-ink-900)', wordBreak: 'break-all' }}>
                        {it.model}
                      </span>
                      <StatusBadge tone="info">{ownerLabel(it)}</StatusBadge>
                    </div>
                    <dl style={priceList}>
                      <div style={priceRow}>
                        <dt style={priceLabel}>输入 / 1M</dt>
                        <dd style={priceVal}>{pricePerMillion(it.input_price_per_token)}</dd>
                      </div>
                      <div style={priceRow}>
                        <dt style={priceLabel}>输出 / 1M</dt>
                        <dd style={priceVal}>{pricePerMillion(it.output_price_per_token)}</dd>
                      </div>
                      {typeof it.context_length === 'number' && (
                        <div style={priceRow}>
                          <dt style={priceLabel}>上下文</dt>
                          <dd style={{ ...priceVal, fontFamily: 'inherit' }}>{it.context_length.toLocaleString('zh-CN')}</dd>
                        </div>
                      )}
                    </dl>
                  </article>
                ))}
              </div>
            )}
          </div>
        </section>

        <footer style={footerStyle}>
          {contact && <span>{contact}</span>}
          {footer ? <span>{footer}</span> : <span>{name} · 稳定中转,按量计费</span>}
        </footer>
      </main>
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const page: React.CSSProperties = { minHeight: '100vh', background: 'var(--hk-canvas)', color: 'var(--hk-ink-900)' }
const main: React.CSSProperties = { maxWidth: 1040, margin: '0 auto', padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-8)' }
const topbar: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)' }
const wordmark: React.CSSProperties = { fontWeight: 700, fontSize: 16, color: 'var(--hk-primary-700)' }
const hero: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', padding: 'var(--hk-space-8)' }
const heroTitle: React.CSSProperties = { fontSize: 34, margin: 0, lineHeight: 1.2, color: 'var(--hk-ink-900)' }
const heroSub: React.CSSProperties = { fontSize: 16, margin: 0, color: 'var(--hk-ink-700)' }
const heroBody: React.CSSProperties = { fontSize: 14, margin: 0, color: 'var(--hk-ink-500)', maxWidth: 640, whiteSpace: 'pre-wrap' }
const cardWrap: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', padding: 'var(--hk-space-4)' }
const grid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))', gap: 'var(--hk-space-4)' }
const card: React.CSSProperties = { border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', background: 'var(--hk-surface)' }
const priceList: React.CSSProperties = { margin: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }
const priceRow: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', gap: 'var(--hk-space-2)' }
const priceLabel: React.CSSProperties = { margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }
const priceVal: React.CSSProperties = { margin: 0, fontSize: 14, fontWeight: 600, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-primary-700)' }
const ghostLink: React.CSSProperties = { fontSize: 13, color: 'var(--hk-ink-700)', textDecoration: 'none', padding: '0 var(--hk-space-2)' }
const primaryLink: React.CSSProperties = { fontSize: 13, fontWeight: 600, color: '#fff', background: 'var(--hk-primary-500)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', padding: '6px var(--hk-space-4)', textDecoration: 'none' }
const ctaPrimary: React.CSSProperties = { fontSize: 15, fontWeight: 600, color: '#fff', background: 'var(--hk-primary-500)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', padding: '10px var(--hk-space-6)', textDecoration: 'none' }
const ctaGhost: React.CSSProperties = { fontSize: 15, fontWeight: 600, color: 'var(--hk-primary-700)', background: 'var(--hk-surface)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', padding: '10px var(--hk-space-6)', textDecoration: 'none' }
const code: React.CSSProperties = { fontFamily: 'var(--hk-font-mono)', background: 'var(--hk-surface-sunken)', padding: '1px 6px', borderRadius: 'var(--hk-radius-sm)' }
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
const footerStyle: React.CSSProperties = { display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-4)', justifyContent: 'space-between', borderTop: '1px solid var(--hk-line)', paddingTop: 'var(--hk-space-4)', fontSize: 12, color: 'var(--hk-ink-500)' }
