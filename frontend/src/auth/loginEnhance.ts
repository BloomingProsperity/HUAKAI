import type { SiteConfig } from './siteConfig'

/*
 * 登录/注册页增强的纯逻辑(可单测,与 React/DOM 解耦):
 *  - 确认密码两次一致校验 + 邀请码必填判定(随 site config);
 *  - "该显示哪些登录/注册方式"的派生(随 site config 门控);
 *  - WebAuthn 选项/凭据的 base64url ↔ ArrayBuffer 转换(passkey 登录需要)。
 * 这些函数不触碰网络/Storage/DOM,便于变异测试。
 */

/* ============================ 表单校验 ============================ */

/**
 * 校验注册表单。返回第一条错误文案(中文),全部通过返回 null。
 * - 邮箱/密码非空;
 * - 两次密码一致(确认密码);
 * - invitation_required 时邀请码必填。
 */
export function validateRegisterForm(input: {
  email: string
  password: string
  confirmPassword: string
  inviteCode: string
  invitationRequired: boolean
}): string | null {
  if (!input.email.trim()) return '请填写邮箱'
  if (!input.password) return '请填写密码'
  // 判别核心:两次密码不一致必须拦截 —— 这是确认密码字段存在的唯一理由。
  if (input.password !== input.confirmPassword) return '两次输入的密码不一致'
  // 判别核心:invitation_required 开启时邀请码不可为空白。
  if (input.invitationRequired && !input.inviteCode.trim()) return '当前站点需要邀请码才能注册'
  return null
}

/* ====================== 邀请码实时预校验(只读提示) ====================== */

/**
 * 邀请码预校验提示的展示态。仅用于注册页失焦时给即时反馈,不参与提交拦截
 * (后端 register 仍是权威)。
 *  - idle:尚未校验(输入框为空 / 站点未开启邀请门 / 未失焦)。
 *  - checking:请求进行中。
 *  - ok:后端判定有效。
 *  - invalid:后端判定无效(附中文原因)。
 *  - unavailable:校验服务暂不可用(网络/后端错误);此态绝不阻断提交。
 */
export type InviteHintStatus = 'idle' | 'checking' | 'ok' | 'invalid' | 'unavailable'

export interface InviteHint {
  status: InviteHintStatus
  /** 面向用户的中文提示文案;idle 态为空串。 */
  message: string
}

/** idle 态常量(输入框为空或无需校验时复位用)。 */
export const INVITE_HINT_IDLE: InviteHint = { status: 'idle', message: '' }

/*
 * 后端 invitevalidatehttp 返回的 reason 取自 userauth.InviteCodeStatus(types.go:97-105):
 *   valid / not_found / disabled / expired / used_or_exhausted。
 * disabled 既表示"该邀请码被停用",也用于"站点未开启邀请门"(此时后端 valid=true)——
 * 故文案选择以 valid 为主、reason 为辅:valid=true 一律视为可用。
 */
const INVITE_INVALID_REASONS: Record<string, string> = {
  not_found: '邀请码不存在',
  expired: '邀请码已过期',
  used_or_exhausted: '邀请码已被使用或已达上限',
  disabled: '邀请码已停用',
}

/**
 * 是否应在失焦时对邀请码发起预校验。判别核心:
 *  - 站点未开启邀请门(invitationRequired=false)时不打扰用户(可选填,无需提示);
 *  - 邀请码为空白时不发请求(空值本就由提交时的必填校验兜底)。
 * 两者任一不满足都返回 false,避免无意义请求与误导性提示。
 */
export function shouldValidateInvite(inviteCode: string, invitationRequired: boolean): boolean {
  return invitationRequired && inviteCode.trim().length > 0
}

/**
 * 把后端 {valid, reason} 映射成展示提示。判别核心:
 *  - valid=true → ok(无论 reason,后端有效即可用);
 *  - valid=false → invalid + 按 reason 取中文原因(未知 reason 给通用兜底文案)。
 * 该函数不决定是否阻断提交 —— 只产出提示文案;阻断与否由调用方策略决定(本页不阻断)。
 */
export function inviteHintFromResult(result: { valid: boolean; reason: string }): InviteHint {
  if (result.valid) {
    return { status: 'ok', message: '邀请码可用' }
  }
  const reason = INVITE_INVALID_REASONS[result.reason.trim()] ?? '邀请码无效'
  return { status: 'invalid', message: reason }
}

