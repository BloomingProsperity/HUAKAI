import { apiGet } from '../lib/api'

/*
 * 站点公开配置(匿名进站引导)。对应后端 GET /v1/site/config(sitepublichttp/handler.go):
 * 无鉴权、无请求体,响应把布尔/字符串旋钮平铺在顶层(注意:不是包在 {config:{...}} 里,
 * 而是 {tenant_id, registration_enabled, ...} 直接平铺 —— 已核源码 handler.go 把字段写进 out map 顶层)。
 *
 * 本模块只做两件事:
 *  1) 拉取原始响应(fetchSiteConfig);任何失败由调用方静默回退到"只显邮箱密码登录"。
 *  2) 把原始响应解析成 UI 直接可用的 enabled 标志(parseSiteConfig,纯逻辑,可单测)。
 *
 * 安全:这是匿名公开端点,返回的都是公开旋钮(注册开关、公开 captcha site_key、passkey
 * relying-party 提示、oauth provider 列表、品牌串),不含任何密钥;前端原样消费即可。
 */

/** 后端 /v1/site/config 的原始响应(平铺字段;全部可缺省,做防御性解析)。 */
export interface RawSiteConfig {
  tenant_id?: number
  registration_enabled?: boolean
  invitation_required?: boolean
  password_register_enabled?: boolean
  password_login_enabled?: boolean
  captcha_enabled?: boolean
  two_factor_enabled?: boolean
  passkey_enabled?: boolean
  promo_enabled?: boolean
  captcha_provider?: string
  captcha_site_key?: string
  passkey_rp_id?: string
  passkey_rp_display_name?: string
  /** 逗号分隔字符串或字符串数组,二者都接受。 */
  oauth_providers_enabled?: string | string[]
  /** Telegram Login Widget 渲染所需的公开 bot 用户名(t.me/<name>);非密钥。空=未配置。 */
  telegram_bot_username?: string
  site_name?: string
  site_logo?: string
  site_footer?: string
  site_doc_url?: string
}

/** UI 直接消费的站点配置标志(已把原始响应规整成确定形态)。 */
export interface SiteConfig {
  tenantId: number | null
  /** 是否允许注册(总开关)。 */
  registrationEnabled: boolean
  /** 注册是否必须邀请码。 */
  invitationRequired: boolean
  /** 是否允许密码注册(注册入口显隐的细分开关)。 */
  passwordRegisterEnabled: boolean
  /** 是否允许密码登录;false 时隐藏密码登录区,只留社交/passkey。 */
  passwordLoginEnabled: boolean
  /** 是否启用人机验证。 */
  captchaEnabled: boolean
  /** captcha 提供方(如 turnstile);小写归一。 */
  captchaProvider: string
  /** captcha 公开 site key(渲染小组件用)。 */
  captchaSiteKey: string
  /** 是否启用通行密钥登录。 */
  passkeyEnabled: boolean
  /** 兑换码(promo)总开关。**行为保持**:缺省/未下发视为开启,仅运营者显式 false 才关闭兑换入口
   *  ——与后端 redeem 门控语义一致(Owner A 方案),故不用 fail-closed 的 asBool。 */
  promoEnabled: boolean
  /** 启用的社交登录 provider 列表(已去空白/去重/小写归一)。 */
  oauthProviders: string[]
  /** Telegram Login Widget 的公开 bot 用户名(t.me/<name>);空=未配置,不渲染 telegram 绑定/登录入口。 */
  telegramBotUsername: string
  /** 品牌/法律链接(用于条款、文档入口);缺省为空串。 */
  siteName: string
  siteDocUrl: string
}

/**
 * 把布尔旋钮安全解析为 bool:严格只认 JSON 布尔 true(后端已把存储串 "true"/"false"
 * 解析成布尔再下发);其余(undefined/false/字符串)一律 false —— fail-closed,
 * 避免把"未知"误当"开启"而暴露未配置的入口。
 */
