import { describe, expect, it } from 'vitest'
import { formatCentsRange, parseTopupAmount, providerLabel } from './wallet'

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

describe('parseTopupAmount(money 敏感)', () => {
  const MIN = 100 // $1.00
  const MAX = 500_000 // $5000.00

  it('合法金额解析成分(美元→分,四舍五入到整分)', () => {
    // 判别核心:美元字符串 → 整数分,且 *100 用 round 规避浮点漂移。
    // 变异(漏 *100 或用 trunc)→ "12.34" 不再得 1234 → RED。
    expect(parseTopupAmount('1', MIN, MAX)).toEqual({ ok: true, amountCents: 100 })
    expect(parseTopupAmount('12.34', MIN, MAX)).toEqual({ ok: true, amountCents: 1234 })
    expect(parseTopupAmount('2.50', MIN, MAX)).toEqual({ ok: true, amountCents: 250 })
    expect(parseTopupAmount('5000', MIN, MAX)).toEqual({ ok: true, amountCents: 500_000 })
  })

  it('空/非数字/负数/科学计数法/多于两位小数 → 非法', () => {
    // 判别核心:正则只放行 \d+(\.\d{1,2})?。变异(放开两位小数限制)→ "1.005" 被吞 → RED。
    expect(parseTopupAmount('', MIN, MAX).ok).toBe(false)
    expect(parseTopupAmount('abc', MIN, MAX).ok).toBe(false)
    expect(parseTopupAmount('-5', MIN, MAX).ok).toBe(false)
    expect(parseTopupAmount('1e3', MIN, MAX).ok).toBe(false)
    expect(parseTopupAmount('1.005', MIN, MAX).ok).toBe(false)
    expect(parseTopupAmount('1.', MIN, MAX).ok).toBe(false)
  })

  it('低于下限 / 高于上限 → 越界(两端都卡)', () => {
    // 判别核心:区间两端都要卡。变异(只卡下限/只卡上限)→ 另一端越界金额漏过 → RED。
    const below = parseTopupAmount('0.50', MIN, MAX)
    expect(below.ok).toBe(false)
    if (!below.ok) expect(below.error).toContain('$1.00 ~ $5000.00')
    const above = parseTopupAmount('5000.01', MIN, MAX)
    expect(above.ok).toBe(false)
  })

  it('端点值含端(=min/=max 合法)', () => {
    // 判别核心:边界用 <min / >max(含端)。变异(用 <= / >=)→ 恰好等于 min/max 被误拒 → RED。
    expect(parseTopupAmount('1.00', MIN, MAX).ok).toBe(true)
    expect(parseTopupAmount('5000.00', MIN, MAX).ok).toBe(true)
  })

  it('解析为 0 分(如 "0" / "0.00")被拒(必须 > 0)', () => {
    // money:绝不允许 0 元开单。变异(去掉 <=0 检查)→ "0" 漏过 → RED(若 min>0 这里也会被区间挡,但显式校验更稳)。
    expect(parseTopupAmount('0', 1, MAX).ok).toBe(false)
    expect(parseTopupAmount('0.00', 1, MAX).ok).toBe(false)
  })
})
