'use client';

import { useState, useEffect, useRef, useCallback } from 'react';
import { getDebugVars, listUsageRecords, listBillingClaims } from '../../lib/api/observability';
import { ApiError } from '../../lib/api/client';
import type { UsageRecord, BillingLedgerClaim } from '../../lib/api/types';

// 从 expvar 提取 per-clientid request count
// clientid_request_count 是嵌套 map：{ "cursor": N, "claude_code": M, ... }
// key 是客户端标识字符串（非 provider_account_id）
interface ClientSelection {
  client_id: string;
  request_count: number;
}

function extractAccountSelections(data: Record<string, unknown>): ClientSelection[] {
  // clientid_request_count 后端返回嵌套 map，不是扁平 dotted key
  const counts = data['clientid_request_count'];
  if (!counts || typeof counts !== 'object') return [];
  return Object.entries(counts as Record<string, unknown>)
    .map(([client_id, val]) => ({ client_id, request_count: typeof val === 'number' ? val : 0 }))
    .sort((a, b) => b.request_count - a.request_count);
}

// 全局 slot 统计（cache token 汇总）
interface SlotStats {
  creation_total: number;
  read_total: number;
  request_count: number;
}

function extractSlotStats(data: Record<string, unknown>): SlotStats {
  function numOf(v: unknown): number {
    if (typeof v === 'number') return v;
    if (typeof v === 'string') { const n = Number(v); return isNaN(n) ? 0 : n; }
    return 0;
  }
  // cache_token_count 后端返回嵌套对象，不是扁平 dotted key
  const g = data['cache_token_count'] as Record<string, unknown> | undefined;
  return {
    creation_total: numOf(g?.creation_total),
    read_total: numOf(g?.read_total),
    request_count: numOf(g?.request_count),
  };
}

const POLL_MS = 3000;

