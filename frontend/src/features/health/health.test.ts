import { describe, expect, it } from 'vitest'
import { componentLabel, fmtBytes, fmtInt, fmtUptime, statusLabel, statusTone } from './health'

describe('statusTone / statusLabel', () => {
  it('三态映射配色', () => {
    // 判别核心:degraded/unhealthy 不能都映射成 ok。变异(恒 ok)→ RED。
    expect(statusTone('healthy')).toBe('ok')
    expect(statusTone('degraded')).toBe('warn')
    expect(statusTone('unhealthy')).toBe('danger')
  })
  it('中文短标', () => {
    expect(statusLabel('healthy')).toBe('健康')
    expect(statusLabel('degraded')).toBe('降级')
    expect(statusLabel('unhealthy')).toBe('故障')
  })
})

describe('componentLabel', () => {
  it('已知组件名中文化,未知原样', () => {
    expect(componentLabel('database')).toBe('数据库')
    expect(componentLabel('dlq')).toBe('死信队列')
    expect(componentLabel('something_new')).toBe('something_new')
  })
})

describe('fmtUptime', () => {
  it('取最高两个量级', () => {
    // 判别核心:含天数时必须显示 d+h。变异(漏小时项/算错进位)→ RED。
    expect(fmtUptime(2 * 86400 + 3 * 3600 + 30 * 60)).toBe('2d 3h')
    expect(fmtUptime(3 * 3600 + 15 * 60 + 9)).toBe('3h 15m')
    expect(fmtUptime(15 * 60 + 9)).toBe('15m 9s')
    expect(fmtUptime(42)).toBe('42s')
  })
  it('非法/负 → —', () => {
    expect(fmtUptime(-1)).toBe('—')
    expect(fmtUptime(NaN)).toBe('—')
  })
})

describe('fmtBytes', () => {
  it('1024 进制逐级缩放', () => {
    // 判别核心:必须按 1024 缩放并选对单位。变异(不除/除错)→ RED。
    expect(fmtBytes(512)).toBe('512 B')
    expect(fmtBytes(1024)).toBe('1 KB')
    expect(fmtBytes(1536)).toBe('1.5 KB')
    expect(fmtBytes(5 * 1024 * 1024)).toBe('5 MB')
    expect(fmtBytes(3.5 * 1024 * 1024 * 1024)).toBe('3.5 GB')
  })
  it('缺省/非法 → —', () => {
    expect(fmtBytes(undefined)).toBe('—')
    expect(fmtBytes(-1)).toBe('—')
  })
})

describe('fmtInt', () => {
  it('千分位', () => {
    expect(fmtInt(1234567)).toBe('1,234,567')
    expect(fmtInt(42)).toBe('42')
  })
})
