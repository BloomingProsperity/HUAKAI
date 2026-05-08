// Renew status — 后端尚无此端点，全部 MOCK
// 接口签名预留形态：GET /admin/v1/auth-credentials/{id}/renew-status
// 当后端实现后，将 MOCK_DATA 替换为真实 apiGet 调用即可

import type { AuthCredentialRenewStatus } from './types';

// ⚠ MOCK — 后端暂无此端点
const MOCK_DATA: AuthCredentialRenewStatus[] = [
  {
    account_id: 1001,
    account_name: 'claude-prod-01',
    last_renew_at: '2026-05-08T10:00:00Z',
    next_renew_at: '2026-05-08T22:00:00Z',
    renew_status: 'idle',
    error_msg: null,
  },
  {
    account_id: 1002,
    account_name: 'claude-prod-02',
    last_renew_at: '2026-05-08T09:30:00Z',
    next_renew_at: null,
    renew_status: 'renewing',
    error_msg: null,
  },
  {
    account_id: 1003,
    account_name: 'claude-staging-01',
    last_renew_at: '2026-05-07T18:00:00Z',
    next_renew_at: '2026-05-08T18:00:00Z',
    renew_status: 'failed',
    error_msg: 'OAuth endpoint returned 401: token_revoked',
  },
];

// listRenewStatus — ⚠ MOCK
// 真实形态：GET /admin/v1/auth-credentials (带 renew_status 信息)
export async function listRenewStatus(): Promise<AuthCredentialRenewStatus[]> {
  // 模拟网络延迟
  await new Promise((r) => setTimeout(r, 120));
  return MOCK_DATA.map((d) => ({ ...d }));
}

// triggerRenew — ⚠ MOCK
// 真实形态：POST /admin/v1/auth-credentials/{id}/renew
export async function triggerRenew(accountId: number): Promise<void> {
  await new Promise((r) => setTimeout(r, 200));
  // mock：将对应条目状态设为 renewing
  const entry = MOCK_DATA.find((d) => d.account_id === accountId);
  if (entry) {
    entry.renew_status = 'renewing';
    entry.error_msg = null;
  }
}
