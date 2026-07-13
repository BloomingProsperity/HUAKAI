import { apiGet, apiSend } from '../../lib/api'
import type { BindingListResponse, CreateBindingRequest, PoolBinding, UpdateBindingRequest } from './types'

/*
 * 路由绑定数据访问层。端点 /admin/v1/model-pool-bindings(admin 鉴权)。
 */
const PATH = '/admin/v1/model-pool-bindings'

export async function listBindings(
  tenantId: number,
  filters: { modelId?: string; poolGroupId?: string } = {},
  signal?: AbortSignal,
): Promise<BindingListResponse> {
  return apiGet<BindingListResponse>(PATH, {
    query: {
      tenant_id: tenantId,
      model_id: filters.modelId?.trim() || undefined,
      pool_group_id: filters.poolGroupId?.trim() || undefined,
    },
    signal,
  })
}

export async function createBinding(body: CreateBindingRequest, tenantId: number): Promise<PoolBinding> {
  return apiSend<PoolBinding>('POST', PATH, body, { query: { tenant_id: tenantId } })
}

export async function updateBinding(id: number, body: UpdateBindingRequest, tenantId: number): Promise<PoolBinding> {
  return apiSend<PoolBinding>('PATCH', `${PATH}/${id}`, body, { query: { tenant_id: tenantId } })
}

export async function deleteBinding(id: number, tenantId: number): Promise<void> {
  await apiSend<unknown>('DELETE', `${PATH}/${id}`, undefined, { query: { tenant_id: tenantId } })
}
