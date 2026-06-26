import { describe, expect, it } from 'vitest'
import { FALLBACK_SITE_CONFIG, parseOauthProviders, parseSiteConfig } from './siteConfig'

describe('parseOauthProviders', () => {
  it('逗号分隔字符串 → 去空白/小写/去重的列表', () => {
    // 判别核心:大小写归一 + 去重 + 去空项。变异(漏 toLowerCase 或漏去重)→ RED。
    expect(parseOauthProviders(' GitHub, google ,,GITHUB ')).toEqual(['github', 'google'])
  })
  it('数组形态同样接受', () => {
    expect(parseOauthProviders(['github', 'oidc'])).toEqual(['github', 'oidc'])
  })
  it('缺省/空串 → 空数组', () => {
    // 判别核心:undefined 不能崩,空串不产生 [''] 这种会渲染空按钮的脏项。
    expect(parseOauthProviders(undefined)).toEqual([])
    expect(parseOauthProviders('')).toEqual([])
    expect(parseOauthProviders('  ,  ')).toEqual([])
  })
})

describe('parseSiteConfig', () => {
  it('布尔旋钮 fail-closed:只有显式 true 才开启', () => {
    const cfg = parseSiteConfig({
      registration_enabled: true,
      passkey_enabled: true,
      captcha_enabled: false,
    })
    // 判别核心:显式 true → 开;false/缺省 → 关。变异(把 asBool 改成 Boolean(v) 放过 truthy 字符串)
    // 不会被这条直接抓,但下面"字符串 true 不算开"那条抓。
    expect(cfg.registrationEnabled).toBe(true)
    expect(cfg.passkeyEnabled).toBe(true)
    expect(cfg.captchaEnabled).toBe(false)
  })
  it('非布尔的 truthy 值不被当作开启(fail-closed)', () => {
    // 判别核心:asBool 严格 === true。变异(用 Boolean(v) 或 !!v)会把字符串 "false" 当 true → RED。
    const cfg = parseSiteConfig({ registration_enabled: 'true' as unknown as boolean })
    expect(cfg.registrationEnabled).toBe(false)
  })
  it('password_login_enabled 缺省默认 true,只有显式 false 才隐藏', () => {
    // 判别核心:缺省时不能把唯一的基础登录方式藏掉。变异(改成 ===true)会让缺省时变 false → RED。
    expect(parseSiteConfig({}).passwordLoginEnabled).toBe(true)
    expect(parseSiteConfig({ password_login_enabled: false }).passwordLoginEnabled).toBe(false)
    expect(parseSiteConfig({ password_login_enabled: true }).passwordLoginEnabled).toBe(true)
  })
  it('promoEnabled 行为保持:缺省/true → true,仅显式 false → false(变异 ===true 则缺省变 false → RED)', () => {
    expect(parseSiteConfig({}).promoEnabled).toBe(true)
    expect(parseSiteConfig({ promo_enabled: true }).promoEnabled).toBe(true)
    expect(parseSiteConfig({ promo_enabled: false }).promoEnabled).toBe(false)
  })
  it('captcha_provider 归一小写', () => {
    expect(parseSiteConfig({ captcha_provider: 'TurnStile' }).captchaProvider).toBe('turnstile')
  })
  it('oauth_providers_enabled 解析进 oauthProviders', () => {
    expect(parseSiteConfig({ oauth_providers_enabled: 'github,google' }).oauthProviders).toEqual([
      'github',
      'google',
    ])
  })
  it('null 输入 → 全 fail-closed(回退配置)', () => {
    const cfg = parseSiteConfig(null)
    expect(cfg.registrationEnabled).toBe(false)
    expect(cfg.passkeyEnabled).toBe(false)
    expect(cfg.captchaEnabled).toBe(false)
    expect(cfg.oauthProviders).toEqual([])
    // 但密码登录默认仍可用(基础流程不被回退误伤)。
    expect(cfg.passwordLoginEnabled).toBe(true)
  })
})

describe('FALLBACK_SITE_CONFIG', () => {
  it('回退配置=只剩密码登录,其它增强全关', () => {
    expect(FALLBACK_SITE_CONFIG.passwordLoginEnabled).toBe(true)
    expect(FALLBACK_SITE_CONFIG.registrationEnabled).toBe(false)
    expect(FALLBACK_SITE_CONFIG.passkeyEnabled).toBe(false)
    expect(FALLBACK_SITE_CONFIG.oauthProviders).toEqual([])
  })
})
