import { Link } from 'react-router-dom'
import { PIPELINE_NAV } from '../app/nav'
import { DashboardMetrics } from '../features/dashboard/DashboardMetrics'

/*
 * 控制台首页(管线总览)。
 * 顶部=真数据指标条(账号/Key/模型/配额,各卡独立加载、无权限端点降级显"—");
 * 下方="管线卡片"呈现 8 个阶段,呼应管线即导航的反克隆方向。
 */
export function Dashboard() {
  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-5)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        <h1 style={{ fontSize: 24 }}>控制台总览</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0 }}>
          沿中转站数据管线运维:从上游账号池,到路由选号,到签发 Key,直至用量计费。
        </p>
      </header>
      <DashboardMetrics />
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fill, minmax(248px, 1fr))',
          gap: 'var(--hk-space-4)',
        }}
      >
        {PIPELINE_NAV.map((section) => (
          <Link
            key={section.key}
            to={section.items[0].path}
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--hk-space-2)',
              padding: 'var(--hk-space-4)',
              background: 'var(--hk-surface)',
              border: '1px solid var(--hk-line)',
              borderRadius: 'var(--hk-radius-lg)',
              boxShadow: 'var(--hk-shadow-1)',
              color: 'inherit',
            }}
          >
            <span style={{ fontSize: 12, color: 'var(--hk-primary-600)', fontWeight: 600 }}>
              管线 {section.stage}
            </span>
            <span style={{ fontSize: 16, fontWeight: 600, color: 'var(--hk-ink-900)' }}>
              {section.label}
            </span>
            <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>{section.hint}</span>
          </Link>
        ))}
      </div>
    </div>
  )
}
