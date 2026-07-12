import { apiGet, apiSend } from '../../lib/api'
import type {
  AdminTokenListResponse,
  CreateAdminTokenRequest,
  CreatedAdminToken,
  CreatePlatformApiKeyRequest,
  CreatedPlatformApiKey,
  PlatformApiKeyListResponse,
  RevokeCredentialResponse,
} from './types'

const ADMIN_TOKEN_PATH = '/admin/v1/admin-tokens'
const API_KEY_PATH = '/admin/v1/api-keys'

export async function listAdminTokens(
  limit = 100,
  offset = 0,
  signal?: AbortSignal,
): Promise<AdminTokenListResponse> {
  return apiGet<AdminTokenListResponse>(ADMIN_TOKEN_PATH, { query: { limit, offset }, signal })
}

export async function createAdminToken(body: CreateAdminTokenRequest): Promise<CreatedAdminToken> {
  return apiSend<CreatedAdminToken>('POST', ADMIN_TOKEN_PATH, body)
}

export async function revokeAdminToken(id: number, reason: string): Promise<RevokeCredentialResponse> {
  return apiSend<RevokeCredentialResponse>('POST', `${ADMIN_TOKEN_PATH}/${id}/revoke`, { reason })
}

export async function listPlatformApiKeys(
  tenantId: number,
  limit = 100,
  offset = 0,
  signal?: AbortSignal,
): Promise<PlatformApiKeyListResponse> {
  return apiGet<PlatformApiKeyListResponse>(API_KEY_PATH, {
    query: { tenant_id: tenantId, limit, offset },
    signal,
  })
}

export async function createPlatformApiKey(
  body: CreatePlatformApiKeyRequest,
): Promise<CreatedPlatformApiKey> {
  return apiSend<CreatedPlatformApiKey>('POST', API_KEY_PATH, body)
}

export async function revokePlatformApiKey(
  id: number,
  tenantId: number,
  reason: string,
): Promise<RevokeCredentialResponse> {
  return apiSend<RevokeCredentialResponse>('POST', `${API_KEY_PATH}/${id}/revoke`, {
    tenant_id: tenantId,
    reason,
  })
}
