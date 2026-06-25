import { apiGet, apiSend } from '../../lib/api'
import type {
  BatchCreateResult,
  BatchCreateVoucherRequest,
  CreateResult,
  CreateVoucherRequest,
  GetBatchResult,
  RevokeResult,
  RevokeVoucherRequest,
  VoucherListResponse,
} from './types'

/*
 * 兑换码管理数据访问层。所有端点挂在 /v1/admin/vouchers 下(adminGate,platform_admin
 * RBAC),lib/api 的 tokenForPath 据 /v1/admin/ 前缀自动注入 admin token。
 * 端点核于 backend/internal/gatewayhttp/voucher_handler.go(MountVoucherAdminRoutes)
 * 与 cmd/gateway/routes.go:1020。
 */
const BASE = '/v1/admin/vouchers'

/** 列表。tenant_id 必填正整数,limit 1..200(默认 50)。返回 {vouchers}。 */
export async function listVouchers(
  tenantId: number,
  limit = 50,
  signal?: AbortSignal,
): Promise<VoucherListResponse> {
  return apiGet<VoucherListResponse>(BASE, { query: { tenant_id: tenantId, limit }, signal })
}

/** 单张创建。返回 {voucher, code},code 为仅此一次返回的明文兑换码。 */
export async function createVoucher(
  req: CreateVoucherRequest,
  signal?: AbortSignal,
): Promise<CreateResult> {
  return apiSend<CreateResult>('POST', BASE, req, { signal })
}

/** 批量创建。返回 {batch, vouchers, codes},codes 含每张明文码(仅此一次)。 */
export async function createVoucherBatch(
  req: BatchCreateVoucherRequest,
  signal?: AbortSignal,
): Promise<BatchCreateResult> {
  return apiSend<BatchCreateResult>('POST', `${BASE}/batch`, req, { signal })
}

/** 吊销一张券。返回 {voucher}(吊销后快照)。 */
export async function revokeVoucher(
  id: number,
  req: RevokeVoucherRequest,
  signal?: AbortSignal,
): Promise<RevokeResult> {
  return apiSend<RevokeResult>('POST', `${BASE}/${id}/revoke`, req, { signal })
}

/** 批次详情。tenant_id 必填。返回 {batch, vouchers}。 */
export async function getBatch(
  tenantId: number,
  batchId: number,
  signal?: AbortSignal,
): Promise<GetBatchResult> {
  return apiGet<GetBatchResult>(`${BASE}/batches/${batchId}`, { query: { tenant_id: tenantId }, signal })
}
