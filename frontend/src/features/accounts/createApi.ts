import { apiGet, ApiError } from '../../lib/api'
import type { ProviderAccount } from './types'
import type {
  AccountModesResponse,
  ChannelCatalogResponse,
  CreateAccountRequest,
  MixedRiskRequired,
  ProviderCatalogResponse,
} from './createTypes'

/*
 * 新建账号向导的数据访问层:三目录读取 + 创建(含混合渠道风险识别)。
 */
export async function fetchAccountModes(signal?: AbortSignal): Promise<AccountModesResponse> {
  return apiGet<AccountModesResponse>('/admin/v1/account-modes', { signal })
}

export async function fetchProviders(signal?: AbortSignal): Promise<ProviderCatalogResponse> {
  return apiGet<ProviderCatalogResponse>('/admin/v1/providers', { query: { limit: 200 }, signal })
}

export async function fetchChannels(signal?: AbortSignal): Promise<ChannelCatalogResponse> {
  return apiGet<ChannelCatalogResponse>('/admin/v1/channels', { query: { limit: 200 }, signal })
}

/**
 * 创建账号。后端混合渠道风险时回 HTTP 400 + error:"mixed_channel_risk_confirmation_required"
 * (注意:此响应的 error 是字符串而非 {code,message},与普通错误形态不同),需特判后让向导
 * 弹二次确认。返回 ProviderAccount 表示创建成功;返回 MixedRiskRequired 表示需确认;其余抛 ApiError。
 */
export async function createProviderAccount(
  body: CreateAccountRequest,
): Promise<ProviderAccount | MixedRiskRequired> {
  const resp = await fetch('/admin/v1/provider-accounts', {
    method: 'POST',
    credentials: 'include',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const text = await resp.text()
  const parsed: unknown = text ? JSON.parse(text) : undefined

  if (resp.ok) {
    return parsed as ProviderAccount
  }

  // 混合渠道风险特判:error 为字符串哨兵 + 带 risks。
  const obj = (parsed ?? {}) as Record<string, unknown>
  if (obj.error === 'mixed_channel_risk_confirmation_required') {
    return {
      mixedRisk: true,
      risks: Array.isArray(obj.risks) ? obj.risks : [],
      message: typeof obj.message === 'string' ? obj.message : '同渠道混入异源账号,请运维确认后再创建',
    }
  }

  // 普通错误形态 {error:{code,message}}。
  const errShape = obj.error as { code?: string; message?: string } | undefined
  throw new ApiError(
    resp.status,
    errShape?.code || `http_${resp.status}`,
    errShape?.message || resp.statusText || '创建账号失败',
  )
}
