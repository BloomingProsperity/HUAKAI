import { useEffect, useMemo, useState } from 'react'
import type { CSSProperties } from 'react'
import { ApiError } from '../../lib/api'
import { fetchSiteConfig } from './api'
import {
  buildFooterMeta,
  DEFAULT_DOC_KEY,
  interpolate,
  LEGAL_DOCS,
  selectDoc,
  type FooterMeta,
  type LegalDocKey,
} from './legal'
import type { SiteConfig } from './types'

/*
 * 法律条款页(public 壳,纯只读对外门面)。
 *
 * 数据源:GET /v1/site/config(匿名,无鉴权)——真实端点见
 *   backend/internal/sitepublichttp/handler.go:79 + routes_siteconfig.go:19。
 * 后端无独立 terms/privacy 文本字段(已核 platformsettings/types.go),故正文主体走
 * 静态占位条款(legal.ts),并把站点真名(site_name)回填到正文 {{name}} 占位、把
 * 页脚/联系方式/文档链接(site_footer/site_contact_info/site_doc_url)展示在页脚。
 * 站点配置加载失败时不挡正文:用兜底主体名照常渲染条款,仅静默降级页脚补充信息。
 *
 * 视觉:玉青·克制(亮),全部引 var(--hk-*) 设计 token,不硬编码新色。
 */
export function LegalPage() {
  const [docKey, setDocKey] = useState<LegalDocKey>(DEFAULT_DOC_KEY)
  const [site, setSite] = useState<SiteConfig | null>(null)
  // 站点配置加载失败不阻断正文(条款用兜底名照常展示),仅记录以便页脚降级。
  const [configFailed, setConfigFailed] = useState(false)

  useEffect(() => {
    const ctrl = new AbortController()
    fetchSiteConfig(ctrl.signal)
      .then((cfg) => setSite(cfg))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        // 仅站点品牌信息缺失,不影响条款主体;标记降级。错误对象不外泄到 UI。
        void (e instanceof ApiError)
        setConfigFailed(true)
      })
    return () => ctrl.abort()
  }, [])

  const siteName = site?.site_name?.trim() || ''
  const doc = useMemo(() => selectDoc(docKey), [docKey])
  const footer: FooterMeta | null = useMemo(() => (site ? buildFooterMeta(site) : null), [site])

  return (
    <div style={shell}>
      <div style={card}>
        <header style={headerBox}>
          <h1 style={{ fontSize: 24, margin: 0, color: 'var(--hk-ink-900)' }}>
            {interpolate('{{name}} · 法律条款', siteName)}
          </h1>
          <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>
            请在使用本服务前阅读以下条款。继续使用即视为您已知悉并同意相关内容。
          </p>
        </header>

        <div style={tabsRow} role="tablist" aria-label="法律条款分类">
          {LEGAL_DOCS.map((d) => {
            const active = d.key === docKey
            return (
              <button
                key={d.key}
                type="button"
                role="tab"
                aria-selected={active}
                onClick={() => setDocKey(d.key)}
                style={active ? { ...tabBtn, ...tabBtnActive } : tabBtn}
              >
                {d.tab}
              </button>
            )
          })}
        </div>

        <article style={docBox}>
          <h2 style={{ fontSize: 18, margin: 0, color: 'var(--hk-ink-900)' }}>{doc.title}</h2>
          {doc.sections.map((sec) => (
            <section key={sec.heading} style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
              <h3 style={sectionHeading}>{sec.heading}</h3>
              {sec.paragraphs.map((p, i) => (
                <p key={i} style={para}>
                  {interpolate(p, siteName)}
                </p>
              ))}
            </section>
          ))}
        </article>

        <FooterMetaBlock footer={footer} failed={configFailed} />
      </div>
    </div>
  )
}

function FooterMetaBlock({ footer, failed }: { footer: FooterMeta | null; failed: boolean }) {
  // 站点品牌信息缺失或加载失败时,本块整体不展示(只读补充信息,可选)。
  if (!footer || failed) return null
  const hasAny = footer.footer || footer.contact || footer.docUrl
  if (!hasAny) return null
  return (
    <footer style={footerBox}>
      {footer.contact && (
        <div style={footerLine}>
          <span style={footerLabel}>联系方式</span>
          <span style={{ color: 'var(--hk-ink-700)' }}>{footer.contact}</span>
        </div>
      )}
      {footer.docUrl && (
        <div style={footerLine}>
          <span style={footerLabel}>文档</span>
          <a href={footer.docUrl} target="_blank" rel="noreferrer noopener" style={{ color: 'var(--hk-primary-600)' }}>
            {footer.docUrl}
          </a>
        </div>
      )}
      {footer.footer && <div style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>{footer.footer}</div>}
    </footer>
  )
}

const shell: CSSProperties = {
  minHeight: '100%',
  padding: 'var(--hk-space-6)',
  display: 'flex',
  justifyContent: 'center',
  background: 'var(--hk-canvas)',
}
const card: CSSProperties = {
  width: '100%',
  maxWidth: 760,
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-5)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  padding: 'var(--hk-space-6)',
}
const headerBox: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-2)',
  borderBottom: '1px solid var(--hk-line)',
  paddingBottom: 'var(--hk-space-4)',
}
const tabsRow: CSSProperties = {
  display: 'inline-flex',
  alignSelf: 'flex-start',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  overflow: 'hidden',
}
const tabBtn: CSSProperties = {
  height: 34,
  padding: '0 var(--hk-space-4)',
  fontSize: 13,
  cursor: 'pointer',
  border: 'none',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
}
const tabBtnActive: CSSProperties = {
  background: 'var(--hk-primary-500)',
  color: '#fff',
}
const docBox: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-5)',
}
const sectionHeading: CSSProperties = {
  fontSize: 15,
  margin: 0,
  color: 'var(--hk-ink-900)',
  fontWeight: 600,
}
const para: CSSProperties = {
  margin: 0,
  fontSize: 14,
  lineHeight: 1.8,
  color: 'var(--hk-ink-700)',
}
const footerBox: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-2)',
  borderTop: '1px solid var(--hk-line)',
  paddingTop: 'var(--hk-space-4)',
}
const footerLine: CSSProperties = {
  display: 'flex',
  gap: 'var(--hk-space-3)',
  fontSize: 13,
  alignItems: 'baseline',
}
const footerLabel: CSSProperties = {
  flex: '0 0 64px',
  color: 'var(--hk-ink-500)',
  fontSize: 12,
}
