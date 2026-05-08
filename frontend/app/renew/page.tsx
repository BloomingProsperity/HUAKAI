'use client';

import { useState, useEffect, useCallback } from 'react';
import { listRenewStatus, triggerRenew } from '../../lib/api/renew';
import type { AuthCredentialRenewStatus, RenewStatus } from '../../lib/api/types';

// ⚠ MOCK — 后端尚无 /admin/v1/auth-credentials/{id}/renew-status 端点
// 接口签名已预留，后端实现后替换 renew.ts 的 mock 实现即可

const STATUS_COLOR: Record<RenewStatus, string> = {
  idle: '#3fb950',
  renewing: '#58a6ff',
  failed: '#f85149',
};

const STATUS_LABEL: Record<RenewStatus, string> = {
  idle: '● 空闲',
  renewing: '↻ 续期中',
  failed: '✕ 失败',
};

function fmtDate(s: string | null): string {
  if (!s) return '—';
  try {
    return new Date(s).toLocaleString('zh-CN');
  } catch {
    return s;
  }
}

export default function RenewPage() {
  const [entries, setEntries] = useState<AuthCredentialRenewStatus[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  // 正在触发 renew 的 account_id set
  const [triggering, setTriggering] = useState<Set<number>>(new Set());

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const res = await listRenewStatus();
      setEntries(res);
    } catch (err: unknown) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  async function handleTrigger(accountId: number) {
    setTriggering((s) => new Set(s).add(accountId));
    try {
      await triggerRenew(accountId);
      // 触发后重新加载以反映状态变化
      await load();
    } catch (err: unknown) {
      setError(String(err));
    } finally {
      setTriggering((s) => { const ns = new Set(s); ns.delete(accountId); return ns; });
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      {/* 标题行 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
        <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
          面板 5 — Auth Credential Renew 状态
        </h1>
        {/* MOCK 标记 */}
        <span style={{
          fontSize: '0.65rem', background: '#6e40c9', color: '#fff',
          padding: '0.15rem 0.5rem', borderRadius: 3, fontWeight: 700,
          letterSpacing: '0.05em',
        }}>
          MOCK
        </span>
        <button onClick={load} disabled={loading} style={{ marginLeft: 'auto', background: '#21262d' }}>
          {loading ? '刷新中…' : '刷新'}
        </button>
      </div>

      {/* Mock 说明 */}
      <div style={{ fontSize: '0.75rem', color: '#8b949e', background: '#161b22', border: '1px solid #30363d', borderRadius: 4, padding: '0.5rem 0.75rem' }}>
        后端尚未实现 <code style={{ color: '#58a6ff' }}>GET /admin/v1/auth-credentials/&#123;id&#125;/renew-status</code>。
        此面板展示 mock 数据，接口签名已预留——后端就绪后替换
        <code style={{ color: '#58a6ff' }}> lib/api/renew.ts</code> 中的 mock 实现即可。
      </div>

      {error && <div className="error-msg">{error}</div>}

      {/* Renew 状态表 */}
      <table className="obs-table">
        <thead>
          <tr>
            <th>account_id</th>
            <th>account_name</th>
            <th>last_renew_at</th>
            <th>next_renew_at</th>
            <th>status</th>
            <th>error_msg</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {entries.length === 0 ? (
            <tr><td colSpan={7} style={{ textAlign: 'center', color: '#484f58' }}>
              {loading ? '加载中…' : '（无数据）'}
            </td></tr>
          ) : entries.map((e) => (
            <tr key={e.account_id}>
              <td style={{ fontFamily: 'monospace' }}>{e.account_id}</td>
              <td>{e.account_name}</td>
              <td style={{ fontSize: '0.75rem', color: '#8b949e' }}>{fmtDate(e.last_renew_at)}</td>
              <td style={{ fontSize: '0.75rem', color: '#8b949e' }}>{fmtDate(e.next_renew_at)}</td>
              <td>
                <span style={{ color: STATUS_COLOR[e.renew_status], fontWeight: 600, fontSize: '0.8rem' }}>
                  {STATUS_LABEL[e.renew_status]}
                </span>
              </td>
              <td style={{ color: '#f85149', fontSize: '0.75rem', maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {e.error_msg ?? '—'}
              </td>
              <td>
                <button
                  onClick={() => handleTrigger(e.account_id)}
                  disabled={triggering.has(e.account_id) || e.renew_status === 'renewing'}
                  style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem', background: '#6e40c9' }}>
                  {triggering.has(e.account_id) ? '触发中…' : 'Trigger Renew'}
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
