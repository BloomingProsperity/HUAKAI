import { Fragment, useMemo, useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import { PIPELINE_NAV, type Shell } from '../app/nav'
import { useMe, visibleNavSections } from '../auth/me'
import { getCurrentShell } from '../features/hermes/hermesContext'

/*
 * 双端分离导航(Owner 2026-07-12:用户端与管理端拆成两套,像 sub2api)。
 *
 * 同一时刻只渲染"当前所在端"的分组:
 *  - 用户端(user):只显用户功能(概览/密钥/用量/充值/账户)。
 *  - 管理端(operator):只显运营功能(账号池/路由/用户/计费/系统/审计)。
 * 当前端由路由判定(getCurrentShell)。admin(operatorEnabled)才在用户端看到「进入管理后台」门;
 * 进管理端后有「返回用户端」门。普通用户永远只有用户端,看不到管理端入口。
 * 前端裁剪仅为体验,不是授权边界(后端每端点独立鉴权)。
 *
 * 分组可折叠:默认只展开当前路由所在分组。仅表现层,不动 nav 数据与路由接线。
 */
const USER_HOME = '/overview'
const ADMIN_HOME = '/accounts' // 管理端落地页 = 第一个运营分组入口

export function PipelineNav() {
  const me = useMe()
  const location = useLocation()
  const sections = visibleNavSections(PIPELINE_NAV, me.access)

  // 是否具备管理端(有可见的 operator 分组 = 该身份是运营者)。
  const hasOperator = sections.some((s) => s.shell === 'operator')
  // 当前端:由路由判定;无法判定或落到无权端时回落用户端。
  let area: Shell = getCurrentShell(location.pathname) ?? 'user'
  if (area === 'operator' && !hasOperator) area = 'user'
  const areaSections = sections.filter((s) => s.shell === area)

  const activeSectionKey = useMemo(() => {
    let bestKey: string | null = null
    let bestLen = -1
    for (const section of areaSections) {
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
  }, [areaSections, location.pathname])

  const [overrides, setOverrides] = useState<Record<string, boolean>>({})
  const isOpen = (key: string) => overrides[key] ?? key === activeSectionKey
  const toggle = (key: string) => setOverrides((m) => ({ ...m, [key]: !isOpen(key) }))

  return (
    <nav
      aria-label={area === 'operator' ? '管理后台导航' : '用户中心导航'}
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
      {/* 当前端标识 */}
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--hk-space-2)',
          padding: 'var(--hk-space-2) var(--hk-space-2)',
          margin: '0 0 var(--hk-space-2)',
          borderRadius: 'var(--hk-radius-sm)',
          background: area === 'operator' ? 'var(--hk-primary-50)' : 'var(--hk-line-soft)',
        }}
      >
        <span style={{ fontSize: 14 }}>{area === 'operator' ? '🛡️' : '👤'}</span>
        <span style={{ fontSize: 12.5, fontWeight: 700, color: area === 'operator' ? 'var(--hk-primary-700)' : 'var(--hk-ink-700)' }}>
          {area === 'operator' ? '管理后台' : '用户中心'}
        </span>
      </div>

      <div style={{ flex: 1, minHeight: 0 }}>
        {areaSections.map((section) => {
          const open = isOpen(section.key)
          return (
            <Fragment key={section.key}>
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
                  <span style={{ fontSize: 11, fontWeight: 700, color: 'var(--hk-primary-600)', fontFamily: 'var(--hk-font-mono)' }}>
                    {String(section.stage).padStart(2, '0')}
                  </span>
                  <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', flex: 1 }}>{section.label}</span>
                  <span aria-hidden style={{ fontSize: 10, color: 'var(--hk-ink-300)', transform: open ? 'rotate(90deg)' : 'none', transition: 'transform .15s' }}>
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
                      {!item.built && <span style={{ fontSize: 10, color: 'var(--hk-ink-300)' }}>建设中</span>}
                    </NavLink>
                  ))}
              </div>
            </Fragment>
          )
        })}
      </div>

      {/* 双端切换门:用户端(admin 才见)→ 管理后台;管理端 → 返回用户端 */}
      {area === 'user' && hasOperator && (
        <NavLink to={ADMIN_HOME} style={switchDoorStyle('operator')}>
          🛡️ 进入管理后台 →
        </NavLink>
      )}
      {area === 'operator' && (
        <NavLink to={USER_HOME} style={switchDoorStyle('user')}>
          ← 返回用户端
        </NavLink>
      )}
    </nav>
  )
}

function switchDoorStyle(target: Shell): React.CSSProperties {
  const toOperator = target === 'operator'
  return {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    gap: 'var(--hk-space-2)',
    marginTop: 'var(--hk-space-2)',
    padding: 'var(--hk-space-2) var(--hk-space-3)',
    borderRadius: 'var(--hk-radius-md)',
    border: `1px solid ${toOperator ? 'var(--hk-primary-600)' : 'var(--hk-line)'}`,
    background: toOperator ? 'var(--hk-primary-500)' : 'var(--hk-surface)',
    color: toOperator ? '#fff' : 'var(--hk-ink-700)',
    fontSize: 13,
    fontWeight: 600,
    textDecoration: 'none',
    flexShrink: 0,
  }
}
