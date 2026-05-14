import type { AuditBundle } from './audit-api';

const rootA = '0000000000000000000000000000000000000000000000000000000000000000';
const rootB = 'a2b651f79e8c4d20c0f61a5195dfc6fb3ac8b8d2e513cb279a55f3c78d22b450';

export const MOCK_AUDIT_BUNDLE: AuditBundle = {
  source: 'mock',
  tree: {
    latest_merkle_root: rootB,
    size: 42,
    total_entries: 42,
  },
  verify: {
    ledger_entry: {
      ledger_id: 'ledger_01HX_T6_mock1',
      timestamp: '2026-05-14T09:30:12.244Z',
      request_id: 'mock1',
      tenant_id: 7,
      model_chain: {
        requested: 'gpt-4.1',
        route_decided: 'gpt-4.1',
        upstream_reported: 'gpt-4.1',
      },
      hop_chain: [
        {
          hop: 'ingress',
          ts: '2026-05-14T09:30:12.020Z',
          request_id: 'mock1',
          duration_ms: 4,
          detail: { status: 'ok', client_shape: 'openai_chat' },
        },
        {
          hop: 'router',
          ts: '2026-05-14T09:30:12.036Z',
          route_id: 'route_cn_prod',
          duration_ms: 9,
          detail: { status: 'ok', alias: 'premium-chat' },
        },
        {
          hop: 'pool',
          ts: '2026-05-14T09:30:12.052Z',
          pool_id: 'pool_high_trust',
          duration_ms: 11,
          detail: { status: 'ok', selector: 'pasr' },
        },
        {
          hop: 'account',
          ts: '2026-05-14T09:30:12.068Z',
          account_id_hash: 'acct_sha256_6f3a9c',
          duration_ms: 7,
          detail: { status: 'ok', lease: 'slot_118' },
        },
        {
          hop: 'provider',
          ts: '2026-05-14T09:30:12.088Z',
          provider: 'openai',
          endpoint: 'https://api.openai.com/v1/chat/completions',
          duration_ms: 128,
          detail: { status: 'ok', status_code: 200 },
        },
        {
          hop: 'response',
          ts: '2026-05-14T09:30:12.244Z',
          duration_ms: 156,
          detail: { status: 'ok', header_model_delivered: 'gpt-4.1' },
        },
      ],
    },
    chain_proof: {
      prev_merkle_root: rootA,
      merkle_root: rootB,
      signature: 'xT6MockSignatureBase64ForDevOnlyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=',
      pubkey_fingerprint: '4f8b24ad15d9c330',
    },
  },
};

export function getMockAuditBundle(requestId: string): AuditBundle {
  return {
    ...MOCK_AUDIT_BUNDLE,
    verify: {
      ...MOCK_AUDIT_BUNDLE.verify,
      ledger_entry: {
        ...MOCK_AUDIT_BUNDLE.verify.ledger_entry,
        request_id: requestId,
        hop_chain: MOCK_AUDIT_BUNDLE.verify.ledger_entry.hop_chain.map((hop) => (
          hop.request_id ? { ...hop, request_id: requestId } : hop
        )),
      },
    },
  };
}
