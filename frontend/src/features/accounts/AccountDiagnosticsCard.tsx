import { useState } from 'react'
import { ApiError } from '../../lib/api'
import {
  getProviderAccountHealth,
  getProviderAccountRecentRequests,
  getProviderAccountUpstreamModels,
  testProviderAccount,
} from './api'
import { healthRows, testSummary, type TestSummary } from './diagnostics'
import { fmtLatency, fmtTtft, recentModelDisplay, recentStatusTone } from './recentRequests'
import type { AccountHealth, AccountRecentRequestsResponse, UpstreamModelsResult } from './types'

/*
 * 账号诊断卡(详情页)。三块运维诊断,均按需触发(避免详情页一进来就打三个上游/DB 请求):
 * - 测试连通:POST /{id}/test,dry-run 校验凭证(不计费),展示 ok/error_class/message。
 * - 实时健康:GET /{id}/health,展示 health_state / failure_count / 5h 会话窗 / 近期请求 等。
 * - 上游模型:GET /{id}/upstream-models,仅 upstream_passthrough 账号支持(否则后端 422,如实展示)。
 * 真码:backend/internal/adminhttp/provider_account_{test,health,upstream_models}_handler.go。
 */

function errText(e: unknown, fallback: string): string {
  return e instanceof ApiError ? `${e.message}(${e.code})` : fallback
}

