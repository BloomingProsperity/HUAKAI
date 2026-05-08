'use client';

import { useState, useEffect, useRef } from 'react';

// expvar 返回的顶层结构（嵌套 map，不是扁平 dotted key）
interface CacheTokenCount {
  creation_total: number;
  read_total: number;
  request_count: number;
}

interface ExpvarPayload {
  // 全局 cache token 计数器（嵌套对象）
  cache_token_count?: CacheTokenCount;
  // per-account 计数器，key 为 provider_account_id（整数字符串）
  cache_token_count_by_account?: Record<string, CacheTokenCount>;
  // per-clientid 请求计数，key 为客户端标识字符串
  clientid_request_count?: Record<string, number>;
  [key: string]: unknown;
}

// 全局汇总指标
interface GlobalMetrics {
  creation_total: number;
  read_total: number;
  request_count: number;
}

// 单账号指标
interface AccountMetrics {
  account_id: string;
  creation_total: number;
  read_total: number;
  request_count: number;
  hit_ratio: number; // read / (creation + read)，NaN 时显示 "—"
}

// 从 expvar JSON 提取全局指标（后端返回嵌套 map，不是扁平 dotted key）
function extractGlobal(data: ExpvarPayload): GlobalMetrics {
  const g = data.cache_token_count;
  return {
    creation_total: numOf(g?.creation_total),
    read_total: numOf(g?.read_total),
    request_count: numOf(g?.request_count),
  };
}

// 从 expvar JSON 提取所有 per-account 指标
// cache_token_count_by_account 是嵌套 map：{ "<provider_account_id>": { creation_total, read_total, request_count } }
function extractByAccount(data: ExpvarPayload): AccountMetrics[] {
  const byAccount = data.cache_token_count_by_account ?? {};
  const result: AccountMetrics[] = [];

  for (const [accountId, counts] of Object.entries(byAccount)) {
    const creation_total = numOf(counts.creation_total);
    const read_total = numOf(counts.read_total);
    const request_count = numOf(counts.request_count);
    const denom = creation_total + read_total;
    const hit_ratio = denom === 0 ? NaN : read_total / denom;
    result.push({ account_id: accountId, creation_total, read_total, request_count, hit_ratio });
  }

  // 按 account_id 升序排列
  return result.sort((a, b) => a.account_id.localeCompare(b.account_id));
}

function numOf(v: unknown): number {
  if (typeof v === 'number') return v;
  if (typeof v === 'string') {
    const n = Number(v);
    return isNaN(n) ? 0 : n;
  }
  return 0;
}

function fmtRatio(r: number): string {
  if (isNaN(r)) return '—';
  return (r * 100).toFixed(1) + '%';
}

// 全局命中率计算
function globalHitRatio(g: GlobalMetrics): string {
  const denom = g.creation_total + g.read_total;
  if (denom === 0) return '—';
  return ((g.read_total / denom) * 100).toFixed(1) + '%';
}

const POLL_INTERVAL_MS = 2000;

export default function ObservabilityPage() {
  const [global, setGlobal] = useState<GlobalMetrics | null>(null);
  const [byAccount, setByAccount] = useState<AccountMetrics[]>([]);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [error, setError] = useState('');
  const [polling, setPolling] = useState(true);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // 发起一次 poll
  async function poll() {
    try {
      const resp = await fetch('/debug/vars', { cache: 'no-store' });
      if (!resp.ok) {
        setError(`HTTP ${resp.status}`);
        return;
      }
      const data: ExpvarPayload = await resp.json();
      setGlobal(extractGlobal(data));
      setByAccount(extractByAccount(data));
      setLastUpdated(new Date());
      setError('');
    } catch (err: unknown) {
      setError(String(err));
    }
  }

  // 定时轮询：polling 为 true 时每 2 秒触发一次
  useEffect(() => {
    if (!polling) return;
    // 立即触发一次
    poll();
    timerRef.current = setInterval(poll, POLL_INTERVAL_MS);
    return () => {
      if (timerRef.current != null) clearInterval(timerRef.current);
    };
  }, [polling]);

  function togglePolling() {
    setPolling((p) => !p);
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      {/* 标题行 + 轮询状态 */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '1rem' }}>
        <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
          Observability — Cache Metrics
        </h1>
        <span className={`poll-badge${polling ? '' : ' stale'}`}>
          {polling ? '● LIVE' : '○ PAUSED'}
        </span>
        <button
          onClick={togglePolling}
          style={{ background: polling ? '#6e40c9' : '#238636', marginLeft: 'auto' }}
        >
          {polling ? 'Pause' : 'Resume'}
        </button>
      </div>

      {/* 最后更新时间 */}
      {lastUpdated && (
        <div style={{ fontSize: '0.7rem', color: '#484f58' }}>
          上次更新：{lastUpdated.toLocaleTimeString('zh-CN')}（每 {POLL_INTERVAL_MS / 1000}s 轮询）
        </div>
      )}

      {/* 错误提示 */}
      {error && <div className="error-msg">{error}</div>}

      {/* 全局汇总表 */}
      <div>
        <div className="section-title">Global（全局汇总）</div>
        <table className="obs-table">
          <thead>
            <tr>
              <th>creation_total</th>
              <th>read_total</th>
              <th>request_count</th>
              <th>hit_ratio</th>
            </tr>
          </thead>
          <tbody>
            {global ? (
              <tr>
                <td>{global.creation_total.toLocaleString()}</td>
                <td>{global.read_total.toLocaleString()}</td>
                <td>{global.request_count.toLocaleString()}</td>
                <td>{globalHitRatio(global)}</td>
              </tr>
            ) : (
              <tr>
                <td colSpan={4} style={{ color: '#484f58', textAlign: 'center' }}>
                  {error ? '加载失败' : '加载中…'}
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Per-account 表 */}
      <div>
        <div className="section-title">Per-Account（按账号）</div>
        <table className="obs-table">
          <thead>
            <tr>
              <th>account_id</th>
              <th>creation_total</th>
              <th>read_total</th>
              <th>request_count</th>
              <th>hit_ratio</th>
            </tr>
          </thead>
          <tbody>
            {byAccount.length === 0 ? (
              <tr>
                <td colSpan={5} style={{ color: '#484f58', textAlign: 'center' }}>
                  {error ? '加载失败' : global === null ? '加载中…' : '（无 per-account 数据）'}
                </td>
              </tr>
            ) : (
              byAccount.map((acc) => (
                <tr key={acc.account_id}>
                  <td style={{ fontFamily: 'monospace' }}>{acc.account_id}</td>
                  <td>{acc.creation_total.toLocaleString()}</td>
                  <td>{acc.read_total.toLocaleString()}</td>
                  <td>{acc.request_count.toLocaleString()}</td>
                  <td>{fmtRatio(acc.hit_ratio)}</td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
