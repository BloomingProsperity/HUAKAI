import { describe, expect, it } from 'vitest'
import { formatCentsRange, providerLabel } from './wallet'

/*
 * 充值配置展示纯逻辑测试。每条都带「判别核心 + 变异→RED」注释,确保缺陷被引入时测试必红(§14)。
 */

describe('providerLabel', () => {
  it('已知渠道映射中文名', () => {
    // 判别核心:必须按归一化后的 key 查表。变异(去掉 toLowerCase 或返回原值)→ "manual" 不再得 "人工转账" → RED。
    expect(providerLabel('manual')).toBe('人工转账')
    expect(providerLabel('taobao')).toBe('淘宝/闲鱼')
    // 大小写与空白须归一后命中。变异(不 trim/不 lower)→ 命不中表落到回落分支 → RED。
    expect(providerLabel('  Manual ')).toBe('人工转账')
  })
  it('未知渠道回落为首字母大写', () => {
    // 判别核心:未知渠道不能返回中文也不能原样返回。变异(回落直接 return key)→ 得 "stripe" 而非 "Stripe" → RED。
    expect(providerLabel('stripe')).toBe('Stripe')
    expect(providerLabel('')).toBe('未知渠道')
  })
})

describe('formatCentsRange', () => {
  it('USD 区间带 $ 符号且补零', () => {
    // 判别核心:两端各走 formatMoney(分→元补零)且夹 " ~ "。
    // 变异(漏掉 max 或不补零)→ 区间字符串结构变化 → RED。
    expect(formatCentsRange(100, 500000, 'USD')).toBe('$1.00 ~ $5000.00')
    expect(formatCentsRange(5, 99, 'USD')).toBe('$0.05 ~ $0.99')
  })
  it('非 USD 币种用后缀币种码、不加 $', () => {
    // 判别核心:sym 仅在 USD 时为 $,否则用「金额 币种码」。
    // 变异(无条件用 $)→ CNY 误带 $ 且无后缀 → RED。
    expect(formatCentsRange(100, 200, 'CNY')).toBe('1.00 CNY ~ 2.00 CNY')
  })
})
