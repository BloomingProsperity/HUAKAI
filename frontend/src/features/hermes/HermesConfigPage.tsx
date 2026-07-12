import { useState } from 'react'
import { useAuth } from '../../auth/store'
import {
  actorReady,
  loadActor,
  saveActor,
  toPositiveInt,
  type HermesActor,
} from './hermesContext'
import type { HermesAuthQuery } from './hermesClient'
import { HermesSettingsCard } from './HermesSettingsCard'
import { HermesToolExecuteCard } from './HermesToolExecuteCard'

/*
 * Hermes 运营台「配置 + 工具执行」子页(运营台壳,/admin/hermes)。
 *
 * 与只读对话面板(HermesPanel)分开:面板是纯只读;本页是 Owner 授权后接入的「改动型」侧——
 * per-user Hermes 配置启停 + api-profile CRUD + mutating 工具 dry-run→confirm 执行。
 *
 * 鉴权与面板同款:/v1/hermes 走 admin-only 中间件,必须显式带 admin token 作 Bearer,并用
 * as_user_id(+ 可选 tenant_id)指明所操作的 tenant user 上下文(裸调会回落 session token → 401)。
 * 无 admin token 走空状态(不发注定 401 的请求);未设操作身份时禁用所有改动入口。
 *
 * 安全姿态:
 *   - secret 只写不回显:创建 profile 只提交 FK 引用,后端响应不返机密;本页不持久任何 secret。
 *   - mutating 工具强制 dry-run→看 preview→强确认;删 profile 二次确认 + 处理 409 in-use。
 */
export function HermesConfigPage() {
  const auth = useAuth()
  const [actor, setActor] = useState<HermesActor>(() => loadActor())
  const [editingActor, setEditingActor] = useState(false)

  const adminToken = auth.adminToken
  const hasAdmin = !!adminToken
  const ready = hasAdmin && actorReady(actor)

  const authQuery: HermesAuthQuery | null =
    actor.asUserId !== null
      ? { asUserId: actor.asUserId, tenantId: actor.tenantId ?? undefined }
      : null

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>Hermes 配置与工具执行</h1>
          <p className="hk-sub">
            管理某用户的 Hermes 启停与 API profile,并执行 Hermes 工具。只读工具可直接运行;
            <strong>改动型工具强制走 dry-run → 看预览 → 二次确认</strong> 才执行。先设置操作身份。
          </p>
        </div>
      </header>

      <ActorBar actor={actor} editing={editingActor} onEdit={() => setEditingActor(true)} onCancel={() => setEditingActor(false)} onSave={(a) => { setActor(a); saveActor(a); setEditingActor(false) }} />

      {!hasAdmin ? (
        <EmptyState
          title="需运维者 token"
          desc="本页只在运营台可用,且必须使用运维者(admin)token。请先在系统设置里配置 admin token 后重试。"
        />
      ) : !ready || authQuery === null ? (
        <EmptyState
          title="请先设置操作身份"
          desc="改动型操作需指明 as_user_id(及可选 tenant_id)。点上方「设置操作身份」后继续。"
        />
      ) : (
        <>
          <HermesSettingsCard adminToken={adminToken!} auth={authQuery} />
          <HermesToolExecuteCard adminToken={adminToken!} auth={authQuery} />
        </>
      )}
    </div>
  )
}

// ── 操作身份条 ──

function ActorBar({
  actor,
  editing,
  onEdit,
  onCancel,
  onSave,
}: {
  actor: HermesActor
  editing: boolean
  onEdit: () => void
  onCancel: () => void
  onSave: (a: HermesActor) => void
}) {
  return (
    <div style={actorBar}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>操作身份</span>
        <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>
          {actorReady(actor)
            ? `as_user_id #${actor.asUserId}${actor.tenantId ? ` · 租户 ${actor.tenantId}` : ''}`
            : '未设置'}
        </span>
      </div>
      {!editing && (
        <button type="button" className="hk-btn" onClick={onEdit}>
          {actorReady(actor) ? '修改' : '设置操作身份'}
        </button>
      )}
      {editing && <ActorForm actor={actor} onSave={onSave} onCancel={onCancel} />}
    </div>
  )
}

function ActorForm({
  actor,
  onSave,
  onCancel,
}: {
  actor: HermesActor
  onSave: (a: HermesActor) => void
  onCancel: () => void
}) {
  const [asUser, setAsUser] = useState(actor.asUserId !== null ? String(actor.asUserId) : '')
  const [tenant, setTenant] = useState(actor.tenantId !== null ? String(actor.tenantId) : '')
  const parsedAsUser = toPositiveInt(asUser)
  const parsedTenant = tenant.trim() === '' ? null : toPositiveInt(tenant)
  const valid = parsedAsUser !== null && (tenant.trim() === '' || parsedTenant !== null)

  return (
    <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
      <label style={fieldLabel}>
        as_user_id(必填)
        <input style={inp} value={asUser} onChange={(e) => setAsUser(e.target.value)} placeholder="如 1" inputMode="numeric" />
      </label>
      <label style={fieldLabel}>
        tenant_id(可选)
        <input style={inp} value={tenant} onChange={(e) => setTenant(e.target.value)} placeholder="留空走 token scope" inputMode="numeric" />
      </label>
      <button type="button" className="hk-btn" onClick={onCancel}>
        取消
      </button>
      <button
        type="button"
        className="hk-btn hk-btn--green"
        style={{ opacity: valid ? 1 : 0.5 }}
        disabled={!valid}
        onClick={() => onSave({ asUserId: parsedAsUser, tenantId: parsedTenant })}
      >
        保存
      </button>
    </div>
  )
}

function EmptyState({ title, desc }: { title: string; desc: string }) {
  return (
    <div style={emptyState}>
      <div style={{ fontSize: 28 }} aria-hidden>
        🔒
      </div>
      <p style={{ margin: '12px 0 4px', fontWeight: 600, color: 'var(--hk-ink-900)' }}>{title}</p>
      <p style={{ margin: 0, color: 'var(--hk-ink-500)', fontSize: 13, lineHeight: 1.6, maxWidth: 520 }}>{desc}</p>
    </div>
  )
}

// ── 样式(沿用既有 token,不引入新外壳)──
const actorBar: React.CSSProperties = {
  display: 'flex',
  gap: 'var(--hk-space-3)',
  alignItems: 'center',
  flexWrap: 'wrap',
  padding: 'var(--hk-space-3) var(--hk-space-4)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
}
const fieldLabel: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-700)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', minWidth: 120 }
const emptyState: React.CSSProperties = { display: 'flex', flexDirection: 'column', alignItems: 'center', padding: 'var(--hk-space-6)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', textAlign: 'center' }
