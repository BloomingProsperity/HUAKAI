import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getInvitationSummary, getMyInvitationCode, listReferralRewards, listReferrals, mintInvitation } from './api'
import {
  buildInviteLink,
  DEFAULT_EXPIRES_DAYS,
  DEFAULT_MAX_USAGE,
  EXPIRES_DAYS_MAX,
  EXPIRES_DAYS_MIN,
  formatCents,
  formatUsd,
  MAX_USAGE_MAX,
  MAX_USAGE_MIN,
  refereeDisplay,
  referralStatusLabel,
  referralStatusTone,
  validateMintForm,
} from './affiliate'
import type { InvitationSummary, MintInvitationResponse, MyInvitationCode, ReferralItem, RewardLedgerResponse } from './types'

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

      {/* ①b 生成活动邀请码(写) */}
      <MintCampaignCode />

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

/*
 * 生成活动邀请码卡(写路径)。
 * - 两个数值输入(使用次数 / 有效天数),前端校验镜像后端范围 [1,100] / [1,90]。
 * - money-coupled:被邀人达标后才返利,生成本身只铸码;提交前 window.confirm 明示影响。
 * - 成功后「一次性」展示 code(放在内存 state,刷新即丢)+ 复制按钮;再看需走只读自助码端点。
 * - code 是可分享的邀请码(非 secret),但仍按一次性下发对待,不写 localStorage / 不 console。
 */
function MintCampaignCode() {
  const [maxUsage, setMaxUsage] = useState(String(DEFAULT_MAX_USAGE))
  const [expiresInDays, setExpiresInDays] = useState(String(DEFAULT_EXPIRES_DAYS))
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [minted, setMinted] = useState<MintInvitationResponse | null>(null)

  const submit = async () => {
    setErr(null)
    const v = validateMintForm(maxUsage, expiresInDays)
    if (!v.ok) {
      setErr(v.error)
      return
    }
    // money-coupled 动作:明示返利链影响后再生成(被邀人达标才返利,生成不立即入账)。
    if (
      !window.confirm(
        `确认生成一个活动邀请码?\n` +
          `可使用 ${v.maxUsage} 次,有效 ${v.expiresInDays} 天。\n\n` +
          `被邀请人注册并达标后将给你返利(返利在对方产生计费后结算,生成本身不入账)。`,
      )
    ) {
      return
    }
    setBusy(true)
    // 每次新生成前清掉上一次的一次性码,避免旧码残留在界面。
    setMinted(null)
    try {
      const res = await mintInvitation({ max_usage: v.maxUsage, expires_in_days: v.expiresInDays })
      setMinted(res)
    } catch (e: unknown) {
      setErr(e instanceof ApiError ? `${e.message}(${e.code})` : '生成邀请码失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section style={card}>
      <h2 style={cardTitle}>生成活动邀请码</h2>
      <p style={mutedLine}>
        为推广活动批量生成可多次使用的邀请码。被邀请人注册并达标后将给你返利。
        生成的码仅本次展示一次,刷新后不再显示;你的固定自助邀请码见上方「专属邀请码」。
      </p>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-4)', alignItems: 'flex-end' }}>
        <label style={fieldCol}>
          <span style={fieldLabel}>使用次数({MAX_USAGE_MIN}–{MAX_USAGE_MAX})</span>
          <input
            type="number"
            inputMode="numeric"
            min={MAX_USAGE_MIN}
            max={MAX_USAGE_MAX}
            value={maxUsage}
            onChange={(e) => setMaxUsage(e.target.value)}
            style={inp}
            disabled={busy}
          />
        </label>
        <label style={fieldCol}>
          <span style={fieldLabel}>有效天数({EXPIRES_DAYS_MIN}–{EXPIRES_DAYS_MAX})</span>
          <input
            type="number"
            inputMode="numeric"
            min={EXPIRES_DAYS_MIN}
            max={EXPIRES_DAYS_MAX}
            value={expiresInDays}
            onChange={(e) => setExpiresInDays(e.target.value)}
            style={inp}
            disabled={busy}
          />
        </label>
        <button type="button" onClick={submit} disabled={busy} style={primaryBtn}>
          {busy ? '生成中…' : '生成邀请码'}
        </button>
      </div>

      {err && <div style={errorBox}>{err}</div>}

      {minted && (
        <div style={mintedBox}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
            <span style={{ fontSize: 12, color: 'var(--hk-ink-500)', minWidth: 72 }}>新邀请码</span>
            <code style={codeVal}>{minted.code}</code>
            <CopyButton value={minted.code} />
          </div>
          <p style={{ ...mutedLine, marginTop: 'var(--hk-space-2)' }}>
            可使用 {minted.max_usage} 次,有效期至 {fmtTime(minted.expires_at)}。请立即复制保存——刷新后此码不再显示。
          </p>
        </div>
      )}
    </section>
  )
}

/** 独立复制按钮(供一次性码展示复用,不带 label 行)。 */
function CopyButton({ value }: { value: string }) {
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
    <button type="button" onClick={copy} style={miniBtn}>
      {copied ? '已复制' : '复制'}
    </button>
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
const fieldCol: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4 }
const fieldLabel: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)' }
const inp: React.CSSProperties = {
  height: 34,
  width: 160,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  fontSize: 13,
  fontFamily: 'var(--hk-font-mono)',
}
const primaryBtn: React.CSSProperties = {
  height: 34,
  padding: '0 var(--hk-space-5)',
  border: '1px solid var(--hk-primary-700)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-700)',
  color: '#fff',
  fontSize: 13,
  fontWeight: 600,
  cursor: 'pointer',
}
const mintedBox: React.CSSProperties = {
  padding: 'var(--hk-space-4)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
}
