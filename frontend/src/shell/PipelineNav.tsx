import { Fragment } from 'react'
import { NavLink } from 'react-router-dom'
import { PIPELINE_NAV, SHELL_LABEL } from '../app/nav'

/*
 * 三壳管线导航栏。命名刻意避开通用 "Sidebar"。按壳(用户门户 / 运营台)分组,
 * 组内每个 section 带刻度序号;切换壳时插入壳标题分隔。
 */
export function PipelineNav() {
  let lastShell: string | null = null
  return (
    <nav
      aria-label="管线导航"
      style={{
        width: 244,
        flexShrink: 0,
        background: 'var(--hk-surface)',
        borderRight: '1px solid var(--hk-line)',
        height: '100%',
        overflowY: 'auto',
        padding: 'var(--hk-space-3) var(--hk-space-2)',
        zIndex: 'var(--hk-z-rail)' as unknown as number,
      }}
    >
      {PIPELINE_NAV.map((section) => {
        const showShellHeader = section.shell !== lastShell
        lastShell = section.shell
        return (
        <Fragment key={section.key}>
          {showShellHeader && (
            <div
              style={{
                fontSize: 11,
                fontWeight: 700,
                letterSpacing: '0.08em',
                color: 'var(--hk-ink-300)',
                padding: '0 var(--hk-space-2)',
                margin: 'var(--hk-space-2) 0 var(--hk-space-1)',
              }}
            >
              {SHELL_LABEL[section.shell]}
            </div>
          )}
          <div style={{ marginBottom: 'var(--hk-space-4)' }}>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--hk-space-2)',
              padding: '0 var(--hk-space-2)',
              marginBottom: 'var(--hk-space-1)',
            }}
          >
            <span
              style={{
                fontSize: 11,
                fontWeight: 700,
                color: 'var(--hk-primary-600)',
                fontFamily: 'var(--hk-font-mono)',
              }}
            >
              {String(section.stage).padStart(2, '0')}
            </span>
            <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)' }}>
              {section.label}
            </span>
          </div>
          {section.items.map((item) => (
            <NavLink
              key={item.path}
              to={item.path}
              style={({ isActive }) => ({
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: 'var(--hk-space-2) var(--hk-space-3)',
                margin: '2px 0',
                borderRadius: 'var(--hk-radius-md)',
                fontSize: 14,
                color: isActive ? 'var(--hk-primary-700)' : 'var(--hk-ink-700)',
                background: isActive ? 'var(--hk-primary-50)' : 'transparent',
                fontWeight: isActive ? 600 : 400,
              })}
            >
              <span>{item.label}</span>
              {!item.built && (
                <span style={{ fontSize: 10, color: 'var(--hk-ink-300)' }}>建设中</span>
              )}
            </NavLink>
          ))}
          </div>
        </Fragment>
        )
      })}
    </nav>
  )
}
