import { Outlet } from 'react-router-dom'
import { PipelineNav } from './PipelineNav'
import { TopBar } from './TopBar'

/*
 * 应用外壳:顶栏 + 管线导航 + 内容区。脚手架阶段的单壳布局;反克隆方向里的"双形壳"
 * (运维台 / 终端用户台 双形态)留待后续切片据登录身份切换。
 */
export function AppShell() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <TopBar />
      <div style={{ display: 'flex', flex: 1, minHeight: 0 }}>
        <PipelineNav />
        <main style={{ flex: 1, minWidth: 0, overflowY: 'auto', background: 'var(--hk-canvas)' }}>
          <Outlet />
        </main>
      </div>
    </div>
  )
}
