import { describe, expect, it } from 'vitest'
import {
  TAB_GROUPS,
  buildSettingUpdate,
  controlFor,
  displayValue,
  groupedKeys,
  isReadOnly,
  isSecretSetting,
} from './settings'
import type { PlatformSetting } from './types'

/*
 * 设置中心纯逻辑单测。判别核心三类:
 *  ①分签全覆盖(后端每个 key 都被某 tab 收下,不漏不重)——证明"全塞进设置";
 *  ②secret-mask(密钥类不显明文 + 控件判 secret);
 *  ③buildSettingUpdate 的密钥空输入 noop 守卫 / env 只读。
 */

// 后端 platformsettings/types.go orderedSettingKeys 的权威全集(55 个)。
// 此清单是"别漏 key"的硬契约:后端加/删 key 时必须同步本清单与 TAB_GROUPS,否则本测试 RED。
const BACKEND_KEYS: string[] = [
  'registration_enabled',
  'invitation_required',
  'password_register_enabled',
  'password_login_enabled',
  'email_domain_allowlist_enabled',
  'email_domain_allowlist',
  'email_alias_restriction_enabled',
  'reserved_email_localparts',
  'captcha_enabled',
  'two_factor_enabled',
  'captcha_provider',
  'captcha_site_key',
  'captcha_secret',
  'oauth_providers_enabled',
  'oauth_providers_config',
  'oauth_providers_secrets',
  'telegram_bot_username',
  'telegram_bot_token',
  'promo_enabled',
  'stream_timeout_seconds',
  'cooldown_429_seconds',
  'cooldown_529_seconds',
  'response_header_deny_extra',
  'response_header_allow_override',
  'model_fallback_chains',
  'budget_limits',
  'payment_provider_config',
  'checkin_enabled',
  'checkin_min_cents',
  'checkin_max_cents',
  'referral_reward_enabled',
  'referral_reward_cents',
  'passkey_enabled',
  'passkey_registration_enabled',
  'passkey_rp_id',
  'passkey_rp_display_name',
  'passkey_rp_origins',
  'mediatask_enabled',
  'mediatask_provider_base_url',
  'mediatask_poll_interval_seconds',
  'mediatask_task_timeout_seconds',
  'mediatask_default_estimated_cents',
  'moderation_external_enabled',
  'moderation_external_base_url',
  'moderation_external_api_keys',
  'moderation_external_model',
  'moderation_external_thresholds',
  'moderation_external_timeout_ms',
  'moderation_external_retry_count',
  'moderation_external_image_enabled',
  'warmup_intercept_enabled',
  'site_name',
  'site_logo',
  'site_footer',
  'site_home_content',
  'site_subtitle',
  'site_contact_info',
  'site_doc_url',
  'site_api_base_url',
  'site_frontend_base_url',
  'admin_notification_email',
]

function setting(over: Partial<PlatformSetting>): PlatformSetting {
  return { key: 'k', value: null, source: 'db', ...over }
}

describe('TAB_GROUPS 分签全覆盖', () => {
  it('后端每个 key 都被某 tab 归组,且不重复(全塞进设置)', () => {
    // 判别核心:已归组集合必须 == 后端全集。
    // 变异(从任一 tab 删掉一个 item / 重复一个 key)→ 集合不等 → 本断言 RED。
    const grouped = groupedKeys()
    expect(new Set(grouped).size).toBe(grouped.length) // 无重复
    expect(new Set(grouped)).toEqual(new Set(BACKEND_KEYS)) // 不漏不多
    expect(grouped.length).toBe(61)
  })

  it('正好 9 个分签且 key 唯一', () => {
    expect(TAB_GROUPS).toHaveLength(9)
    expect(TAB_GROUPS.map((t) => t.key)).toEqual([
      'general',
      'users',
      'security',
      'gateway',
      'features',
      'payment',
      'email',
      'agreement',
      'backup',
    ])
  })
})

describe('controlFor 控件判定', () => {
  it('密钥 key 判为 secret(脱敏优先)', () => {
    // 判别核心:moderation_external_api_keys 必须先判 secret,不能落到 json/string。
    // 变异(controlFor 去掉 secret 分支)→ 该 key 走 json → 本断言 RED。
    expect(controlFor('moderation_external_api_keys')).toBe('secret')
  })
  it('布尔/数字/JSON/字符串各归其类', () => {
    expect(controlFor('registration_enabled')).toBe('bool')
    expect(controlFor('stream_timeout_seconds')).toBe('number')
    expect(controlFor('model_fallback_chains')).toBe('json')
    expect(controlFor('site_home_content')).toBe('multiline')
    expect(controlFor('site_name')).toBe('string')
  })
})

describe('secret-mask 显示', () => {
  it('密钥类(value_configured 出现)绝不显明文,只显已配置/未配置', () => {
    // 判别核心:密钥类走 value_configured 文案,不回显 value。
    // 变异(isSecretSetting 恒 false)→ displayValue 尝试显 value → 本断言 RED。
    const secret = setting({ value: null, value_configured: true })
    expect(isSecretSetting(secret)).toBe(true)
    expect(displayValue(secret)).toBe('已配置')
    expect(displayValue(setting({ value_configured: false }))).toBe('未配置')
  })
  it('普通键(无 value_configured)显原值', () => {
    expect(isSecretSetting(setting({ value: 'on' }))).toBe(false)
    expect(displayValue(setting({ value: 'on' }))).toBe('on')
    expect(displayValue(setting({ value: null }))).toBe('(空)')
  })
})

describe('buildSettingUpdate 守卫', () => {
  it('env 来源只读 → 报错', () => {
    expect(buildSettingUpdate(setting({ source: 'env', value: 'x' }), 'y', '')).toEqual({
      error: '该项来自环境变量,只读不可改',
    })
    expect(isReadOnly(setting({ source: 'env' }))).toBe(true)
  })

  it('密钥类空输入 → noop(不空串覆盖已配置密钥)', () => {
    // 判别核心:密钥空输入禁止下发空串。
    // 变异(去掉 noop 分支)→ 会下发 value:'' → 本断言 RED。
    expect(buildSettingUpdate(setting({ value_configured: true }), '   ', '')).toEqual({ noop: true })
  })

  it('密钥类有输入 → 正常下发,reason 带上', () => {
    expect(buildSettingUpdate(setting({ value_configured: false }), 'new-secret', '轮换')).toEqual({
      value: 'new-secret',
      reason: '轮换',
    })
  })

  it('普通键允许设空串,reason 空白省略', () => {
    expect(buildSettingUpdate(setting({ value: 'on' }), '', '')).toEqual({ value: '' })
  })
})
