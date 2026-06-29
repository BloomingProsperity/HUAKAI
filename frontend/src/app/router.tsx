import { createBrowserRouter, Navigate } from 'react-router-dom'
import type { ReactElement } from 'react'
import { AppShell } from '../shell/AppShell'
import { Dashboard } from '../pages/Dashboard'
import { Placeholder } from '../pages/Placeholder'
import { AccountsPage } from '../features/accounts/AccountsPage'
import { AccountDetailPage } from '../features/accounts/AccountDetailPage'
import { KeysPage } from '../features/keys/KeysPage'
import { UsagePage } from '../features/usage/UsagePage'
import { UsageRecordsPage } from '../features/usagerecords/UsageRecordsPage'
import { RoutingPage } from '../features/routing/RoutingPage'
import { UsersPage } from '../features/users/UsersPage'
import { UserDetailPage } from '../features/users/UserDetailPage'
import { AuditPage } from '../features/audit/AuditPage'
import { SettingsCenterPage } from '../features/settings/SettingsCenterPage'
import { ModelsPage } from '../features/models/ModelsPage'
import { OpsPage } from '../features/ops/OpsPage'
import { HealthPage } from '../features/health/HealthPage'
import { OverviewPage } from '../features/overview/OverviewPage'
import { RedeemPage } from '../features/redeem/RedeemPage'
import { AffiliatePage } from '../features/affiliate/AffiliatePage'
import { OrdersPage } from '../features/orders/OrdersPage'
import { WalletPage } from '../features/wallet/WalletPage'
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
import { NotificationsPage } from '../features/notifications/NotificationsPage'
import { UserActivityPage } from '../features/useractivity/UserActivityPage'
import { MediaTasksPage } from '../features/mediatasks/MediaTasksPage'
import { AvailableChannelsPage } from '../features/availablechannels/AvailableChannelsPage'
import { MyGroupsPage } from '../features/megroups/MyGroupsPage'
import { AlertingPage } from '../features/alerting/AlertingPage'
import { RiskOverviewPage } from '../features/risk/RiskOverviewPage'
import { BackupPage } from '../features/backup/BackupPage'
import { ProxiesPage } from '../features/proxies/ProxiesPage'
import { PlaygroundPage } from '../features/playground/PlaygroundPage'
import { PricingAdminPage } from '../features/pricingadmin/PricingAdminPage'
import { GroupsPage } from '../features/groups/GroupsPage'
import { ModelRegistryPage } from '../features/modelregistry/ModelRegistryPage'
import { VersionPage } from '../features/version/VersionPage'
import { LogsDiagPage } from '../features/logsdiag/LogsDiagPage'
import { UpstreamModelsPage } from '../features/upstreammodels/UpstreamModelsPage'
import { RouteRulesPage } from '../features/routeadmin/RouteRulesPage'
import { QuotaPoliciesPage } from '../features/quotapolicies/QuotaPoliciesPage'
import { ChannelHealthPage } from '../features/channelhealth/ChannelHealthPage'
import { CatalogsPage } from '../features/catalogs/CatalogsPage'
import { TLSFingerprintsPage } from '../features/tlsfp/TLSFingerprintsPage'
import { ChannelTestTemplatesPage } from '../features/channeltesttemplates/ChannelTestTemplatesPage'
import { ModuleRegistryPage } from '../features/moduleregistry/ModuleRegistryPage'
import { CredentialRenewPage } from '../features/credentialrenew/CredentialRenewPage'
import { BroadcastPage } from '../features/broadcast/BroadcastPage'
import { DisputesAdminPage } from '../features/disputesadmin/DisputesAdminPage'
import { DlqPage } from '../features/dlq/DlqPage'
import { BillingClaimsPage } from '../features/billingadmin/BillingClaimsPage'
import { CacheMonitorPage } from '../features/cachemonitor/CacheMonitorPage'
import { OrphanReconcilePage } from '../features/orphanreconcile/OrphanReconcilePage'
import { LandingPage } from '../features/landing/LandingPage'
import { LegalPage } from '../features/legal/LegalPage'
import { LoginPage } from '../auth/LoginPage'
import { OAuthCallbackPage } from '../auth/OAuthCallbackPage'
import { ForgotPasswordPage } from '../auth/ForgotPasswordPage'
import { ResetPasswordPage } from '../auth/ResetPasswordPage'
import { EmailVerifyPage } from '../auth/EmailVerifyPage'
import { RequireAuth } from '../auth/RequireAuth'
import { ErrorFallback } from '../ui/ErrorFallback'
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
  '/usage-records': <UsageRecordsPage />,
  '/media-tasks': <MediaTasksPage />,
  '/subscriptions': <SubscriptionsPage />,
  '/orders': <OrdersPage />,
  '/wallet': <WalletPage />,
  '/redeem': <RedeemPage />,
  '/checkin': <CheckinPage />,
  '/affiliate': <AffiliatePage />,
  '/available-channels': <AvailableChannelsPage />,
  '/my-groups': <MyGroupsPage />,
  '/profile': <ProfilePage />,
  '/notifications': <NotificationsPage />,
  '/activity': <UserActivityPage />,
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
  '/admin/groups': <GroupsPage />,
  '/admin/model-registry': <ModelRegistryPage />,
  '/admin/model-sync': <UpstreamModelsPage />,
  '/admin/catalogs': <CatalogsPage />,
  '/admin/channel-test-templates': <ChannelTestTemplatesPage />,
  '/admin/version': <VersionPage />,
  '/admin/logs': <LogsDiagPage />,
  '/admin/modules': <ModuleRegistryPage />,
  '/admin/channel-health': <ChannelHealthPage />,
  '/admin/dlq': <DlqPage />,
  '/admin/orphan-reconcile': <OrphanReconcilePage />,
  '/admin/cache': <CacheMonitorPage />,
  '/admin/route-rules': <RouteRulesPage />,
  '/admin/quota-policies': <QuotaPoliciesPage />,
  '/admin/tls-fingerprints': <TLSFingerprintsPage />,
  '/admin/credential-renew': <CredentialRenewPage />,
  '/admin/broadcast': <BroadcastPage />,
  '/admin/disputes': <DisputesAdminPage />,
  '/admin/billing-claims': <BillingClaimsPage />,
  '/system': <SettingsCenterPage />,
  '/ops': <OpsPage />,
  '/health': <HealthPage />,
  '/security': <AuditPage />,
  '/admin/alerting': <AlertingPage />,
  '/admin/risk': <RiskOverviewPage />,
  '/admin/backup': <BackupPage />,
  '/admin/proxies': <ProxiesPage />,
  '/playground': <PlaygroundPage />,
}

