import { describe, expect, it } from 'vitest'
import { parseSiteConfig } from './siteConfig'
import {
  BACKEND_SOCIAL_PROVIDERS,
  base64urlToBuffer,
  bufferToBase64url,
  captchaWidgetRenderable,
  deriveAffordances,
  inviteHintFromResult,
  providerLabel,
  shouldValidateInvite,
  validateRegisterForm,
} from './loginEnhance'

describe('validateRegisterForm', () => {
  const base = {
    email: 'a@b.com',
    password: 'pw123456',
    confirmPassword: 'pw123456',
    inviteCode: '',
    invitationRequired: false,
  }
  it('齐全且两次一致 → null', () => {
    expect(validateRegisterForm(base)).toBeNull()
  })
  it('两次密码不一致 → 拦截', () => {
    // 判别核心:确认密码不一致必须报错。变异(删掉 password!==confirmPassword 分支)→ RED。
    expect(validateRegisterForm({ ...base, confirmPassword: 'different' })).toContain('不一致')
  })
  it('invitation_required 且邀请码空白 → 拦截', () => {
    // 判别核心:开启邀请门时空邀请码必须拦。变异(忽略 invitationRequired)→ RED。
    expect(validateRegisterForm({ ...base, invitationRequired: true, inviteCode: '   ' })).toContain('邀请码')
  })
  it('invitation_required 但邀请码已填 → 通过', () => {
    expect(validateRegisterForm({ ...base, invitationRequired: true, inviteCode: 'INV-1' })).toBeNull()
  })
  it('邮箱/密码为空各自拦截', () => {
    expect(validateRegisterForm({ ...base, email: '  ' })).toContain('邮箱')
    expect(validateRegisterForm({ ...base, password: '', confirmPassword: '' })).toContain('密码')
  })
})

describe('shouldValidateInvite', () => {
  it('站点未开启邀请门 → 即便有码也不校验', () => {
    // 判别核心:invitationRequired=false 时不打扰(可选填)。
    // 变异(去掉 invitationRequired 判定)→ 此处会误返回 true → RED。
    expect(shouldValidateInvite('ABC', false)).toBe(false)
  })
  it('开启邀请门但邀请码空白 → 不校验', () => {
    // 判别核心:空白码不发请求(由提交必填校验兜底)。
    // 变异(去掉 trim().length>0 判定)→ 空白也会触发校验 → RED。
    expect(shouldValidateInvite('   ', true)).toBe(false)
    expect(shouldValidateInvite('', true)).toBe(false)
  })
  it('开启邀请门且邀请码非空 → 校验', () => {
    expect(shouldValidateInvite('INV-1', true)).toBe(true)
  })
})

describe('inviteHintFromResult', () => {
  it('valid=true → ok(无论 reason)', () => {
    // 判别核心:后端有效即可用,reason 不影响 ok 判定(disabled 在站点关门时 valid=true)。
    // 变异(把 result.valid 改成 !result.valid)→ 有效码会被判 invalid → RED。
    expect(inviteHintFromResult({ valid: true, reason: 'valid' }).status).toBe('ok')
    expect(inviteHintFromResult({ valid: true, reason: 'disabled' }).status).toBe('ok')
  })
  it('valid=false 各 reason → invalid + 对应中文原因', () => {
    // 判别核心:不同失败原因映射到不同中文文案。
    // 变异(把 not_found 的映射删掉/改成兜底)→ 文案不再含"不存在" → RED。
    expect(inviteHintFromResult({ valid: false, reason: 'not_found' }).message).toContain('不存在')
    expect(inviteHintFromResult({ valid: false, reason: 'expired' }).message).toContain('过期')
    expect(inviteHintFromResult({ valid: false, reason: 'used_or_exhausted' }).message).toContain('使用')
    expect(inviteHintFromResult({ valid: false, reason: 'disabled' }).message).toContain('停用')
  })
  it('valid=false 且 reason 为后端未知值 → 通用兜底文案', () => {
    // 判别核心:未知 reason 不应崩、给通用"无效"提示。
    // 变异(去掉 ?? 兜底,改成直接索引)→ message 变 undefined,toContain 抛错 → RED。
    const hint = inviteHintFromResult({ valid: false, reason: 'something_new' })
    expect(hint.status).toBe('invalid')
    expect(hint.message).toContain('无效')
  })
  it('valid=false 的状态恒为 invalid(非 ok/unavailable)', () => {
    // 判别核心:无效结果一定是 invalid 态(才会渲染危险色提示)。
    expect(inviteHintFromResult({ valid: false, reason: 'expired' }).status).toBe('invalid')
  })
})

