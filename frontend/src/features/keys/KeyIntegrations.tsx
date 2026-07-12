import { useMemo, useState } from 'react'
import { buildIntegrations, type Integration } from './integrations'

/*
 * 「一键接入」面板:在 Key 创建成功(拿到一次性明文)的当下,直接给出各客户端可用的接入配置。
 * 纯展示层 —— 所有拼接逻辑在 integrations.ts(已单测 + 亲手变异)。
 * secret-mask:明文只在内存与本视图,复制走 clipboard,不写日志、不持久化。
 */
export function KeyIntegrations({ plaintext, keyName }: { plaintext: string; keyName: string }) {
  // origin 在组件层注入(纯逻辑可单测);本地未知时回退占位仅用于展示。
  const origin = typeof window !== 'undefined' && window.location?.origin ? window.location.origin : 'https://<你的-relay-域名>'
  const integrations = useMemo(() => buildIntegrations(origin, plaintext, keyName), [origin, plaintext, keyName])
  const [active, setActive] = useState<Integration['id']>(integrations[0]?.id ?? 'claude-code')
  const current = integrations.find((i) => i.id === active) ?? integrations[0]

  if (!current) return null

  return (
    <div style={wrap}>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>一键接入客户端</div>
      <div style={tabRow}>
        {integrations.map((i) => (
          <button
            key={i.id}
            type="button"
            onClick={() => setActive(i.id)}
            style={i.id === active ? tabActive : tab}
          >
            {i.label}
          </button>
        ))}
      </div>
      <div style={{ fontSize: 12, color: 'var(--hk-ink-500)', lineHeight: 1.5 }}>{current.hint}</div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        {current.fields.map((f) => (
          <FieldRow key={f.label} label={f.label} value={f.value} secret={f.secret} />
        ))}
      </div>
      <CopyBlock label="复制接入脚本" value={current.snippet} />
      <a href={current.deepLink} style={deepLinkBtn}>
        在客户端中打开(huakai://)
      </a>
    </div>
  )
}

function FieldRow({ label, value, secret }: { label: string; value: string; secret?: boolean }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }
  return (
    <div style={fieldRow}>
      <span style={{ fontSize: 11, color: 'var(--hk-ink-500)', minWidth: 150 }}>{label}</span>
      <code style={{ ...fieldVal, ...(secret ? secretVal : null) }}>{value}</code>
      <button type="button" onClick={copy} style={miniBtn}>
        {copied ? '已复制' : '复制'}
      </button>
    </div>
  )
}

function CopyBlock({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{label}</span>
        <button type="button" onClick={copy} style={miniBtn}>
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <pre style={snippetBox}>{value}</pre>
    </div>
  )
}

const wrap: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-2)',
  padding: 'var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface-muted, #f7faf8)',
}
const tabRow: React.CSSProperties = { display: 'flex', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }
const tabBase: React.CSSProperties = {
  height: 28,
  padding: '0 var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 12,
  cursor: 'pointer',
  border: '1px solid var(--hk-line)',
}
const tab: React.CSSProperties = { ...tabBase, background: 'var(--hk-surface)', color: 'var(--hk-ink-700)' }
const tabActive: React.CSSProperties = { ...tabBase, background: 'var(--hk-primary-500)', color: '#fff', border: '1px solid var(--hk-primary-600)' }
const fieldRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }
const fieldVal: React.CSSProperties = {
  flex: 1,
  fontSize: 12,
  fontFamily: 'var(--hk-font-mono)',
  wordBreak: 'break-all',
  color: 'var(--hk-ink-900)',
  background: 'var(--hk-surface)',
  padding: '4px var(--hk-space-2)',
  borderRadius: 'var(--hk-radius-sm)',
  border: '1px solid var(--hk-line)',
}
const secretVal: React.CSSProperties = { background: 'var(--hk-ink-900)', color: '#d9f2e8' }
const miniBtn: React.CSSProperties = {
  height: 24,
  padding: '0 var(--hk-space-2)',
  borderRadius: 'var(--hk-radius-sm)',
  fontSize: 11,
  cursor: 'pointer',
  border: '1px solid var(--hk-line)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
}
const snippetBox: React.CSSProperties = {
  margin: 0,
  padding: 'var(--hk-space-3)',
  background: 'var(--hk-ink-900)',
  color: '#d9f2e8',
  borderRadius: 'var(--hk-radius-md)',
  fontFamily: 'var(--hk-font-mono)',
  fontSize: 12,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
}
const deepLinkBtn: React.CSSProperties = {
  alignSelf: 'flex-start',
  height: 30,
  display: 'inline-flex',
  alignItems: 'center',
  padding: '0 var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 12,
  textDecoration: 'none',
  border: '1px solid var(--hk-primary-600)',
  color: 'var(--hk-primary-700, #1f6f54)',
  background: 'var(--hk-surface)',
}
