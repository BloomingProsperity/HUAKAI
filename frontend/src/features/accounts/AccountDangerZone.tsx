import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { deleteProviderAccount } from './api'
import { confirmPromptText, deleteResultMessage, nameMatchesConfirmation } from './dangerzone'

/*
 * 账号详情页「危险操作」区:硬删账号(不可逆)。
 *
 * 后端:DELETE /admin/v1/provider-accounts/{id}
 *   (backend/internal/gatewayhttp/admin_pool_accounts_handler.go:665 newDeleteProviderAccountHandler,
 *    经 :172 MountAdminPoolAccountRoutes 挂在 /admin/v1/provider-accounts 组的 DELETE /{id})。
 *
 * 与运维动作里的「停用账号」(PATCH /{id}/enabled,可恢复软停)严格区分:这是删除,删完账号
 * 从可调度池永久移除、不可恢复。两道护栏:
 *   ① 必须手抄账号名到输入框(nameMatchesConfirmation 严格相等才解锁删除按钮);
 *   ② 点删除时再弹 window.confirm(含账号名 + #id)做操作系统级二次阻断。
 * 删除成功后调用 onDeleted(),由父页跳回 /accounts 列表。
 *
 * 独立成卡,不动 Wave C 的 AccountDiagnosticsCard / Wave I 的 AccountFingerprintBind。
 */
export function AccountDangerZone({
  accountId,
  accountName,
  onDeleted,
}: {
  accountId: number
  accountName: string
  onDeleted: () => void
}) {
  const [typed, setTyped] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)

  // 手抄账号名严格匹配才解锁删除按钮(第一道护栏)。
  const unlocked = nameMatchesConfirmation(typed, accountName)

  async function onDelete() {
    // 双保险:即便按钮误触发(理论上 unlocked 已挡),这里仍复核一次手抄名。
    if (!nameMatchesConfirmation(typed, accountName)) {
      setError('请先在下方输入框完整抄写账号名以确认。')
      return
    }
    // 第二道护栏:操作系统级 window.confirm(含账号名 + #id,核对目标)。
    if (!window.confirm(confirmPromptText(accountName, accountId))) {
      return
    }
    setBusy(true)
    setError(null)
    setFlash(null)
    try {
      const res = await deleteProviderAccount(accountId, reason)
      setFlash(deleteResultMessage(res, accountName))
      // 删除成功 → 交父页跳回列表(短暂展示成功文案后由父页接管)。
      onDeleted()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '硬删账号失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section style={card}>
      <h2 style={{ fontSize: 14, color: 'var(--hk-danger)' }}>危险操作</h2>
      <p style={hint}>
        硬删该账号 <strong style={{ color: 'var(--hk-ink-900)' }}>{accountName}</strong>(#{accountId})。
        这是<strong style={{ color: 'var(--hk-danger)' }}>不可逆</strong>操作 —— 账号将从可调度池永久移除、无法恢复。
        若只是临时下线,请改用上方运维动作里的「停用账号」(可随时重新启用)。
      </p>

      {flash && <Banner tone="ok">{flash}</Banner>}
      {error && <Banner tone="danger">{error}</Banner>}

      <label style={{ fontSize: 12, color: 'var(--hk-ink-500)', display: 'flex', flexDirection: 'column', gap: 4 }}>
        确认:完整抄写账号名「{accountName}」以解锁删除
        <input
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          placeholder={accountName}
          style={inputStyle}
          autoComplete="off"
        />
      </label>

      <label style={{ fontSize: 12, color: 'var(--hk-ink-500)', display: 'flex', flexDirection: 'column', gap: 4 }}>
        审计原因(可选,记入 admin 审计)
        <input
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="如:账号已下线,清理出池"
          style={inputStyle}
        />
      </label>

      <div>
        <button type="button" disabled={busy || !unlocked} onClick={onDelete} style={unlocked ? dangerBtn : dangerDisabledBtn}>
          {busy ? '删除中…' : '硬删此账号(不可逆)'}
        </button>
        {!unlocked && <span style={{ ...hint, marginLeft: 'var(--hk-space-3)' }}>抄写账号名后按钮才会启用。</span>}
      </div>
    </section>
  )
}

const card: React.CSSProperties = {
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-danger-soft)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  padding: 'var(--hk-space-4)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
}
const hint: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-300)', margin: 0 }
const inputStyle: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
const baseBtn: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-4)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  fontWeight: 600,
  cursor: 'pointer',
}
const dangerBtn: React.CSSProperties = { ...baseBtn, border: '1px solid #c0463a', background: '#c0463a', color: '#fff' }
const dangerDisabledBtn: React.CSSProperties = {
  ...baseBtn,
  border: '1px solid var(--hk-line)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-300)',
  cursor: 'not-allowed',
}

function Banner({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const ok = tone === 'ok'
  return (
    <div
      style={{
        padding: 'var(--hk-space-3) var(--hk-space-4)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: ok ? '#0b6553' : 'var(--hk-danger)',
        background: ok ? 'var(--hk-primary-50)' : 'var(--hk-danger-soft)',
        border: `1px solid ${ok ? 'var(--hk-primary-100)' : 'var(--hk-danger-soft)'}`,
      }}
    >
      {children}
    </div>
  )
}
