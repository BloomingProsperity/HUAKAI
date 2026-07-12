import type {
  BuildInfo,
  EmailSettingItem,
  EmailSettingsResponse,
  EmailSettingsUpdateRequest,
  SmtpTestRequest,
} from './types'

/*
 * 版本与维护纯逻辑(可单测):
 *  - 构建版本字段的友好化展示(后端默认占位 dev/unknown 显示为「本地/开发构建」语义,不误导)。
 *  - commit 短哈希截断。
 *  - SMTP 测试请求构造与校验(收件人必填且为合法邮箱;租户号留空→0)。
 * 纯函数不触网、不读 token、零 console。
 */

/** 后端 buildinfo 的占位值:Version 默认 "dev",Commit/BuildTime 默认 "unknown"。 */
const PLACEHOLDER = new Set(['', 'dev', 'unknown'])

/** 字段是否为占位(未通过 -ldflags 注入真实值)。 */
export function isPlaceholder(value: string): boolean {
  return PLACEHOLDER.has(value.trim().toLowerCase())
}

/**
 * 版本号展示:真实版本原样;占位(dev/空)→「开发构建」。
 * 判别核心:占位与真实版本必须区分,避免把 dev 当正式发布版误导运维。
 */
export function displayVersion(version: string): string {
  return isPlaceholder(version) ? '开发构建' : version.trim()
}

/** commit 展示:真实哈希截前 12 位;占位 → 「未知」。 */
export function displayCommit(commit: string): string {
  const v = commit.trim()
  if (isPlaceholder(v)) return '未知'
  return v.length > 12 ? v.slice(0, 12) : v
}

/** 构建时间展示:能解析的 ISO/RFC3339 转本地时间;占位或不可解析 → 原样/「未知」。 */
export function displayBuildTime(buildTime: string): string {
  const v = buildTime.trim()
  if (isPlaceholder(v)) return '未知'
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? v : d.toLocaleString('zh-CN', { hour12: false })
}

/** 整卡是否「全为占位」——用于提示运维「这是未打标的本地构建」。 */
export function isDevBuild(info: BuildInfo): boolean {
  return isPlaceholder(info.version) && isPlaceholder(info.commit) && isPlaceholder(info.build_time)
}

export interface SmtpTestForm {
  to: string
  tenantId: string
}

export const EMPTY_SMTP_TEST: SmtpTestForm = { to: '', tenantId: '' }

/** 极简邮箱形态校验(有 @ 且两侧非空、含点的域名);仅做客户端预检,真正可达性由后端 SMTP 决定。 */
function looksLikeEmail(s: string): boolean {
  const m = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s)
  return m
}

/**
 * 构造 SMTP 测试请求或返回错误。
 * 判别核心:收件人必填且须像邮箱(挡空/缺@),否则后端必返 400 email_test_recipient_required;
 * 租户号留空视为 0(单租户运营),非法数字报错(避免把 NaN 发给后端)。
 */
export function buildSmtpTest(form: SmtpTestForm): SmtpTestRequest | { error: string } {
  const to = form.to.trim()
  if (!to) return { error: '请填写测试收件邮箱' }
  if (!looksLikeEmail(to)) return { error: '收件邮箱格式不正确' }
  const raw = form.tenantId.trim()
  let tenantId = 0
  if (raw) {
    const n = Number(raw)
    if (!Number.isInteger(n) || n < 0) return { error: '租户号须为非负整数' }
    tenantId = n
  }
  return { tenant_id: tenantId, to }
}

/*
 * ===== SMTP 设置(GET 回填 + PUT 保存)纯逻辑 =====
 * 后端 key 见 backend/internal/email/types.go:13;掩码与省略语义见
 * admin_email_settings_handler.go(maskEmailSettings:223 / adminEmailSettingsValues:184)。
 */

/** 后端 email_settings 表的 setting_key 常量(与 backend/internal/email/types.go 对齐)。 */
export const SMTP_KEY = {
  host: 'smtp_host',
  port: 'smtp_port',
  username: 'smtp_username',
  password: 'smtp_password',
  from: 'smtp_from',
  fromName: 'smtp_from_name',
  useTls: 'smtp_use_tls',
  verify: 'email_verify_enabled',
} as const

