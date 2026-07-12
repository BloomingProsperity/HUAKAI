import { useState } from 'react'
import { ApiError } from '../../lib/api'
import {
  forceDisable2FA,
  resetPasskeys,
  setUserGroup,
  setUserRemark,
  softDeleteUser,
  unlinkSocialIdentity,
} from './api'
import { SOCIAL_PROVIDERS, validateGroup, validateRemark } from './actions'
import type { UserDetail } from './detail'

/*
 * 用户运维动作区(运维台)。把 adminuserhttp 已有但前端缺入口的管理动作接上:
 * 可编辑用户组 / 备注、强制关闭 2FA、清空通行密钥、解绑社交登录、软删用户。
 * 用户组变更影响计费倍率(随组),故单独标注;软删/清 passkey/解绑为高危,带二次确认。
 * 余额变更不在此(money 另走 Wave E 的手动调额卡)。
 */
export function UserAdminActions({ user, onChanged }: { user: UserDetail; onChanged: () => void }) {
  const [group, setGroup] = useState(user.user_group || '')
  const [remark, setRemark] = useState(user.remark || '')
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [ok, setOk] = useState<string | null>(null)
  const [provider, setProvider] = useState(SOCIAL_PROVIDERS[0]?.value ?? '')

  // run 包一层统一态:busy 标签、错误归一化、成功提示、变更后回调刷新。
  const run = async (label: string, fn: () => Promise<unknown>, okMsg: string, reload = true) => {
    setBusy(label)
    setError(null)
    setOk(null)
    try {
      await fn()
      setOk(okMsg)
      if (reload) onChanged()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusy(null)
    }
  }

  const saveGroup = () => {
    const err = validateGroup(group)
    if (err) {
      setError(err)
      return
    }
    void run('group', () => setUserGroup(user.id, group.trim()), '用户组已更新(计费倍率随新组生效)')
  }

  const saveRemark = () => {
    const err = validateRemark(remark)
    if (err) {
      setError(err)
      return
    }
    void run('remark', () => setUserRemark(user.id, remark), '备注已保存')
  }

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <h2 style={{ fontSize: 15, color: 'var(--hk-ink-700)' }}>运维动作</h2>

      {error && <Banner tone="danger">{error}</Banner>}
      {ok && <Banner tone="ok">{ok}</Banner>}

      <div style={card}>
        {/* 用户组(影响计费倍率) */}
        <Row label="用户组" hint="影响该用户的计费倍率与可达模型(随组)">
          <input value={group} onChange={(e) => setGroup(e.target.value)} placeholder="如 default / vip" style={inp} />
          <button type="button" disabled={busy !== null} onClick={saveGroup} style={primaryBtn}>
            {busy === 'group' ? '保存中…' : '保存组'}
          </button>
        </Row>

        {/* 备注 */}
        <Row label="备注" hint="运维备注,最多 1024 字符">
          <input value={remark} onChange={(e) => setRemark(e.target.value)} placeholder="(空)" style={inp} />
          <button type="button" disabled={busy !== null} onClick={saveRemark} style={ghostBtn}>
            {busy === 'remark' ? '保存中…' : '保存备注'}
          </button>
        </Row>

        {/* 解绑社交登录 */}
        <Row label="解绑社交登录" hint="解除该用户与某第三方登录的绑定">
          <select value={provider} onChange={(e) => setProvider(e.target.value)} style={inp}>
            {SOCIAL_PROVIDERS.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </select>
          <button
            type="button"
            disabled={busy !== null}
            onClick={() =>
              void run(
                'unlink',
                () => unlinkSocialIdentity(user.id, provider).then((r) => {
                  if (r.unlinked === 0) throw new ApiError(404, 'not_bound', '该用户未绑定此登录方式')
                  return r
                }),
                '已解绑',
              )
            }
            style={ghostBtn}
          >
            {busy === 'unlink' ? '解绑中…' : '解绑'}
          </button>
        </Row>
      </div>

      {/* 危险动作区 */}
      <div style={{ ...card, borderColor: 'var(--hk-danger-soft)', background: '#fdf6f5' }}>
        <h3 style={{ fontSize: 13, color: 'var(--hk-danger)', margin: 0 }}>高危动作</h3>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => {
              if (!window.confirm(`强制关闭 ${user.email} 的两步验证(2FA)?用户下次需重新设置。`)) return
              void run('2fa', () => forceDisable2FA(user.id), '已强制关闭该用户 2FA')
            }}
            style={dangerGhost}
          >
            {busy === '2fa' ? '处理中…' : '强制关闭 2FA'}
          </button>
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => {
              if (!window.confirm(`清空 ${user.email} 的全部通行密钥(passkey)?`)) return
              void run('passkey', () => resetPasskeys(user.id), '已清空该用户通行密钥')
            }}
            style={dangerGhost}
          >
            {busy === 'passkey' ? '处理中…' : '清空通行密钥'}
          </button>
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => {
              if (!window.confirm(`软删用户 ${user.email}?将停用账号并撤销其全部会话(可由后端恢复)。`)) return
              void run('delete', () => softDeleteUser(user.id), '用户已软删', true)
            }}
            style={dangerSolid}
          >
            {busy === 'delete' ? '处理中…' : '软删用户'}
          </button>
        </div>
      </div>
    </section>
  )
}

function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--hk-space-2)' }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>{label}</span>
        {hint && <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{hint}</span>}
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>{children}</div>
    </div>
  )
}

function Banner({ tone, children }: { tone: 'danger' | 'ok'; children: React.ReactNode }) {
  const danger = tone === 'danger'
  return (
    <div
      style={{
        padding: 'var(--hk-space-2) var(--hk-space-3)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: danger ? 'var(--hk-danger)' : 'var(--hk-primary-700)',
        background: danger ? 'var(--hk-danger-soft)' : 'var(--hk-primary-50, #eef7f2)',
        border: `1px solid ${danger ? 'var(--hk-danger-soft)' : 'var(--hk-line)'}`,
      }}
    >
      {children}
    </div>
  )
}

const card: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
  padding: 'var(--hk-space-4)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
}
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', flex: 1, minWidth: 120 }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer', flexShrink: 0 }
const dangerGhost: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #e6b3ab', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-danger)', fontSize: 13, cursor: 'pointer' }
const dangerSolid: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #c0392b', borderRadius: 'var(--hk-radius-md)', background: '#c0392b', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
