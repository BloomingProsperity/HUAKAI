import { useCallback, useEffect, useRef, useState } from 'react'
import type { CSSProperties } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getRuntimeLogSinkHealth, listRuntimeLogs } from './api'
import type { RuntimeLogRow, RuntimeLogSinkHealth } from './api'
import { appendOlderLogs, fmtAttrs, fmtLogTime, levelToneOf, mergeRuntimeLogs } from './runtimeLogs'

/*
 * 运行日志面板(platform_admin)。后端 sink 只采集 warn+;「实时」= 开关打开后
 * 3s 轮询首页并按 id 去重合入(三镜均无服务端推送流,轮询即业界形态)。
 * 支持级别/组件/request_id 过滤与键集加载更旧。
 */

const POLL_MS = 3000
const PAGE_LIMIT = 100

export function RuntimeLogsPanel() {
  const [rows, setRows] = useState<RuntimeLogRow[]>([])
  const [level, setLevel] = useState('')
  const [component, setComponent] = useState('')
  const [requestID, setRequestID] = useState('')
  const [live, setLive] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [health, setHealth] = useState<RuntimeLogSinkHealth | null>(null)
  const [noMore, setNoMore] = useState(false)
  // 过滤条件的即时引用:轮询定时器闭包里读到的永远是最新值。
  const filtersRef = useRef({ level, component, requestID })
  filtersRef.current = { level, component, requestID }

  const refresh = useCallback(async (signal?: AbortSignal) => {
    setError(null)
    const f = filtersRef.current
    try {
      const resp = await listRuntimeLogs(
        { level: f.level, component: f.component, request_id: f.requestID, limit: PAGE_LIMIT },
        signal,
      )
      setRows((prev) => mergeRuntimeLogs(prev, resp.items))
      setNoMore(resp.items.length < PAGE_LIMIT)
    } catch (e) {
      if (signal?.aborted) return
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '查询运行日志失败')
    }
  }, [])

  // 过滤条件变化 → 清空重查(不保留旧条件的结果)。
  useEffect(() => {
    const ctrl = new AbortController()
    setRows([])
    setLoading(true)
    void refresh(ctrl.signal).finally(() => {
      if (!ctrl.signal.aborted) setLoading(false)
    })
    return () => ctrl.abort()
  }, [refresh, level, component, requestID])

  // 实时开关:3s 轮询首页增量合入。
  useEffect(() => {
    if (!live) return
    const timer = setInterval(() => { void refresh() }, POLL_MS)
    return () => clearInterval(timer)
  }, [live, refresh])

  // sink 健康随面板加载展示一次,实时开着时跟着轮询节奏更新。
  useEffect(() => {
    const ctrl = new AbortController()
    const pull = () => {
      getRuntimeLogSinkHealth(ctrl.signal).then(setHealth).catch(() => {})
    }
    pull()
    if (!live) return () => ctrl.abort()
    const timer = setInterval(pull, POLL_MS * 5)
    return () => { clearInterval(timer); ctrl.abort() }
  }, [live])

  const loadOlder = async () => {
    if (rows.length === 0) return
    setLoading(true)
    setError(null)
    const f = filtersRef.current
    try {
      const resp = await listRuntimeLogs({
        level: f.level,
        component: f.component,
        request_id: f.requestID,
        before_id: rows[rows.length - 1].id,
        limit: PAGE_LIMIT,
      })
      setRows((prev) => appendOlderLogs(prev, resp.items))
      setNoMore(resp.items.length < PAGE_LIMIT)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载更旧日志失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <section className="hk-card" style={{ padding: 'var(--hk-space-5)', marginTop: 'var(--hk-space-4)' }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <h2 style={{ fontSize: 15, margin: 0 }}>运行日志(warn 及以上)</h2>
        {health && (
          <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
            采集:入库 {health.inserted} · 积压 {health.queue_len} · 丢弃 {health.dropped}
            {health.dropped > 0 && <StatusBadge tone="warn">有丢弃</StatusBadge>}
          </span>
        )}
      </div>
      <p className="hk-sub" style={{ marginTop: 4 }}>
        两栈(zap+slog)警告与错误异步入库;可按 request_id 关联单请求。实时 = 3 秒轮询增量。
      </p>

      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', flexWrap: 'wrap', marginTop: 'var(--hk-space-3)', alignItems: 'center' }}>
        <select value={level} onChange={(e) => setLevel(e.target.value)} style={inp} aria-label="级别过滤">
          <option value="">全部级别</option>
          <option value="warn">warn</option>
          <option value="error">error</option>
        </select>
        <input value={component} onChange={(e) => setComponent(e.target.value)} placeholder="组件(精确)" style={{ ...inp, width: 140 }} />
        <input value={requestID} onChange={(e) => setRequestID(e.target.value)} placeholder="request_id(精确)" style={{ ...inp, width: 220 }} />
        <button type="button" className="hk-btn" onClick={() => void refresh()} disabled={loading}>刷新</button>
        <button
          type="button"
          className={live ? 'hk-btn hk-btn--green' : 'hk-btn'}
          aria-pressed={live}
          onClick={() => setLive((v) => !v)}
        >
          {live ? '实时:开(3s)' : '实时:关'}
        </button>
      </div>

      {error && <div style={errBox}>{error}</div>}

      <div style={{ overflowX: 'auto', marginTop: 'var(--hk-space-3)' }}>
        <table className="hk-table">
          <thead>
            <tr>
              <th>时间</th>
              <th>级别</th>
              <th>组件</th>
              <th>消息</th>
              <th>request_id</th>
              <th>属性</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr><td colSpan={6} className="hk-empty">{loading ? '加载中…' : '暂无满足条件的运行日志(warn+ 才采集)。'}</td></tr>
            ) : rows.map((r) => (
              <tr key={r.id}>
                <td style={mono}>{fmtLogTime(r.created_at)}</td>
                <td><StatusBadge tone={levelToneOf(r.level)}>{r.level}</StatusBadge></td>
                <td style={mono}>{r.component}</td>
                <td style={{ maxWidth: 420, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={r.message}>{r.message}</td>
                <td style={mono}>
                  {r.request_id ? (
                    <button type="button" style={linkBtn} title="按此 request_id 过滤" onClick={() => setRequestID(r.request_id ?? '')}>
                      {r.request_id}
                    </button>
                  ) : '—'}
                </td>
                <td style={{ ...mono, maxWidth: 320, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={fmtAttrs(r.attrs)}>{fmtAttrs(r.attrs)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {rows.length > 0 && !noMore && (
        <div style={{ marginTop: 'var(--hk-space-3)' }}>
          <button type="button" className="hk-btn" onClick={() => void loadOlder()} disabled={loading}>
            {loading ? '加载中…' : '加载更旧'}
          </button>
        </div>
      )}
    </section>
  )
}

const inp: CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)' }
const mono: CSSProperties = { fontFamily: 'var(--hk-font-mono)', fontSize: 12 }
const errBox: CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', marginTop: 'var(--hk-space-2)' }
const linkBtn: CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 12, cursor: 'pointer', fontFamily: 'var(--hk-font-mono)', padding: 0 }
