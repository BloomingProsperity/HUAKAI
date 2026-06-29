/*
 * 套餐管理(运营台/admin)前端类型 —— 镜像 subscriptionhttp 的 JSON DTO。
 * 端点:/v1/admin/subscriptions/*(admin token 鉴权)。
 *
 * 后端真码:
 *  - planView          → internal/subscriptionhttp/handler.go:128
 *  - adminSubscriptionView/subscriptionView → handler.go:147,161
 *  - 路由挂载           → handler.go:251 (MountSubscriptionAdminRoutes)
 *  - 挂载点 /v1/admin/subscriptions → cmd/gateway/routes.go:1047
 *
 * 金额单位:price_cents 为最小货币单位(分);cap 字段为非负十进制美元字符串(可空=不限)。
 */

/** 单租户部署(运营者自跑实例)默认租户恒为 1;与 auth/emailVerify.ts 的约定一致。 */
export const DEFAULT_TENANT_ID = 1

/** 套餐视图(planView):后端 toPlanView 输出。 */
export interface Plan {
  id: number
  tenant_id: number
  name: string
  description?: string
  /** 名义售价,最小货币单位(分)。 */
  price_cents: number
  currency_code: string
  /** 一次授予的有效天数。 */
  validity_days: number
  /** 授予的用户组(可空)。 */
  granted_group?: string
  /** 日/周/月封顶(美元十进制字符串,可空=不限)。 */
  daily_cap_usd?: string | null
  weekly_cap_usd?: string | null
  monthly_cap_usd?: string | null
  /** 是否对用户上架可售。 */
  for_sale: boolean
  /** 是否启用(停用后不可分配)。 */
  enabled: boolean
  sort_order: number
  created_at: string
  updated_at: string
}

/** 用户订阅视图(subscriptionView 基类)。 */
export interface SubscriptionBase {
  id: number
  plan_id: number
  granted_group?: string
  daily_cap_usd?: string | null
  weekly_cap_usd?: string | null
  monthly_cap_usd?: string | null
  status: string
  starts_at: string
  expires_at: string
  cancelled_at?: string | null
  created_at: string
}

/** 管理员视角订阅(adminSubscriptionView):比用户视图多 user_id/来源等。 */
export interface AdminSubscription extends SubscriptionBase {
  user_id: number
  source: string
  assigned_by_admin_id?: number
  prev_user_group?: string
}

/** GET /plans 响应。 */
export interface ListPlansResponse {
  plans: Plan[]
}

/** POST/PUT /plans 与 /plans/{id} 响应(单个套餐)。 */
export interface PlanResponse {
  plan: Plan
}

/** GET /assignments 响应。 */
export interface ListAssignmentsResponse {
  subscriptions: AdminSubscription[]
}

/** 写动作(分配/取消/重置/续期/改套餐)响应(单个订阅)。 */
export interface SubscriptionResponse {
  subscription: AdminSubscription
}

/** POST /assignments 响应(含幂等标志)。 */
export interface AssignResponse {
  subscription: AdminSubscription
  idempotent: boolean
}

/* ---- 写动作请求体(对齐后端请求结构体的 json tag) ---- */

/** 建/改套餐请求(createPlanRequest / 复用于 UpdatePlan)。 */
export interface UpsertPlanRequest {
  tenant_id: number
  name: string
  description?: string
  price_cents?: number
  currency_code?: string
  validity_days: number
  granted_group?: string
  daily_cap_usd?: string
  weekly_cap_usd?: string
  monthly_cap_usd?: string
  /** 改套餐(PUT)时后端要求必传;新建可省略(默认上架)。 */
  for_sale?: boolean
  sort_order?: number
}

/* ---- 批量分配 / 延长 / 改套餐 / 撤销 / 兑换券 请求与响应(对齐后端 admin_ops.go 的 json tag) ---- */

/** 批量分配请求(POST /assignments/bulk):同一套餐发给多个用户。 */
export interface BulkAssignRequest {
  tenant_id: number
  user_ids: number[]
  plan_id: number
}

/** 批量分配单用户结果(bulkAssignUserView)。OK=true 时带 subscription。 */
export interface BulkAssignUserResult {
  user_id: number
  ok: boolean
  error?: string
  idempotent?: boolean
  subscription?: AdminSubscription
}

/** POST /assignments/bulk 响应。 */
export interface BulkAssignResponse {
  results: BulkAssignUserResult[]
}

/**
 * 延长订阅请求(POST /assignments/{id}/extend)。
 * days 与 until 二选一(后端 extendAssignmentRequest:Days int / Until *time.Time)。
 */
export interface ExtendAssignmentRequest {
  tenant_id: number
  days?: number
  /** RFC3339 时间串(到期时间);与 days 二选一。 */
  until?: string
}

/** 改套餐请求(POST /assignments/{id}/change-plan)。new_plan_id 必填;降级需显式放行。 */
export interface ChangePlanRequest {
  tenant_id: number
  new_plan_id: number
  allow_downgrade?: boolean
}

/** 撤销订阅请求(POST /assignments/{id}/revoke)。硬性终止 admin 指派的订阅。 */
export interface RevokeAssignmentRequest {
  tenant_id: number
  reason: string
}

/** 建订阅兑换券请求(POST /vouchers)。grant_kind 由端点强制 subscription,不传。 */
export interface CreateSubscriptionVoucherRequest {
  tenant_id: number
  plan_id: number
  code?: string
  /** 名义价(分,信息性;兑换时不入余额)。 */
  amount_cents: number
  currency_code?: string
  /** 券码可兑换窗口起(RFC3339)。 */
  valid_from: string
  /** 券码可兑换窗口止(RFC3339)。 */
  valid_until: string
  max_redemptions?: number
  single_use_per_user?: boolean
  eligible_user_id?: number
}

/** 订阅券视图(voucher.Voucher,只展示必要字段)。code_hash 等敏感字段后端不回传。 */
export interface SubscriptionVoucher {
  id: number
  tenant_id: number
  code_fingerprint: string
  amount_cents: number
  currency_code: string
  valid_from: string
  valid_until: string
  max_redemptions: number
  redeemed_count: number
  single_use_per_user: boolean
  grant_kind: string
  subscription_plan_id?: number
  status: string
  created_at: string
}

/** POST /vouchers 响应:voucher 视图 + 明文 code(仅建券时回显一次)。 */
export interface CreateVoucherResponse {
  voucher: SubscriptionVoucher
  /** 明文券码,仅本次返回(后端只在创建时回显)。 */
  code?: string
}

/** 套餐编辑表单态(字符串承载数值输入,提交前归一)。 */
export interface PlanFormState {
  name: string
  description: string
  priceUsd: string
  currencyCode: string
  validityDays: string
  grantedGroup: string
  dailyCapUsd: string
  weeklyCapUsd: string
  monthlyCapUsd: string
  forSale: boolean
  sortOrder: string
}

export const EMPTY_PLAN_FORM: PlanFormState = {
  name: '',
  description: '',
  priceUsd: '',
  currencyCode: 'USD',
  validityDays: '30',
  grantedGroup: '',
  dailyCapUsd: '',
  weeklyCapUsd: '',
  monthlyCapUsd: '',
  forSale: true,
  sortOrder: '0',
}
