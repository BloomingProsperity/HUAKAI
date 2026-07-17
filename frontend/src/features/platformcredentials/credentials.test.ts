import { describe, expect, it } from 'vitest'
import {
  buildAdminTokenRequest,
  buildPlatformApiKeyRequest,
  credentialStatusLabel,
  EMPTY_ADMIN_TOKEN_FORM,
  EMPTY_PLATFORM_API_KEY_FORM,
  mapAdminTokenTableRows,
  mapPlatformApiKeyTableRows,
  positiveID,
} from './credentials'
import type { AdminTokenListItem, PlatformApiKeyListItem } from './types'

const NOW = new Date('2026-07-12T12:00:00.000Z')

describe('运维令牌请求构造', () => {
  it('平台管理员省略残留 tenant_id，空选填字段不下传', () => {
    expect(
      buildAdminTokenRequest(
        { ...EMPTY_ADMIN_TOKEN_FORM, role: 'platform_admin', tenantId: '99' },
        NOW,
      ),
    ).toEqual({ value: { role: 'platform_admin' } })
  })

  it('租户运维必须带正整数 tenant_id，并把未来时间转 RFC3339', () => {
    expect(
      buildAdminTokenRequest(
        {
          name: ' 临时值班 ',
          role: 'tenant_operator',
          tenantId: '7',
          expiresAt: '2026-07-13T12:30:00.000Z',
          note: ' 夜班 ',
        },
        NOW,
      ),
    ).toEqual({
      value: {
        role: 'tenant_operator',
        tenant_id: 7,
        expires_at: '2026-07-13T12:30:00.000Z',
        name: '临时值班',
        note: '夜班',
      },
    })
    expect(buildAdminTokenRequest({ ...EMPTY_ADMIN_TOKEN_FORM, role: 'tenant_operator' }, NOW)).toEqual({
      error: '租户运维令牌必须填写有效租户 ID',
    })
  })

  it('已过期时间被前端拦截，避免铸出立即失效的凭证', () => {
    expect(
      buildAdminTokenRequest({ ...EMPTY_ADMIN_TOKEN_FORM, expiresAt: '2026-07-12T11:59:59.000Z' }, NOW),
    ).toEqual({ error: '过期时间必须晚于当前时间' })
  })
})

describe('平台 API Key 请求构造', () => {
  it('严格校验租户、用户、名称，并保留真实字段名', () => {
    expect(buildPlatformApiKeyRequest(EMPTY_PLATFORM_API_KEY_FORM, NOW)).toEqual({ error: '请填写有效租户 ID' })
    expect(
      buildPlatformApiKeyRequest({ ...EMPTY_PLATFORM_API_KEY_FORM, tenantId: '7' }, NOW),
    ).toEqual({ error: '请填写有效用户 ID' })
    expect(
      buildPlatformApiKeyRequest({ ...EMPTY_PLATFORM_API_KEY_FORM, tenantId: '7', userId: '3' }, NOW),
    ).toEqual({ error: '请填写 Key 名称' })

    expect(
      buildPlatformApiKeyRequest(
        {
          tenantId: '7',
          userId: '3',
          name: ' 测试 Key ',
          environment: 'test',
          expiresAt: '',
          reason: ' 联调 ',
        },
        NOW,
      ),
    ).toEqual({
      value: { tenant_id: 7, user_id: 3, name: '测试 Key', environment: 'test', reason: '联调' },
    })
  })
})

describe('凭证辅助逻辑', () => {
  it('只接受安全正整数 ID', () => {
    expect(positiveID('8')).toBe(8)
    expect(positiveID('0')).toBeNull()
    expect(positiveID('1.5')).toBeNull()
    expect(positiveID('9007199254740992')).toBeNull()
  })

  it('状态标签不会把吊销误显示成有效', () => {
    expect(credentialStatusLabel('active')).toBe('有效')
    expect(credentialStatusLabel('revoked')).toBe('已吊销')
    expect(credentialStatusLabel('expired')).toBe('已过期')
  })
})

describe('凭证列表列映射', () => {
  it('运维令牌仅展示脱敏前缀并精确映射状态与作用域', () => {
    const source: AdminTokenListItem = {
      id: 41,
      name: '夜班令牌',
      key_prefix: 'hua_masked',
      role: 'tenant_operator',
      scope_tenant_id: 7,
      bootstrap: true,
      status: 'active',
      expires_at: null,
      last_used_at: null,
      revoked_at: null,
      revoked_reason: null,
      created_at: 'invalid-created',
    }
    const [row] = mapAdminTokenTableRows([source])

    // 判别核心:keyPrefix 必须来自已脱敏前缀；作用域、状态与可吊销条件错接即变红。
    expect(row).toEqual({
      id: 41,
      name: '夜班令牌',
      keyPrefix: 'hua_masked',
      role: '租户运维',
      scope: '租户 #7',
      status: 'active',
      bootstrap: true,
      expiresAt: '永不过期',
      lastUsedAt: '从未使用',
      createdAt: 'invalid-created',
      revocable: true,
      source,
    })
    expect('plaintext' in row).toBe(false)
  })

  it('平台 API Key 仅展示名称与脱敏前缀，吊销态不可再次吊销', () => {
    const source: PlatformApiKeyListItem = {
      id: 52,
      tenant_id: 7,
      user_id: 19,
      name: '联调 Key',
      key_prefix: 'hk_live_masked',
      status: 'revoked',
      expires_at: null,
      last_used_at: null,
      revoked_at: '2026-07-12T00:00:00Z',
      revoked_reason: 'manual',
      created_at: 'invalid-created',
    }
    const [row] = mapPlatformApiKeyTableRows([source])

    expect(row).toEqual({
      id: 52,
      name: '联调 Key',
      keyPrefix: 'hk_live_masked',
      userID: '#19',
      status: 'revoked',
      expiresAt: '永不过期',
      lastUsedAt: '从未使用',
      createdAt: 'invalid-created',
      revocable: false,
      source,
    })
    expect('plaintext_bearer' in row).toBe(false)
  })
})
