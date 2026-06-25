import { apiGet, apiSend } from '../../lib/api'
import type {
  CurrentSubscriptionResponse,
  ListPlansResponse,
  PurchaseRequest,
  PurchaseResponse,
  SubscriptionProgressResponse,
} from './types'

/*
 * 订阅页数据访问层。所有端点挂载在 /v1/users/me/subscriptions(session 鉴权,
 * 由 lib/api 按路径自动注入当前登录用户的 session token)。真实路由见:
 *   backend/cmd/gateway/routes.go:313(挂载前缀)
 *   backend/internal/subscriptionhttp/handler.go:271(MountSubscriptionUserRoutes)
 *     GET  /plans       → newUserListPlansHandler            (handler.go:275)
 *     GET  /me          → newUserCurrentSubscriptionHandler  (handler.go:274)
 *     GET  /me/progress → newUserSubscriptionProgressHandler (handler.go:273)
 *     POST /purchase    → newUserPurchaseHandler             (handler.go:278)
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
