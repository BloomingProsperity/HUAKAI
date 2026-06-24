import type { PlatformSetting, SettingUpdateRequest } from './types'

/*
 * 系统设置纯逻辑(可单测)。关键安全约定:密钥类 key 后端不回吐明文(value_configured 出现、value=null);
 * 前端据此:不显明文、编辑时空输入=不修改(不下发空串覆盖已配置的密钥)。
 */

/** 是否密钥/凭据类 key(后端以 value_configured 出现作信号)。 */
export function isSecretKey(s: PlatformSetting): boolean {
  return s.value_configured !== undefined
}

/** 列表展示用的值文案:密钥类显示已配置/未配置(绝不显明文);普通键显原值。 */
export function displayValue(s: PlatformSetting): string {
  if (isSecretKey(s)) return s.value_configured ? '已配置' : '未配置'
  return s.value ?? '(空)'
}

/** 来源标签:env=环境变量(只读)、db=数据库覆盖、default=默认。 */
export function sourceLabel(source: string): string {
  switch (source) {
    case 'env':
      return '环境变量'
    case 'db':
      return '数据库覆盖'
    case 'default':
      return '默认值'
    default:
      return source
  }
}

/** env 来源只读(进程环境变量,不可经 API 改),禁止编辑。 */
export function isReadOnly(s: PlatformSetting): boolean {
  return s.source === 'env'
}

export type UpdateResult = SettingUpdateRequest | { error: string } | { noop: true }

/**
 * 构造 PUT 请求体。密钥类:空输入视为"不修改"返回 noop(避免空串覆盖已配置密钥);
 * 普通键:允许设空串。reason 可选,空白省略。
 */
export function buildSettingUpdate(s: PlatformSetting, draftValue: string, reason: string): UpdateResult {
  if (isReadOnly(s)) return { error: '该项来自环境变量,只读不可改' }
  const secret = isSecretKey(s)
  if (secret && draftValue.trim() === '') {
    return { noop: true }
  }
  const req: SettingUpdateRequest = { value: draftValue }
  const r = reason.trim()
  if (r) req.reason = r
  return req
}
