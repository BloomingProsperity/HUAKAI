import { apiGet, apiSend } from '../../lib/api'
import type {
  ChangePlanRequest,
  ChangePlanResponse,
  CurrentSubscriptionResponse,
  ListPlansResponse,
  PurchaseRequest,
  PurchaseResponse,
  SubscriptionHistoryResponse,
  SubscriptionProgressResponse,
} from './types'

/*
 * 订阅页数据访问层。所有端点挂载在 /v1/users/me/subscriptions(session 鉴权,
 * 由 lib/api 按路径自动注入当前登录用户的 session token)。真实路由见:
 *   backend/cmd/gateway/routes.go:317(挂载前缀)
 *   backend/internal/subscriptionhttp/handler.go:271(MountSubscriptionUserRoutes)
 *     GET  /            → newUserListSubscriptionsHandler     (handler.go:272/558,订阅历史)
 *     GET  /plans        → newUserListPlansHandler            (handler.go:275)
 *     GET  /me           → newUserCurrentSubscriptionHandler  (handler.go:274)
 *     GET  /me/progress  → newUserSubscriptionProgressHandler (handler.go:273)
 *     POST /purchase     → newUserPurchaseHandler             (purchase.go:294)
 *     POST /cancel-renew → newUserCancelRenewHandler          (purchase.go:248,handler.go:276)
 *     POST /change-plan  → newUserChangePlanHandler           (purchase.go:266,handler.go:277)
 */
const BASE = '/v1/users/me/subscriptions'

/** 在售套餐列表。后端只回 for_sale && enabled 的套餐(用户可见集)。 */
export async function listPlans(signal?: AbortSignal): Promise<ListPlansResponse> {
  return apiGet<ListPlansResponse>(`${BASE}/plans`, { signal })
}

/** 当前生效订阅 + 自动续订状态。无生效订阅时 subscription 为 null。 */
export async function getCurrentSubscription(signal?: AbortSignal): Promise<CurrentSubscriptionResponse> {
  return apiGet<CurrentSubscriptionResponse>(`${BASE}/me`, { signal })
}

/**
 * 订阅历史:本人全部订阅记录(不只当前生效一条,含已过期/已取消/待生效)。
 * 真码 handler.go:558 newUserListSubscriptionsHandler,挂在 BASE 的 "/"(handler.go:272),
 * 故路径带尾斜杠;响应 {subscriptions: subscriptionView[]}。只读,身份取自 session。
 */
export async function listSubscriptionHistory(signal?: AbortSignal): Promise<SubscriptionHistoryResponse> {
  return apiGet<SubscriptionHistoryResponse>(`${BASE}/`, { signal })
}

/** 当前订阅各配额窗口(日/周/月)的用量进度。无生效订阅时 progress 为空数组。 */
export async function getProgress(signal?: AbortSignal): Promise<SubscriptionProgressResponse> {
  return apiGet<SubscriptionProgressResponse>(`${BASE}/me/progress`, { signal })
}

/**
 * 发起购买:后端复用支付建一张 subscription 订单(money-gated)。
 * 不在此即时授予订阅 —— 订单履约(admin confirm / 支付回调)后才生效,响应附支付指引。
 */
export async function purchasePlan(req: PurchaseRequest, signal?: AbortSignal): Promise<PurchaseResponse> {
  return apiSend<PurchaseResponse>('POST', `${BASE}/purchase`, req, { signal })
}

/**
 * 关闭自动续订(自助)。后端只置 auto_renew=false,当前已生效权益保留到 expires_at 不受影响。
 * 真码 purchase.go:248 newUserCancelRenewHandler → SetAutoRenew(...,false),返回 {subscription, auto_renew}。
 * 无 body;身份取自 session,前端绝不传 user_id。
 */
export async function cancelRenew(signal?: AbortSignal): Promise<CurrentSubscriptionResponse> {
  return apiSend<CurrentSubscriptionResponse>('POST', `${BASE}/cancel-renew`, {}, { signal })
}

/**
 * 自助换套餐(money 相关:可能产生新计费窗口/权益变更)。后端只允许升级(AllowDowngrade=false),
 * 同档/降级会被后端拒(invalid_plan / 其它订阅错误码)。真码 purchase.go:266 newUserChangePlanHandler,
 * 请求体 {new_plan_id},响应 {subscription}。
 */
export async function changePlan(newPlanId: number, signal?: AbortSignal): Promise<ChangePlanResponse> {
  const req: ChangePlanRequest = { new_plan_id: newPlanId }
  return apiSend<ChangePlanResponse>('POST', `${BASE}/change-plan`, req, { signal })
}
