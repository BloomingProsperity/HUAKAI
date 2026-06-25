import { describe, expect, it } from 'vitest'
import {
  actionLabel,
  hasMore,
  humanizeAction,
  nextOffset,
  outcomeLabel,
  outcomeTone,
  PAGE_LIMIT,
} from './useractivity'

describe('actionLabel / humanizeAction', () => {
  it('已知动作给中文标签', () => {
    expect(actionLabel('issue_api_key')).toBe('签发 API Key')
    expect(actionLabel('revoke_api_key')).toBe('撤销 API Key')
    expect(actionLabel('force_disable_2fa')).toBe('关闭两步验证')
    expect(actionLabel('login')).toBe('登录')
  })

  it('未知动作兜底人性化:下划线/点转空格 + 首字母大写,不丢信息', () => {
    expect(actionLabel('some_custom_event')).toBe('Some Custom Event')
    expect(actionLabel('hermes.tool.account_pause')).toBe('Hermes Tool Account Pause')
  })

  it('空动作给占位', () => {
    expect(humanizeAction('   ')).toBe('未知动作')
    expect(actionLabel('')).toBe('未知动作')
  })

  it('前后空白被 trim 后再查表', () => {
    expect(actionLabel('  login  ')).toBe('登录')
  })
})

describe('outcomeTone', () => {
  it('后端权威成功值 committed → ok(最常见的真实成功事件,不可漏)', () => {
    // userauditlog/store.go:20 OutcomeCommitted="committed";Key 签发/撤销成功即写此值
    expect(outcomeTone('committed')).toBe('ok')
  })

  it('成功别名类 → ok', () => {
    expect(outcomeTone('success')).toBe('ok')
    expect(outcomeTone('OK')).toBe('ok')
    expect(outcomeTone('allowed')).toBe('ok')
    expect(outcomeTone('granted')).toBe('ok')
  })

  it('后端权威失败值 denied / error → danger', () => {
    // userauditlog/store.go:21-22 OutcomeDenied="denied" / OutcomeError="error"
    expect(outcomeTone('denied')).toBe('danger')
    expect(outcomeTone('error')).toBe('danger')
  })

  it('失败/拒绝/错误别名(含变体子串)→ danger', () => {
    expect(outcomeTone('failure')).toBe('danger')
    expect(outcomeTone('failed')).toBe('danger')
    expect(outcomeTone('ERROR')).toBe('danger')
    expect(outcomeTone('rejected')).toBe('danger')
  })

  it('告警/限流类 → warn', () => {
    expect(outcomeTone('rate_limited')).toBe('warn')
    expect(outcomeTone('throttled')).toBe('warn')
    expect(outcomeTone('warning')).toBe('warn')
  })

  it('空 / 未知 → muted', () => {
    expect(outcomeTone('')).toBe('muted')
    expect(outcomeTone('pending')).toBe('muted')
  })

  it('成功不被失败子串误伤(精确优先)', () => {
    // "success" 不含 fail/deny/error,应为 ok 而非 danger
    expect(outcomeTone('success')).toBe('ok')
  })
})

describe('outcomeLabel', () => {
  it('权威 committed → 成功;denied/error 中文;未知原样,空给占位', () => {
    expect(outcomeLabel('committed')).toBe('成功')
    expect(outcomeLabel('error')).toBe('错误')
    expect(outcomeLabel('DENIED')).toBe('已拒绝')
    expect(outcomeLabel('weird_state')).toBe('weird_state')
    expect(outcomeLabel('  ')).toBe('—')
  })
})

describe('分页推断', () => {
  it('PAGE_LIMIT 为 50(对齐后端默认)', () => {
    expect(PAGE_LIMIT).toBe(50)
  })

  it('hasMore:返回数等于 limit → 可能有更多;少于 limit → 到末页', () => {
    expect(hasMore(50, 50)).toBe(true)
    expect(hasMore(49, 50)).toBe(false)
    expect(hasMore(0, 50)).toBe(false)
  })

  it('hasMore:limit<=0 防御为 false(避免死循环翻页)', () => {
    expect(hasMore(0, 0)).toBe(false)
  })

  it('nextOffset 累加 limit', () => {
    expect(nextOffset(0, 50)).toBe(50)
    expect(nextOffset(50, 50)).toBe(100)
  })
})
