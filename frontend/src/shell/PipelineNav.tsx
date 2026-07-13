import { useMemo } from 'react'
import { NavLink } from 'react-router-dom'
import { PIPELINE_NAV, type NavItem, type NavSection } from '../app/nav'
import { useMe, visibleNavSections } from '../auth/me'

/*
 * 单侧栏按角色渲染：管理员看到运营导航和末尾“我的账户”，普通用户只看到用户导航。
 * 所有分组始终展开；前端裁剪仅改善体验，后端端点仍独立鉴权。
 */
export function buildDisplaySections(sections: NavSection[], isOperator: boolean): NavSection[] {
  if (!isOperator) return sections.filter((section) => section.shell === 'user')

  const operatorSections = sections.filter((section) => section.shell === 'operator')
  const personalItems = sections.filter((section) => section.shell === 'user').flatMap((section) => section.items)
  return [
    ...operatorSections,
    {
      key: 'personal',
      shell: 'user',
      label: '我的账户',
      items: personalItems,
    },
  ]
}

function NavEntry({ item, overview = false }: { item: NavItem; overview?: boolean }) {
  return (
    <NavLink
      to={item.path}
      className={({ isActive }) =>
        `hk-pipeline-nav__item hk-pipeline-nav__link${overview ? ' hk-pipeline-nav__overview' : ''}${isActive ? ' is-active' : ''}`
      }
    >
      <span className="hk-pipeline-nav__label">
        <span className="hk-pipeline-nav__icon" aria-hidden="true">{item.icon}</span>
        <span>{item.label}</span>
      </span>
      {!item.built && <span className="hk-pipeline-nav__pending">建设中</span>}
    </NavLink>
  )
}

export function PipelineNav() {
  const me = useMe()
  const sections = visibleNavSections(PIPELINE_NAV, me.access)
  const isOperator = sections.some((section) => section.shell === 'operator')
  const displaySections = useMemo(() => buildDisplaySections(sections, isOperator), [sections, isOperator])
  const overviewItems = displaySections.filter((section) => section.standalone).flatMap((section) => section.items)
  const groupedSections = displaySections.filter((section) => !section.standalone)

  return (
    <nav className="hk-pipeline-nav" aria-label={isOperator ? '管理后台导航' : '用户中心导航'}>
      <div className={`hk-pipeline-nav__identity${isOperator ? ' is-operator' : ''}`}>
        <span className="hk-pipeline-nav__identity-icon" aria-hidden="true">{isOperator ? '🛡️' : '👤'}</span>
        <span>{isOperator ? '管理后台' : '用户中心'}</span>
      </div>

      <div className="hk-pipeline-nav__content">
        <div className="hk-pipeline-nav__primary">
          {overviewItems.map((item) => <NavEntry key={item.path} item={item} overview />)}
        </div>

        {groupedSections.map((section) => {
          const headingId = `hk-nav-${section.key}`
          return (
            <section className="hk-pipeline-nav__group" aria-labelledby={headingId} key={section.key}>
              <h2 className="hk-pipeline-nav__caption" id={headingId}>{section.label}</h2>
              <div className="hk-pipeline-nav__entries">
                {section.items.map((item) => <NavEntry key={item.path} item={item} />)}
              </div>
            </section>
          )
        })}
      </div>
    </nav>
  )
}
