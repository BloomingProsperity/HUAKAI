import type { AdminNotifyResponse, AdminNotifyUpdate } from './types'

/*
 * 通知偏好(管理员代管)纯逻辑(可单测,无 IO 无 DOM)。
 * 与用户自助侧(features/profile/notifyPrefs.ts)同构,但承载管理员代管语义:
 *   - 目标租户 tenant_id 校验(platform_admin 必须显式给正整数);
 *   - 渠道类型中文标签;
 *   - 后端只读视图 → 可编辑表单投影(secret/token 绝不回填明文);
 *   - 表单 → PUT 更新体构造 + 必填/格式预校验(对齐后端 ValidateSettings,提前拦无谓 400);
 *   - 空 secret/token 不写入 body(只在管理员本次填了明文才下发)。
 *
 * ⚠️ 密钥覆盖语义(真码 backend/internal/notify/store.go):UpsertSettings 走 INSERT … ON CONFLICT
 * DO UPDATE SET webhook_secret = EXCLUDED.webhook_secret / gotify_token = EXCLUDED.gotify_token(:209/:213),
 * 而 encodeSecret 对空明文返回空串(:287-289)。故对当前激活渠道,留空提交 = 把已存密钥清成空
 * (且 webhook/gotify 渠道空 secret 还会被 ValidateSettings 直接判 400,types.go:178/189)。
 * 因此「已配置但留空」必须二次确认(在 UserNotifyPrefs.tsx),逻辑层这里只负责「不凭空塞空串」。
 */

/** 后端支持的通知渠道类型(notify.Type,types.go:16)。 */
export const ADMIN_NOTIFY_TYPES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'none', label: '不通知' },
  { value: 'email', label: '邮件' },
  { value: 'webhook', label: 'Webhook' },
  { value: 'bark', label: 'Bark' },
  { value: 'gotify', label: 'Gotify' },
]

/** 渠道类型 → 中文标签;未知原样回显,空值兜底「不通知」。 */
export function adminNotifyTypeLabel(t: string): string {
  const known = ADMIN_NOTIFY_TYPES.find((x) => x.value === t)?.label
  return known ?? (t || '不通知')
}

/** 抄送邮箱上限(对齐后端 ValidateSettings 的 ≤10 条,types.go:156)。 */
export const MAX_ADMIN_EXTRA_EMAILS = 10

/** 把「逗号/分号/空白分隔」的文本拆成去空去重的邮箱数组(保序)。 */
export function parseAdminExtraEmails(text: string): string[] {
  const parts = text
    .split(/[\s,;]+/)
    .map((s) => s.trim())
    .filter((s) => s !== '')
  const seen = new Set<string>()
  const out: string[] = []
  for (const p of parts) {
    if (!seen.has(p)) {
      seen.add(p)
      out.push(p)
    }
  }
  return out
}

/** 数组 → 每行一个邮箱的文本(供 textarea 回填)。 */
export function joinAdminExtraEmails(emails: string[] | undefined): string {
  return (emails ?? []).join('\n')
}

/** 极简邮箱形态校验(后端用 mail.ParseAddress 终判,types.go:163;前端只做明显格式拦截)。 */
function looksLikeEmail(s: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s)
}

/**
 * 校验目标租户 ID(代管特有):platform_admin 必须显式给正整数(否则后端 400 tenant_id_required,
 * notify_handler.go:209-212)。返回归一化后的正整数,或错误文案。
 * 判别核心:空/非数字/<=0/非整数一律拒(变异:放行 0 或非整数 → RED)。
 */
export function validateTenantId(raw: string): { ok: true; tenantId: number } | { ok: false; error: string } {
  const v = raw.trim()
  if (v === '') return { ok: false, error: '请填写目标租户 ID(tenant_id)' }
  if (!/^[0-9]+$/.test(v)) return { ok: false, error: '租户 ID 必须是正整数' }
  const n = Number(v)
  if (!Number.isInteger(n) || n <= 0) return { ok: false, error: '租户 ID 必须为正整数' }
  return { ok: true, tenantId: n }
}

/**
 * 代管表单形态。secret/token 用独立字段承载管理员「本次新填」的明文;绝不回填后端已存明文。
 */
export interface AdminNotifyForm {
  notifyType: string
  notificationEmail: string
  webhookURL: string
  webhookSecret: string
  barkURL: string
  gotifyURL: string
  gotifyToken: string
  gotifyPriority: string
  balanceThreshold: string
  extraEmailsText: string
}

/** 空表单(全部留空,按需覆盖)。 */
export function emptyAdminNotifyForm(): AdminNotifyForm {
  return {
    notifyType: 'none',
    notificationEmail: '',
    webhookURL: '',
    webhookSecret: '',
    barkURL: '',
    gotifyURL: '',
    gotifyToken: '',
    gotifyPriority: '',
    balanceThreshold: '',
    extraEmailsText: '',
  }
}

