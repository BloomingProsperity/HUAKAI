import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from './store'

/*
 * 路由守卫:未登录(无 session token)跳转到 /login,带上原目标用于登录后回跳。
 * 运维端点另需 admin token,但缺它的失败由各页面的 401 反馈呈现,不在此硬挡(用户态页面仍可用)。
 */
export function RequireAuth({ children }: { children: React.ReactNode }) {
  const { isLoggedIn } = useAuth()
  const loc = useLocation()
  if (!isLoggedIn) {
    return <Navigate to="/login" state={{ from: loc.pathname }} replace />
  }
  return <>{children}</>
}
