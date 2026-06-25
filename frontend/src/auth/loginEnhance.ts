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

/* ====================== 该显示哪些方式(门控) ====================== */

/** 登录/注册页根据 site config 派生出的"显示哪些入口"标志。 */
export interface AuthAffordances {
  /** 是否显示密码登录区(邮箱+密码)。 */
  showPasswordLogin: boolean
  /** 是否显示注册入口(注册 tab / 注册表单)。 */
  showRegister: boolean
  /** 是否显示社交登录按钮行。 */
  showOauth: boolean
  /** 要渲染的社交 provider 列表。 */
  oauthProviders: string[]
  /** 是否显示通行密钥登录按钮。 */
  showPasskey: boolean
  /** 是否需要渲染人机验证。 */
  showCaptcha: boolean
}

/**
 * 从 SiteConfig 派生页面入口可见性。每项独立门控:
 *  - 密码登录:password_login_enabled(缺省 true,见 siteConfig)。
 *  - 注册入口:registration_enabled 且 password_register_enabled 才显示密码注册。
 *  - 社交:有 provider 列表才显示。
 *  - passkey:passkey_enabled 才显示(浏览器支持性由 UI 层再叠加判定)。
 *  - captcha:captcha_enabled 才渲染。
 */
export function deriveAffordances(cfg: SiteConfig): AuthAffordances {
  return {
    showPasswordLogin: cfg.passwordLoginEnabled,
    // 判别核心:注册入口需"总开关 registration_enabled"与"密码注册 password_register_enabled"同时为真。
    showRegister: cfg.registrationEnabled && cfg.passwordRegisterEnabled,
    showOauth: cfg.oauthProviders.length > 0,
    oauthProviders: cfg.oauthProviders,
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
