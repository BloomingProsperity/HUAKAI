import { apiGet, apiSend } from '../../lib/api'
import type {
  BindingListResponse,
  CreateBindingRequest,
  CreateRoutingOverrideRequest,
  ModelRoutingOverride,
  PoolBinding,
  RoutingOverrideListResponse,
  UpdateBindingRequest,
  UpdateRoutingOverrideRequest,
} from './types'

/*
 * 路由绑定数据访问层。端点 /admin/v1/model-pool-bindings(admin 鉴权)。
 */
const BINDING_PATH = '/admin/v1/model-pool-bindings'
const ROUTING_OVERRIDE_PATH = '/admin/v1/model-routing-overrides'

export async function listBindings(
  tenantId: number,
  filters: { modelId?: string; poolGroupId?: string } = {},
  signal?: AbortSignal,
): Promise<BindingListResponse> {
  return apiGet<BindingListResponse>(BINDING_PATH, {
    query: {
      tenant_id: tenantId,
      model_id: filters.modelId?.trim() || undefined,
      pool_group_id: filters.poolGroupId?.trim() || undefined,
    },
    signal,
  })
}

export async function createBinding(body: CreateBindingRequest, tenantId: number): Promise<PoolBinding> {
  return apiSend<PoolBinding>('POST', BINDING_PATH, body, { query: { tenant_id: tenantId } })
}

export async function updateBinding(id: number, body: UpdateBindingRequest, tenantId: number): Promise<PoolBinding> {
  return apiSend<PoolBinding>('PATCH', `${BINDING_PATH}/${id}`, body, { query: { tenant_id: tenantId } })
}

export async function deleteBinding(id: number, tenantId: number): Promise<void> {
  await apiSend<unknown>('DELETE', `${BINDING_PATH}/${id}`, undefined, { query: { tenant_id: tenantId } })
}

export async function listRoutingOverrides(tenantId: number, signal?: AbortSignal): Promise<RoutingOverrideListResponse> {
  return apiGet<RoutingOverrideListResponse>(ROUTING_OVERRIDE_PATH, {
    query: { tenant_id: tenantId },
    signal,
  })
}

export async function createRoutingOverride(body: CreateRoutingOverrideRequest, tenantId: number): Promise<ModelRoutingOverride> {
  return apiSend<ModelRoutingOverride>('POST', ROUTING_OVERRIDE_PATH, body, { query: { tenant_id: tenantId } })
}

export async function updateRoutingOverride(id: number, body: UpdateRoutingOverrideRequest, tenantId: number): Promise<ModelRoutingOverride> {
  return apiSend<ModelRoutingOverride>('PATCH', `${ROUTING_OVERRIDE_PATH}/${id}`, body, { query: { tenant_id: tenantId } })
}

export async function deleteRoutingOverride(id: number, tenantId: number): Promise<void> {
  await apiSend<unknown>('DELETE', `${ROUTING_OVERRIDE_PATH}/${id}`, undefined, { query: { tenant_id: tenantId } })
}
