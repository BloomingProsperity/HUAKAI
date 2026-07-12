import { useState } from 'react'

interface Props {
  kind: string
  plaintext: string
  keyPrefix: string
  onClose: () => void
}

export function OneTimeSecretBox({ kind, plaintext, keyPrefix, onClose }: Props) {
  const [copyState, setCopyState] = useState<'idle' | 'done' | 'error'>('idle')

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(plaintext)
      setCopyState('done')
    } catch {
      setCopyState('error')
    }
  }

  return (
    <div
      role="alert"
      style={{
        margin: 'var(--hk-space-3) var(--hk-space-4)',
        padding: 'var(--hk-space-4)',
        border: '1px solid var(--hk-warn)',
        borderRadius: 'var(--hk-radius-md)',
        background: 'var(--hk-warn-soft)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
      }}
    >
      <div>
        <strong>{kind}已创建，明文仅显示这一次。</strong>
        <div style={{ marginTop: 4, color: 'var(--hk-ink-700)', fontSize: 12 }}>
          前缀 {keyPrefix}；请立即复制并妥善保存，关闭后不可再查看。
        </div>
      </div>
      <code
        style={{
          padding: 'var(--hk-space-3)',
          borderRadius: 'var(--hk-radius-sm)',
          background: 'var(--hk-ink-900)',
          color: '#d9f2e8',
          fontFamily: 'var(--hk-font-mono)',
          overflowWrap: 'anywhere',
        }}
      >
        {plaintext}
      </code>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
        <button type="button" className="hk-btn hk-btn--green" onClick={copy}>
          {copyState === 'done' ? '已复制' : '复制明文'}
        </button>
        <button type="button" className="hk-btn" onClick={onClose}>
          已保存并关闭
        </button>
        {copyState === 'error' && (
          <span style={{ color: 'var(--hk-danger)', fontSize: 12 }}>复制失败，请手动选中复制。</span>
        )}
      </div>
    </div>
  )
}
