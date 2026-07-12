import { PIPELINE_NAV } from '../app/nav'

/*
 * 通用占位页 —— 域模块尚未落地时挂在路由上。
 * 显式标注"建设中"并指回前端编写方案文档,避免空白页误导;后续 P0 切片逐个替换为真实模块。
 */
export function Placeholder({ routeKey }: { routeKey: string }) {
  const section = PIPELINE_NAV.find((s) => s.items.some((i) => i.path === routeKey))
  const item = section?.items.find((i) => i.path === routeKey)
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
        padding: 'var(--hk-space-6)',
        maxWidth: 720,
      }}
    >
      <span
        style={{
          alignSelf: 'flex-start',
          fontSize: 12,
          color: 'var(--hk-primary-600)',
          background: 'var(--hk-primary-50)',
          border: '1px solid var(--hk-primary-100)',
          borderRadius: 'var(--hk-radius-pill)',
          padding: '2px 10px',
        }}
      >
        管线第 {section?.stage} 站 · {section?.label}
      </span>
      <h1 style={{ fontSize: 22 }}>{item?.label ?? '模块'}</h1>
      <p style={{ color: 'var(--hk-ink-500)', margin: 0 }}>
        本模块前端建设中。后端能力已就绪,将按《源码梳理与前端编写方案》逐个 P0 切片点亮。
      </p>
    </div>
  )
}
