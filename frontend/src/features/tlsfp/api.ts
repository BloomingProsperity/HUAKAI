import { apiGet, apiSend } from '../../lib/api'
import { buildTenantQuery } from './tlsfp'
import type {
  DeleteResponse,
  ProfileCreateRequest,
  ProfileListResponse,
  ProfileResponse,
  ProfileUpdateRequest,
  SettableStatus,
  TLSFingerprintProfile,
} from './types'

/*
 * TLS 指纹 profile(出口拟真)数据访问层。所有端点挂在 /v1/admin/tls-fingerprint-profiles,
 * 经 tokenForPath(/v1/admin 前缀)自动带 admin Bearer。
 * 端点真实性见 backend/internal/tlsfphttp/handler.go(MountTLSFPAdminRoutes:87)
 * + cmd/gateway/routes.go:1104。platform_admin 角色下 tenant_id 必填(handler.go:101)。
 */

const BASE = '/v1/admin/tls-fingerprint-profiles'

/** 列某租户的 profile。GET /v1/admin/tls-fingerprint-profiles?tenant_id=N(handler.go:96)。 */
export async function listProfiles(
  tenantId: number,
  signal?: AbortSignal,
): Promise<ProfileListResponse> {
  return apiGet<ProfileListResponse>(BASE, { query: buildTenantQuery(tenantId), signal })
}

/** 取单个 profile。GET /v1/admin/tls-fingerprint-profiles/{id}?tenant_id=N(handler.go:114)。 */
export async function getProfile(
  id: number,
  tenantId: number,
  signal?: AbortSignal,
): Promise<TLSFingerprintProfile> {
  const resp = await apiGet<ProfileResponse>(`${BASE}/${id}`, {
    query: buildTenantQuery(tenantId),
    signal,
  })
  return resp.profile
}

/** 新建 profile。POST /v1/admin/tls-fingerprint-profiles(tenant_id 在 body,handler.go:136)。 */
export async function createProfile(body: ProfileCreateRequest): Promise<TLSFingerprintProfile> {
  const resp = await apiSend<ProfileResponse>('POST', BASE, body)
  return resp.profile
}

/**
 * 全字段内容更新。PUT /v1/admin/tls-fingerprint-profiles/{id}?tenant_id=N(handler.go:160)。
 * 后端 DisallowUnknownFields:body 不可含 tenant_id/id/status(夹带即 400)。
 */
export async function updateProfile(
  id: number,
  tenantId: number,
  body: ProfileUpdateRequest,
): Promise<TLSFingerprintProfile> {
  const resp = await apiSend<ProfileResponse>('PUT', `${BASE}/${id}`, body, {
    query: buildTenantQuery(tenantId),
  })
  return resp.profile
}

/**
 * 改状态(改动型运维动作,UI 须二次确认)。
 * POST /v1/admin/tls-fingerprint-profiles/{id}/status?tenant_id=N(handler.go:192)。
 * 管理员仅可设 active / disabled(service.go:27)。
 */
export async function setProfileStatus(
  id: number,
  tenantId: number,
  status: SettableStatus,
): Promise<TLSFingerprintProfile> {
  const resp = await apiSend<ProfileResponse>('POST', `${BASE}/${id}/status`, { status }, {
    query: buildTenantQuery(tenantId),
  })
  return resp.profile
}

/**
 * 软删除 profile(破坏性动作,UI 须二次确认)。
 * DELETE /v1/admin/tls-fingerprint-profiles/{id}?tenant_id=N(handler.go:218,resp {deleted,id})。
 */
export async function deleteProfile(id: number, tenantId: number): Promise<DeleteResponse> {
  return apiSend<DeleteResponse>('DELETE', `${BASE}/${id}`, undefined, {
    query: buildTenantQuery(tenantId),
  })
}