/* ====================== 该显示哪些方式(门控) ====================== */

/** 登录/注册页根据 site config 派生出的"显示哪些入口"标志。 */
export interface AuthAffordances {
  /** 是否显示密码登录区(邮箱+密码)。 */
  showPasswordLogin: boolean
  /** 是否显示注册入口(注册 tab / 注册表单)。 */
  showRegister: boolean
  /** 是否显示社交登录按钮行。 */
  showOauth: boolean
  /** 要渲染为 oauth-init 跳转按钮的社交 provider 列表(已排除 telegram —— 它走 Login Widget 不走 oauth-init)。 */
  oauthProviders: string[]
  /** 是否渲染 Telegram Login Widget(运营启用 telegram 且配了公开 bot 用户名才可渲染)。 */
  telegramLogin: boolean
  /** Telegram 公开 bot 用户名(渲染 widget 用);telegramLogin 为 false 时无意义。 */
  telegramBotUsername: string
  /** 是否显示通行密钥登录按钮。 */
  showPasskey: boolean
  /** 是否需要渲染人机验证。 */
  showCaptcha: boolean
}

/**
 * telegram 走 Telegram Login Widget(独立端点 /v1/auth/telegram-login),不走 oauth-init 跳转。
 * 故从 oauth-init 按钮列表里排除,避免渲染一个点了必然报错(后端 oauth-init 不认 telegram)的按钮。
 * telegram 登录入口由专门的 widget 组件提供;且在「先绑定后登录」模型下,需用户先在设置里绑定。
 */
export const TELEGRAM_PROVIDER = 'telegram'

/**
 * 从 SiteConfig 派生页面入口可见性。每项独立门控:
 *  - 密码登录:password_login_enabled(缺省 true,见 siteConfig)。
 *  - 注册入口:registration_enabled 且 password_register_enabled 才显示密码注册。
 *  - 社交:有 provider 列表才显示。
 *  - passkey:passkey_enabled 才显示(浏览器支持性由 UI 层再叠加判定)。
 *  - captcha:captcha_enabled 才渲染。
 */
export function deriveAffordances(cfg: SiteConfig): AuthAffordances {
  // oauth-init 按钮列表排除 telegram(它走 widget,不走 oauth-init,渲染成按钮点了必报错)。
  const oauthInitProviders = cfg.oauthProviders.filter((p) => p !== TELEGRAM_PROVIDER)
  // telegram 登录 widget:运营启用 telegram 且配了公开 bot 用户名才可渲染。
  const telegramLogin = cfg.oauthProviders.includes(TELEGRAM_PROVIDER) && cfg.telegramBotUsername.length > 0
  return {
    showPasswordLogin: cfg.passwordLoginEnabled,
    // 判别核心:注册入口需"总开关 registration_enabled"与"密码注册 password_register_enabled"同时为真。
    showRegister: cfg.registrationEnabled && cfg.passwordRegisterEnabled,
    showOauth: oauthInitProviders.length > 0,
    oauthProviders: oauthInitProviders,
    telegramLogin,
    telegramBotUsername: cfg.telegramBotUsername,
    showPasskey: cfg.passkeyEnabled,
    showCaptcha: cfg.captchaEnabled,
  }
}

/**
 * 是否应在提交前要求 captcha token:只有"启用了 captcha 且我们能渲染(turnstile + site_key)"
 * 才真正要求;启用但 provider 不是我们支持渲染的(或无 site_key)时,不阻断基础登录 ——
 * 把 token 留空交给后端按其策略处理,绝不因前端无法渲染就锁死登录入口。
 */
export function captchaWidgetRenderable(cfg: SiteConfig): boolean {
  // 判别核心:必须 enabled + provider===turnstile + 有 site_key 三者齐备才渲染小组件。
  return cfg.captchaEnabled && cfg.captchaProvider === 'turnstile' && cfg.captchaSiteKey.length > 0
}

/* ====================== 社交 provider 展示名 ====================== */

const PROVIDER_LABELS: Record<string, string> = {
  github: 'GitHub',
  google: 'Google',
  discord: 'Discord',
  wechat: '微信',
  qq: 'QQ',
  dingtalk: '钉钉',
  nodeseek: 'NodeSeek',
  linuxdo: 'LINUX DO',
  oidc: 'OIDC',
  telegram: 'Telegram',
}

