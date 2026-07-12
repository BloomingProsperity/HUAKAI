import { describe, expect, it } from 'vitest'
import {
  brandName,
  brandSubtitle,
  DEFAULT_SITE_NAME,
  DEFAULT_SITE_SUBTITLE,
  docLinkOrNull,
  ownerLabel,
  pricePerMillion,
  pricingHighlights,
} from './landing'
import type { PricingItem } from './types'

function item(over: Partial<PricingItem>): PricingItem {
  return { model: 'm', ...over }
}

describe('brandName / brandSubtitle', () => {
  it('空/纯空白回退默认,非空原样(trim)', () => {
    // 判别核心:空白必须回退。变异(直接返回 raw)→ 第一/第二断言 RED。
    expect(brandName({ site_name: '' })).toBe(DEFAULT_SITE_NAME)
    expect(brandName({ site_name: '   ' })).toBe(DEFAULT_SITE_NAME)
    expect(brandName(null)).toBe(DEFAULT_SITE_NAME)
    expect(brandName({ site_name: ' 玉青中转 ' })).toBe('玉青中转')
    expect(brandSubtitle({ site_subtitle: '' })).toBe(DEFAULT_SITE_SUBTITLE)
    expect(brandSubtitle({ site_subtitle: ' 一句话 ' })).toBe('一句话')
  })
})

describe('docLinkOrNull', () => {
  it('仅放行 http(s) 绝对地址,其余一律 null', () => {
    // 判别核心:相对路径/非 http 协议必须拒绝。变异(去掉协议校验)→ 后两断言 RED。
    expect(docLinkOrNull({ site_doc_url: 'https://docs.example.com' })).toBe('https://docs.example.com')
    expect(docLinkOrNull({ site_doc_url: '  http://d.io/x ' })).toBe('http://d.io/x')
    expect(docLinkOrNull({ site_doc_url: '' })).toBeNull()
    expect(docLinkOrNull({ site_doc_url: '/docs' })).toBeNull()
    expect(docLinkOrNull({ site_doc_url: 'javascript:alert(1)' })).toBeNull()
  })
})

describe('pricePerMillion', () => {
  it('×1e6 换算并按量级取小数位;非法→—', () => {
    // 判别核心:必须 ×1e6。变异(不乘)→ "0.000003" 会得 $0.0000 而非 $3.00 → RED。
    expect(pricePerMillion('0.000003')).toBe('$3.00')
    // 0.0000008 ×1e6 = 0.8(≥0.01)→ 2 位
    expect(pricePerMillion('0.0000008')).toBe('$0.80')
    // 0.000000005 ×1e6 = 0.005(<0.01)→ 4 位,验证极小价不被显示成 $0.00
    expect(pricePerMillion('0.000000005')).toBe('$0.0050')
    expect(pricePerMillion('0')).toBe('$0')
    expect(pricePerMillion(undefined)).toBe('—')
    expect(pricePerMillion('-1')).toBe('—')
    expect(pricePerMillion('abc')).toBe('—')
  })
})

describe('pricingHighlights', () => {
  it('剔除缺任一价的项并截断到 limit', () => {
    const items: PricingItem[] = [
      item({ model: 'full', input_price_per_token: '0.000001', output_price_per_token: '0.000002' }),
      item({ model: 'no-out', input_price_per_token: '0.000001' }),
      item({ model: 'no-in', output_price_per_token: '0.000002' }),
      item({ model: 'full2', input_price_per_token: '0.000003', output_price_per_token: '0.000004' }),
    ]
    // 判别核心:缺输出价的 'no-out' 必须被剔除。变异(只判输入价)→ 结果含 no-out → RED。
    const out = pricingHighlights(items, 6)
    expect(out.map((i) => i.model)).toEqual(['full', 'full2'])
    // 截断:limit=1 只留第一条完整项。
    expect(pricingHighlights(items, 1).map((i) => i.model)).toEqual(['full'])
  })
})

describe('ownerLabel', () => {
  it('空 owned_by → 其他', () => {
    expect(ownerLabel({ owned_by: '' })).toBe('其他')
    expect(ownerLabel({})).toBe('其他')
    expect(ownerLabel({ owned_by: 'anthropic' })).toBe('anthropic')
  })
})
