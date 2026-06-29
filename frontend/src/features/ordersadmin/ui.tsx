import type { CSSProperties, ReactNode } from 'react'

/*
 * 订单管理台共享 UI 原子(样式 token + 小展示组件)。从 OrdersAdminPage 抽出,
 * 供主页、退款工单 Tab、仪表盘卡片、导出工具栏复用,避免各文件重复定义同一套
 * inline style(token 一律走 var(--hk-*))。纯展示,无业务逻辑。
 */

/** ISO 时间串 → 本地化展示;非法回落 '—'。 */
export function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

export function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)', fontSize: 13 }}>
      <span style={{ color: 'var(--hk-ink-500)' }}>{label}</span>
      <span style={{ color: 'var(--hk-ink-900)', textAlign: 'right' }}>{children}</span>
    </div>
  )
}

export function Empty({ children }: { children: ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

export const inp: CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
export const th: CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
export const td: CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
export const tdNum: CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }
export const panel: CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
export const errBox: CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
export const primaryBtn: CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
export const ghostBtn: CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
export const dangerBtn: CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #f2cdc8', borderRadius: 'var(--hk-radius-md)', background: '#fbe9e7', color: '#8f322a', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
export const linkBtn: CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
