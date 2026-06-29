import { describe, expect, it } from 'vitest'
import {
  aggregateMetrics,
  capacityPercent,
  capacityRatio,
  capacityTone,
  enabledLabel,
  enabledTone,
  formatBytes,
  formatTTL,
  hitRatePercent,
  shortKey,
  validateEvictKey,
} from './cachemonitor'
import type { L2MetricsRow } from './types'

describe('formatBytes', () => {
  it('按二进制 1024 分档,各档不同后缀', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(2048)).toBe('2.0 KB')
    // 判别核心:1MB 边界用 MB 而非 KB。变异(把 MB 阈值写成 GB)→ 仍输出 KB → RED。
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB')
    expect(formatBytes(3 * 1024 * 1024 * 1024)).toBe('3.00 GB')
  })

  it('负数/非有限值回退 —', () => {
    // 判别核心:负数不得展示 "-1 B"。变异(去掉 <0 守卫)→ "-1 B" → RED。
    expect(formatBytes(-1)).toBe('—')
    expect(formatBytes(NaN)).toBe('—')
  })
})

describe('formatTTL', () => {
  it('按 时/分/秒 分档', () => {
    expect(formatTTL(7200)).toBe('2 小时')
    expect(formatTTL(120)).toBe('2 分钟')
    expect(formatTTL(45)).toBe('45 秒')
    // 判别核心:59 秒不能进入「分钟」档。变异(把 60 阈值写成 30)→ "1 分钟" → RED。
    expect(formatTTL(59)).toBe('59 秒')
  })

  it('<=0 视为未设置', () => {
    expect(formatTTL(0)).toBe('未设置')
    expect(formatTTL(-5)).toBe('未设置')
  })
})

describe('capacityRatio / capacityPercent', () => {
  it('正常占用比', () => {
    expect(capacityRatio(50, 100)).toBeCloseTo(0.5)
    expect(capacityPercent(50, 100)).toBe('50%')
  })

  it('max<=0 返回 0 而非 Infinity/NaN', () => {
    // 判别核心:除零守卫。变异(去掉 max<=0 守卫)→ Infinity → RED。
    expect(capacityRatio(50, 0)).toBe(0)
    expect(capacityPercent(50, 0)).toBe('0%')
  })

  it('超过上限夹到 1', () => {
    // 判别核心:size>max 不得 >1。变异(去掉夹取)→ 1.5 → RED。
    expect(capacityRatio(150, 100)).toBe(1)
    expect(capacityPercent(150, 100)).toBe('100%')
  })
})

describe('capacityTone', () => {
  it('>=0.9 danger,>=0.7 warn,否则 ok', () => {
    expect(capacityTone(95, 100)).toBe('danger')
    // 判别核心:0.7 边界是 warn 而非 ok。变异(阈值写成 0.71)→ ok → RED。
    expect(capacityTone(70, 100)).toBe('warn')
    expect(capacityTone(30, 100)).toBe('ok')
    // 无上限(ratio=0)落 ok。
    expect(capacityTone(50, 0)).toBe('ok')
  })
})

describe('enabledTone / enabledLabel', () => {
  it('启用 ok/已启用,未启用 muted/未启用', () => {
    expect(enabledTone(true)).toBe('ok')
    expect(enabledTone(false)).toBe('muted')
    expect(enabledLabel(true)).toBe('已启用')
    expect(enabledLabel(false)).toBe('未启用')
  })
})

describe('validateEvictKey', () => {
  it('非空 key 通过且 trim', () => {
    expect(validateEvictKey('  abc123  ')).toEqual({ ok: true, value: 'abc123' })
  })

  it('空/纯空白拒', () => {
    // 判别核心:空 key 必须拒,否则 DELETE 会打到错误路径。变异(去掉空串拒)→ ok → RED。
    expect(validateEvictKey('')).toEqual({ ok: false, error: '请输入要驱逐的缓存 key' })
    expect(validateEvictKey('   ').ok).toBe(false)
  })
})

describe('aggregateMetrics / hitRatePercent', () => {
  const metrics: Record<string, L2MetricsRow> = {
    'anthropic/claude': { hit_total: 30, miss_total: 10, size_bytes: 100 },
    'openai/gpt': { hit_total: 0, miss_total: 10, size_bytes: 50 },
  }

  it('聚合总命中/未命中 + 命中率(分母=hit+miss)', () => {
    const t = aggregateMetrics(metrics)
    expect(t.hit).toBe(30)
    expect(t.miss).toBe(20)
    expect(t.rows).toBe(2)
    // 判别核心:命中率 = 30/(30+20)=0.6,而非 30/请求数 或 仅 hit。
    // 变异(分母写成只用 hit 或只用第一行)→ 数值偏离 → RED。
    expect(t.hitRate).toBeCloseTo(0.6)
    expect(hitRatePercent({ metrics })).toBe('60.0%')
  })

  it('空 metrics → 全 0,命中率展示 —', () => {
    const t = aggregateMetrics({})
    expect(t).toEqual({ hit: 0, miss: 0, hitRate: 0, rows: 0 })
    // 判别核心:0/0 不得为 NaN。变异(去掉 denom>0 守卫)→ NaN → RED。
    expect(t.hitRate).toBe(0)
    expect(hitRatePercent({ metrics: {} })).toBe('—')
  })
})

describe('shortKey', () => {
  it('长 key 缩头16尾6,短 key 原样', () => {
    const long = 'a'.repeat(40)
    expect(shortKey(long)).toBe(`${'a'.repeat(16)}…${'a'.repeat(6)}`)
    // 判别核心:<=24 不缩。变异(阈值写成 10)→ 短 key 被截 → RED。
    expect(shortKey('shortkey-123456')).toBe('shortkey-123456')
    expect(shortKey('')).toBe('—')
  })
})
