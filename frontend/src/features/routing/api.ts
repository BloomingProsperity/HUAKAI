import { apiGet, apiSend } from '../../lib/api'
import type { BindingListResponse, CreateBindingRequest, PoolBinding, UpdateBindingRequest } from './types'

/*
 * 路由绑定数据访问层。端点 /admin/v1/model-pool-bindings(admin 鉴权)。
 */
const PATH = '/admin/v1/model-pool-bindings'

export async function listBindings(
  filters: { modelId?: string; poolGroupId?: string } = {},
  signal?: AbortSignal,
): Promise<BindingListResponse> {
  return apiGet<BindingListResponse>(PATH, {
    query: {
      model_id: filters.modelId?.trim() || undefined,
      pool_group_id: filters.poolGroupId?.trim() || undefined,
    },
    signal,
  })
}

export async function createBinding(body: CreateBindingRequest): Promise<PoolBinding> {
  return apiSend<PoolBinding>('POST', PATH, body)
}

export async function updateBinding(id: number, body: UpdateBindingRequest): Promise<PoolBinding> {
  return apiSend<PoolBinding>('PATCH', `${PATH}/${id}`, body)
}

export async function deleteBinding(id: number): Promise<void> {
  await apiSend<unknown>('DELETE', `${PATH}/${id}`)
}
