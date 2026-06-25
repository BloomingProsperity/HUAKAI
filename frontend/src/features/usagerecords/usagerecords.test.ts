import { describe, expect, it } from 'vitest'
import {
  formatCost,
  hasMore,
  isSuccess,
  modelDisplay,
  statusLabel,
  statusTone,
  tokensSummary,
  totalTokens,
} from './usagerecords'

describe('statusTone / statusLabel(镜像后端 usageStatus)', () => {
  it('成功类 end_class → ok / 成功', () => {
    expect(statusTone('stream_end_graceful')).toBe('ok')
    expect(statusTone('non_streaming')).toBe('ok')
    expect(statusLabel('stream_end_graceful')).toBe('成功')
    expect(statusLabel('non_streaming')).toBe('成功')
  })
  it('待对账 → warn', () => {
    expect(statusTone('pending_reconciliation')).toBe('warn')
    expect(statusLabel('pending_reconciliation')).toBe('待对账')
  })
  it('错误类 → danger,标签原样保留诊断信息', () => {
    expect(statusTone('upstream_error_5xx')).toBe('danger')
    expect(statusLabel('upstream_error_5xx')).toBe('upstream_error_5xx')
    expect(statusTone('client_abort')).toBe('danger')
  })
  it('空 → muted / 占位', () => {
    expect(statusTone('')).toBe('muted')
    expect(statusLabel('   ')).toBe('—')
  })
  it('成功类不被错误分支误伤(精确集合判定)', () => {
    // 必须靠白名单集合,而非"非 pending 即 danger"
    expect(statusTone('non_streaming')).not.toBe('danger')
  })
})

describe('isSuccess', () => {
  it('仅两个成功 end_class 为真', () => {
    expect(isSuccess('stream_end_graceful')).toBe(true)
    expect(isSuccess('non_streaming')).toBe(true)
    expect(isSuccess('pending_reconciliation')).toBe(false)
    expect(isSuccess('upstream_error_5xx')).toBe(false)
  })
})

describe('formatCost', () => {
  it('定点小数串去尾零,保留 ≥2 位小数观感', () => {
    expect(formatCost('0.01000000')).toBe('$0.01')
    expect(formatCost('0.10000000')).toBe('$0.10')
    expect(formatCost('1.50000000')).toBe('$1.50')
    expect(formatCost('2.00000000')).toBe('$2')
  })
  it('极小额放宽小数位,不塌成 $0.00', () => {
    const out = formatCost('0.00005000')
    expect(out).not.toBe('$0.00')
    expect(out.startsWith('$0.0000')).toBe(true)
  })
  it('非零但低于最小精度 → 阈值形态,不塌成 $0(计费诚实性)', () => {
    // 真实微小扣费(亚微分)不能显示成 $0 误导用户
    expect(formatCost('0.00000001')).toBe('<$0.000001')
    expect(formatCost('0')).toBe('$0') // 真零仍是 $0
  })
  it('0 → $0;非数字原样;空 → 占位', () => {
    expect(formatCost('0')).toBe('$0')
    expect(formatCost('abc')).toBe('abc')
    expect(formatCost('   ')).toBe('—')
  })
})

describe('token 汇总', () => {
  it('totalTokens = 入+出(不含缓存)', () => {
    expect(totalTokens({ input: 10, output: 20 })).toBe(30)
    expect(totalTokens({ input: 0, output: 0, cache_read: 99 })).toBe(0)
  })
  it('tokensSummary 含缓存(若有)', () => {
    expect(tokensSummary({ input: 10, output: 20 })).toBe('入 10 / 出 20')
    expect(tokensSummary({ input: 10, output: 20, cache_creation: 5, cache_read: 3 })).toBe('入 10 / 出 20 / 缓存写 5 / 缓存读 3')
  })
})

describe('modelDisplay', () => {
  it('上游同则只显请求模型,不同则附注箭头', () => {
    expect(modelDisplay({ requested_model: 'claude-x', upstream_model: 'claude-x' })).toBe('claude-x')
    expect(modelDisplay({ requested_model: 'claude-x', upstream_model: 'claude-upstream' })).toBe('claude-x → claude-upstream')
  })
  it('请求模型空时回退上游 / 占位', () => {
    expect(modelDisplay({ requested_model: '', upstream_model: 'up' })).toBe('up')
    expect(modelDisplay({ requested_model: '', upstream_model: '' })).toBe('—')
  })
})

describe('hasMore(游标分页)', () => {
  it('next_cursor 非空 → 还有下一页', () => {
    expect(hasMore('abc123')).toBe(true)
    expect(hasMore('')).toBe(false)
    expect(hasMore('   ')).toBe(false)
  })
})