const domainRoutes = PIPELINE_NAV.flatMap((section) =>
  section.items.map((item) => ({
    path: item.path,
    element: BUILT_PAGES[item.path] ?? <Placeholder routeKey={item.path} />,
  })),
)

// errorElement 挂在各路由:任一组件渲染期抛错时,react-router 用 ErrorFallback 替换该子树,
// 而非让整个 SPA 白屏。根 `/` 路由的 errorElement 覆盖 AppShell 及全部认证子页。
const errEl = <ErrorFallback />

export const router = createBrowserRouter([
  // 公开壳:登录/找回/重置/邮箱验证/模型排行,壳外、无需鉴权。
  { path: '/login', element: <LoginPage />, errorElement: errEl },
  { path: '/oauth/callback', element: <OAuthCallbackPage />, errorElement: errEl },
  { path: '/forgot-password', element: <ForgotPasswordPage />, errorElement: errEl },
  { path: '/reset-password', element: <ResetPasswordPage />, errorElement: errEl },
  { path: '/email-verify', element: <EmailVerifyPage />, errorElement: errEl },
  { path: '/rankings', element: <RankingsPage />, errorElement: errEl },
  { path: '/welcome', element: <LandingPage />, errorElement: errEl },
  { path: '/legal', element: <LegalPage />, errorElement: errEl },
  {
    path: '/',
    element: (
      <RequireAuth>
        <AppShell />
      </RequireAuth>
    ),
    errorElement: errEl,
    children: [
      { index: true, element: <Dashboard /> },
      { path: '/accounts/:id', element: <AccountDetailPage /> },
      { path: '/users/:id', element: <UserDetailPage /> },
      ...domainRoutes,
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
])
