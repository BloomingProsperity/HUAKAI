import { beforeEach, describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', () => ({ apiGet: client.get, apiSend: client.send }))

import {
  getAuditMerkleTree,
  getAuditPubkeyByFingerprint,
  getCurrentAuditPubkey,
  getWellKnownPubkeys,
  listAuditPubkeys,
  verifyAuditEntry,
  verifyAuditEntryByQuery,
  verifyTrustProof,
} from './api'

describe('信任验证 API 接线', () => {
  beforeEach(() => {
    client.get.mockReset()
    client.send.mockReset()
    client.get.mockResolvedValue({})
    client.send.mockResolvedValue({})
  })

  it('平台公钥四个 GET 路径逐条锁定，指纹按路径段编码', async () => {
    const controller = new AbortController()
    await getWellKnownPubkeys(controller.signal)
    await getCurrentAuditPubkey(controller.signal)
    await listAuditPubkeys(controller.signal)
    await getAuditPubkeyByFingerprint(' abcd ef? ', controller.signal)
    expect(client.get.mock.calls).toEqual([
      ['/.well-known/huakai-pubkey.json', { signal: controller.signal }],
      ['/v1/audit/pubkey', { signal: controller.signal }],
      ['/v1/audit/pubkeys', { signal: controller.signal }],
      ['/v1/audit/pubkey/abcd%20ef%3F', { signal: controller.signal }],
    ])
  })

  it('审计证明 POST 锁定 request_id + tenant_scope_ref body', async () => {
    const body = { request_id: 'req_42', tenant_scope_ref: 'tsr_abc' }
    await verifyAuditEntry(body)
    expect(client.send).toHaveBeenCalledWith('POST', '/v1/audit/verify', body, { signal: undefined })
  })

  it('审计证明 GET 使用同名 query，不把 tenant scope 丢掉', async () => {
    const body = { request_id: 'req_42', tenant_scope_ref: 'tsr_abc' }
    await verifyAuditEntryByQuery(body)
    expect(client.get).toHaveBeenCalledWith('/v1/audit/verify', {
      query: body,
      signal: undefined,
    })
  })

  it('Merkle 根固定 GET /v1/audit/merkle-tree.json', async () => {
    await getAuditMerkleTree()
    expect(client.get).toHaveBeenCalledWith('/v1/audit/merkle-tree.json', { signal: undefined })
  })

  it('粘贴证明固定 POST /v1/trust/verify 与真实三字段 body', async () => {
    const body = {
      payload: { schema_version: 'trust.receipt.v1', request_id: 'req_42' },
      signature: 'base64-signature',
      pubkey_fingerprint: '0123456789abcdef',
    }
    await verifyTrustProof(body)
    expect(client.send).toHaveBeenCalledWith('POST', '/v1/trust/verify', body, { signal: undefined })
  })
})
