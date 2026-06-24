import { Link } from 'react-router-dom'
import { PIPELINE_NAV } from '../app/nav'

/*
 * 控制台首页(管线总览)。
 * 以"管线卡片"呈现 8 个阶段,每张卡显示该站定位与入口,呼应管线即导航的反克隆方向。
 * 这是脚手架阶段的概览页;后续可叠加实时指标(账号健康/今日用量/余额等)。
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