describe('deriveAffordances', () => {
  it('注册入口需 registration_enabled 与 password_register_enabled 同时为真', () => {
    // 判别核心:两个开关是 AND 关系。变异(改成 OR)会让只开其一时错误显示注册 → RED。
    expect(deriveAffordances(parseSiteConfig({ registration_enabled: true, password_register_enabled: true })).showRegister).toBe(true)
    expect(deriveAffordances(parseSiteConfig({ registration_enabled: true, password_register_enabled: false })).showRegister).toBe(false)
    expect(deriveAffordances(parseSiteConfig({ registration_enabled: false, password_register_enabled: true })).showRegister).toBe(false)
  })
  it('密码登录区随 password_login_enabled,缺省显示', () => {
    expect(deriveAffordances(parseSiteConfig({})).showPasswordLogin).toBe(true)
    expect(deriveAffordances(parseSiteConfig({ password_login_enabled: false })).showPasswordLogin).toBe(false)
  })
  it('社交按钮行只在有 provider 时显示', () => {
    expect(deriveAffordances(parseSiteConfig({ oauth_providers_enabled: 'github' })).showOauth).toBe(true)
    expect(deriveAffordances(parseSiteConfig({})).showOauth).toBe(false)
  })
  it('telegram 从 oauth-init 按钮列表排除(它走 widget,渲染成按钮点了必报错)', () => {
    // 判别核心:telegram 绝不能进 oauthProviders(那会渲染一个调 oauth-init 的坏按钮)。
    // 变异(deriveAffordances 不 filter telegram)→ telegram 出现在 oauthProviders,本断言 RED。
    const af = deriveAffordances(parseSiteConfig({ oauth_providers_enabled: 'github,telegram' }))
    expect(af.oauthProviders).toEqual(['github'])
    expect(af.oauthProviders).not.toContain('telegram')
    // 只有 telegram 时,oauth-init 按钮行不显示(没有可跳转的 provider)。
    expect(deriveAffordances(parseSiteConfig({ oauth_providers_enabled: 'telegram' })).showOauth).toBe(false)
  })
  it('telegram 登录 widget 需 telegram∈providers 且有公开 bot_username', () => {
    // 判别核心:两个条件 AND。变异(只看 providers 不看 username)→ 无 username 时误判可渲染 → RED。
    expect(deriveAffordances(parseSiteConfig({ oauth_providers_enabled: 'telegram', telegram_bot_username: 'HuakaiBot' })).telegramLogin).toBe(true)
    expect(deriveAffordances(parseSiteConfig({ oauth_providers_enabled: 'telegram' })).telegramLogin).toBe(false)
    expect(deriveAffordances(parseSiteConfig({ telegram_bot_username: 'HuakaiBot' })).telegramLogin).toBe(false)
  })
  it('passkey 按钮随 passkey_enabled', () => {
    expect(deriveAffordances(parseSiteConfig({ passkey_enabled: true })).showPasskey).toBe(true)
    expect(deriveAffordances(parseSiteConfig({})).showPasskey).toBe(false)
  })
})

describe('captchaWidgetRenderable', () => {
  it('需 enabled + turnstile + site_key 三者齐备', () => {
    // 判别核心:三个条件 AND。变异(去掉 site_key 检查)会在无 key 时误判可渲染 → RED。
    expect(captchaWidgetRenderable(parseSiteConfig({ captcha_enabled: true, captcha_provider: 'turnstile', captcha_site_key: 'k' }))).toBe(true)
    expect(captchaWidgetRenderable(parseSiteConfig({ captcha_enabled: true, captcha_provider: 'turnstile', captcha_site_key: '' }))).toBe(false)
    expect(captchaWidgetRenderable(parseSiteConfig({ captcha_enabled: true, captcha_provider: 'hcaptcha', captcha_site_key: 'k' }))).toBe(false)
    expect(captchaWidgetRenderable(parseSiteConfig({ captcha_enabled: false, captcha_provider: 'turnstile', captcha_site_key: 'k' }))).toBe(false)
  })
})

describe('providerLabel', () => {
  it('已知 provider 用友好名,未知首字母大写', () => {
    expect(providerLabel('github')).toBe('GitHub')
    expect(providerLabel('GOOGLE')).toBe('Google')
    expect(providerLabel('custom')).toBe('Custom')
  })
  it('parity 守卫:后端全部 10 家社交 provider 都有专属友好名(非首字母兜底)', () => {
    // 判别核心:Owner 硬规则"别人有的我也要有"。后端任一 provider 漏配展示名时,
    // providerLabel 会退化成首字母大写兜底(如 nodeseek→Nodeseek),此处必须转红提醒补表。
    // 变异(从 PROVIDER_LABELS 删任一家)→ 该家走兜底、与期望友好名不等 → RED。
    const expected: Record<string, string> = {
      google: 'Google', github: 'GitHub', qq: 'QQ', wechat: '微信', dingtalk: '钉钉',
      nodeseek: 'NodeSeek', linuxdo: 'LINUX DO', oidc: 'OIDC', discord: 'Discord', telegram: 'Telegram',
    }
    expect(BACKEND_SOCIAL_PROVIDERS.length).toBe(10)
    for (const p of BACKEND_SOCIAL_PROVIDERS) {
      // 兜底输出必为首字母大写;专属名必须与之不同(QQ/OIDC 已是大写,用 expected 显式校验避免误判)。
      expect(providerLabel(p)).toBe(expected[p])
    }
  })
})

describe('base64url 往返转换', () => {
  it('buffer → base64url → buffer 还原一致(含 +// 字符与 padding 场景)', () => {
    // 0xfb 0xff 0xbf 在标准 base64 会产生 + 和 /,验证 base64url 替换与还原正确。
    const bytes = new Uint8Array([0xfb, 0xff, 0xbf, 0x00, 0x10])
    const b64url = bufferToBase64url(bytes.buffer)
    // 判别核心:base64url 输出不含 + / =。变异(漏掉 replace 链)→ 出现 +// → RED。
    expect(b64url).not.toMatch(/[+/=]/)
    const round = new Uint8Array(base64urlToBuffer(b64url))
    expect(Array.from(round)).toEqual(Array.from(bytes))
  })
  it('解码后端式 base64url challenge(无 padding)成功', () => {
    // "AQID" = [1,2,3];典型 WebAuthn challenge 形态。
    const buf = new Uint8Array(base64urlToBuffer('AQID'))
    expect(Array.from(buf)).toEqual([1, 2, 3])
  })
})