/*
 * 后端支持的全部社交 provider(userauth/social_login.go 的 SocialProvider* 常量,共 10 家)。
 * providerLabel 必须为每一家给出友好展示名 —— 这是 parity 守卫(Owner:别人有的我也要有)。
 * 新增/删除后端 provider 时,本表与 PROVIDER_LABELS 必须同步,否则 loginEnhance.test.ts 转红。
 */
export const BACKEND_SOCIAL_PROVIDERS = [
  'google',
  'github',
  'qq',
  'wechat',
  'dingtalk',
  'nodeseek',
  'linuxdo',
  'oidc',
  'discord',
  'telegram',
] as const

/** 社交 provider 的展示名:已知的用友好名,未知的首字母大写兜底。 */
export function providerLabel(provider: string): string {
  const key = provider.trim().toLowerCase()
  if (PROVIDER_LABELS[key]) return PROVIDER_LABELS[key]
  return key ? key.charAt(0).toUpperCase() + key.slice(1) : provider
}

/* ====================== WebAuthn base64url 转换 ====================== */

/** base64url 字符串 → ArrayBuffer。WebAuthn 的 challenge/credential id 都是 base64url。 */
export function base64urlToBuffer(b64url: string): ArrayBuffer {
  // 还原 base64url 到标准 base64:- → +,_ → /,补足 = padding。
  const pad = b64url.length % 4 === 0 ? '' : '='.repeat(4 - (b64url.length % 4))
  const base64 = b64url.replace(/-/g, '+').replace(/_/g, '/') + pad
  const binary = atob(base64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

/** ArrayBuffer → base64url 字符串(无 padding)。finish 时把凭据回传后端。 */
export function bufferToBase64url(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf)
  let binary = ''
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i])
  // 判别核心:输出必须是 base64url(+→-、/→_、去 =),否则后端解码失败。
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/**
 * 把后端下发的 WebAuthn assertion 选项(public_key 字段,JSON,内部 id/challenge 是 base64url 串)
 * 转成 navigator.credentials.get 需要的 PublicKeyCredentialRequestOptions(ArrayBuffer 形态)。
 * 只转换 challenge 与 allowCredentials[].id,其余字段(rpId/userVerification/timeout)原样透传。
 */
export function toPublicKeyRequestOptions(raw: unknown): PublicKeyCredentialRequestOptions {
  // 后端 public_key 可能是 {publicKey:{...}} 包一层(go-webauthn 的 CredentialAssertion 形态),
  // 也可能直接是 options 本体;两种都兼容。
  const root = raw as Record<string, unknown>
  const pk = (root && typeof root === 'object' && 'publicKey' in root ? root.publicKey : root) as Record<
    string,
    unknown
  >
  const options: PublicKeyCredentialRequestOptions = {
    ...(pk as unknown as PublicKeyCredentialRequestOptions),
    challenge: base64urlToBuffer(String(pk.challenge ?? '')),
  }
  const allow = pk.allowCredentials
  if (Array.isArray(allow)) {
    options.allowCredentials = allow.map((c) => {
      const cred = c as Record<string, unknown>
      return {
        ...(cred as unknown as PublicKeyCredentialDescriptor),
        id: base64urlToBuffer(String(cred.id ?? '')),
      }
    })
  }
  return options
}

/**
 * 把 navigator.credentials.get 返回的 PublicKeyCredential 序列化成后端 finish 需要的 JSON
 * (id/rawId/response 各字段转成 base64url 串)。
 */
export function serializeAssertion(cred: PublicKeyCredential): Record<string, unknown> {
  const resp = cred.response as AuthenticatorAssertionResponse
  return {
    id: cred.id,
    rawId: bufferToBase64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bufferToBase64url(resp.clientDataJSON),
      authenticatorData: bufferToBase64url(resp.authenticatorData),
      signature: bufferToBase64url(resp.signature),
      userHandle: resp.userHandle ? bufferToBase64url(resp.userHandle) : null,
    },
  }
}

/** 浏览器是否支持 WebAuthn(passkey 登录前置)。无 navigator.credentials 时禁用 passkey 按钮。 */
export function webAuthnSupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.PublicKeyCredential !== 'undefined' &&
    typeof navigator !== 'undefined' &&
    !!navigator.credentials &&
    typeof navigator.credentials.get === 'function'
  )
}