/**
 * 后端只读视图 → 可编辑表单投影。secret/token 一律留空,绝不回填明文(后端只给 *_configured 标志)。
 * 判别核心:即便后端标记已配置,表单密钥位也必须为空(变异成回填占位/明文 → RED)。
 */
export function adminFormFromResponse(r: AdminNotifyResponse): AdminNotifyForm {
  return {
    notifyType: r.notify_type || 'none',
    notificationEmail: r.notification_email ?? '',
    webhookURL: r.webhook_url ?? '',
    webhookSecret: '', // 绝不回填
    barkURL: r.bark_url ?? '',
    gotifyURL: r.gotify_url ?? '',
    gotifyToken: '', // 绝不回填
    gotifyPriority: r.gotify_priority != null ? String(r.gotify_priority) : '',
    balanceThreshold: r.balance_threshold ?? '',
    extraEmailsText: joinAdminExtraEmails(r.extra_emails),
  }
}

/**
 * 构造 PUT 更新体并做前端预校验。成功返回 {body},失败返回 {error}。
 * 判别核心:
 *  ① 选了某渠道就要求对应地址(email→notification_email、webhook→webhook_url、bark→bark_url、
 *     gotify→gotify_url),对齐后端 ValidateSettings(types.go:170-200);变异成不校验 → 残缺配置被后端拒 → RED。
 *  ② 空 webhookSecret/gotifyToken 不写入 body(只在本次填了明文才下发);变异成总写空串 → 静默覆盖密钥 → RED。
 *  ③ 抄送邮箱 ≤10 条且每条形似邮箱;变异成不限数量/不校验 → 后端 400 → RED。
 *  ④ gotify_priority 必须整数;balance_threshold 必须非负数字。
 */
export function buildAdminNotifyUpdate(form: AdminNotifyForm): { body: AdminNotifyUpdate } | { error: string } {
  const notifyType = form.notifyType.trim() || 'none'
  const email = form.notificationEmail.trim()
  const webhookURL = form.webhookURL.trim()

  if (notifyType === 'email' && !email) {
    return { error: '选择「邮件」渠道时必须填写通知邮箱' }
  }
  if (notifyType === 'webhook' && !webhookURL) {
    return { error: '选择「Webhook」渠道时必须填写 Webhook URL' }
  }
  if (notifyType === 'bark' && !form.barkURL.trim()) {
    return { error: '选择「Bark」渠道时必须填写 Bark URL' }
  }
  if (notifyType === 'gotify' && !form.gotifyURL.trim()) {
    return { error: '选择「Gotify」渠道时必须填写 Gotify URL' }
  }

  const extraEmails = parseAdminExtraEmails(form.extraEmailsText)
  if (extraEmails.length > MAX_ADMIN_EXTRA_EMAILS) {
    return { error: `抄送邮箱最多 ${MAX_ADMIN_EXTRA_EMAILS} 条` }
  }
  const bad = extraEmails.find((e) => !looksLikeEmail(e))
  if (bad) {
    return { error: `抄送邮箱「${bad}」格式不正确` }
  }

  const body: AdminNotifyUpdate = {
    notify_type: notifyType,
    notification_email: email,
    webhook_url: webhookURL,
    bark_url: form.barkURL.trim(),
    gotify_url: form.gotifyURL.trim(),
    extra_emails: extraEmails,
  }

  // 密钥仅在本次输入了明文时才下传(留空=后端清除,由卡片二次确认)。
  if (form.webhookSecret.trim() !== '') {
    body.webhook_secret = form.webhookSecret.trim()
  }
  if (form.gotifyToken.trim() !== '') {
    body.gotify_token = form.gotifyToken.trim()
  }

  // gotify 优先级:填了才下传(后端缺省回落 5)。
  const pr = form.gotifyPriority.trim()
  if (pr !== '') {
    const n = Number(pr)
    if (!Number.isInteger(n)) {
      return { error: 'Gotify 优先级必须是整数' }
    }
    body.gotify_priority = n
  }

  // 低余额阈值:填了才下传(后端缺省回落 DefaultLowBalanceThreshold)。
  const bt = form.balanceThreshold.trim()
  if (bt !== '') {
    if (!/^\d+(\.\d+)?$/.test(bt)) {
      return { error: '低余额阈值必须是非负数字' }
    }
    body.balance_threshold = bt
  }

  return { body }
}

/**
 * 计算「已配置但本次留空 → 会被清除」的密钥集合(代管二次确认依据)。
 * 判别核心:仅当后端标记已配置(*_configured=true)且本次表单对应密钥位为空时,才纳入清除集合。
 * 变异(忽略 configured 标志或忽略留空判断)→ 误报/漏报清除 → RED。
 */
export function adminClearingSecrets(
  prev: AdminNotifyResponse | null,
  form: AdminNotifyForm,
): string[] {
  const out: string[] = []
  if (prev?.webhook_secret_configured && form.webhookSecret.trim() === '') {
    out.push('Webhook 密钥')
  }
  if (prev?.gotify_token_configured && form.gotifyToken.trim() === '') {
    out.push('Gotify Token')
  }
  return out
}
