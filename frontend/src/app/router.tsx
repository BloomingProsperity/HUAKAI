import { createBrowserRouter, Navigate } from 'react-router-dom'
import { AppShell } from '../shell/AppShell'
import { Dashboard } from '../pages/Dashboard'
import { Placeholder } from '../pages/Placeholder'
import { PIPELINE_NAV } from './nav'

/*
 * 路由表。AppShell 为壳,首页=管线总览;8 个域路由先挂占位页,后续 P0 切片逐个替换为
 * 真实模块。createBrowserRouter 配合网关 go:embed 的 index.html 客户端路由回退使用。
 */
const domainRoutes = PIPELINE_NAV.flatMap((section) =>
  section.items.map((item) => ({
    path: item.path,
    element: <Placeholder routeKey={item.path} />,
  })),
)

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppShell />,
    children: [
      { index: true, element: <Dashboard /> },
      ...domainRoutes,
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
])
