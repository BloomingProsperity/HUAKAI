import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { fetchSiteConfig } from '../landing/api'
import { buildSnippets, keyPlaceholder, type Snippet } from './snippets'

/*
 * 接入指引(用户端)。取 /v1/site/config 的 site_api_base_url,拼各客户端接入配置。
 * 创建后 Key 明文不可回读,故默认用占位符;用户可临时粘贴 Key 填充(仅内存,不保存)。
 * 卖 Key 场景的开箱体验:一键复制 Claude Code / OpenAI SDK / curl 配置。
 */
export function IntegrationGuidePage() {
  const [baseUrl, setBaseUrl] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    fetchSiteConfig(ctrl.signal)
      .then((cfg) => setBaseUrl(cfg.site_api_base_url || ''))
      .catch((e) => {
        if (!ctrl.signal.aborted) setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载站点配置失败')
      })
    return () => ctrl.abort()
  }, [])

  const snippets = buildSnippets(baseUrl, apiKey)

  const copy = async (s: Snippet) => {
    try {
      await navigator.clipboard.writeText(s.body)
      setCopied(s.id)
      window.setTimeout(() => setCopied((c) => (c === s.id ? null : c)), 1500)
    } catch {
      setError('复制失败,请手动选择文本')
    }
  }

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>接入指引</h1>
          <p className="hk-sub">用你的 API Key 接入各客户端。Key 创建后明文不可回读,下方默认用占位符,可临时粘贴填充(仅本页内存)。</p>
        </div>
      </header>

      {error && <div className="hk-errorbox">{error}</div>}

      <section className="hk-card">
        <div className="hk-card__head"><h3>参数</h3></div>
        <div className="hk-card__body" style={{ display: 'flex', gap: 'var(--hk-space-4)', flexWrap: 'wrap' }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, minWidth: 240 }}>
            <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>网关地址</span>
            <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder="https://你的网关地址/v1" style={input} aria-label="网关地址" />
          </label>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, minWidth: 240 }}>
            <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>API Key（可选，留空用占位符）</span>
            <input type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder={keyPlaceholder} autoComplete="off" spellCheck={false} style={input} aria-label="API Key" />
          </label>
        </div>
      </section>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: 'var(--hk-space-4)' }}>
        {snippets.map((s) => (
          <section className="hk-card" key={s.id}>
            <div className="hk-card__head">
              <h3>{s.title}</h3>
              <button type="button" className="hk-btn hk-btn--sm" style={{ marginLeft: 'auto' }} onClick={() => void copy(s)}>
                {copied === s.id ? '已复制' : '复制'}
              </button>
            </div>
            <div className="hk-card__body">
              <pre style={pre}><code>{s.body}</code></pre>
            </div>
          </section>
        ))}
      </div>
    </div>
  )
}

const input: React.CSSProperties = { padding: '7px 9px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontFamily: 'inherit' }
const pre: React.CSSProperties = { margin: 0, padding: 'var(--hk-space-3)', background: 'var(--hk-surface-sunken)', border: '1px solid var(--hk-line-soft)', borderRadius: 'var(--hk-radius-sm)', fontSize: 12, overflowX: 'auto', whiteSpace: 'pre' }
