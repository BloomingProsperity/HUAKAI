export interface WellKnownKey {
  kty: string
  crv: string
  kid: string
  x: string
  alg: string
  use: string
  status: string
  effective_from?: string
  effective_to?: string
  revoked_at?: string
  reason_class?: string
}

export interface WellKnownRevocation {
  fingerprint: string
  revoked_at?: string
  reason_class?: string
}

export interface WellKnownPubkeysResponse {
  schema_version: string
  generated_at: string
  next_rotation_after: string
  keys: WellKnownKey[]
  current: string
  revoked: WellKnownRevocation[]
}

export interface AuditPubkey {
  algorithm: string
  fingerprint: string
  pubkey_fingerprint: string
  public_key_base64: string
  key_status?: string
  effective_from?: string
  effective_to?: string
}

export interface AuditPubkeysResponse {
  keys: AuditPubkey[]
}

export interface AuditVerifyRequest {
  request_id: string
  tenant_scope_ref: string
}

export interface AuditLedgerEntry {
  ledger_id: string
  timestamp: string
  request_id: string
  tenant_scope_ref?: string
  hop_chain: unknown[]
  model_chain?: unknown
}

export interface AuditChainProof {
  prev_merkle_root: string
  merkle_root: string
  signature: string
  pubkey_fingerprint: string
  signature_valid?: boolean
  key_status?: string
  reason?: string
}

export interface AuditVerifyResponse {
  ledger_entry: AuditLedgerEntry
  chain_proof: AuditChainProof
}

export interface AuditMerkleTreeResponse {
  latest_merkle_root: string
  size: number
}

/** 两类 verify 入参不同，避免把 request_id 证明误送到此端点。 */
export interface TrustVerifyRequest {
  payload: unknown
  signature: string
  pubkey_fingerprint: string
}

export interface TrustVerifyResponse {
  valid: boolean
  status: string
  signature_valid: boolean
  key_status: string
  reason?: string
  fields_mismatch?: string[]
  canonical_hash: string
  schema_version: string
}

export interface VerificationPresentation {
  passed: boolean
  tone: 'ok' | 'crit'
  label: string
  signature: string
  chain?: string
  detail: string
}
