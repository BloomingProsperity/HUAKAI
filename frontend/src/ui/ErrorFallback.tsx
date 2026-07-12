import type { CSSProperties } from 'react'
import { useRouteError } from 'react-router-dom'
import { errorMessage } from './errorFallback'

/*
 * 路由级错误边界回退页。挂在 createBrowserRouter 各路由的 errorElement 上:任一路由组件渲染期抛错时,
 * react-router 用本页替换该子树,而非让整个 SPA 白屏。展示友好中文文案 + 刷新 / 回首页两个出口。
 * 不暴露原始 stack(errorMessage 已归一)。开发期可在 console 看完整 error。
 */
export function ErrorFallback() {
  const error = useRouteError()
  // 开发期把完整错误打到 console,便于排查;归一文案只给用户看友好版。
  if (typeof console !== 'undefined') {
    console.error('[ErrorFallback] 路由渲染错误:', error)
  }
  const { title, detail } = errorMessage(error)

  return (
    <div style={wrap}>
      <div style={card}>
        <div style={{ fontSize: 40, lineHeight: 1 }}>⚠️</div>
        <h1 style={{ fontSize: 20, margin: 0, color: 'var(--hk-ink-900)' }}>{title}</h1>
        <p style={{ margin: 0, fontSize: 14, color: 'var(--hk-ink-500)', maxWidth: 420, textAlign: 'center' }}>{detail}</p>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-2)' }}>
          <button type="button" onClick={() => window.location.reload()} style={primaryBtn}>
            刷新重试
          </button>
          <button type="button" onClick={() => { window.location.href = '/' }} style={ghostBtn}>
            回首页
          </button>
        </div>
      </div>
    </div>
  )
}

const wrap: CSSProperties = { minHeight: '60vh', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 'var(--hk-space-6)' }
const card: CSSProperties = { display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--hk-space-3)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', padding: 'var(--hk-space-8)' }
const primaryBtn: CSSProperties = { height: 36, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: CSSProperties = { height: 36, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 14, cursor: 'pointer' }
