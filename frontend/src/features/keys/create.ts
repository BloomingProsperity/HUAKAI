import type { CreateKeyRequest } from './types'

/*
 * 创建 API Key 的纯逻辑(可单测,now 由调用方注入以避免 Date.now 不确定性)。
 * 后端契约:expires_at 省略=永不过期;RFC3339=指定时刻;environment 缺省 live。
 */

export const KEY_ENVIRONMENTS = ['live', 'test'] as const
export type KeyEnvironment = (typeof KEY_ENVIRONMENTS)[number]

/** 过期预设:never=不传 expires_at;Nd=now+N 天;custom=用自定义日期。 */
export type ExpiryPreset = 'never' | '7d' | '30d' | '90d' | 'custom'

export const EXPIRY_PRESETS: ReadonlyArray<{ value: ExpiryPreset; label: string }> = [
  { value: 'never', label: '永不过期' },
  { value: '7d', label: '7 天后' },
  { value: '30d', label: '30 天后' },
  { value: '90d', label: '90 天后' },
  { value: 'custom', label: '自定义日期' },
]

const PRESET_DAYS: Record<string, number> = { '7d': 7, '30d': 30, '90d': 90 }

export interface CreateKeyForm {
  name: string
  environment: KeyEnvironment
  expiryPreset: ExpiryPreset
  /** custom 预设时的日期(YYYY-MM-DD,date input 值)。 */
  customDate: string
}

export const EMPTY_KEY_FORM: CreateKeyForm = {
  name: '',
  environment: 'live',
  expiryPreset: 'never',
  customDate: '',
}

/**
 * 据预设算出 expires_at(RFC3339)或 undefined(永不)。now 注入便于单测。
 * custom 预设:把 YYYY-MM-DD 解释成当天 23:59:59 本地时刻再转 RFC3339;非法/空返回 undefined。
 */
export function resolveExpiresAt(
  preset: ExpiryPreset,
  customDate: string,
  now: Date,
): string | undefined {
  if (preset === 'never') return undefined
  if (preset === 'custom') {
    const d = customDate.trim()
    if (!d) return undefined
    const parsed = new Date(`${d}T23:59:59`)
    return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString()
  }
  const days = PRESET_DAYS[preset]
  if (!days) return undefined
  const t = new Date(now.getTime() + days * 24 * 60 * 60 * 1000)
  return t.toISOString()
}

export function buildCreateKeyRequest(form: CreateKeyForm, now: Date): CreateKeyRequest {
  const req: CreateKeyRequest = { name: form.name.trim(), environment: form.environment }
  const expiresAt = resolveExpiresAt(form.expiryPreset, form.customDate, now)
  if (expiresAt) req.expires_at = expiresAt
  return req
}

/** 前端先校验:名称必填且 ≤128;custom 预设须选日期且不能是过去。返回首条错误或 null。 */
export function validateCreateKeyForm(form: CreateKeyForm, now: Date): string | null {
  const name = form.name.trim()
  if (!name) return '请填写 Key 名称'
  if (name.length > 128) return 'Key 名称不能超过 128 字符'
  if (form.expiryPreset === 'custom') {
    if (!form.customDate.trim()) return '请选择自定义过期日期'
    const exp = resolveExpiresAt('custom', form.customDate, now)
    if (!exp) return '过期日期非法'
    if (new Date(exp).getTime() <= now.getTime()) return '过期日期必须在未来'
  }
  return null
}
