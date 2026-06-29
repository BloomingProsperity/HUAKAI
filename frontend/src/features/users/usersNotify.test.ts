import { describe, expect, it } from 'vitest'
import {
  adminClearingSecrets,
  adminFormFromResponse,
  adminNotifyTypeLabel,
  buildAdminNotifyUpdate,
  emptyAdminNotifyForm,
  joinAdminExtraEmails,
  parseAdminExtraEmails,
  validateTenantId,
  type AdminNotifyForm,
} from './usersNotify'
import type { AdminNotifyResponse } from './types'

/** 构造一个基础表单(全部留空,按需覆盖)。 */
function form(over: Partial<AdminNotifyForm> = {}): AdminNotifyForm {
  return { ...emptyAdminNotifyForm(), ...over }
}

describe('adminNotifyTypeLabel', () => {
  it('已知类型译中文,未知原样,空兜底', () => {
    expect(adminNotifyTypeLabel('email')).toBe('邮件')
    expect(adminNotifyTypeLabel('webhook')).toBe('Webhook')
    expect(adminNotifyTypeLabel('zzz')).toBe('zzz')
    expect(adminNotifyTypeLabel('')).toBe('不通知')
  })
})

describe('validateTenantId', () => {
  it('放行正整数', () => {
    expect(validateTenantId('1')).toEqual({ ok: true, tenantId: 1 })
    expect(validateTenantId('  42 ')).toEqual({ ok: true, tenantId: 42 })
  })
  it('拒空/非数字/0/负/小数(变异:放行 0 或非整数即 RED)', () => {
    // 判别核心:platform_admin 缺 tenant_id 后端 400(notify_handler.go:209-212);0/负为非法目标。
    expect(validateTenantId('').ok).toBe(false)
    expect(validateTenantId('abc').ok).toBe(false)
    expect(validateTenantId('0').ok).toBe(false)
    expect(validateTenantId('-1').ok).toBe(false)
    expect(validateTenantId('1.5').ok).toBe(false)
  })
})

describe('parseAdminExtraEmails / joinAdminExtraEmails', () => {
  it('逗号/分号/换行/空格分隔 → 去空去重数组', () => {
    expect(parseAdminExtraEmails('a@x.com, b@x.com;b@x.com\n c@x.com')).toEqual(['a@x.com', 'b@x.com', 'c@x.com'])
  })
  it('数组 → 每行一个', () => {
    expect(joinAdminExtraEmails(['a@x.com', 'b@x.com'])).toBe('a@x.com\nb@x.com')
    expect(joinAdminExtraEmails(undefined)).toBe('')
  })
})

describe('adminFormFromResponse(secret 绝不回填)', () => {
  it('已配置标志不污染表单密钥位,其它字段如实回填', () => {
    const r: AdminNotifyResponse = {
      tenant_id: 3,
      user_id: 7,
      notify_type: 'webhook',
      webhook_url: 'https://h',
      webhook_secret_configured: true,
      gotify_token_configured: true,
      balance_threshold: '5',
      extra_emails: ['cc@x.com'],
    }
    const f = adminFormFromResponse(r)
    // 判别核心:即便后端标记已配置,表单密钥位必须为空(变异成回填占位/明文 → RED)。
    expect(f.webhookSecret).toBe('')
    expect(f.gotifyToken).toBe('')
    expect(f.webhookURL).toBe('https://h')
    expect(f.notifyType).toBe('webhook')
    expect(f.extraEmailsText).toBe('cc@x.com')
  })
})

describe('buildAdminNotifyUpdate', () => {
  it('email 渠道缺邮箱 → 报错', () => {
    const r = buildAdminNotifyUpdate(form({ notifyType: 'email' }))
    expect('error' in r && r.error).toContain('通知邮箱')
  })
  it('webhook 渠道缺 URL → 报错', () => {
    const r = buildAdminNotifyUpdate(form({ notifyType: 'webhook' }))
    expect('error' in r && r.error).toContain('Webhook URL')
  })
  it('bark / gotify 渠道缺 URL → 报错', () => {
    expect('error' in buildAdminNotifyUpdate(form({ notifyType: 'bark' }))).toBe(true)
    expect('error' in buildAdminNotifyUpdate(form({ notifyType: 'gotify' }))).toBe(true)
  })
  it('空 secret/token 不进 body(不覆盖已配置密钥)', () => {
    const r = buildAdminNotifyUpdate(form({ notifyType: 'webhook', webhookURL: 'https://h' }))
    expect('body' in r).toBe(true)
    if ('body' in r) {
      // 判别核心:留空时 body 不含 webhook_secret(变异成总写空串 → 覆盖密钥为空 → RED)。
      expect(r.body.webhook_secret).toBeUndefined()
      expect(r.body.webhook_url).toBe('https://h')
    }
  })
  it('填了 secret 才进 body', () => {
    const r = buildAdminNotifyUpdate(form({ notifyType: 'webhook', webhookURL: 'https://h', webhookSecret: 's3cr3t' }))
    expect('body' in r && r.body.webhook_secret).toBe('s3cr3t')
  })
  it('抄送邮箱超 10 条 → 报错(变异:不限数量即 RED)', () => {
    const many = Array.from({ length: 11 }, (_, i) => `a${i}@x.com`).join('\n')
    const r = buildAdminNotifyUpdate(form({ notifyType: 'none', extraEmailsText: many }))
    expect('error' in r && r.error).toContain('10')
  })
  it('抄送邮箱格式错 → 报错', () => {
    const r = buildAdminNotifyUpdate(form({ notifyType: 'none', extraEmailsText: 'not-an-email' }))
    expect('error' in r && r.error).toContain('格式不正确')
  })
  it('gotify 优先级非整数 → 报错', () => {
    const r = buildAdminNotifyUpdate(form({ notifyType: 'gotify', gotifyURL: 'https://g', gotifyPriority: '1.5' }))
    expect('error' in r && r.error).toContain('整数')
  })
  it('阈值非数字 → 报错', () => {
    const r = buildAdminNotifyUpdate(form({ notifyType: 'none', balanceThreshold: 'abc' }))
    expect('error' in r && r.error).toContain('阈值')
  })
})

describe('adminClearingSecrets(已配置+留空=会被清除)', () => {
  const configured: AdminNotifyResponse = {
    tenant_id: 1,
    user_id: 1,
    notify_type: 'webhook',
    webhook_secret_configured: true,
    gotify_token_configured: true,
    balance_threshold: '5',
  }
  it('已配置且本次留空 → 列入清除集合', () => {
    // 判别核心:命中两个已配置密钥且都留空,二次确认才会触发(变异:忽略 configured → 漏报 → RED)。
    expect(adminClearingSecrets(configured, form())).toEqual(['Webhook 密钥', 'Gotify Token'])
  })
  it('本次填了明文 → 不算清除', () => {
    expect(adminClearingSecrets(configured, form({ webhookSecret: 'x', gotifyToken: 'y' }))).toEqual([])
  })
  it('后端未标记已配置 → 即便留空也不算清除(无密钥可清)', () => {
    // 判别核心:无已存密钥时留空不是「清除」(变异:忽略留空判断/忽略 configured → 误报 → RED)。
    expect(adminClearingSecrets({ ...configured, webhook_secret_configured: false, gotify_token_configured: false }, form())).toEqual([])
    expect(adminClearingSecrets(null, form())).toEqual([])
  })
})
