import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getInvitationSummary, getMyInvitationCode, listReferralRewards, listReferrals } from './api'
import {
  buildInviteLink,
  formatCents,
  formatUsd,
  refereeDisplay,
  referralStatusLabel,
  referralStatusTone,
} from './affiliate'
import type { InvitationSummary, MyInvitationCode, ReferralItem, RewardLedgerResponse } from './types'

/*
 * 推广(邀请返利)页 —— user 壳,只读。
 * 三块:① 专属邀请码 / 邀请链接(可复制) ② 累计返利汇总(只读) ③ 被邀请人列表 + 返利流水。
 * money-gated:提现/动钱不做,仅展示。端点全部 session 鉴权,挂 /v1/me 组。
 */
export function AffiliatePage() {
  const [code, setCode] = useState<MyInvitationCode | null>(null)
  const [summary, setSummary] = useState<InvitationSummary | null>(null)
  const [referrals, setReferrals] = useState<ReferralItem[]>([])
  const [referralsTotal, setReferralsTotal] = useState(0)
  const [rewards, setRewards] = useState<RewardLedgerResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    Promise.all([
      getMyInvitationCode(signal),
      getInvitationSummary(signal),
      listReferrals(0, 50, signal),
      listReferralRewards(0, 50, signal),
    ])
      .then(([c, s, refs, rw]) => {
        setCode(c)
        setSummary(s)
        setReferrals(refs.items)
        setReferralsTotal(refs.total)
        setRewards(rw)
      })
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载推广数据失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const inviteLink = code ? buildInviteLink(window.location.origin, code.code) : ''

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>推广</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          分享你的专属邀请链接,好友注册并达成条件后你将获得返利。以下为只读概览。
        </p>
      </header>

      {error && (
        <div style={errorBox}>{error}</div>
      )}

      {/* ① 邀请码 / 邀请链接 */}
      <section style={card}>
        <h2 style={cardTitle}>专属邀请码</h2>
        {loading && !code ? (
          <p style={mutedLine}>加载中…</p>
        ) : code ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
            <CopyRow label="邀请码" value={code.code} />
            {inviteLink && <CopyRow label="邀请链接" value={inviteLink} />}
          </div>
        ) : (
          <p style={mutedLine}>暂无邀请码。</p>
        )}
      </section>

      {/* ② 累计返利汇总(只读) */}
      <section style={card}>
        <h2 style={cardTitle}>返利汇总</h2>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-5)' }}>
          <Metric label="被邀请人总数" value={String(referralsTotal)} />
          <Metric label="已合格" value={summary ? String(summary.qualified_count) : '—'} />
          <Metric label="已返利人数" value={summary ? String(summary.rewarded_count) : '—'} />
          <Metric label="累计返利(汇总)" value={summary ? formatCents(summary.rewards_earned_cents) : '—'} accent />
          <Metric label="累计返利(流水)" value={rewards ? formatUsd(rewards.total_reward_usd) : '—'} />
        </div>
      </section>

      {/* ③ 被邀请人列表 */}
      <section style={card}>
        <h2 style={cardTitle}>被邀请人</h2>
        {loading && referrals.length === 0 ? (
          <p style={mutedLine}>加载中…</p>
        ) : referrals.length === 0 ? (
          <p style={mutedLine}>还没有被邀请人。分享你的邀请链接开始吧。</p>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={table}>
              <thead>
                <tr>
                  {['被邀请人', '状态', '邀请时间', '返利时间'].map((h) => (
                    <th key={h} style={th}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {referrals.map((r) => (
                  <tr key={r.referral_id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>{refereeDisplay(r.referee_user_id)}</td>
                    <td style={td}>
                      <StatusBadge tone={referralStatusTone(r.status)}>{referralStatusLabel(r.status)}</StatusBadge>
                    </td>
                    <td style={td}>{fmtTime(r.created_at)}</td>
                    <td style={td}>{r.rewarded_at ? fmtTime(r.rewarded_at) : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {/* ③b 返利流水 */}
      <section style={card}>
        <h2 style={cardTitle}>返利流水</h2>
        {loading && !rewards ? (
          <p style={mutedLine}>加载中…</p>
        ) : !rewards || rewards.items.length === 0 ? (
          <p style={mutedLine}>暂无返利记录。</p>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={table}>
              <thead>
                <tr>
                  {['关联邀请', '类型', '金额', '时间'].map((h) => (
                    <th key={h} style={th}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rewards.items.map((it, idx) => (
                  <tr key={`${it.referral_id}-${idx}`} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>#{it.referral_id}</td>
                    <td style={td}>{it.reward_type}</td>
                    <td style={tdNum}>{formatUsd(it.amount_usd)}</td>
                    <td style={td}>{fmtTime(it.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}

function CopyRow({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      setCopied(false)
    }
  }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
      <span style={{ fontSize: 11, color: 'var(--hk-ink-500)', minWidth: 72 }}>{label}</span>
      <code style={codeVal}>{value}</code>
      <button type="button" onClick={copy} style={miniBtn}>
        {copied ? '已复制' : '复制'}
      </button>
    </div>
  )
}

function Metric({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}</span>
      <span style={{ fontSize: 20, fontWeight: 600, color: accent ? 'var(--hk-primary-700)' : 'var(--hk-ink-900)', fontFamily: 'var(--hk-font-mono)' }}>
        {value}
      </span>
    </div>
  )
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}

const card: React.CSSProperties = {
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  padding: 'var(--hk-space-5)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
}
const cardTitle: React.CSSProperties = { fontSize: 15, fontWeight: 600, margin: 0, color: 'var(--hk-ink-900)' }
const mutedLine: React.CSSProperties = { margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
const table: React.CSSProperties = { width: '100%', borderCollapse: 'collapse', fontSize: 13 }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }
const codeVal: React.CSSProperties = {
  flex: 1,
  fontSize: 13,
  fontFamily: 'var(--hk-font-mono)',
  wordBreak: 'break-all',
  color: 'var(--hk-ink-900)',
  background: 'var(--hk-surface-sunken)',
  padding: '6px var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-sm)',
  border: '1px solid var(--hk-line)',
}
const miniBtn: React.CSSProperties = {
  height: 28,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
  fontSize: 12,
  cursor: 'pointer',
  flexShrink: 0,
}
