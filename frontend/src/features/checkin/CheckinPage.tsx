import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { doCheckin, getCheckinStatus } from './api'
import {
  buildMonthGrid,
  formatUsd,
  monthOf,
  rewardRangeText,
  shiftMonth,
  totalCheckins,
  totalRewardCents,
} from './calendar'
import type { CheckinStatus } from './types'

/*
 * 每日签到(用户态)。GET/POST /v1/me/checkin(session 鉴权)。
 * 今日是否已签 + 签到按钮 + 本月签到日历 + 奖励区间说明。
 * 金额单位 cents,展示统一走 formatUsd 换算。日历按 UTC 历法(与后端口径一致)。
 */
const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六']

export function CheckinPage() {
  const [month, setMonth] = useState<string>(() => monthOf(new Date()))
  const [status, setStatus] = useState<CheckinStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [refreshNonce, setRefreshNonce] = useState(0)

  const load = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      getCheckinStatus(month, signal)
        .then((s) => setStatus(s))
        .catch((e: unknown) => {
          if (signal.aborted) return
          // 平台关闭签到时后端回 404 daily_checkin_disabled;此处归一成可读提示。
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载签到状态失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [month],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const submit = async () => {
    setSubmitting(true)
    setError(null)
    setFlash(null)
    try {
      const res = await doCheckin()
      setFlash(`签到成功,获得 ${formatUsd(res.reward_cents)},当前余额 ${formatUsd(res.new_balance)}`)
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      // 重复签到 → 409 daily_checkin_already_claimed;停用页等 → 其它 code。
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '签到失败,请稍后再试')
      setRefreshNonce((n) => n + 1)
    } finally {
      setSubmitting(false)
    }
  }

  const records = status?.records ?? []
  const grid = buildMonthGrid(month, records)
  const checkedToday = status?.checked_in_today ?? false
  const enabled = status?.enabled ?? false
  const currentMonth = monthOf(new Date())

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>每日签到</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          每天签到一次,把奖励直接返还到账户余额。
        </p>
      </header>

      {error && (
        <div style={errBox}>{error}</div>
      )}
      {flash && (
        <div style={okBox}>{flash}</div>
      )}

      {/* 今日签到卡 */}
      <section style={card}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-4)', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
              <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)', fontSize: 15 }}>今日状态</span>
              {!enabled ? (
                <StatusBadge tone="muted">已停用</StatusBadge>
              ) : checkedToday ? (
                <StatusBadge tone="ok">今日已签</StatusBadge>
              ) : (
                <StatusBadge tone="info">待签到</StatusBadge>
              )}
            </div>
            <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>
              {status ? rewardRangeText(status) : '加载中…'}
            </p>
          </div>
          <button
            type="button"
            onClick={submit}
            disabled={submitting || checkedToday || !enabled || loading}
            style={checkedToday || !enabled ? primaryBtnDisabled : primaryBtn}
          >
            {submitting ? '签到中…' : checkedToday ? '今日已签到' : '立即签到'}
          </button>
        </div>
      </section>

      {/* 本月统计 + 日历 */}
      <section style={card}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--hk-space-4)', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
            <button type="button" onClick={() => setMonth((m) => shiftMonth(m, -1))} style={navBtn} aria-label="上一月">
              ‹
            </button>
            <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)', fontSize: 15, minWidth: 92, textAlign: 'center' }}>
              {month}
            </span>
            <button
              type="button"
              onClick={() => setMonth((m) => shiftMonth(m, 1))}
              disabled={month >= currentMonth}
              style={month >= currentMonth ? navBtnDisabled : navBtn}
              aria-label="下一月"
            >
              ›
            </button>
          </div>
          <div style={{ display: 'flex', gap: 'var(--hk-space-5)', fontSize: 13, color: 'var(--hk-ink-700)' }}>
            <span>本月签到 <b style={{ color: 'var(--hk-ink-900)' }}>{totalCheckins(records)}</b> 天</span>
            <span>累计返还 <b style={{ color: 'var(--hk-primary-600)' }}>{formatUsd(totalRewardCents(records))}</b></span>
          </div>
        </div>

        {loading && !status ? (
          <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>加载中…</div>
        ) : (
          <div>
            <div style={weekRow}>
              {WEEKDAYS.map((w) => (
                <div key={w} style={weekHead}>{w}</div>
              ))}
            </div>
            <div style={gridStyle}>
              {grid.map((cell, i) =>
                cell.inMonth ? (
                  <div
                    key={cell.date}
                    title={cell.checkedIn ? `已签到 · 返还 ${formatUsd(cell.rewardCents)}` : '未签到'}
                    style={cell.checkedIn ? dayCellChecked : dayCell}
                  >
                    <span style={{ fontSize: 13, fontWeight: cell.checkedIn ? 600 : 400 }}>{cell.day}</span>
                    {cell.checkedIn && (
                      <span style={{ fontSize: 11, color: 'var(--hk-primary-600)', fontVariantNumeric: 'tabular-nums' }}>
                        {formatUsd(cell.rewardCents)}
                      </span>
                    )}
                  </div>
                ) : (
                  // 前导占位格,仅用于把 1 号推到正确星期列。
                  <div key={`pad-${i}`} style={{ ...dayCell, background: 'transparent', border: '1px solid transparent' }} />
                ),
              )}
            </div>
          </div>
        )}
      </section>
    </div>
  )
}

const card: React.CSSProperties = {
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  padding: 'var(--hk-space-5)',
}
const errBox: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  color: 'var(--hk-danger)',
  background: 'var(--hk-danger-soft)',
  border: '1px solid var(--hk-danger-soft)',
}
const okBox: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  color: '#0b6553',
  background: 'var(--hk-primary-50)',
  border: '1px solid var(--hk-primary-100)',
}
const primaryBtn: React.CSSProperties = {
  height: 40,
  padding: '0 var(--hk-space-6)',
  border: '1px solid var(--hk-primary-600)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 15,
  fontWeight: 600,
  cursor: 'pointer',
  flexShrink: 0,
}
const primaryBtnDisabled: React.CSSProperties = {
  ...primaryBtn,
  background: 'var(--hk-surface-sunken)',
  color: 'var(--hk-ink-300)',
  border: '1px solid var(--hk-line)',
  cursor: 'not-allowed',
}
const navBtn: React.CSSProperties = {
  width: 32,
  height: 32,
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
  fontSize: 16,
  lineHeight: 1,
  cursor: 'pointer',
}
const navBtnDisabled: React.CSSProperties = {
  ...navBtn,
  color: 'var(--hk-ink-300)',
  cursor: 'not-allowed',
}
const weekRow: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(7, 1fr)',
  gap: 'var(--hk-space-2)',
  marginBottom: 'var(--hk-space-2)',
}
const weekHead: React.CSSProperties = {
  textAlign: 'center',
  fontSize: 12,
  color: 'var(--hk-ink-500)',
  paddingBottom: 'var(--hk-space-1)',
}
const gridStyle: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(7, 1fr)',
  gap: 'var(--hk-space-2)',
}
const dayCell: React.CSSProperties = {
  minHeight: 56,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 2,
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface-sunken)',
  color: 'var(--hk-ink-700)',
}
const dayCellChecked: React.CSSProperties = {
  ...dayCell,
  background: 'var(--hk-primary-50)',
  border: '1px solid var(--hk-primary-100)',
  color: 'var(--hk-primary-700)',
}
