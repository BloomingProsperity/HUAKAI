import { createBrowserRouter, Navigate } from 'react-router-dom'
import type { ReactElement } from 'react'
import { AppShell } from '../shell/AppShell'
import { Dashboard } from '../pages/Dashboard'
import { Placeholder } from '../pages/Placeholder'
import { AccountsPage } from '../features/accounts/AccountsPage'
import { AccountDetailPage } from '../features/accounts/AccountDetailPage'
import { KeysPage } from '../features/keys/KeysPage'
import { UsagePage } from '../features/usage/UsagePage'
import { RoutingPage } from '../features/routing/RoutingPage'
import { UsersPage } from '../features/users/UsersPage'
import { UserDetailPage } from '../features/users/UserDetailPage'
import { AuditPage } from '../features/audit/AuditPage'
import { SystemPage } from '../features/system/SystemPage'
import { ModelsPage } from '../features/models/ModelsPage'
import { OpsPage } from '../features/ops/OpsPage'
import { LoginPage } from '../auth/LoginPage'
import { RequireAuth } from '../auth/RequireAuth'
import { PIPELINE_NAV } from './nav'

/*
 * 路由表。AppShell 为壳,首页=管线总览;已点亮的域走真实模块,未点亮的挂占位页。
 * createBrowserRouter 配合网关 go:embed 的 index.html 客户端路由回退使用。
 */
// 已实现模块的 path→element 覆盖表;后续 P0 切片逐条追加。
const BUILT_PAGES: Record<string, ReactElement> = {
  '/accounts': <AccountsPage />,
  '/keys': <KeysPage />,
  '/usage': <UsagePage />,
  '/routing': <RoutingPage />,
  '/users': <UsersPage />,
  '/security': <AuditPage />,
  '/system': <SystemPage />,
  '/models': <ModelsPage />,
  '/ops': <OpsPage />,
}

const domainRoutes = PIPELINE_NAV.flatMap((section) =>
  section.items.map((item) => ({
    path: item.path,
    element: BUILT_PAGES[item.path] ?? <Placeholder routeKey={item.path} />,
  })),
)

export const router = createBrowserRouter([
  // 登录页在壳外、无需鉴权;其余路由经 RequireAuth 守卫(未登录跳 /login)。
  { path: '/login', element: <LoginPage /> },
  {
    path: '/',
    element: (
      <RequireAuth>
        <AppShell />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Dashboard /> },
      { path: '/accounts/:id', element: <AccountDetailPage /> },
      { path: '/users/:id', element: <UserDetailPage /> },
      ...domainRoutes,
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
])