/** SMTP 配置表单(全字符串/布尔,贴近输入控件;口令永远不预填,只用占位提示)。 */
export interface SmtpSettingsForm {
  host: string
  port: string
  username: string
  /** 口令输入:留空=保留原口令(不发字段);填值=覆盖。永不从后端回显预填。 */
  password: string
  from: string
  fromName: string
  useTls: boolean
  verifyEmail: boolean
}

export const EMPTY_SMTP_SETTINGS: SmtpSettingsForm = {
  host: '',
  port: '',
  username: '',
  password: '',
  from: '',
  fromName: '',
  useTls: false,
  verifyEmail: false,
}

/** 把掩码设置项数组转成「key→item」便于查表。 */
function indexSettings(items: EmailSettingItem[]): Map<string, EmailSettingItem> {
  const m = new Map<string, EmailSettingItem>()
  for (const it of items) m.set(it.key, it)
  return m
}

/** 后端布尔以字符串 "true"/"false" 存储(strconv.FormatBool),这里宽松解析。 */
function parseStoredBool(v: string | undefined): boolean {
  return (v ?? '').trim().toLowerCase() === 'true'
}

/**
 * 把 GET /settings 响应映射为表单初值。
 * 判别核心:口令字段绝不回显——无论后端是否 configured,form.password 恒为空串(留空=保留);
 * 其余字段原样回填。配套返回 passwordConfigured 供 UI 显示「已配置/未配置」与占位文案。
 */
export function settingsToForm(resp: EmailSettingsResponse): {
  form: SmtpSettingsForm
  passwordConfigured: boolean
} {
  const idx = indexSettings(resp.settings ?? [])
  const get = (k: string) => (idx.get(k)?.value ?? '').trim()
  const pwItem = idx.get(SMTP_KEY.password)
  return {
    form: {
      host: get(SMTP_KEY.host),
      port: get(SMTP_KEY.port),
      username: get(SMTP_KEY.username),
      password: '', // 永不预填口令
      from: get(SMTP_KEY.from),
      fromName: get(SMTP_KEY.fromName),
      useTls: parseStoredBool(idx.get(SMTP_KEY.useTls)?.value),
      verifyEmail: parseStoredBool(idx.get(SMTP_KEY.verify)?.value),
    },
    // 后端掩码:口令项带 configured 布尔;无该项=从未配置。
    passwordConfigured: pwItem?.configured === true,
  }
}

/**
 * 构造 PUT /settings 请求体或返回错误。
 *
 * 关键省略语义(对齐后端 adminEmailSettingsValues:184,避免误清除):
 *  - 文本字段(host/username/from/fromName)留空 → 省略该字段,后端保留原值(留空=不改);
 *  - 端口留空 → 省略 smtp_port;非空须为 1..65535 的整数,否则报错(后端同样校验);
 *  - 口令留空 → 省略 smtp_password,后端保留原口令;填值 → 覆盖。绝不发空串(空串会清除口令)。
 *  - useTls/verifyEmail 是开关,始终随表单当前状态显式下发(它们无「留空保留」语义)。
 * tenant_id 必须为非负整数(平台 admin 须为正,这里只做形态校验,正性由后端按角色判定)。
 */
export function buildEmailSettingsUpdate(
  form: SmtpSettingsForm,
  tenantIdRaw: string,
): EmailSettingsUpdateRequest | { error: string } {
  const raw = tenantIdRaw.trim()
  let tenantId = 0
  if (raw) {
    const n = Number(raw)
    if (!Number.isInteger(n) || n < 0) return { error: '租户号须为非负整数' }
    tenantId = n
  }

  const req: EmailSettingsUpdateRequest = { tenant_id: tenantId }

  const host = form.host.trim()
  if (host) req.smtp_host = host

  const portRaw = form.port.trim()
  if (portRaw) {
    const p = Number(portRaw)
    if (!Number.isInteger(p) || p < 1 || p > 65535) return { error: '端口须为 1–65535 的整数' }
    req.smtp_port = p
  }

  const username = form.username.trim()
  if (username) req.smtp_username = username

  const from = form.from.trim()
  if (from) req.smtp_from = from

  const fromName = form.fromName.trim()
  if (fromName) req.smtp_from_name = fromName

  // 口令:仅在用户输入了非空值时才下发;留空一律省略以保留原口令。
  if (form.password.length > 0) req.smtp_password = form.password

  // 开关始终显式下发当前状态。
  req.smtp_use_tls = form.useTls
  req.email_verify_enabled = form.verifyEmail

  return req
}
