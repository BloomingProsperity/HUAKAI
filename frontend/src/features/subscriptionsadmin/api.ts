import { apiGet, apiSend } from '../../lib/api'
import type {
  AssignResponse,
  ListAssignmentsResponse,
  ListPlansResponse,
  PlanResponse,
  SubscriptionResponse,
  UpsertPlanRequest,
} from './types'

/*
 * 套餐管理数据访问层。端点根 /v1/admin/subscriptions(admin token 鉴权)。
 * 路径经 tokenForPath(/v1/admin/* 前缀)自动带 admin token。
 *
 * 真码对照:internal/subscriptionhttp/handler.go:251 MountSubscriptionAdminRoutes
 *  - POST   /plans                       建套餐 → {plan}
 *  - GET    /plans?tenant_id=            列套餐 → {plans}
 *  - PUT    /plans/{id}                  改套餐(全量,for_sale 必传)→ {plan}
 *  - POST   /plans/{id}/disable          停用套餐 → {disabled:true}
 *  - POST   /assignments                 给用户分配套餐 → {subscription,idempotent}
 *  - GET    /assignments?tenant_id&user_id  列某用户订阅 → {subscriptions}
 *  - POST   /assignments/{id}/cancel     取消订阅 → {subscription}
 *  - POST   /assignments/{id}/reset-quota 重置配额 → {subscription}
 */
const ROOT = '/v1/admin/subscriptions'

/** 列套餐:GET /plans?tenant_id=。后端 onlyForSale 可选,运营台默认列全部。 */
export async function listPlans(tenantID: number, signal?: AbortSignal): Promise<ListPlansResponse> {
  return apiGet<ListPlansResponse>(`${ROOT}/plans`, { query: { tenant_id: tenantID }, signal })
}

/** 建套餐:POST /plans。请求体含 tenant_id。 */
export async function createPlan(req: UpsertPlanRequest): Promise<PlanResponse> {
  return apiSend<PlanResponse>('POST', `${ROOT}/plans`, req)
}

/** 改套餐:PUT /plans/{id}(后端要求 for_sale 必传,buildPlanRequest 始终带)。 */
export async function updatePlan(id: number, req: UpsertPlanRequest): Promise<PlanResponse> {
  return apiSend<PlanResponse>('PUT', `${ROOT}/plans/${id}`, req)
}

/** 停用套餐:POST /plans/{id}/disable {tenant_id}。 */
export async function disablePlan(id: number, tenantID: number): Promise<{ disabled: boolean }> {
  return apiSend<{ disabled: boolean }>('POST', `${ROOT}/plans/${id}/disable`, { tenant_id: tenantID })
}

/** 列某用户的订阅:GET /assignments?tenant_id&user_id。 */
export async function listAssignments(
  tenantID: number,
  userID: number,
  signal?: AbortSignal,
): Promise<ListAssignmentsResponse> {
  return apiGet<ListAssignmentsResponse>(`${ROOT}/assignments`, {
    query: { tenant_id: tenantID, user_id: userID },
    signal,
  })
}

/** 给用户分配套餐:POST /assignments {tenant_id,user_id,plan_id}。 */
export async function assignSubscription(
  tenantID: number,
  userID: number,
  planID: number,
): Promise<AssignResponse> {
  return apiSend<AssignResponse>('POST', `${ROOT}/assignments`, {
    tenant_id: tenantID,
    user_id: userID,
    plan_id: planID,
  })
}

/** 取消订阅:POST /assignments/{id}/cancel {tenant_id}。 */
export async function cancelSubscription(id: number, tenantID: number): Promise<SubscriptionResponse> {
  return apiSend<SubscriptionResponse>('POST', `${ROOT}/assignments/${id}/cancel`, { tenant_id: tenantID })
}

/** 重置配额:POST /assignments/{id}/reset-quota {tenant_id}。 */
export async function resetQuota(id: number, tenantID: number): Promise<SubscriptionResponse> {
  return apiSend<SubscriptionResponse>('POST', `${ROOT}/assignments/${id}/reset-quota`, { tenant_id: tenantID })
}
