import { createBrowserRouter, Navigate } from 'react-router-dom'
import type { ReactElement } from 'react'
import { AppShell } from '../shell/AppShell'
import { Dashboard } from '../pages/Dashboard'
import { Placeholder } from '../pages/Placeholder'
import { AccountsPage } from '../features/accounts/AccountsPage'
import { AccountDetailPage } from '../features/accounts/AccountDetailPage'
import { PIPELINE_NAV } from './nav'

/*
 * 路由表。AppShell 为壳,首页=管线总览;已点亮的域走真实模块,未点亮的挂占位页。
 * createBrowserRouter 配合网关 go:embed 的 index.html 客户端路由回退使用。
 */
// 已实现模块的 path→element 覆盖表;后续 P0 切片逐条追加。
const BUILT_PAGES: Record<string, ReactElement> = {
  '/accounts': <AccountsPage />,
}

const domainRoutes = PIPELINE_NAV.flatMap((section) =>
  section.items.map((item) => ({
    path: item.path,
    element: BUILT_PAGES[item.path] ?? <Placeholder routeKey={item.path} />,
  })),
)

export const router = createBrowserRouter([
  {
    path: '/',
    element: <AppShell />,
    children: [
      { index: true, element: <Dashboard /> },
      { path: '/accounts/:id', element: <AccountDetailPage /> },
      ...domainRoutes,
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
])
