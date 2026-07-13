export interface MetricDisplay {
  value: string
  title: string
}

/**
 * 十进制美元金额展示。显示值固定到美分并四舍五入，title 保留后端原始精度。
 * 全程按字符串进位，避免先转 number 导致大数或长小数丢精度。
 */
export function formatUsdMetric(raw: string): MetricDisplay {
  const input = raw.trim()
  const match = /^([+-]?)(\d+)(?:\.(\d+))?$/.exec(input)
  if (!match) return { value: '—', title: input || '—' }

  const sign = match[1] === '-' ? '-' : ''
  const whole = match[2].replace(/^0+(?=\d)/, '')
  const fraction = match[3] ?? ''
  const scaled = BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0').slice(0, 2))
  const rounded = scaled + (fraction[2] !== undefined && fraction[2] >= '5' ? 1n : 0n)
  const integerPart = rounded / 100n
  const decimalPart = String(rounded % 100n).padStart(2, '0')
  const roundedSign = rounded === 0n ? '' : sign

  return {
    value: `${roundedSign}$${integerPart}.${decimalPart}`,
    title: `${sign}$${whole}${fraction ? `.${fraction}` : ''}`,
  }
}

/** 毫秒指标展示：一秒以下取整毫秒，一秒及以上换算为两位小数秒。 */
export function formatLatencyMetric(raw: string | number): MetricDisplay {
  const input = String(raw).trim()
  const value = Number(input)
  if (!Number.isFinite(value)) return { value: '—', title: input || '—' }
  return {
    value: value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${Math.round(value)}ms`,
    title: `${input}ms`,
  }
}

/** TPS 指标展示为一位小数，title 保留后端原始精度。 */
export function formatTpsMetric(raw: string | number): MetricDisplay {
  const input = String(raw).trim()
  const value = Number(input)
  if (!Number.isFinite(value)) return { value: '—', title: input || '—' }
  return { value: value.toFixed(1), title: input }
}
