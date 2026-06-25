import { describe, expect, it } from 'vitest'
import {
  buildCreate,
  buildUpdate,
  displayState,
  EMPTY_ANNOUNCEMENT_FORM,
  localToRFC3339,
  severityLabel,
  toggleActiveTarget,
  type AnnouncementForm,
} from './announcements'

function form(over: Partial<AnnouncementForm>): AnnouncementForm {
  return { ...EMPTY_ANNOUNCEMENT_FORM, ...over }
}

describe('buildCreate', () => {
  it('标题/正文为空各报错', () => {
    expect(buildCreate(form({ title: '', body: 'x' }), 1)).toEqual({ error: '请填写标题' })
    expect(buildCreate(form({ title: 'x', body: '   ' }), 1)).toEqual({ error: '请填写正文' })
  })

  it('齐全 → 正确请求(trim 标题正文,带 tenant_id/severity/active)', () => {
    const r = buildCreate(form({ title: ' 维护通知 ', body: ' 今晚维护 ', severity: 'warning', active: true }), 7)
    expect(r).toEqual({ tenant_id: 7, title: '维护通知', body: '今晚维护', severity: 'warning', active: true })
  })

  it('新建不带过期时间时 expires_at 字段省略(区别于编辑的显式 null)', () => {
    const r = buildCreate(form({ title: 't', body: 'b' }), 1)
    // 判别核心:新建未填过期 → 不应出现 expires_at 键。变异(buildCreate 里改成总是塞 expires_at)→ RED。
    expect('expires_at' in (r as object)).toBe(false)
  })

  it('过期时间必须晚于生效时间,否则报错', () => {
    // 判别核心:止<=起 必须被挡。变异(validateCore 把 <= 改成 < 或删掉该判断)→ 此断言 RED。
    const bad = buildCreate(
      form({ title: 't', body: 'b', publishedAt: '2026-06-25T10:00', expiresAt: '2026-06-25T10:00' }),
      1,
    )
    expect(bad).toEqual({ error: '过期时间必须晚于生效时间' })
    const ok = buildCreate(
      form({ title: 't', body: 'b', publishedAt: '2026-06-25T10:00', expiresAt: '2026-06-25T11:00' }),
      1,
    )
    expect('error' in (ok as object)).toBe(false)
  })
})

describe('buildUpdate', () => {
  it('编辑时空过期时间显式传 null(清空语义,区别于新建的省略)', () => {
    const r = buildUpdate(form({ title: 't', body: 'b', expiresAt: '' }))
    // 判别核心:编辑必须把 expires_at 设为 null 才能清空。变异(改成 undefined / 省略)→ RED。
    expect((r as { expires_at?: unknown }).expires_at).toBeNull()
  })
})

describe('localToRFC3339', () => {
  it('空串 → undefined,合法 → UTC ISO,非法 → null', () => {
    expect(localToRFC3339('  ')).toBeUndefined()
    expect(localToRFC3339('不是时间')).toBeNull()
    expect(localToRFC3339('2026-06-25T10:00')).toMatch(/^2026-06-25T/)
  })
})

describe('displayState', () => {
  const NOW = Date.parse('2026-06-25T12:00:00Z')

  it('active=false → disabled(优先于时间判断)', () => {
    // 判别核心:停用必须直接返回 disabled。变异(去掉 !active 短路)→ 会算成 live → RED。
    expect(displayState({ active: false, published_at: '2026-06-25T01:00:00Z' }, NOW)).toBe('disabled')
  })

  it('未到生效时间 → scheduled', () => {
    expect(displayState({ active: true, published_at: '2026-06-25T18:00:00Z' }, NOW)).toBe('scheduled')
  })

  it('已过期 → expired', () => {
    expect(
      displayState({ active: true, published_at: '2026-06-25T01:00:00Z', expires_at: '2026-06-25T06:00:00Z' }, NOW),
    ).toBe('expired')
  })

  it('生效中 → live', () => {
    expect(
      displayState({ active: true, published_at: '2026-06-25T01:00:00Z', expires_at: '2026-06-25T23:00:00Z' }, NOW),
    ).toBe('live')
  })
})

describe('severityLabel / toggleActiveTarget', () => {
  it('级别中文映射 + 未知透传', () => {
    expect(severityLabel('critical')).toBe('严重')
    expect(severityLabel('unknown')).toBe('unknown')
  })
  it('启停取反', () => {
    expect(toggleActiveTarget(true)).toBe(false)
    expect(toggleActiveTarget(false)).toBe(true)
  })
})
