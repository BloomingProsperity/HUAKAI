import { describe, expect, it } from 'vitest'
import {
  buildConfigQuery,
  buildLogQuery,
  configToForm,
  decisionLabel,
  decisionTone,
  formatFee,
  validateConfig,
} from './moderation'
import { EMPTY_LOG_FILTERS, type ModerationConfig } from './types'

describe('buildLogQuery', () => {
  it('tenant_id/limit/offset 必带,api_key_id 空则省略', () => {
    const q = buildLogQuery(7, EMPTY_LOG_FILTERS, 50, 100)
    expect(q).toEqual({ tenant_id: 7, limit: 50, offset: 100 })
    // 判别核心:空 api_key_id 不得出现在 query。变异(无条件赋值)→ 含 api_key_id→RED。
    expect('api_key_id' in q).toBe(false)
  })

  it('合法正整数 api_key_id 才下发', () => {
    expect(buildLogQuery(7, { apiKeyId: '42' }, 50, 0).api_key_id).toBe('42')
    // 判别核心:0 / 负数 / 非数字一律省略(正则守卫)。
    expect('api_key_id' in buildLogQuery(7, { apiKeyId: '0' }, 50, 0)).toBe(false)
    expect('api_key_id' in buildLogQuery(7, { apiKeyId: 'abc' }, 50, 0)).toBe(false)
    expect('api_key_id' in buildLogQuery(7, { apiKeyId: ' ' }, 50, 0)).toBe(false)
  })
})

describe('buildConfigQuery', () => {
  it('只带 tenant_id', () => {
    expect(buildConfigQuery(9)).toEqual({ tenant_id: 9 })
  })
})

describe('decisionTone', () => {
  it('pass→ok,block_*→danger,fee_charged→warn,未知→muted', () => {
    expect(decisionTone('pass')).toBe('ok')
    expect(decisionTone('block_keyword')).toBe('danger')
    expect(decisionTone('block_hash')).toBe('danger')
    expect(decisionTone('block_external')).toBe('danger')
    expect(decisionTone('block_backend')).toBe('danger')
    // 判别核心:fee_charged 是放行但计费,必须 warn(不可与 pass 同级)。
    expect(decisionTone('fee_charged')).toBe('warn')
    expect(decisionTone('something')).toBe('muted')
  })
})

describe('decisionLabel', () => {
  it('给出中文标签,未知回退原值', () => {
    expect(decisionLabel('pass')).toBe('通过')
    expect(decisionLabel('block_keyword')).toBe('关键词拦截')
    expect(decisionLabel('fee_charged')).toBe('已计违规费')
    expect(decisionLabel('weird')).toBe('weird')
    expect(decisionLabel('')).toBe('—')
  })
})

describe('validateConfig', () => {
  const base = {
    enabled: true,
    failClosed: true,
    sampleRatePct: 100,
    banThreshold: 3,
    banWindowSeconds: 3600,
    violationFeeUsd: '0',
  }

  it('合法配置 → ok 且回带请求体', () => {
    const r = validateConfig(7, base)
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value).toEqual({
        tenant_id: 7,
        enabled: true,
        fail_closed: true,
        sample_rate_pct: 100,
        ban_threshold: 3,
        ban_window_seconds: 3600,
        violation_fee_usd: '0',
      })
    }
  })

  it('tenant_id 非正 → 报错', () => {
    expect(validateConfig(0, base).ok).toBe(false)
    expect(validateConfig(-1, base).ok).toBe(false)
  })

  it('采样率越界 → 报错(边界 0/100 合法,101/-1 非法)', () => {
    expect(validateConfig(7, { ...base, sampleRatePct: 0 }).ok).toBe(true)
    expect(validateConfig(7, { ...base, sampleRatePct: 100 }).ok).toBe(true)
    // 判别核心:>100 必须拒。变异(去掉上界)→ 此断言 RED。
    expect(validateConfig(7, { ...base, sampleRatePct: 101 }).ok).toBe(false)
    expect(validateConfig(7, { ...base, sampleRatePct: -1 }).ok).toBe(false)
  })

  it('封禁窗口必须严格 > 0', () => {
    // 判别核心:0 必须拒(镜像后端 <= 0 即 400)。变异(改成 < 0)→ 此断言 RED。
    expect(validateConfig(7, { ...base, banWindowSeconds: 0 }).ok).toBe(false)
    expect(validateConfig(7, { ...base, banWindowSeconds: 1 }).ok).toBe(true)
  })

  it('封禁阈值非负;0 合法,负数非法', () => {
    expect(validateConfig(7, { ...base, banThreshold: 0 }).ok).toBe(true)
    expect(validateConfig(7, { ...base, banThreshold: -1 }).ok).toBe(false)
  })

  it('罚款空串归一为 "0";负数/非数字非法', () => {
    const empty = validateConfig(7, { ...base, violationFeeUsd: '' })
    expect(empty.ok).toBe(true)
    if (empty.ok) expect(empty.value.violation_fee_usd).toBe('0')
    expect(validateConfig(7, { ...base, violationFeeUsd: '1.50' }).ok).toBe(true)
    expect(validateConfig(7, { ...base, violationFeeUsd: '-1' }).ok).toBe(false)
    expect(validateConfig(7, { ...base, violationFeeUsd: 'abc' }).ok).toBe(false)
  })
})

