import { describe, expect, it } from 'vitest'
import {
  buildCreateUser,
  EMPTY_CREATE_USER,
  mapUserPagination,
  mapUserRows,
  mapUserStats,
  roleLabel,
  statusLabel,
  toggleStatusTarget,
  type CreateUserForm,
} from './users'
import type { AdminUser } from './types'

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

describe('用户列表展示映射', () => {
  it('角色和状态映射保留已知中文语义与未知原值', () => {
    // 变异:把 user/active 标签改错或未知值吞掉,对应断言立即变红。
    expect(roleLabel('user')).toBe('普通用户')
    expect(roleLabel('owner')).toBe('owner')
    expect(statusLabel('active')).toBe('正常')
    expect(statusLabel('pending')).toBe('pending')
  })

  it('统计卡使用全量统计值并给每个数字补人数上下文', () => {
    const cards = mapUserStats({ enabled_users: 49, total_users: 196, enabled_rate: 0.25 })
    // 变异:若误用 enabled_users/当页条数作为总用户,第一断言会变红。
    expect(cards).toEqual([
      { label: '总用户', value: '196 人', hint: '全租户用户，不受当前分页影响' },
      { label: '2FA 普及率', value: '25%', hint: '49 人已开启 / 共 196 人' },
    ])
    expect(mapUserStats(null).every((card) => card.value === '—')).toBe(true)
  })

  it('表格行补齐用户组、备注、币种、状态和详情字段', () => {
    const rows = mapUserRows([user({
      id: 7,
      email: 'ops@example.com',
      role: 'admin',
      status: 'locked',
      user_group: ' premium ',
      remark: ' 重点客户 ',
      balance: '12.50000000',
      created_at: 'not-a-date',
    })])
    // 变异:漏映 user_group/remark 或余额币种、锁定语气算错,任一字段都会变红。
    expect(rows[0]).toEqual({
      id: 7,
      email: 'ops@example.com',
      role: '管理员',
      status: 'locked',
      statusText: '已锁定',
      statusTone: 'danger',
      userGroup: 'premium',
      remark: '重点客户',
      balance: '12.50000000 USD',
      createdAt: '—',
    })
    const empty = mapUserRows([user({ user_group: ' ', remark: '' })])[0]
    expect([empty.userGroup, empty.remark]).toEqual(['未分组', '无备注'])
    expect(empty.createdAt).toBe(new Date('2026-07-13T00:00:00Z').toLocaleString('zh-CN', { hour12: false }))
  })

  it('分页用 196 全量总数开放第二页，并在搜索时不伪造筛选总数', () => {
    const first = mapUserPagination({ offset: 0, limit: 100, returnedCount: 100, totalUsers: 196, searching: false })
    // 变异:若仍把第一页 100 条当总数,canNext 会错误变 false 且口径文本不等。
    expect(first).toEqual({
      page: 1,
      start: 1,
      end: 100,
      canPrevious: false,
      canNext: true,
      scopeText: '第 1–100 条 · 全体共 196 人',
    })
    const second = mapUserPagination({ offset: 100, limit: 100, returnedCount: 96, totalUsers: 196, searching: false })
    expect(second.canNext).toBe(false)
    expect(second.scopeText).toBe('第 101–196 条 · 全体共 196 人')
    const searched = mapUserPagination({ offset: 0, limit: 25, returnedCount: 25, totalUsers: 196, searching: true })
    expect(searched.canNext).toBe(true)
    expect(searched.scopeText).toBe('当前搜索第 1–25 条 · 全体共 196 人')
    const emptyPage = mapUserPagination({ offset: 25, limit: 25, returnedCount: 0, totalUsers: 196, searching: true })
    expect(emptyPage.scopeText).toBe('当前搜索第 0–0 条 · 全体共 196 人')
    expect(emptyPage.canPrevious).toBe(true)
  })
})

function user(overrides: Partial<AdminUser>): AdminUser {
  return {
    id: 1,
    email: 'user@example.com',
    role: 'user',
    status: 'active',
    user_group: '',
    remark: '',
    balance: '0.00000000',
    created_at: '2026-07-13T00:00:00Z',
    ...overrides,
  }
}
