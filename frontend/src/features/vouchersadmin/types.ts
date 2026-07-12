/*
 * 兑换码管理(运营台,admin 壳)前端类型 —— 镜像后端 voucher 包 JSON 形态。
 * 端点(均 adminGate,platform_admin RBAC,带 admin token):
 *   GET  /v1/admin/vouchers?tenant_id=N&limit=N        列表
 *   POST /v1/admin/vouchers                             单张创建 → 201 {voucher, code}
 *   POST /v1/admin/vouchers/batch                       批量创建 → 201 {batch, vouchers, codes}
 *   POST /v1/admin/vouchers/{id}/revoke                 吊销     → 200 {voucher}
 *   GET  /v1/admin/vouchers/batches/{batch_id}?tenant_id=N  批次  → 200 {batch, vouchers}
 * 字段名严格对齐 backend/internal/voucher/types.go 的 json tag。
 */

/** 券状态(VoucherStatus,与后端常量对齐)。 */
export type VoucherStatus = 'active' | 'expired' | 'exhausted' | 'revoked'

/** 批次状态(BatchStatus)。 */
export type BatchStatus = 'active' | 'completed' | 'failed' | 'revoked'

/** 券记录(voucher.Voucher)。注意:后端绝不回明文 code/code_hash,只回指纹(code_fingerprint)。 */
export interface Voucher {
  id: number
  tenant_id: number
  batch_id?: number | null
  code_fingerprint: string
  amount_cents: number
  currency_code: string
  valid_from: string
  valid_until: string
  max_redemptions: number
  redeemed_count: number
  single_use_per_user: boolean
  eligible_user_id?: number | null
  grant_kind: string
  subscription_plan_id?: number | null
  status: VoucherStatus
  created_by_admin_id?: number
  revoked_by_admin_id?: number
  revoked_reason?: string
  created_at: string
  updated_at: string
  revoked_at?: string | null
}

/** 批次(voucher.Batch)。 */
export interface Batch {
  id: number
  tenant_id: number
  created_by_admin_id?: number
  requested_count: number
  created_count: number
  amount_cents: number
  currency_code: string
  valid_from: string
  valid_until: string
  max_redemptions: number
  single_use_per_user: boolean
  status: BatchStatus
  created_at: string
}

/** 列表响应:{vouchers: [...]}。 */
export interface VoucherListResponse {
  vouchers: Voucher[]
}

/**
 * 单张创建响应:{voucher, code}。code 是仅此一次返回的明文兑换码(后续不可再取),
 * 必须当场展示给运营者保存。
 */
export interface CreateResult {
  voucher: Voucher
  code?: string
}

/** 批量创建里单条码(voucher.CreatedCode)。code 明文仅此一次返回。 */
export interface CreatedCode {
  voucher_id: number
  code: string
  code_fingerprint: string
}

/** 批量创建响应:{batch, vouchers, codes}。 */
export interface BatchCreateResult {
  batch: Batch
  vouchers: Voucher[]
  codes: CreatedCode[]
}

/** 吊销响应:{voucher}。 */
export interface RevokeResult {
  voucher: Voucher
}

/** 批次详情响应:{batch, vouchers}。 */
export interface GetBatchResult {
  batch: Batch
  vouchers: Voucher[]
}

/** 单张创建请求体(voucherCreateRequest)。 */
export interface CreateVoucherRequest {
  tenant_id: number
  code?: string
  amount_cents: number
  currency_code?: string
  valid_from: string
  valid_until: string
  max_redemptions?: number
  single_use_per_user?: boolean
  eligible_user_id?: number | null
}

/** 批量创建请求体(voucherBatchCreateRequest)。 */
export interface BatchCreateVoucherRequest {
  tenant_id: number
  count: number
  amount_cents: number
  currency_code?: string
  valid_from: string
  valid_until: string
  max_redemptions?: number
  single_use_per_user?: boolean
  eligible_user_id?: number | null
}

/** 吊销请求体(voucherRevokeRequest)。 */
export interface RevokeVoucherRequest {
  tenant_id: number
  reason?: string
}