function asBool(v: boolean | undefined): boolean {
  return v === true
}

/** 字符串旋钮归一:非字符串 → 空串;去首尾空白。 */
function asStr(v: string | undefined): string {
  return typeof v === 'string' ? v.trim() : ''
}

/**
 * 解析 oauth_providers_enabled:接受逗号分隔字符串或字符串数组两种形态(后端字符串旋钮,
 * 但调用方/未来可能下发数组,做双形态兼容)。规整为去空白、去空项、小写、去重的有序列表。
 * 解析失败/缺省 → 空数组(不渲染任何社交按钮)。
 */
export function parseOauthProviders(raw: string | string[] | undefined): string[] {
  if (raw == null) return []
  let parts: string[]
  if (Array.isArray(raw)) {
    parts = raw
  } else {
    const s = String(raw).trim()
    // 兼容 JSON 数组字符串(如 '["github"]';settings 允许该形态、site/config 原样下发);
    // 否则整串会被当成一个 provider 名。解析失败/非数组 → 回退逗号分隔。
    if (s.startsWith('[')) {
      try {
        const arr: unknown = JSON.parse(s)
        parts = Array.isArray(arr) ? arr.map((x) => String(x)) : s.split(',')
      } catch {
        parts = s.split(',')
      }
    } else {
      parts = s.split(',')
    }
  }
  const seen = new Set<string>()
  const out: string[] = []
  for (const p of parts) {
    const name = String(p).trim().toLowerCase()
    if (!name || seen.has(name)) continue
    seen.add(name)
    out.push(name)
  }
  return out
}

/**
 * 把后端原始响应规整成 UI 用的 SiteConfig。纯逻辑:无副作用、可单测。
 * null/undefined 输入(加载失败)→ 全 fail-closed 的最小配置(只剩邮箱密码登录可用)。
 */
export function parseSiteConfig(raw: RawSiteConfig | null | undefined): SiteConfig {
  const r = raw ?? {}
  return {
    tenantId: typeof r.tenant_id === 'number' ? r.tenant_id : null,
    registrationEnabled: asBool(r.registration_enabled),
    invitationRequired: asBool(r.invitation_required),
    passwordRegisterEnabled: asBool(r.password_register_enabled),
    // 关键:password_login_enabled 缺省时默认 true —— 后端没下发该旋钮(老部署/加载部分失败)
    // 不应把唯一的基础登录方式藏掉。只有后端显式 false 才隐藏密码登录区。
    passwordLoginEnabled: r.password_login_enabled !== false,
    captchaEnabled: asBool(r.captcha_enabled),
    captchaProvider: asStr(r.captcha_provider).toLowerCase(),
    captchaSiteKey: asStr(r.captcha_site_key),
    passkeyEnabled: asBool(r.passkey_enabled),
    // promo 行为保持:仅显式 false 才关闭(同 passwordLoginEnabled 思路),与后端门控一致。
    promoEnabled: r.promo_enabled !== false,
    oauthProviders: parseOauthProviders(r.oauth_providers_enabled),
    telegramBotUsername: asStr(r.telegram_bot_username),
    siteName: asStr(r.site_name),
    siteDocUrl: asStr(r.site_doc_url),
  }
}

/** 加载失败/未配置时的回退配置:只允许邮箱密码登录,其它增强一律不显示。 */
export const FALLBACK_SITE_CONFIG: SiteConfig = parseSiteConfig(null)

/**
 * 拉取站点公开配置。失败时由调用方决定回退;这里不吞错误,让调用方能区分"成功但全关"
 * 与"加载失败"。注意 /v1/site/config 是匿名端点,apiGet 不会带 token(tokenForPath 对非
 * /v1/auth/admin 路径返回 session token,但此端点无鉴权中间件,带不带都不影响)。
 */
export async function fetchSiteConfig(): Promise<SiteConfig> {
  const raw = await apiGet<RawSiteConfig>('/v1/site/config')
  return parseSiteConfig(raw)
}
