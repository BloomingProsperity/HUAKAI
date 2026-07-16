import type {
  AdminTokenListItem,
  AdminTokenRole,
  CreateAdminTokenRequest,
  CreatePlatformApiKeyRequest,
  PlatformApiKeyEnvironment,
  PlatformApiKeyListItem,
} from './types'

export interface AdminTokenForm {
  name: string
  role: AdminTokenRole
  tenantId: string
  expiresAt: string
  note: string
}

export const EMPTY_ADMIN_TOKEN_FORM: AdminTokenForm = {
  name: '',
  role: 'platform_admin',
  tenantId: '',
  expiresAt: '',
  note: '',
}

export interface PlatformApiKeyForm {
  tenantId: string
  userId: string
  name: string
  environment: PlatformApiKeyEnvironment
  expiresAt: string
  reason: string
}

export const EMPTY_PLATFORM_API_KEY_FORM: PlatformApiKeyForm = {
  tenantId: '',
  userId: '',
  name: '',
  environment: 'live',
  expiresAt: '',
  reason: '',
}

export type BuildResult<T> = { value: T } | { error: string }

export function positiveID(raw: string): number | null {
  const value = Number(raw.trim())
  return Number.isSafeInteger(value) && value > 0 ? value : null
}

function expiryValue(raw: string, now: Date): BuildResult<string | undefined> {
  if (raw.trim() === '') return { value: undefined }
  const value = new Date(raw)
  if (Number.isNaN(value.getTime())) return { error: '过期时间无效' }
  if (value.getTime() <= now.getTime()) return { error: '过期时间必须晚于当前时间' }
  return { value: value.toISOString() }
}

export function buildAdminTokenRequest(
  form: AdminTokenForm,
  now = new Date(),
): BuildResult<CreateAdminTokenRequest> {
  const expires = expiryValue(form.expiresAt, now)
  if ('error' in expires) return expires

  const request: CreateAdminTokenRequest = { role: form.role }
  if (form.role === 'tenant_operator') {
    const tenantId = positiveID(form.tenantId)
    if (tenantId === null) return { error: '租户运维令牌必须填写有效租户 ID' }
    request.tenant_id = tenantId
  }
  const name = form.name.trim()
  const note = form.note.trim()
  if (name) request.name = name
  if (note) request.note = note
  if (expires.value) request.expires_at = expires.value
  return { value: request }
}

export function buildPlatformApiKeyRequest(
  form: PlatformApiKeyForm,
  now = new Date(),
): BuildResult<CreatePlatformApiKeyRequest> {
  const tenantId = positiveID(form.tenantId)
  if (tenantId === null) return { error: '请填写有效租户 ID' }
  const userId = positiveID(form.userId)
  if (userId === null) return { error: '请填写有效用户 ID' }
  const name = form.name.trim()
  if (!name) return { error: '请填写 Key 名称' }
  const expires = expiryValue(form.expiresAt, now)
  if ('error' in expires) return expires

  const request: CreatePlatformApiKeyRequest = {
    tenant_id: tenantId,
    user_id: userId,
    name,
    environment: form.environment,
  }
  if (expires.value) request.expires_at = expires.value
  const reason = form.reason.trim()
  if (reason) request.reason = reason
  return { value: request }
}

export function credentialStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '有效'
    case 'revoked':
      return '已吊销'
    case 'expired':
      return '已过期'
    default:
      return status || '—'
  }
}

export function credentialStatusTone(status: string): 'ok' | 'warn' | 'danger' | 'muted' {
  switch (status) {
    case 'active':
      return 'ok'
    case 'expired':
      return 'warn'
    case 'revoked':
      return 'danger'
    default:
      return 'muted'
  }
}

export function formatCredentialTime(value: string | null | undefined): string {
  if (!value) return '永不过期'
  const time = new Date(value)
  return Number.isNaN(time.getTime()) ? value : time.toLocaleString('zh-CN', { hour12: false })
}

export interface AdminTokenTableRow {
  id: number
  name: string
  keyPrefix: string
  role: string
  scope: string
  status: string
  bootstrap: boolean
  expiresAt: string
  lastUsedAt: string
  createdAt: string
  revocable: boolean
  source: AdminTokenListItem
}

export interface PlatformApiKeyTableRow {
  id: number
  name: string
  keyPrefix: string
  userID: string
  status: string
  expiresAt: string
  lastUsedAt: string
  createdAt: string
  revocable: boolean
  source: PlatformApiKeyListItem
}

/** 列表仅映射后端提供的脱敏前缀，不构造或暴露任何凭证明文。 */
export function mapAdminTokenTableRows(items: AdminTokenListItem[]): AdminTokenTableRow[] {
  return items.map((item) => ({
    id: item.id,
    name: item.name || `令牌 #${item.id}`,
    keyPrefix: item.key_prefix,
    role: item.role === 'platform_admin' ? '平台管理员' : '租户运维',
    scope: item.scope_tenant_id ? `租户 #${item.scope_tenant_id}` : '全平台',
    status: item.status,
    bootstrap: item.bootstrap,
    expiresAt: formatCredentialTime(item.expires_at),
    lastUsedAt: item.last_used_at ? formatCredentialTime(item.last_used_at) : '从未使用',
    createdAt: formatCredentialTime(item.created_at),
    revocable: item.status === 'active' && item.revoked_at === null,
    source: item,
  }))
}

/** 平台 Key 列表只保留名称和脱敏前缀，明文仍仅由一次性创建响应展示。 */
export function mapPlatformApiKeyTableRows(items: PlatformApiKeyListItem[]): PlatformApiKeyTableRow[] {
  return items.map((item) => ({
    id: item.id,
    name: item.name,
    keyPrefix: item.key_prefix,
    userID: `#${item.user_id}`,
    status: item.status,
    expiresAt: formatCredentialTime(item.expires_at),
    lastUsedAt: item.last_used_at ? formatCredentialTime(item.last_used_at) : '从未使用',
    createdAt: formatCredentialTime(item.created_at),
    revocable: item.status === 'active' && item.revoked_at === null,
    source: item,
  }))
}