export default function SelectionPage() {
  // 客户端请求计数 + slot 状态（来自 /debug/vars）
  const [selections, setSelections] = useState<ClientSelection[]>([]);
  const [slot, setSlot] = useState<SlotStats | null>(null);
  const [polling, setPolling] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [debugError, setDebugError] = useState('');
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Billing claims
  const [claims, setClaims] = useState<BillingLedgerClaim[]>([]);
  const [claimsLoading, setClaimsLoading] = useState(false);
  const [claimsError, setClaimsError] = useState('');

  // Usage records
  const [usageRecords, setUsageRecords] = useState<UsageRecord[]>([]);
  const [usageLoading, setUsageLoading] = useState(false);
  const [usageError, setUsageError] = useState('');

  // 轮询 debug/vars
  const pollDebug = useCallback(async () => {
    try {
      const data = await getDebugVars();
      setSelections(extractAccountSelections(data));
      setSlot(extractSlotStats(data));
      setLastUpdated(new Date());
      setDebugError('');
    } catch (err: unknown) {
      setDebugError(String(err));
    }
  }, []);

  useEffect(() => {
    if (!polling) return;
    void pollDebug();
    timerRef.current = setInterval(() => { void pollDebug(); }, POLL_MS);
    return () => { if (timerRef.current) clearInterval(timerRef.current); };
  }, [polling, pollDebug]);

  // 加载 billing claims
  async function loadClaims() {
    setClaimsLoading(true);
    setClaimsError('');
    try {
      const res = await listBillingClaims({ limit: 20 });
      setClaims(res.items);
    } catch (err: unknown) {
      // 501 单独提示，与其它错误区分
      if (err instanceof ApiError && err.isNotImplemented()) {
        setClaimsError('__501__');
      } else {
        setClaimsError(String(err));
      }
    } finally {
      setClaimsLoading(false);
    }
  }

  // 加载 usage records
  async function loadUsage() {
    setUsageLoading(true);
    setUsageError('');
    try {
      const res = await listUsageRecords({ limit: 20 });
      setUsageRecords(res.items);
    } catch (err: unknown) {
      if (err instanceof ApiError && err.isNotImplemented()) {
        setUsageError('__501__');
      } else {
        setUsageError(String(err));
      }
    } finally {
      setUsageLoading(false);
    }
  }

  useEffect(() => {
    void loadClaims();
    void loadUsage();
  }, []);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}>
      {/* 标题行 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
        <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
          面板 4 — 账号选中 / Slot / Claim / Usage
        </h1>
        <span className={`poll-badge${polling ? '' : ' stale'}`}>
          {polling ? '● LIVE' : '○ PAUSED'}
        </span>
        <button onClick={() => setPolling((p) => !p)}
          style={{ background: polling ? '#6e40c9' : '#238636', marginLeft: 'auto' }}>
          {polling ? 'Pause' : 'Resume'}
        </button>
      </div>

      {lastUpdated && (
        <div style={{ fontSize: '0.7rem', color: '#484f58' }}>
          debug/vars 上次更新：{lastUpdated.toLocaleTimeString('zh-CN')}（每 {POLL_MS / 1000}s 轮询）
        </div>
      )}

      {/* 三栏布局 */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: '1rem' }}>

        {/* 栏 1：账号选中 + slot */}
        <div style={{ background: '#161b22', border: '1px solid #30363d', borderRadius: 6, padding: '0.75rem' }}>
          <div className="section-title" style={{ marginTop: 0 }}>当前选中 Account（/debug/vars）</div>
          {debugError && <div className="error-msg" style={{ fontSize: '0.75rem' }}>{debugError}</div>}

          {slot && (
            <div style={{ fontSize: '0.75rem', color: '#8b949e', margin: '0.5rem 0', display: 'flex', gap: '1rem', flexWrap: 'wrap' }}>
              <span>requests: <strong style={{ color: '#e6edf3' }}>{slot.request_count}</strong></span>
              <span>cache_create: <strong style={{ color: '#e6edf3' }}>{slot.creation_total}</strong></span>
              <span>cache_read: <strong style={{ color: '#e6edf3' }}>{slot.read_total}</strong></span>
            </div>
          )}

          <table className="obs-table" style={{ fontSize: '0.75rem' }}>
            <thead>
              <tr>
                {/* client identity（如 cursor / claude_code / cody），非 provider_account_id */}
                <th>client identity</th>
                <th>requests</th>
              </tr>
            </thead>
            <tbody>
              {selections.length === 0 ? (
                <tr><td colSpan={2} style={{ color: '#484f58', textAlign: 'center' }}>
                  {debugError ? '加载失败' : '（无数据）'}
                </td></tr>
              ) : selections.map((s) => (
                <tr key={s.client_id}>
                  <td style={{ fontFamily: 'monospace' }}>{s.client_id}</td>
                  <td>{s.request_count}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* 栏 2：Billing Claims */}
        <div style={{ background: '#161b22', border: '1px solid #30363d', borderRadius: 6, padding: '0.75rem' }}>
          <div style={{ display: 'flex', alignItems: 'center', marginBottom: '0.5rem' }}>
            <div className="section-title" style={{ marginTop: 0, marginBottom: 0 }}>已 Claim（billing/claims）</div>
            <button onClick={loadClaims} disabled={claimsLoading}
              style={{ marginLeft: 'auto', background: '#21262d', fontSize: '0.7rem', padding: '0.2rem 0.5rem' }}>
              {claimsLoading ? '…' : '刷新'}
            </button>
          </div>
          {claimsError && (
            claimsError === '__501__'
              ? <div className="not-impl-msg" style={{ fontSize: '0.75rem' }}>后端尚未实现此端点 (501) — 等待 backend wire</div>
              : <div className="error-msg" style={{ fontSize: '0.75rem' }}>{claimsError}</div>
          )}
          <table className="obs-table" style={{ fontSize: '0.7rem' }}>
            <thead>
              <tr>
                <th>id</th>
                <th>model</th>
                <th>status</th>
                <th>cost</th>
              </tr>
            </thead>
            <tbody>
              {claims.length === 0 ? (
                <tr><td colSpan={4} style={{ color: '#484f58', textAlign: 'center' }}>
                  {claimsLoading ? '加载中…' : claimsError ? '失败' : '（无数据）'}
                </td></tr>
              ) : claims.map((c) => (
                <tr key={c.id}>
                  <td style={{ fontFamily: 'monospace' }}>{c.id}</td>
                  <td style={{ color: '#8b949e', maxWidth: 80, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {c.requested_model ?? '—'}
                  </td>
                  <td style={{ color: c.status === 'committed' ? '#3fb950' : c.status === 'aborted' ? '#f85149' : '#d29922' }}>
                    {c.status}
                  </td>
                  <td style={{ fontFamily: 'monospace' }}>{c.actual_cost ?? c.predicted_cost ?? '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        {/* 栏 3：Usage Records */}
        <div style={{ background: '#161b22', border: '1px solid #30363d', borderRadius: 6, padding: '0.75rem' }}>
          <div style={{ display: 'flex', alignItems: 'center', marginBottom: '0.5rem' }}>
            <div className="section-title" style={{ marginTop: 0, marginBottom: 0 }}>Usage Records</div>
            <button onClick={loadUsage} disabled={usageLoading}
              style={{ marginLeft: 'auto', background: '#21262d', fontSize: '0.7rem', padding: '0.2rem 0.5rem' }}>
              {usageLoading ? '…' : '刷新'}
            </button>
          </div>
          {usageError && (
            usageError === '__501__'
              ? <div className="not-impl-msg" style={{ fontSize: '0.75rem' }}>后端尚未实现此端点 (501) — 等待 backend wire</div>
              : <div className="error-msg" style={{ fontSize: '0.75rem' }}>{usageError}</div>
          )}
          <table className="obs-table" style={{ fontSize: '0.7rem' }}>
            <thead>
              <tr>
                <th>id</th>
                <th>model</th>
                <th>in</th>
                <th>out</th>
                <th>cost</th>
                <th>end</th>
              </tr>
            </thead>
            <tbody>
              {usageRecords.length === 0 ? (
                <tr><td colSpan={6} style={{ color: '#484f58', textAlign: 'center' }}>
                  {usageLoading ? '加载中…' : usageError ? '失败' : '（无数据）'}
                </td></tr>
              ) : usageRecords.map((r) => (
                <tr key={r.id}>
                  <td style={{ fontFamily: 'monospace' }}>{r.id}</td>
                  <td style={{ color: '#8b949e', maxWidth: 70, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {r.requested_model ?? '—'}
                  </td>
                  <td>{r.tokens_input ?? 0}</td>
                  <td>{r.tokens_output ?? 0}</td>
                  <td style={{ fontFamily: 'monospace' }}>{r.actual_cost}</td>
                  <td style={{ color: r.end_class === 'stream_end_graceful' ? '#3fb950' : '#d29922', fontSize: '0.65rem' }}>
                    {r.end_class.replace('stream_end_', '').replace('upstream_', 'up_')}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

      </div>
    </div>
  );
}
