import { describe, expect, it } from 'vitest'
import { keyStatusLabel, mapAuditVerification, mapTrustVerification, parseTrustProofJSON } from './trust'
import type { AuditVerifyResponse, TrustVerifyResponse } from './types'

const ROOT_A = 'a'.repeat(64)
const ROOT_B = 'b'.repeat(64)

function audit(over: Partial<AuditVerifyResponse['chain_proof']> = {}): AuditVerifyResponse {
  return {
    ledger_entry: {
      ledger_id: 'ledger-1',
      timestamp: '2026-07-12T00:00:00Z',
      request_id: 'req-1',
      tenant_scope_ref: 'scope-1',
      hop_chain: [],
    },
    chain_proof: {
      prev_merkle_root: ROOT_A,
      merkle_root: ROOT_B,
      signature: 'sig',
      pubkey_fingerprint: '0123456789abcdef',
      signature_valid: true,
      key_status: 'active',
      ...over,
    },
  }
}

function trust(over: Partial<TrustVerifyResponse> = {}): TrustVerifyResponse {
  return {
    valid: true,
    status: 'signed-only',
    signature_valid: true,
    key_status: 'active',
    canonical_hash: ROOT_A,
    schema_version: 'trust.receipt.v1',
    ...over,
  }
}

describe('verify 结果映射纯函数', () => {
  it('审计签名明确通过且双 Merkle 根齐备才映射为绿色通过', () => {
    const result = mapAuditVerification(audit())
    expect(result).toMatchObject({ passed: true, tone: 'ok', label: '证明通过', signature: '签名通过' })
    expect(result.chain).toContain('锚点')
  })

  it('删掉签名结论或链锚点都会映射为红色失败', () => {
    const unsigned = mapAuditVerification(audit({ signature_valid: undefined }))
    const chainMissing = mapAuditVerification(audit({ merkle_root: 'not-a-root' }))
    expect(unsigned).toMatchObject({ passed: false, tone: 'crit', signature: '签名失败' })
    expect(chainMissing).toMatchObject({ passed: false, tone: 'crit', chain: '链证明缺失' })
  })

  it('密码学签名为真但密钥已吊销，仍必须红色失败', () => {
    const result = mapTrustVerification(trust({ valid: false, key_status: 'revoked', reason: 'key_revoked' }))
    expect(result).toMatchObject({ passed: false, tone: 'crit', label: '证明失败', signature: '签名通过' })
    expect(result.detail).toContain('吊销')
  })

  it('trust valid=true 但 signature_valid=false 不能误报通过', () => {
    expect(mapTrustVerification(trust({ signature_valid: false, reason: 'signature_mismatch' }))).toMatchObject({
      passed: false,
      tone: 'crit',
      signature: '签名失败',
    })
  })

  it('未知或新增密钥状态失败闭合，不能因 valid=true 自动放行', () => {
    expect(mapTrustVerification(trust({ key_status: 'future_state' }))).toMatchObject({ passed: false, tone: 'crit' })
    expect(mapAuditVerification(audit({ key_status: '' }))).toMatchObject({ passed: false, tone: 'crit' })
  })
})

describe('parseTrustProofJSON', () => {
  it('只提取后端接收的 payload/signature/pubkey_fingerprint', () => {
    expect(parseTrustProofJSON(JSON.stringify({
      payload: { schema_version: 'trust.receipt.v1', request_id: 'req-1' },
      signature: ' sig ',
      pubkey_fingerprint: ' 0123456789abcdef ',
      ignored: '不下发',
    }))).toEqual({
      payload: { schema_version: 'trust.receipt.v1', request_id: 'req-1' },
      signature: 'sig',
      pubkey_fingerprint: '0123456789abcdef',
    })
  })

  it('任意 JSON 或缺签名信封字段都不能误送 verify', () => {
    expect(() => parseTrustProofJSON('{"request_id":"req-1"}')).toThrow('payload')
    expect(() => parseTrustProofJSON('{bad json')).toThrow('有效')
  })
})

describe('keyStatusLabel', () => {
  it('轮换态不误写成吊销态', () => {
    expect(keyStatusLabel('rotated')).toBe('已轮换')
    expect(keyStatusLabel('revoked')).toBe('已吊销')
  })
})
