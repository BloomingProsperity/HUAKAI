import { apiGet, ApiError } from '../../lib/api'
import { getTokens } from '../../auth/store'
import { tokenForPath } from '../../auth/tokenForPath'
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

const CREATE_PATH = '/admin/v1/provider-accounts'

/**
 * 创建请求的请求头。【关键】此处必须显式注入 admin Bearer:创建走裸 fetch(为读"混合渠道风险"
 * 的字符串哨兵响应体,无法借 lib/api 自动注入),若漏注则后端 admin 中间件恒 401(只认
 * Authorization、不吃 cookie),新建账号生产中提交不成功。token 为空时不带头(后端自然 401),
 * 不伪造。抽成纯函数便于单测。
 */
export function createRequestHeaders(token: string | null): HeadersInit {
  return {
    Accept: 'application/json',
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  }
}
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
  // 按路径选 admin token(与 lib/api 同一机制),显式注入到裸 fetch。
  const token = tokenForPath(CREATE_PATH, getTokens())
  const resp = await fetch(CREATE_PATH, {
    method: 'POST',
    credentials: 'include',
    headers: createRequestHeaders(token),
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
