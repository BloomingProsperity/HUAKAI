import { describe, expect, it } from 'vitest'
import {
  buildNotifyUpdate,
  formFromResponse,
  joinExtraEmails,
  notifyTypeLabel,
  parseExtraEmails,
  type NotifyPrefsForm,
} from './notifyPrefs'
import type { NotifyPrefsResponse } from './notifyPrefsTypes'

/** 构造一个基础表单(全部留空,按需覆盖)。 */
function emptyForm(over: Partial<NotifyPrefsForm> = {}): NotifyPrefsForm {
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
    ...over,
  }
}

describe('notifyTypeLabel', () => {
  it('已知类型译中文,未知原样', () => {
    expect(notifyTypeLabel('email')).toBe('邮件')
    expect(notifyTypeLabel('webhook')).toBe('Webhook')
    expect(notifyTypeLabel('zzz')).toBe('zzz')
  })
})

describe('parseExtraEmails / joinExtraEmails', () => {
  it('逗号/换行/空格分隔 → 去空去重数组', () => {
    expect(parseExtraEmails('a@x.com, b@x.com\nb@x.com  c@x.com')).toEqual(['a@x.com', 'b@x.com', 'c@x.com'])
  })
  it('数组 → 每行一个', () => {
    expect(joinExtraEmails(['a@x.com', 'b@x.com'])).toBe('a@x.com\nb@x.com')
  })
})

describe('formFromResponse(secret 绝不回填)', () => {
  it('webhook_secret / gotify_token 一律留空,只读标志不影响表单密钥位', () => {
    const r: NotifyPrefsResponse = {
      tenant_id: 1,
      user_id: 2,
      notify_type: 'webhook',
      webhook_url: 'https://h',
      webhook_secret_configured: true,
      gotify_token_configured: true,
      balance_threshold: '5',
      extra_emails: ['cc@x.com'],
    }
    const form = formFromResponse(r)
    // 判别核心:即便后端标记已配置,表单密钥位也必须为空(变异成回填占位/明文 → RED)。
    expect(form.webhookSecret).toBe('')
    expect(form.gotifyToken).toBe('')
    expect(form.webhookURL).toBe('https://h')
    expect(form.extraEmailsText).toBe('cc@x.com')
  })
})

describe('buildNotifyUpdate', () => {
  it('email 渠道缺邮箱 → 报错', () => {
    // 判别核心:必填校验。变异(不校验)→ 残缺配置提交 → RED。
    const r = buildNotifyUpdate(emptyForm({ notifyType: 'email' }))
    expect('error' in r && r.error).toContain('通知邮箱')
  })
  it('webhook 渠道缺 URL → 报错', () => {
    const r = buildNotifyUpdate(emptyForm({ notifyType: 'webhook' }))
    expect('error' in r && r.error).toContain('Webhook URL')
  })
  it('空 secret/token 不进 body(不覆盖已配置密钥)', () => {
    const r = buildNotifyUpdate(emptyForm({ notifyType: 'webhook', webhookURL: 'https://h' }))
    expect('body' in r).toBe(true)
    if ('body' in r) {
      // 判别核心:留空时 body 不含 webhook_secret(变异成总是写空串 → 覆盖密钥为空 → RED)。
      expect(r.body.webhook_secret).toBeUndefined()
      expect(r.body.webhook_url).toBe('https://h')
    }
  })
  it('填了 secret 才进 body', () => {
    const r = buildNotifyUpdate(emptyForm({ notifyType: 'webhook', webhookURL: 'https://h', webhookSecret: 's3cr3t' }))
    expect('body' in r && r.body.webhook_secret).toBe('s3cr3t')
  })
  it('抄送邮箱超 10 条 → 报错', () => {
    const many = Array.from({ length: 11 }, (_, i) => `a${i}@x.com`).join('\n')
    const r = buildNotifyUpdate(emptyForm({ notifyType: 'none', extraEmailsText: many }))
    // 判别核心:>10 必须拦(变异成不限 → 后端 400 → RED)。
    expect('error' in r && r.error).toContain('10')
  })
  it('抄送邮箱格式错 → 报错', () => {
    const r = buildNotifyUpdate(emptyForm({ notifyType: 'none', extraEmailsText: 'not-an-email' }))
    expect('error' in r && r.error).toContain('格式不正确')
  })
  it('gotify 优先级非整数 → 报错', () => {
    const r = buildNotifyUpdate(emptyForm({ notifyType: 'gotify', gotifyURL: 'https://g', gotifyPriority: '1.5' }))
    expect('error' in r && r.error).toContain('整数')
  })
  it('阈值非数字 → 报错', () => {
    const r = buildNotifyUpdate(emptyForm({ notifyType: 'none', balanceThreshold: 'abc' }))
    expect('error' in r && r.error).toContain('阈值')
  })
})
