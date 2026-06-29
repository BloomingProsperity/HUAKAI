import { apiGet, apiSend } from '../../lib/api'
import type {
  CredentialListResponse,
  CredentialMetadata,
  CredentialStateValue,
  CredentialWriteBody,
} from './credentials'

/*
 * 账号凭证子系统数据访问层。所有端点挂在 provider-accounts 组下:
 *   /admin/v1/provider-accounts/{id}/credentials...(routes.go:955 MountAdminCredentialRoutes
 *    挂在 mountProviderAccountAdminRoutes,前缀 /admin/v1/provider-accounts,同时有
 *    /v1/admin/provider-accounts 别名,routes.go:975-976)。
 * 经 lib/api 的 tokenForPath 自动注入 admin Bearer(/admin/v1 与 /v1/admin 前缀都识别)。
 *
 * 端点真实性(backend/internal/gatewayhttp/admin_credentials_handler.go:71-77 MountAdminCredentialRoutes):
 *   GET    /{id}/credentials?tenant_id=        列凭证(handler:129,返回 {credentials:[metadata]})
 *   POST   /{id}/credentials                   新增(handler:149,201+CredentialMetadata)
 *   POST   /{id}/credentials/{cid}/rotate      轮换(handler:175,200+CredentialMetadata)
 *   PATCH  /{id}/credentials/{cid}/state       置状态(handler:198,200 {id,state})
 *   DELETE /{id}/credentials/{cid}             删除(handler:225,body 带 tenant_id+reason)
 *
 * SECRET-MASK:create/rotate 的 body.credentials 是 secret 唯一出口;所有响应类型
 * 都是 metadata,不含 secret。本层不打印、不缓存 body,调用方提交后即丢弃 body 引用。
 */

const BASE = '/admin/v1/provider-accounts'

/** 列某账号的凭证(只 metadata)。GET /{id}/credentials?tenant_id=N。 */
export async function listAccountCredentials(
  accountId: number,
  tenantId: number,
  signal?: AbortSignal,
): Promise<CredentialMetadata[]> {
  const res = await apiGet<CredentialListResponse>(`${BASE}/${accountId}/credentials`, {
    query: { tenant_id: tenantId },
    signal,
  })
  // 后端可能在零凭证时返回 {credentials:null};归一成数组。
  return res.credentials ?? []
}

/**
 * 新增凭证。POST /{id}/credentials,body=credentialWriteRequest,返回 201+CredentialMetadata。
 * SECRET-MASK:body.credentials 是原始 secret;响应只 metadata。
 */
export async function createAccountCredential(
  accountId: number,
  body: CredentialWriteBody,
): Promise<CredentialMetadata> {
  return apiSend<CredentialMetadata>('POST', `${BASE}/${accountId}/credentials`, body)
}

/**
 * 轮换凭证(替换活跃 secret,破坏性)。POST /{id}/credentials/{cid}/rotate,
 * body.credentials=新 secret,返回 CredentialMetadata。UI 须二次确认。
 */
export async function rotateAccountCredential(
  accountId: number,
  credentialId: number,
  body: CredentialWriteBody,
): Promise<CredentialMetadata> {
  return apiSend<CredentialMetadata>(
    'POST',
    `${BASE}/${accountId}/credentials/${credentialId}/rotate`,
    body,
  )
}

/** 置状态(active/disabled 等)。PATCH /{id}/credentials/{cid}/state,返回 {id,state}。 */
export async function setAccountCredentialState(
  accountId: number,
  credentialId: number,
  body: { tenant_id: number; state: CredentialStateValue; reason?: string },
): Promise<{ id: number; state: string }> {
  return apiSend<{ id: number; state: string }>(
    'PATCH',
    `${BASE}/${accountId}/credentials/${credentialId}/state`,
    body,
  )
}

/**
 * 删除凭证(破坏性)。DELETE /{id}/credentials/{cid}。
 * 后端 handler:225 从 body 读 credentialStateRequest{tenant_id,reason};DELETE 仍带 body。
 * UI 须二次确认。返回 {id,deleted}。
 */
export async function deleteAccountCredential(
  accountId: number,
  credentialId: number,
  body: { tenant_id: number; reason?: string },
): Promise<{ id: number; deleted: boolean }> {
  return apiSend<{ id: number; deleted: boolean }>(
    'DELETE',
    `${BASE}/${accountId}/credentials/${credentialId}`,
    body,
  )
}
