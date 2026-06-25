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
import { HealthPage } from '../features/health/HealthPage'
import { OverviewPage } from '../features/overview/OverviewPage'
import { RedeemPage } from '../features/redeem/RedeemPage'
import { AffiliatePage } from '../features/affiliate/AffiliatePage'
// 注:OrdersPage(我的订单)暂不挂载——后端无用户订单列表端点(仅 POST /v1/users/me/recharges
// 与 GET /v1/me/orders/{id}/receipt),其调用的 /v1/users/me/payments/orders 不存在。
// 待后端补 GET 用户订单列表端点后再点亮(nav 标"建设中")。
import { SubscriptionsPage } from '../features/subscriptions/SubscriptionsPage'
import { CheckinPage } from '../features/checkin/CheckinPage'
import { RankingsPage } from '../features/rankings/RankingsPage'
import { VouchersAdminPage } from '../features/vouchersadmin/VouchersAdminPage'
import { AnnouncementsPage } from '../features/announcements/AnnouncementsPage'
import { OrdersAdminPage } from '../features/ordersadmin/OrdersAdminPage'
import { SubscriptionsAdminPage } from '../features/subscriptionsadmin/SubscriptionsAdminPage'
import { AffiliateAdminPage } from '../features/affiliateadmin/AffiliateAdminPage'
import { ModerationPage } from '../features/moderation/ModerationPage'
import { ProfilePage } from '../features/profile/ProfilePage'
import { AvailableChannelsPage } from '../features/availablechannels/AvailableChannelsPage'
import { PricingAdminPage } from '../features/pricingadmin/PricingAdminPage'
import { LandingPage } from '../features/landing/LandingPage'
import { LegalPage } from '../features/legal/LegalPage'
import { LoginPage } from '../auth/LoginPage'
import { ForgotPasswordPage } from '../auth/ForgotPasswordPage'
import { ResetPasswordPage } from '../auth/ResetPasswordPage'
import { EmailVerifyPage } from '../auth/EmailVerifyPage'
import { RequireAuth } from '../auth/RequireAuth'
import { PIPELINE_NAV } from './nav'

/*
 * 路由表。AppShell 为壳,首页=管线总览;已点亮的域走真实模块,未点亮的挂占位页。
 * createBrowserRouter 配合网关 go:embed 的 index.html 客户端路由回退使用。
 */
// 已实现模块的 path→element 覆盖表;后续 P0 切片逐条追加。
const BUILT_PAGES: Record<string, ReactElement> = {
  // 用户门户壳
  '/overview': <OverviewPage />,
  '/keys': <KeysPage />,
  '/usage': <UsagePage />,
  '/subscriptions': <SubscriptionsPage />,
  '/redeem': <RedeemPage />,
  '/checkin': <CheckinPage />,
  '/affiliate': <AffiliatePage />,
  '/available-channels': <AvailableChannelsPage />,
  '/profile': <ProfilePage />,
  // 运营台壳
  '/accounts': <AccountsPage />,
  '/routing': <RoutingPage />,
  '/users': <UsersPage />,
  '/models': <ModelsPage />,
  '/admin/orders': <OrdersAdminPage />,
  '/admin/subscriptions': <SubscriptionsAdminPage />,
  '/admin/vouchers': <VouchersAdminPage />,
  '/admin/affiliates': <AffiliateAdminPage />,
  '/admin/announcements': <AnnouncementsPage />,
  '/admin/moderation': <ModerationPage />,
  '/admin/pricing': <PricingAdminPage />,
  '/system': <SystemPage />,
  '/ops': <OpsPage />,
  '/health': <HealthPage />,
  '/security': <AuditPage />,
}

const domainRoutes = PIPELINE_NAV.flatMap((section) =>
  section.items.map((item) => ({
    path: item.path,
    element: BUILT_PAGES[item.path] ?? <Placeholder routeKey={item.path} />,
  })),
)

export const router = createBrowserRouter([
  // 公开壳:登录/找回/重置/邮箱验证/模型排行,壳外、无需鉴权。
  { path: '/login', element: <LoginPage /> },
  { path: '/forgot-password', element: <ForgotPasswordPage /> },
  { path: '/reset-password', element: <ResetPasswordPage /> },
  { path: '/email-verify', element: <EmailVerifyPage /> },
  { path: '/rankings', element: <RankingsPage /> },
  { path: '/welcome', element: <LandingPage /> },
  { path: '/legal', element: <LegalPage /> },
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