describe('formatFee', () => {
  it('裁尾随 0,整数不留小数点,非法原样', () => {
    expect(formatFee('0.00000000')).toBe('0')
    expect(formatFee('1.50000000')).toBe('1.5')
    expect(formatFee('2.00000000')).toBe('2')
    expect(formatFee('3')).toBe('3')
    expect(formatFee('xyz')).toBe('xyz')
  })
})

describe('configToForm', () => {
  it('拍平 DTO 为表单初值,fee 经 formatFee', () => {
    const cfg: ModerationConfig = {
      tenant_id: 7,
      enabled: false,
      fail_closed: true,
      sample_rate_pct: 80,
      ban_threshold: 5,
      ban_window_seconds: 600,
      violation_fee_usd: '2.50000000',
    }
    expect(configToForm(cfg)).toEqual({
      enabled: false,
      failClosed: true,
      sampleRatePct: 80,
      banThreshold: 5,
      banWindowSeconds: 600,
      violationFeeUsd: '2.5',
    })
  })
})

// ── 关键词/哈希规则校验 + 批量解析(Wave B 接线;§14 变异法)─────────────────────
import {
  BULK_MAX_ITEMS,
  normalizeHash,
  parseBulkLines,
  shortHash,
  validateBulkCount,
  validateHash,
  validateKeyword,
} from './moderation'

describe('validateKeyword', () => {
  it('空白拒绝、非空通过', () => {
    // 变异(删 trim()===''守卫)→ 纯空白本应报错却放行,首断言 RED。
    expect(validateKeyword('   ')).toBe('关键词不能为空')
    expect(validateKeyword('')).toBe('关键词不能为空')
    expect(validateKeyword('badword')).toBeNull()
  })
})

describe('validateHash', () => {
  const valid = 'a'.repeat(64)
  it('恰好 64 位小写 hex 通过;63/65 位、非 hex 字符拒绝', () => {
    // 变异(长度判从 ==64 改成 >=64,或字符集放宽)→ 65 位/含 g 本应拒却放行,断言 RED。
    expect(validateHash(valid)).toBeNull()
    expect(validateHash('a'.repeat(63))).toBe('须为 64 位十六进制(SHA-256)')
    expect(validateHash('a'.repeat(65))).toBe('须为 64 位十六进制(SHA-256)')
    expect(validateHash('g'.repeat(64))).toBe('须为 64 位十六进制(SHA-256)')
  })
  it('大写输入先归一为小写再判,合法大写哈希应通过', () => {
    // 变异(normalizeHash 去掉 toLowerCase)→ 大写哈希被判非法,断言 RED。
    expect(validateHash('A'.repeat(64))).toBeNull()
    expect(normalizeHash('  ABCDEF  ')).toBe('abcdef')
  })
})

describe('validateBulkCount', () => {
  it('0 拒、1 与 1000 过、1001 拒(边界打在 BULK_MAX_ITEMS)', () => {
    // 变异(把 n>MAX 改成 n>MAX+1,或 n<=0 改 n<0)→ 1001/0 本应拒却放行,断言 RED。
    expect(validateBulkCount(0).ok).toBe(false)
    expect(validateBulkCount(1).ok).toBe(true)
    expect(validateBulkCount(BULK_MAX_ITEMS).ok).toBe(true)
    expect(validateBulkCount(BULK_MAX_ITEMS + 1).ok).toBe(false)
  })
})

describe('parseBulkLines', () => {
  it('按行拆、trim、丢空行、保序', () => {
    // 变异(去掉 filter 空行)→ 中间空行会混入,length/内容断言 RED。
    expect(parseBulkLines('a\n  b  \n\n c\n')).toEqual(['a', 'b', 'c'])
    expect(parseBulkLines('   \n\n')).toEqual([])
  })
})

describe('shortHash', () => {
  it('长串缩为头8尾4、短串原样、空串破折号', () => {
    expect(shortHash('a'.repeat(64))).toBe('aaaaaaaa…aaaa')
    expect(shortHash('abc')).toBe('abc')
    expect(shortHash('')).toBe('—')
  })
})
