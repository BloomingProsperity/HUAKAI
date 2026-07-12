import { apiGet, apiSend } from '../../lib/api'
import type {
  AuditMerkleTreeResponse,
  AuditPubkey,
  AuditPubkeysResponse,
  AuditVerifyRequest,
  AuditVerifyResponse,
  TrustVerifyRequest,
  TrustVerifyResponse,
  WellKnownPubkeysResponse,
} from './types'

export function getWellKnownPubkeys(signal?: AbortSignal): Promise<WellKnownPubkeysResponse> {
  return apiGet<WellKnownPubkeysResponse>('/.well-known/huakai-pubkey.json', { signal })
}

export function getCurrentAuditPubkey(signal?: AbortSignal): Promise<AuditPubkey> {
  return apiGet<AuditPubkey>('/v1/audit/pubkey', { signal })
}

export function listAuditPubkeys(signal?: AbortSignal): Promise<AuditPubkeysResponse> {
  return apiGet<AuditPubkeysResponse>('/v1/audit/pubkeys', { signal })
}

export function getAuditPubkeyByFingerprint(fingerprint: string, signal?: AbortSignal): Promise<AuditPubkey> {
  return apiGet<AuditPubkey>(`/v1/audit/pubkey/${encodeURIComponent(fingerprint.trim())}`, { signal })
}

/** POST 与 GET 语义相同；页面用 POST，避免作用域引用出现在地址栏。 */
export function verifyAuditEntry(request: AuditVerifyRequest, signal?: AbortSignal): Promise<AuditVerifyResponse> {
  return apiSend<AuditVerifyResponse>('POST', '/v1/audit/verify', request, { signal })
}

export function verifyAuditEntryByQuery(request: AuditVerifyRequest, signal?: AbortSignal): Promise<AuditVerifyResponse> {
  return apiGet<AuditVerifyResponse>('/v1/audit/verify', {
    query: { request_id: request.request_id, tenant_scope_ref: request.tenant_scope_ref },
    signal,
  })
}

export function getAuditMerkleTree(signal?: AbortSignal): Promise<AuditMerkleTreeResponse> {
  return apiGet<AuditMerkleTreeResponse>('/v1/audit/merkle-tree.json', { signal })
}

export function verifyTrustProof(request: TrustVerifyRequest, signal?: AbortSignal): Promise<TrustVerifyResponse> {
  return apiSend<TrustVerifyResponse>('POST', '/v1/trust/verify', request, { signal })
}
