'use client';

import { useState, useEffect, useCallback } from 'react';
import type { FormEvent } from 'react';
import { RenewCredentialsForbiddenError, listRenewStatus } from '../../lib/api/renew';
import type { AuthCredentialRenewState, AuthCredentialRenewStatus } from '../../lib/api/types';

const PAGE_SIZE = 100;

type StateMeta = { color: string; label: string };

const STATE_META: Partial<Record<AuthCredentialRenewState, StateMeta>> = {
  active: { color: '#3fb950', label: 'active' },
  refreshing: { color: '#58a6ff', label: 'refreshing' },
  refreshing_with_grace: { color: '#79c0ff', label: 'refreshing_with_grace' },
  expired: { color: '#f85149', label: 'expired' },
  temp_unschedulable: { color: '#d29922', label: 'temp_unschedulable' },
  needs_rotation: { color: '#f2cc60', label: 'needs_rotation' },
  revoked: { color: '#8b949e', label: 'revoked' },
  operator_attention: { color: '#ff7b72', label: 'operator_attention' },
};

function stateMetaFor(state: AuthCredentialRenewState): StateMeta {
  return STATE_META[state] ?? { color: '#8b949e', label: state || 'unknown' };
}

function fmtDate(s: string | null | undefined): string {
  if (!s) return '—';
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) {
    return s;
  }
  return d.toLocaleString('en-US', { dateStyle: 'medium', timeStyle: 'medium' });
}

function formatUnknownError(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function fmtLastResult(entry: AuthCredentialRenewStatus): string {
  const parts: string[] = [];
  if (entry.last_refresh_outcome) parts.push(`outcome: ${entry.last_refresh_outcome}`);
  if (entry.failure_class) parts.push(`class: ${entry.failure_class}`);
  if (entry.failure_count > 0) parts.push(`failures: ${entry.failure_count}`);
  if (parts.length === 0) return '—';
  return parts.join(' · ');
}

function parseTenantFilter(raw: string): { tenantId?: number; error?: string } {
  const trimmed = raw.trim();
  if (!trimmed) return {};
  const tenantId = Number(trimmed);
  if (!Number.isSafeInteger(tenantId) || tenantId <= 0) {
    return { error: 'Tenant ID must be a positive integer.' };
  }
  return { tenantId };
}

export default function RenewPage() {
  const [entries, setEntries] = useState<AuthCredentialRenewStatus[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [permissionError, setPermissionError] = useState('');
  const [tenantFilter, setTenantFilter] = useState('');
  const [tenantId, setTenantId] = useState<number | undefined>(undefined);

  const load = useCallback(async (cursor?: string) => {
    const append = Boolean(cursor);
    setLoading(true);
    setError('');
    setPermissionError('');
    try {
      const res = await listRenewStatus({ limit: PAGE_SIZE, cursor, tenantId });
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
  }, [tenantId]);

  useEffect(() => { void load(); }, [load]);

  function handleTenantSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const parsed = parseTenantFilter(tenantFilter);
    if (parsed.error) {
      setEntries([]);
      setNextCursor(null);
      setPermissionError('');
      setError(parsed.error);
      return;
    }
    if (parsed.tenantId === tenantId) {
      void load();
      return;
    }
    setTenantId(parsed.tenantId);
  }

  function clearTenantFilter() {
    setTenantFilter('');
    if (tenantId === undefined) {
      void load();
      return;
    }
    setTenantId(undefined);
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
        <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
          Panel 5 — Auth Credential Renew Status
        </h1>
        <button onClick={() => { void load(); }} disabled={loading} style={{ marginLeft: 'auto', background: '#21262d' }}>
          {loading ? 'Refreshing...' : 'Refresh'}
        </button>
      </div>

      <form onSubmit={handleTenantSubmit} style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
        <label htmlFor="renew-tenant-id" style={{ fontSize: '0.8rem', color: '#8b949e' }}>
          Tenant ID
        </label>
        <input
          id="renew-tenant-id"
          inputMode="numeric"
          pattern="[0-9]*"
          placeholder="All tenants"
          value={tenantFilter}
          onChange={(e) => setTenantFilter(e.target.value)}
          style={{
            background: '#0d1117',
            border: '1px solid #30363d',
            borderRadius: 6,
            color: '#e6edf3',
            fontSize: '0.8rem',
            padding: '0.45rem 0.6rem',
            width: 160,
          }}
        />
        <button type="submit" disabled={loading} style={{ background: '#21262d' }}>
          Apply
        </button>
        <button type="button" onClick={clearTenantFilter} disabled={loading} style={{ background: '#21262d' }}>
          Clear
        </button>
        <span style={{ color: '#8b949e', fontSize: '0.75rem' }}>
          Scope: {tenantId === undefined ? 'token scope' : `tenant #${tenantId}`}
        </span>
      </form>

      {error && <div className="error-msg">{error}</div>}
      {permissionError && <div className="not-impl-msg">{permissionError}</div>}

      <table className="obs-table">
        <thead>
          <tr>
            <th>Tenant</th>
            <th>Account</th>
            <th>Vendor</th>
            <th>Auth Mode</th>
            <th>Version</th>
            <th>State</th>
            <th>Last Refresh</th>
            <th>Refresh Before</th>
            <th>Last Result</th>
            <th>Actions</th>
          </tr>
        </thead>
        <tbody>
          {entries.length === 0 ? (
            <tr>
              <td colSpan={10} style={{ textAlign: 'center', color: '#484f58' }}>
                {permissionError ? 'Admin permission required' : error ? 'Load failed' : loading ? 'Loading...' : 'No credentials'}
              </td>
            </tr>
          ) : entries.map((entry) => {
            const stateMeta = stateMetaFor(entry.state);
            const hasFailure = Boolean(entry.failure_class) || entry.failure_count > 0;
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
                <td style={{ color: '#8b949e' }}>{entry.credential_version}</td>
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
                  style={{ color: hasFailure ? '#f85149' : '#8b949e', fontSize: '0.75rem', maxWidth: 240, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  title={entry.last_refresh_outcome ? `last_refresh_outcome: ${entry.last_refresh_outcome}` : undefined}
                >
                  {fmtLastResult(entry)}
                </td>
                <td>
                  <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center', flexWrap: 'wrap' }}>
                    <button
                      disabled
                      style={{ fontSize: '0.75rem', padding: '0.25rem 0.6rem', background: '#21262d' }}
                      title="No backend endpoint is exposed for manual renew triggers."
                    >
                      Trigger Renew
                    </button>
                    <span style={{ color: '#8b949e', fontSize: '0.72rem' }}>
                      Backend endpoint not implemented
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
            {loading ? 'Loading...' : 'Load More'}
          </button>
        </div>
      )}
    </div>
  );
}
