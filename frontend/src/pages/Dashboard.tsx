import { Navigate } from 'react-router-dom'
import { useMe } from '../auth/me'
import { OperatorOverview } from '../features/dashboard/OperatorOverview'

/*
 * 控制台首页是运营总览单一落地页；普通用户仍送往用户总览。
 */
export function Dashboard() {
  const me = useMe()
  // 身份未知(首拉进行中)先不渲染,避免把管理员闪跳去 /overview 再弹回。
  if (me.status === 'idle' || me.status === 'loading') return null
  if (!me.access.operatorEnabled) return <Navigate to="/overview" replace />
  return (
    <div style={{ padding: 'var(--hk-space-5)' }}><OperatorOverview /></div>
  )
}
