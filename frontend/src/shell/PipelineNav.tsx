import { Fragment, useMemo, useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { PIPELINE_NAV, type NavSection } from '../app/nav'
import { useMe, visibleNavSections } from '../auth/me'

/*
 * 单侧栏按角色渲染(sub2api 形态,Owner 2026-07-13「直接照抄」):
 *  - 管理员:运营导航全组 +「我的账户」一组(个人功能:概览/密钥/用量/钱包/账户等收纳于此),
 *    登录即见管理面板,没有"两个端来回切"的门。
 *  - 普通用户:只有用户功能分组,永远看不到管理入口。
 * 前端裁剪仅为体验,不是授权边界(后端每端点独立鉴权)。
 * 分组可折叠:默认只展开当前路由所在分组。
 */
export function PipelineNav() {
  const me = useMe()
  const location = useLocation()
  const sections = visibleNavSections(PIPELINE_NAV, me.access)
  const isOperator = sections.some((s) => s.shell === 'operator')

  // 管理员:运营分组在前,个人功能压成一组"我的账户"垫底;普通用户:仅用户分组。
  const displaySections = useMemo<NavSection[]>(() => {
    if (!isOperator) return sections.filter((s) => s.shell === 'user')
    const operator = sections.filter((s) => s.shell === 'operator')
    const personalItems = sections.filter((s) => s.shell === 'user').flatMap((s) => s.items)
    return [
      ...operator,
      {
        stage: operator.length + 1,
        key: 'personal',
        shell: 'user',
        label: '我的账户',
        hint: '管理员的个人侧:密钥、用量、钱包、资料',
        items: personalItems,
      },
    ]
  }, [sections, isOperator])

  const activeSectionKey = useMemo(() => {
    let bestKey: string | null = null
    let bestLen = -1
    for (const section of displaySections) {
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
  }, [displaySections, location.pathname])

  const [overrides, setOverrides] = useState<Record<string, boolean>>({})
  const isOpen = (key: string) => overrides[key] ?? key === activeSectionKey
  const toggle = (key: string) => setOverrides((m) => ({ ...m, [key]: !isOpen(key) }))

  return (
    <nav
      aria-label={isOperator ? '管理后台导航' : '用户中心导航'}
      style={{
        width: 244,
        flexShrink: 0,
        background: 'var(--hk-surface)',
        borderRight: '1px solid var(--hk-line)',
        height: '100%',
        overflowY: 'auto',
        display: 'flex',
        flexDirection: 'column',
        padding: 'var(--hk-space-3) var(--hk-space-2)',
        zIndex: 'var(--hk-z-rail)' as unknown as number,
      }}
    >
      {/* 身份标识:按角色而非路由 */}
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--hk-space-2)',
          padding: 'var(--hk-space-2) var(--hk-space-2)',
          margin: '0 0 var(--hk-space-2)',
          borderRadius: 'var(--hk-radius-sm)',
          background: isOperator ? 'var(--hk-primary-50)' : 'var(--hk-line-soft)',
        }}
      >
        <span style={{ fontSize: 14 }}>{isOperator ? '🛡️' : '👤'}</span>
        <span
          style={{
            fontSize: 12.5,
            fontWeight: 700,
            color: isOperator ? 'var(--hk-primary-700)' : 'var(--hk-ink-700)',
          }}
        >
          {isOperator ? '管理后台' : '用户中心'}
        </span>
      </div>

      <div style={{ flex: 1, minHeight: 0 }}>
        {displaySections.map((section) => {
          const open = isOpen(section.key)
          return (
            <Fragment key={section.key}>
              <div style={{ marginBottom: 'var(--hk-space-2)' }}>
                <button
                  type="button"
                  onClick={() => toggle(section.key)}
                  aria-expanded={open}
                  className={`hk-pipeline-nav__item hk-pipeline-nav__section${section.key === activeSectionKey ? ' is-current' : ''}`}
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
                      className={({ isActive }) =>
                        `hk-pipeline-nav__item hk-pipeline-nav__link${isActive ? ' is-active' : ''}`
                      }
                    >
                      <span>{item.label}</span>
                      {!item.built && <span style={{ fontSize: 10, color: 'var(--hk-ink-300)' }}>建设中</span>}
                    </NavLink>
                  ))}
              </div>
            </Fragment>
          )
        })}
      </div>
    </nav>
  )
}
