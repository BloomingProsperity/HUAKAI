import { apiGet, apiSend } from '../../lib/api'
import type {
  AssignResponse,
  BulkAssignRequest,
  BulkAssignResponse,
  ChangePlanRequest,
  CreateSubscriptionVoucherRequest,
  CreateVoucherResponse,
  ExtendAssignmentRequest,
  ListAssignmentsResponse,
  ListPlansResponse,
  PlanResponse,
  RevokeAssignmentRequest,
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
 *  - POST   /assignments/bulk            批量分配 → {results}
 *  - POST   /assignments/{id}/extend     延长有效期(days 或 until)→ {subscription}
 *  - POST   /assignments/{id}/change-plan 改套餐 → {subscription}
 *  - POST   /assignments/{id}/revoke     撤销(硬性终止)→ {subscription}
 *  - POST   /vouchers                    发订阅兑换券 → {voucher,code}
 *  - GET    /plans/{id}?tenant_id=       套餐详情 → {plan}
 *
 * 真码路由对照:handler.go:251 + admin_ops.go(extend/change-plan/revoke/bulk handler 在 admin_ops.go,
 * 路径均含 {id};兑换券 handler.go:515)。注意 extend/change-plan/revoke 都带订阅 {id},
 * 不是无 id 的 /assignments/extend 等。
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

/** 套餐详情:GET /plans/{id}?tenant_id=。 */
export async function getPlan(id: number, tenantID: number, signal?: AbortSignal): Promise<PlanResponse> {
  return apiGet<PlanResponse>(`${ROOT}/plans/${id}`, { query: { tenant_id: tenantID }, signal })
}

/** 批量分配:POST /assignments/bulk {tenant_id,user_ids,plan_id}。逐用户返回结果(部分成功)。 */
export async function bulkAssign(req: BulkAssignRequest): Promise<BulkAssignResponse> {
  return apiSend<BulkAssignResponse>('POST', `${ROOT}/assignments/bulk`, req)
}

/**
 * 延长订阅有效期:POST /assignments/{id}/extend {tenant_id,days?,until?}。money(改权益)。
 * days 与 until 二选一,由调用方校验后下发。
 */
export async function extendSubscription(
  id: number,
  req: ExtendAssignmentRequest,
): Promise<SubscriptionResponse> {
  return apiSend<SubscriptionResponse>('POST', `${ROOT}/assignments/${id}/extend`, req)
}

/** 改套餐:POST /assignments/{id}/change-plan {tenant_id,new_plan_id,allow_downgrade?}。money(改权益)。 */
export async function changePlan(id: number, req: ChangePlanRequest): Promise<SubscriptionResponse> {
  return apiSend<SubscriptionResponse>('POST', `${ROOT}/assignments/${id}/change-plan`, req)
}

/** 撤销订阅(硬性终止):POST /assignments/{id}/revoke {tenant_id,reason}。money + 破坏性。 */
export async function revokeSubscription(
  id: number,
  req: RevokeAssignmentRequest,
): Promise<SubscriptionResponse> {
  return apiSend<SubscriptionResponse>('POST', `${ROOT}/assignments/${id}/revoke`, req)
}

/** 发订阅兑换券:POST /vouchers。返回券视图 + 明文 code(仅此次回显)。money。 */
export async function createSubscriptionVoucher(
  req: CreateSubscriptionVoucherRequest,
): Promise<CreateVoucherResponse> {
  return apiSend<CreateVoucherResponse>('POST', `${ROOT}/vouchers`, req)
}
