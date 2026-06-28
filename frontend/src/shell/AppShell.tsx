import { useCallback, useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { PipelineNav } from './PipelineNav'
import { TopBar } from './TopBar'
import { HermesPanel } from '../features/hermes/HermesPanel'
import { getCurrentShell } from '../features/hermes/hermesContext'

/*
 * 应用外壳:顶栏 + 管线导航 + 内容区。脚手架阶段的单壳布局;反克隆方向里的"双形壳"
 * (运维台 / 终端用户台 双形态)留待后续切片据登录身份切换。
 *
 * 叠加:运营台嵌入式 Hermes 对话面板(纯只读)。仅在 operator 壳渲染,作为根 div 内的同级
 * fixed 覆盖层(position:fixed 右侧停靠,z-index=overlay)。Cmd/Ctrl-K 与顶栏按钮唤起。
 */
export function AppShell() {
  const location = useLocation()
  const isOperator = getCurrentShell(location.pathname) === 'operator'
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
