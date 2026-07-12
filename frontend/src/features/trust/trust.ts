import type {
  AuditVerifyResponse,
  TrustVerifyRequest,
  TrustVerifyResponse,
  VerificationPresentation,
} from './types'

const ROOT_HEX = /^[0-9a-f]{64}$/i

const REASON_LABELS: Record<string, string> = {
  invalid_json: '证明 JSON 无效',
  required_field_missing: '证明缺少必填字段',
  payload_invalid: '载荷不是受支持的 HUAKAI 信任回执',
  unknown_signer: '找不到签名公钥',
  invalid_signature: '签名编码无效',
  signature_mismatch: '签名与载荷不匹配',
  signature_verify_error: '签名校验过程失败',
  key_revoked: '签名密钥已吊销',
  signature_outside_key_window: '签名时间不在密钥有效期内',
  canonical_hash_mismatch: '规范化摘要不一致',
}

export function reasonLabel(reason: string | undefined): string {
  const value = (reason ?? '').trim()
  return value ? (REASON_LABELS[value] ?? value) : '未返回失败原因'
}

export function keyStatusLabel(status: string | undefined): string {
  const value = (status ?? '').trim().toLowerCase()
  if (value === 'active') return '当前有效'
  if (value === 'rotated') return '已轮换'
  if (value === 'revoked') return '已吊销'
  if (value === 'unknown') return '未知密钥'
  return value || '—'
}

function keyTrusted(status: string | undefined): boolean {
  const value = (status ?? '').trim().toLowerCase()
  return value === 'active' || value === 'rotated'
}

/** 审计端点没有 chain_valid 字段，只能如实展示签名结论与链锚点是否齐备。 */
export function mapAuditVerification(response: AuditVerifyResponse): VerificationPresentation {
  const proof = response.chain_proof
  const signaturePassed = proof.signature_valid === true
  const chainPresent = ROOT_HEX.test(proof.prev_merkle_root) && ROOT_HEX.test(proof.merkle_root)
  const passed = signaturePassed && chainPresent && keyTrusted(proof.key_status) && !proof.reason
  let detail = '签名有效，且已返回该账本条目的前后 Merkle 锚点。'
  if (!passed) {
    if (proof.reason) detail = reasonLabel(proof.reason)
    else if (!signaturePassed) detail = proof.signature_valid === false ? '签名校验未通过' : '后端未提供签名校验结论'
    else if (!chainPresent) detail = '链证明缺少有效的 Merkle 根'
    else detail = `密钥状态不可采信：${keyStatusLabel(proof.key_status)}`
  }
  return {
    passed,
    tone: passed ? 'ok' : 'crit',
    label: passed ? '证明通过' : '证明失败',
    signature: signaturePassed ? '签名通过' : '签名失败',
    chain: chainPresent ? '链锚点已返回' : '链证明缺失',
    detail,
  }
}

export function mapTrustVerification(response: TrustVerifyResponse): VerificationPresentation {
  const passed = response.valid && response.signature_valid && keyTrusted(response.key_status) && !response.reason
  return {
    passed,
    tone: passed ? 'ok' : 'crit',
    label: passed ? '证明通过' : '证明失败',
    signature: response.signature_valid ? '签名通过' : '签名失败',
    detail: passed ? '载荷、签名与签名密钥均可采信。' : reasonLabel(response.reason),
  }
}

/** 粘贴区只接受后端真实签名信封，拒绝把任意 JSON 误当成 proof。 */
export function parseTrustProofJSON(raw: string): TrustVerifyRequest {
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    throw new Error('请输入有效的证明 JSON')
  }
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('证明 JSON 必须是对象')
  }
  const proof = value as Record<string, unknown>
  const payload = proof.payload
  const payloadValid = typeof payload === 'string' || (payload !== null && typeof payload === 'object' && !Array.isArray(payload))
  if (!payloadValid) throw new Error('证明缺少对象或 Base64 字符串 payload')
  if (typeof proof.signature !== 'string' || !proof.signature.trim()) throw new Error('证明缺少 signature')
  if (typeof proof.pubkey_fingerprint !== 'string' || !proof.pubkey_fingerprint.trim()) {
    throw new Error('证明缺少 pubkey_fingerprint')
  }
  return {
    payload,
    signature: proof.signature.trim(),
    pubkey_fingerprint: proof.pubkey_fingerprint.trim(),
  }
}

export function formatTrustTime(value: string | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}
