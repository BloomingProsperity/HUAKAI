import { describe, expect, it } from 'vitest'
import { parseSiteConfig } from './siteConfig'
import {
  BACKEND_SOCIAL_PROVIDERS,
  base64urlToBuffer,
  bufferToBase64url,
  captchaWidgetRenderable,
  deriveAffordances,
  providerLabel,
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
