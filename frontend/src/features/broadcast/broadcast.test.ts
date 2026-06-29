import { describe, expect, it } from 'vitest'
import { failureRate, severityLabel, severityTone, validateBroadcast } from './broadcast'

describe('validateBroadcast', () => {
  it('合法表单通过并 trim 标题/正文', () => {
    // 判别核心:后端先 trim 再判空(service.go:105-106);前缀/后缀空白应被裁掉后下发。
    const r = validateBroadcast(1, { title: '  维护通知 ', body: '  今晚维护 ', severity: 'warning' })
    expect(r).toEqual({
      ok: true,
      value: { tenant_id: 1, title: '维护通知', body: '今晚维护', severity: 'warning' },
    })
  })

  it('tenant_id 非正整数拒绝', () => {
    // 变异(删 tenantId<=0 守卫)→ 0/负数本应拒却放行,这两条断言 RED。
    expect(validateBroadcast(0, { title: 't', body: 'b', severity: 'info' })).toEqual({
      ok: false,
      error: 'tenant_id 必须为正整数',
    })
    expect(validateBroadcast(-3, { title: 't', body: 'b', severity: 'info' }).ok).toBe(false)
    expect(validateBroadcast(1.5, { title: 't', body: 'b', severity: 'info' }).ok).toBe(false)
  })

  it('标题仅空白拒绝(镜像后端 trim 后判空)', () => {
    // 变异(去掉 title.trim()==='' 守卫)→ 全空白标题本应拒却放行,断言 RED。
    const r = validateBroadcast(1, { title: '   ', body: '正文', severity: 'info' })
    expect(r).toEqual({ ok: false, error: '标题不能为空' })
  })

  it('正文仅空白拒绝', () => {
    // 变异(去掉 body.trim()==='' 守卫)→ 全空白正文本应拒却放行,断言 RED。
    const r = validateBroadcast(1, { title: '标题', body: '\n\t ', severity: 'info' })
    expect(r).toEqual({ ok: false, error: '正文不能为空' })
  })

  it('非法 severity 拒绝(镜像后端白名单 service.go:117-118)', () => {
    // 变异(去掉 SEVERITIES.some 守卫)→ 越界级别本应拒却放行,断言 RED。
    const r = validateBroadcast(1, {
      title: '标题',
      body: '正文',
      severity: 'urgent' as unknown as 'info',
    })
    expect(r).toEqual({ ok: false, error: '严重级别非法' })
  })
})

describe('severityTone / severityLabel', () => {
  it('级别映射到语气与中文标签', () => {
    // 判别核心:critical=danger、warning=warn、info=muted;变异(交换分支)→ 断言 RED。
    expect(severityTone('critical')).toBe('danger')
    expect(severityTone('warning')).toBe('warn')
    expect(severityTone('info')).toBe('muted')
    expect(severityTone('未知')).toBe('muted')
    expect(severityLabel('critical')).toBe('严重')
    expect(severityLabel('warning')).toBe('警告')
    expect(severityLabel('info')).toBe('普通')
  })
})

describe('failureRate', () => {
  it('tick>0 给四舍五入百分比', () => {
    // 判别核心:1/4=0.25 → "25%";变异(去掉 *100 或漏 round)→ 断言 RED。
    expect(failureRate(1, 4)).toBe('25%')
    expect(failureRate(1, 3)).toBe('33%')
    expect(failureRate(0, 10)).toBe('0%')
  })
  it('tick=0 回退破折号,不漏 NaN%', () => {
    // 变异(去掉 tickCount<=0 守卫)→ 0/0=NaN 渲染 "NaN%",此断言 RED。
    expect(failureRate(0, 0)).toBe('—')
    expect(failureRate(3, 0)).toBe('—')
  })
})
