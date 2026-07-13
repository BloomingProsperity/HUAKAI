import { describe, expect, it } from 'vitest'
import { formatLatencyMetric, formatTpsMetric, formatUsdMetric } from './metricFormat'

describe('运维指标共享格式化', () => {
  it('金额按字符串安全四舍五入到美分，并保留全精度 title', () => {
    // 判别核心：第三位小数决定进位，且 title 不能复用舍入后的短值。
    expect(formatUsdMetric('241.2743402048')).toEqual({ value: '$241.27', title: '$241.2743402048' })
    expect(formatUsdMetric('999999999999999999.999')).toEqual({
      value: '$1000000000000000000.00',
      title: '$999999999999999999.999',
    })
    expect(formatUsdMetric('-0.005')).toEqual({ value: '-$0.01', title: '-$0.005' })
    expect(formatUsdMetric('bad')).toEqual({ value: '—', title: 'bad' })
  })

  it('TTFT 在秒边界两侧采用稳定单位，title 保留原始毫秒', () => {
    // 判别核心：3950.1503 不能直接拼接成超长毫秒串，也不能误当 3.95ms。
    expect(formatLatencyMetric('3950.1503')).toEqual({ value: '3.95s', title: '3950.1503ms' })
    expect(formatLatencyMetric('999.6')).toEqual({ value: '1000ms', title: '999.6ms' })
    expect(formatLatencyMetric('nope').value).toBe('—')
  })

  it('TPS 固定一位小数且 title 保留全精度', () => {
    // 判别核心：第二位小数必须参与四舍五入，删除 toFixed 会立即打红。
    expect(formatTpsMetric('164.2553')).toEqual({ value: '164.3', title: '164.2553' })
    expect(formatTpsMetric('18')).toEqual({ value: '18.0', title: '18' })
    expect(formatTpsMetric('nope').value).toBe('—')
  })
})
