import { useCallback, useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { PipelineNav } from './PipelineNav'
import { TopBar } from './TopBar'
import { HermesPanel } from '../features/hermes/HermesPanel'
import { getCurrentShell } from '../features/hermes/hermesContext'
import { refreshMe, resetMe, useMe } from '../auth/me'
import { useAuth } from '../auth/store'

/*
 * 应用外壳:顶栏 + 管线导航 + 内容区。据登录身份(/v1/auth/me 的 panel)切壳:
 * role=admin 才启用运营台能力(运营台导航 + Hermes 面板),其余仅用户门户。
 *
 * getMe 走 best-effort:壳挂载后触发,拉取失败(5xx/网络)降级到最小用户壳,绝不白屏、
 * 绝不默认 admin 壳(deny-by-default,防提权)。触发点在壳而非登录 handler,故邮箱密码
 * 与 OAuth 回调两条登录路径都会拉到 panel(OAuth admin 不会漏判)。
 *
 * 叠加:运营台嵌入式 Hermes 对话面板(纯只读)。仅当 role 启用运营台【且】当前在 operator
 * 路由时渲染,作为根 div 内 fixed 覆盖层。Cmd/Ctrl-K 与顶栏按钮唤起。
 * 铁律:前端切壳仅为体验,不是授权边界——后端每个 admin 端点仍独立鉴权。
 */
export function AppShell() {
  const location = useLocation()
  const me = useMe()
  const { sessionToken } = useAuth()
  // 壳的 bootstrap:唯一的 getMe 触发点,按 session token 触发。挂载即拉;token 变更(换人登录/
  // OAuth 回调进来)重拉。token 变空(登出/刷新失败 clearAll)→ resetMe 清态,避免残留上一位 panel;
  // 换人时的跨身份残留由 refreshMe 的 isSameIdentity 判定先清成 loading 兜底(deny-by-default)。
  useEffect(() => {
    if (!sessionToken) {
      resetMe()
      return
    }
    const ctrl = new AbortController()
    void refreshMe(ctrl.signal)
    return () => ctrl.abort()
  }, [sessionToken])

  // 运营台能力 = role 启用运营台(operatorEnabled)【且】人在 operator 路由。二者缺一不给。
  const isOperator = me.access.operatorEnabled && getCurrentShell(location.pathname) === 'operator'
  const [hermesOpen, setHermesOpen] = useState(false)

  const toggleHermes = useCallback(() => setHermesOpen((v) => !v), [])
  const closeHermes = useCallback(() => setHermesOpen(false), [])

  // 全局 Cmd/Ctrl-K 唤起/收起面板(仅运营台壳生效)。
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
        if (!isOperator) return
        e.preventDefault()
        setHermesOpen((v) => !v)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [isOperator])

  // 离开运营台壳时自动收起面板,避免在用户门户残留。
  useEffect(() => {
    if (!isOperator && hermesOpen) setHermesOpen(false)
  }, [isOperator, hermesOpen])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <TopBar onOpenHermes={isOperator ? toggleHermes : undefined} />
      <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
        <PipelineNav />
        <main style={{ flex: 1, minWidth: 0, overflowY: 'auto', background: 'var(--hk-canvas)' }}>
          <Outlet />
        </main>
      </div>
      {isOperator && hermesOpen && <HermesPanel onClose={closeHermes} />}
    </div>
  )
}
