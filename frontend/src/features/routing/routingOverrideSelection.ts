import type {
  CreateRoutingOverrideRequest,
  ModelRoutingOverride,
  UpdateRoutingOverrideRequest,
} from './types'

export interface RoutingOverrideForm {
  poolGroupId: string
  model: string
  providerAccountIDs: string
  enabled: boolean
}

export const EMPTY_ROUTING_OVERRIDE_FORM: RoutingOverrideForm = {
  poolGroupId: '',
  model: '',
  providerAccountIDs: '',
  enabled: true,
}

function parsePositiveInteger(value: string): number | undefined {
  const normalized = value.trim()
  if (!/^[1-9]\d*$/.test(normalized)) return undefined
  const parsed = Number(normalized)
  return Number.isSafeInteger(parsed) ? parsed : undefined
}

function parseProviderAccountIDs(value: string): number[] | { error: string } {
  const tokens = value.trim().split(/[\s,，]+/).filter(Boolean)
  if (tokens.length === 0) return { error: '请填写至少一个有效的 provider_account_id' }
  const seen = new Set<number>()
  const result: number[] = []
  for (const token of tokens) {
    const id = parsePositiveInteger(token)
    if (id === undefined) return { error: 'provider_account_ids 只能包含正整数' }
    if (seen.has(id)) continue
    seen.add(id)
    result.push(id)
  }
  return result
}

export function buildRoutingOverrideCreate(form: RoutingOverrideForm): CreateRoutingOverrideRequest | { error: string } {
  const poolGroupID = parsePositiveInteger(form.poolGroupId)
  if (poolGroupID === undefined) return { error: '请填写有效的 pool_group_id' }
  const model = form.model.trim()
  if (model === '') return { error: '请填写模型名' }
  const accountIDs = parseProviderAccountIDs(form.providerAccountIDs)
  if ('error' in accountIDs) return accountIDs
  return {
    pool_group_id: poolGroupID,
    model,
    provider_account_ids: accountIDs,
    enabled: form.enabled,
  }
}

export function buildRoutingOverrideUpdate(form: RoutingOverrideForm): UpdateRoutingOverrideRequest | { error: string } {
  const accountIDs = parseProviderAccountIDs(form.providerAccountIDs)
  if ('error' in accountIDs) return accountIDs
  return { provider_account_ids: accountIDs, enabled: form.enabled }
}

export function editRoutingOverrideForm(item: ModelRoutingOverride): RoutingOverrideForm {
  return {
    poolGroupId: String(item.pool_group_id),
    model: item.model,
    providerAccountIDs: item.provider_account_ids.join(', '),
    enabled: item.enabled,
  }
}