export function AccountDiagnosticsCard({ tenantId, id }: { tenantId: number; id: number }) {
  // 三块各自独立的 busy / 结果态。
  const [test, setTest] = useState<{ busy: boolean; summary?: TestSummary }>({ busy: false })
  const [health, setHealth] = useState<{ busy: boolean; data?: AccountHealth; error?: string }>({ busy: false })
  const [models, setModels] = useState<{ busy: boolean; data?: UpstreamModelsResult; error?: string }>({ busy: false })
  const [recent, setRecent] = useState<{ busy: boolean; data?: AccountRecentRequestsResponse; error?: string; limit: number }>({ busy: false, limit: 20 })

  async function loadRecent(limit = recent.limit) {
    setRecent((s) => ({ ...s, busy: true, limit }))
    try {
      const data = await getProviderAccountRecentRequests(tenantId, id, limit)
      setRecent({ busy: false, data, limit })
    } catch (e) {
      setRecent({ busy: false, error: errText(e, '加载最近请求失败'), limit })
    }
  }

  async function runTest() {
    setTest({ busy: true })
    try {
      const res = await testProviderAccount(tenantId, id)
      setTest({ busy: false, summary: testSummary(res) })
    } catch (e) {
      setTest({ busy: false, summary: { label: errText(e, '测试请求失败'), tone: 'fail' } })
    }
  }

  async function loadHealth() {
    setHealth({ busy: true })
    try {
      const data = await getProviderAccountHealth(tenantId, id)
      setHealth({ busy: false, data })
    } catch (e) {
      setHealth({ busy: false, error: errText(e, '加载健康失败') })
    }
  }

  async function loadModels() {
    setModels({ busy: true })
    try {
      const data = await getProviderAccountUpstreamModels(tenantId, id)
      setModels({ busy: false, data })
    } catch (e) {
      setModels({ busy: false, error: errText(e, '加载上游模型失败') })
    }
  }

  return (
    <section style={card}>
      <h2 style={{ fontSize: 14, color: 'var(--hk-ink-500)' }}>诊断</h2>

      {/* 测试连通 */}
      <div style={row}>
        <button type="button" disabled={test.busy} onClick={runTest} style={ghostBtn}>
          {test.busy ? '测试中…' : '测试连通'}
        </button>
        {test.summary && (
          <span style={{ fontSize: 13, color: test.summary.tone === 'ok' ? 'var(--hk-ok, var(--hk-success))' : 'var(--hk-danger, var(--hk-danger))' }}>
            {test.summary.label}
          </span>
        )}
        <span style={hint}>dry-run 校验凭证,不计费、进审计</span>
      </div>

      {/* 实时健康 */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        <div style={row}>
          <button type="button" disabled={health.busy} onClick={loadHealth} style={ghostBtn}>
            {health.busy ? '加载中…' : health.data ? '刷新健康' : '加载实时健康'}
          </button>
          {health.error && <span style={{ fontSize: 13, color: 'var(--hk-danger, var(--hk-danger))' }}>{health.error}</span>}
        </div>
        {health.data && (
          <dl style={grid}>
            {healthRows(health.data).map(([k, v]) => (
              <div key={k} style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <dt style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>{k}</dt>
                <dd style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-900)', wordBreak: 'break-word' }}>{v}</dd>
              </div>
            ))}
          </dl>
        )}
      </div>

      {/* 上游可用模型 */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        <div style={row}>
          <button type="button" disabled={models.busy} onClick={loadModels} style={ghostBtn}>
            {models.busy ? '探测中…' : '探测上游模型'}
          </button>
          {models.error && <span style={{ fontSize: 13, color: 'var(--hk-danger, var(--hk-danger))' }}>{models.error}</span>}
          <span style={hint}>仅 upstream_passthrough 账号支持</span>
        </div>
        {models.data && (
          models.data.count === 0 ? (
            <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>上游返回 0 个模型。</span>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
              <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>共 {models.data.count} 个模型</span>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                {models.data.models.map((m) => (
                  <span key={m} className="hk-mono" style={chip}>{m}</span>
                ))}
              </div>
            </div>
          )
        )}
      </div>

      {/* 最近请求(已结算 usage_records,只读、不含金额) */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        <div style={row}>
          <button type="button" disabled={recent.busy} onClick={() => void loadRecent()} style={ghostBtn}>
            {recent.busy ? '加载中…' : recent.data ? '刷新最近请求' : '加载最近请求'}
          </button>
          <div className="hk-seg">
            {[20, 50, 100].map((n) => (
              <button key={n} type="button" className={recent.limit === n ? 'is-on' : ''} onClick={() => void loadRecent(n)} disabled={recent.busy}>
                {n}
              </button>
            ))}
          </div>
          {recent.error && <span style={{ fontSize: 13, color: 'var(--hk-danger)' }}>{recent.error}</span>}
          <span style={hint}>按结算时间倒序 · 不含金额</span>
        </div>
        {recent.data && (
          recent.data.items.length === 0 ? (
            <div className="hk-empty">该账号暂无已结算请求。</div>
          ) : (
            <div className="hk-tablewrap">
              <table className="hk-table">
                <thead>
                  <tr>{['时间', '模型', '状态', '延迟', 'TTFT', '输入', '输出', '流式', 'attempt'].map((h) => <th key={h}>{h}</th>)}</tr>
                </thead>
                <tbody>
                  {recent.data.items.map((it, i) => (
                    <tr key={`${it.at}-${i}`}>
                      <td className="hk-mono">{fmtTime(it.at)}</td>
                      <td title={recentModelDisplay(it)} style={{ maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{recentModelDisplay(it)}</td>
                      <td><span className={`hk-pill ${recentStatusTone(it.status) === 'ok' ? 'hk-pill--ok' : 'hk-pill--crit'}`}>{it.status}</span></td>
                      <td className="hk-mono">{fmtLatency(it.latency_ms)}</td>
                      <td className="hk-mono">{fmtTtft(it.ttft_ms)}</td>
                      <td className="hk-mono" style={{ textAlign: 'right' }}>{it.tokens_in}</td>
                      <td className="hk-mono" style={{ textAlign: 'right' }}>{it.tokens_out}</td>
                      <td>{it.stream ? '是' : '否'}</td>
                      <td className="hk-mono" style={{ textAlign: 'right' }}>{it.attempt_seq}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        )}
      </div>
    </section>
  )
}

// fmtTime:ISO → 本地化;空/非法 → 原样或 "—"。
function fmtTime(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const card: React.CSSProperties = {
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  padding: 'var(--hk-space-4)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
}
const row: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }
const hint: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-300)' }
const grid: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))',
  gap: 'var(--hk-space-3)',
  margin: 0,
}
const chip: React.CSSProperties = {
  fontSize: 12,
  padding: '2px 8px',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-2)',
  background: 'var(--hk-surface-sunken, transparent)',
  color: 'var(--hk-ink-700)',
}
const ghostBtn: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-4)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
  fontSize: 13,
  cursor: 'pointer',
}
