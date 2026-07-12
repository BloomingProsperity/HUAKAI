import type { NotifyPrefsResponse, NotifyPrefsUpdate } from './notifyPrefsTypes'

/*
 * 通知偏好纯逻辑(可单测,无 IO 无 DOM):
 *   - 渠道类型中文标签;
 *   - 表单 → 更新体构造(关键:空 secret/token 不下传,避免覆盖已配置的密钥为空);
 *   - 额外抄送邮箱的「逗号/换行分隔串 ↔ 数组」互转 + 数量/格式前端预校验;
 *   - 必填项校验:选了某渠道就要求对应地址(对齐后端 ValidateSettings 的语义,提前给清晰提示)。
 * secret 安全:本模块绝不把 secret 回填进只读视图;明文只在用户主动输入时进入更新体。
 */

/** 后端支持的通知渠道类型(notify.Type)。 */
export const NOTIFY_TYPES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'none', label: '不通知' },
  { value: 'email', label: '邮件' },
  { value: 'webhook', label: 'Webhook' },
  { value: 'bark', label: 'Bark' },
  { value: 'gotify', label: 'Gotify' },
]

/** 渠道类型 → 中文标签;未知原样回显,空值兜底「不通知」。 */
export function notifyTypeLabel(t: string): string {
  const known = NOTIFY_TYPES.find((x) => x.value === t)?.label
  return known ?? (t || '不通知')
}

/** 抄送邮箱上限(对齐后端 ValidateSettings 的 ≤10 条)。 */
export const MAX_EXTRA_EMAILS = 10

/** 把「逗号/换行/空格分隔」的文本拆成去空去重的邮箱数组。 */
export function parseExtraEmails(text: string): string[] {
  const parts = text
    .split(/[\s,;]+/)
    .map((s) => s.trim())
    .filter((s) => s !== '')
  // 去重(保序)。
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
export function joinExtraEmails(emails: string[] | undefined): string {
  return (emails ?? []).join('\n')
}

/** 极简邮箱形态校验(后端用 mail.ParseAddress 终判;前端只做明显格式拦截)。 */
function looksLikeEmail(s: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s)
}

/**
 * 通知偏好表单。secret/token 用独立字段承载用户「本次新填」的明文。
 * ⚠️ 后端 UpsertSettings 对 webhook_secret/gotify_token 无条件覆盖(notify/store.go:209/213,
 * EXCLUDED.*),故「留空」语义=清除已存密钥(非保留);卡片在已配置且留空时二次确认。
 */
export interface NotifyPrefsForm {
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

/** 把后端只读视图投影成可编辑表单(secret/token 一律留空,绝不回填明文)。 */
export function formFromResponse(r: NotifyPrefsResponse): NotifyPrefsForm {
  return {
    notifyType: r.notify_type || 'none',
    notificationEmail: r.notification_email ?? '',
    webhookURL: r.webhook_url ?? '',
    webhookSecret: '', // 绝不回填:后端只给 *_configured 标志
    barkURL: r.bark_url ?? '',
    gotifyURL: r.gotify_url ?? '',
    gotifyToken: '', // 绝不回填
    gotifyPriority: r.gotify_priority != null ? String(r.gotify_priority) : '',
    balanceThreshold: r.balance_threshold ?? '',
    extraEmailsText: joinExtraEmails(r.extra_emails),
  }
}

/**
 * 构造 PUT 更新体并做前端预校验。成功返回 {body},失败返回 {error}。
 * 判别核心:
 *  ① notify_type=email 必须有 notification_email;webhook 必须有 webhook_url
 *     (变异成不校验 → 提交残缺配置被后端拒/告警发不出 → RED)。
 *  ② 空 webhookSecret/gotifyToken 不写入 body(只在用户本次填了明文才下发;变异成总是写空串则
 *     与未填同样会被后端覆盖,此用例锁的是「不在 body 里凭空塞空串」)。
 *     注:后端对该字段无条件覆盖,故「留空」实际语义=清除,清除前的二次确认在 NotificationPrefsCard。
 *  ③ 抄送邮箱 ≤10 条且每条形似邮箱(变异成不限数量/不校验 → 后端 400 → RED)。
 */
export function buildNotifyUpdate(form: NotifyPrefsForm): { body: NotifyPrefsUpdate } | { error: string } {
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

  const extraEmails = parseExtraEmails(form.extraEmailsText)
  if (extraEmails.length > MAX_EXTRA_EMAILS) {
    return { error: `抄送邮箱最多 ${MAX_EXTRA_EMAILS} 条` }
  }
  const bad = extraEmails.find((e) => !looksLikeEmail(e))
  if (bad) {
    return { error: `抄送邮箱「${bad}」格式不正确` }
  }

  const body: NotifyPrefsUpdate = {
    notify_type: notifyType,
    notification_email: email,
    webhook_url: webhookURL,
    bark_url: form.barkURL.trim(),
    gotify_url: form.gotifyURL.trim(),
    extra_emails: extraEmails,
  }

  // 密钥仅在用户本次输入了明文时才下传。注:后端无条件覆盖,留空=清除(卡片已二次确认)。
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
