import { describe, expect, it } from 'vitest'
import { buildCreateUser, EMPTY_CREATE_USER, toggleStatusTarget, type CreateUserForm } from './users'

function form(over: Partial<CreateUserForm>): CreateUserForm {
  return { ...EMPTY_CREATE_USER, ...over }
}

describe('buildCreateUser', () => {
  it('邮箱非法 / 密码过短 各报错', () => {
    expect(buildCreateUser(form({ email: 'bad' }))).toEqual({ error: '请填写有效邮箱' })
    expect(buildCreateUser(form({ email: 'a@b.com', password: '123' }))).toEqual({ error: '密码至少 8 位' })
  })

  it('创建仅允许 user 角色:admin / 未知角色都报错(后端越权护栏拒 admin,客户端先挡)', () => {
    // 判别核心:admin 不再是合法创建角色。变异(放回 USER_ROLES 校验)→ admin 通过 → RED。
    expect(buildCreateUser(form({ email: 'a@b.com', password: '12345678', role: 'admin' }))).toEqual({ error: '新建用户仅支持普通用户角色' })
    expect(buildCreateUser(form({ email: 'a@b.com', password: '12345678', role: 'root' }))).toEqual({ error: '新建用户仅支持普通用户角色' })
  })

  it('齐全 → 正确请求,空 display_name 省略', () => {
    const r = buildCreateUser(form({ email: ' a@b.com ', password: 'pass1234', role: 'user' }))
    expect(r).toEqual({ email: 'a@b.com', password: 'pass1234', role: 'user' })
    expect('display_name' in (r as object)).toBe(false)
  })

  it('带 display_name', () => {
    const r = buildCreateUser(form({ email: 'a@b.com', password: 'pass1234', displayName: ' 小开 ' }))
    expect((r as { display_name?: string }).display_name).toBe('小开')
  })
})

describe('toggleStatusTarget', () => {
  it('active → disabled,其余 → active', () => {
    // 判别核心:active 必须翻成 disabled。变异(恒返回 active)→ 第一断言 RED。
    expect(toggleStatusTarget('active')).toBe('disabled')
    expect(toggleStatusTarget('disabled')).toBe('active')
    expect(toggleStatusTarget('locked')).toBe('active')
  })
})
