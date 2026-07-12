import { Fragment, useMemo, useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { PIPELINE_NAV, SHELL_LABEL } from '../app/nav'
import { useMe, visibleNavSections } from '../auth/me'

/*
 * 三壳管线导航栏。命名刻意避开通用 "Sidebar"。按壳(用户门户 / 运营台)分组,
 * 组内每个 section 带刻度序号;切换壳时插入壳标题分隔。
 *
 * role 切壳:据 /v1/auth/me 的 panel 过滤可见壳——非 admin(含加载中/降级)看不到运营台入口。
 * 前端裁剪仅为体验,不是授权边界(后端每端点独立鉴权)。
 *
 * 分组可折叠(Owner 2026-07-12:项目多的收进下拉):默认只展开"当前路由所在分组",其余收起,
 * 避免长导航一屏塞不下。点分组标题切换展开/收起。仅表现层,不动 nav 数据与路由接线。
 */
export function PipelineNav() {
  const me = useMe()
  const location = useLocation()
  const sections = visibleNavSections(PIPELINE_NAV, me.access)

  // 当前路由命中的分组:该分组默认展开。用 path 前缀匹配,取最长命中,避免 "/" 误命中全部。
  const activeSectionKey = useMemo(() => {
    let bestKey: string | null = null
    let bestLen = -1
    for (const section of sections) {
      for (const item of section.items) {
        if (
          (location.pathname === item.path || location.pathname.startsWith(item.path + '/')) &&
          item.path.length > bestLen
        ) {
          bestLen = item.path.length
          bestKey = section.key
        }
      }
    }
    return bestKey
  }, [sections, location.pathname])

  // 手动覆盖展开态:用户点过的分组记在这里,未点过的分组回落到"是否为当前分组"。
  const [overrides, setOverrides] = useState<Record<string, boolean>>({})
  const isOpen = (key: string) => overrides[key] ?? key === activeSectionKey
  const toggle = (key: string) => setOverrides((m) => ({ ...m, [key]: !isOpen(key) }))

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
      {sections.map((section) => {
        const showShellHeader = section.shell !== lastShell
        lastShell = section.shell
        const open = isOpen(section.key)
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
            <div style={{ marginBottom: 'var(--hk-space-2)' }}>
              <button
                type="button"
                onClick={() => toggle(section.key)}
                aria-expanded={open}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 'var(--hk-space-2)',
                  width: '100%',
                  padding: 'var(--hk-space-2)',
                  marginBottom: 2,
                  background: 'transparent',
                  border: 0,
                  borderRadius: 'var(--hk-radius-sm)',
                  cursor: 'pointer',
                  fontFamily: 'inherit',
                  textAlign: 'left',
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
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', flex: 1 }}>
                  {section.label}
                </span>
                <span
                  aria-hidden
                  style={{
                    fontSize: 10,
                    color: 'var(--hk-ink-300)',
                    transform: open ? 'rotate(90deg)' : 'none',
                    transition: 'transform .15s',
                  }}
                >
                  ▶
                </span>
              </button>
              {open &&
                section.items.map((item) => (
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
