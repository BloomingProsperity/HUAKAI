'use client';

import { useState, useEffect, useCallback } from 'react';
import { RenewCredentialsForbiddenError, listRenewStatus } from '../../lib/api/renew';
import type { AuthCredentialRenewState, AuthCredentialRenewStatus } from '../../lib/api/types';

const STATE_META: Record<AuthCredentialRenewState, { color: string; label: string }> = {
  active: { color: '#3fb950', label: 'active' },
  refreshing: { color: '#58a6ff', label: 'refreshing' },
  refreshing_with_grace: { color: '#79c0ff', label: 'refreshing_with_grace' },
  expired: { color: '#f85149', label: 'expired' },
  temp_unschedulable: { color: '#d29922', label: 'temp_unschedulable' },
  needs_rotation: { color: '#f2cc60', label: 'needs_rotation' },
  revoked: { color: '#8b949e', label: 'revoked' },
  operator_attention: { color: '#ff7b72', label: 'operator_attention' },
};

function fmtDate(s: string | null | undefined): string {
  if (!s) return '—';
  try {
    return new Date(s).toLocaleString('zh-CN');
  } catch {
    return s;
  }
}

function formatUnknownError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function fmtLastError(entry: AuthCredentialRenewStatus): string {
  const parts: string[] = [];
  if (entry.failure_class) parts.push(entry.failure_class);
  if (entry.failure_count > 0) parts.push(`failures: ${entry.failure_count}`);
  if (parts.length === 0) return '—';
  if (entry.last_refresh_outcome) parts.push(`outcome: ${entry.last_refresh_outcome}`);
  return parts.join(' · ');
}

export default function RenewPage() {
  const [entries, setEntries] = useState<AuthCredentialRenewStatus[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [permissionError, setPermissionError] = useState('');

  const load = useCallback(async (cursor?: string) => {
    const append = Boolean(cursor);
    setLoading(true);
    setError('');
    setPermissionError('');
    try {
      const res = await listRenewStatus({ limit: 100, cursor });
      setEntries((prev) => (append ? [...prev, ...res.items] : res.items));
      setNextCursor(res.next_cursor);
    } catch (err: unknown) {
      if (!append) setEntries([]);
      setNextCursor(null);
      if (err instanceof RenewCredentialsForbiddenError) {
        setPermissionError(err.message);
      } else {
        setError(formatUnknownError(err));
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      {/* 标题行 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
        <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
          Panel 5 — Auth Credential Renew Status
        </h1>
        <button onClick={() => { void load(); }} disabled={loading} style={{ marginLeft: 'auto', background: '#21262d' }}>
          {loading ? '刷新中…' : '刷新'}
        </button>
      </div>

      {error && <div className="error-msg">{error}</div>}
      {permissionError && <div className="not-impl-msg">{permissionError}</div>}

      {/* Renew 状态表：一行一个凭据，避免账号聚合掩盖单凭据失败 */}
      <table className="obs-table">
        <thead>
          <tr>
            <th>Tenant</th>
            <th>Account</th>
            <th>Vendor</th>
            <th>Auth Mode</th>
            <th>State</th>
            <th>Last Refresh</th>
            <th>Refresh Before</th>
            <th>Last Error</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {entries.length === 0 ? (
            <tr>
              <td colSpan={9} style={{ textAlign: 'center', color: '#484f58' }}>
                {permissionError ? '需要平台管理员权限' : error ? '加载失败' : loading ? '加载中…' : '（无凭据）'}
              </td>
            </tr>
          ) : entries.map((entry) => {
            const stateMeta = STATE_META[entry.state];
            return (
              <tr key={entry.id}>
                <td>
                  <div>{entry.tenant_name}</div>
                  <div style={{ fontFamily: 'monospace', fontSize: '0.7rem', color: '#8b949e' }}>
                    #{entry.tenant_id}
                  </div>
                </td>
                <td>
                  <div>{entry.account_name}</div>
                  <div style={{ fontFamily: 'monospace', fontSize: '0.7rem', color: '#8b949e' }}>
                    #{entry.account_id}
                  </div>
                </td>
                <td style={{ color: '#8b949e' }}>{entry.vendor}</td>
                <td style={{ color: '#8b949e' }}>{entry.auth_mode}</td>
                <td>
                  <span style={{ color: stateMeta.color, fontWeight: 600, fontSize: '0.8rem' }}>
                    ● {stateMeta.label}
                  </span>
                </td>
                <td style={{ fontSize: '0.75rem', color: '#8b949e' }}>
                  {fmtDate(entry.last_refresh_at)}
                </td>
                <td
                  style={{ fontSize: '0.75rem', color: '#8b949e' }}
                  title={entry.access_expires_at ? `Access expires: ${fmtDate(entry.access_expires_at)}` : undefined}
                >
                  {fmtDate(entry.refresh_before_at)}
                </td>
                <td
                  style={{ color: '#f85149', fontSize: '0.75rem', maxWidth: 240, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  title={entry.last_refresh_outcome ? `last_refresh_outcome: ${entry.last_refresh_outcome}` : undefined}
                >
                  {fmtLastError(entry)}
                </td>
                <td>
                  <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
                    <button
                      disabled
                      style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem', background: '#21262d' }}
                    >
                      Trigger Renew
                    </button>
                    <span style={{ color: '#8b949e', fontSize: '0.72rem' }}>
                      自动续期·由系统调度
                    </span>
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      {nextCursor && (
        <div style={{ display: 'flex', justifyContent: 'center' }}>
          <button
            onClick={() => { void load(nextCursor); }}
            disabled={loading}
            style={{ background: '#21262d' }}
          >
            {loading ? '加载中…' : '加载更多'}
          </button>
        </div>
      )}
    </div>
  );
}
