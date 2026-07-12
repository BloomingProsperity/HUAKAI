export type AdminTokenRole = 'platform_admin' | 'tenant_operator'

export interface AdminTokenListItem {
  id: number
  name: string
  key_prefix: string
  role: AdminTokenRole
  scope_tenant_id: number | null
  bootstrap: boolean
  status: string
  expires_at: string | null
  last_used_at: string | null
  revoked_at: string | null
  revoked_reason: string | null
  created_at: string
}

export interface AdminTokenListResponse {
  items: AdminTokenListItem[]
  limit: number
  offset: number
}

export interface CreateAdminTokenRequest {
  role: AdminTokenRole
  tenant_id?: number
  expires_at?: string
  name?: string
  note?: string
}

export interface CreatedAdminToken {
  id: number
  role: AdminTokenRole
  key_prefix: string
  status: string
  expires_at: string | null
  created_at: string
  plaintext_bearer: string
}

export interface RevokeCredentialResponse {
  id: number
  already_revoked: boolean
}

export type PlatformApiKeyEnvironment = 'live' | 'test'

export interface PlatformApiKeyListItem {
  id: number
  tenant_id: number
  user_id: number
  name: string
  key_prefix: string
  status: string
  expires_at: string | null
  last_used_at: string | null
  revoked_at: string | null
  revoked_reason: string | null
  created_at: string
}

export interface PlatformApiKeyListResponse {
  items: PlatformApiKeyListItem[]
  limit: number
  offset: number
}

export interface CreatePlatformApiKeyRequest {
  tenant_id: number
  user_id: number
  name: string
  environment?: PlatformApiKeyEnvironment
  expires_at?: string
  reason?: string
}

export interface CreatedPlatformApiKey {
  id: number
  tenant_id: number
  user_id: number
  name: string
  key_prefix: string
  status: string
  expires_at: string | null
  created_at: string
  plaintext_bearer: string
}
