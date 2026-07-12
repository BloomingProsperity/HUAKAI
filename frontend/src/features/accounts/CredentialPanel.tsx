import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  createAccountCredential,
  deleteAccountCredential,
  listAccountCredentials,
  rotateAccountCredential,
  setAccountCredentialState,
} from './credentialsApi'
import {
  AUTH_MODE_OPTIONS,
  CREDENTIAL_STATES,
  VENDOR_OPTIONS,
  buildCreateBody,
  buildRotateBody,
  buildStateBody,
  credentialStateLabel,
  credentialStateTone,
  externalAccountLabel,
  fmtTime,
} from './credentials'
import type { CredentialMetadata, CredentialStateValue } from './credentials'

/*
 * 账号凭证 CRUD 面板(账号详情页,独立组件,不改 AccountDetailPage)。
 *
 * 端点全部挂在 /admin/v1/provider-accounts/{id}/credentials...(见 credentialsApi.ts 头注与
 * backend/internal/gatewayhttp/admin_credentials_handler.go)。tenant_id:平台管理员需带,
 * 单租户默认 1,与 AccountFingerprintBind 同样从 account.tenant_id 注入。
 *
 * SECRET-MASK(§4 铁律):
 *   - 列表/状态/详情只展示 metadata(CredentialMetadata,结构上无 secret 字段)。
 *   - secret 只在「文本域 → buildCreateBody/buildRotateBody → POST body.credentials」上出现。
 *   - 提交成功(或失败)后立即清空 secret 文本域;secret 绝不进入任何 useState 持久态、
 *     绝不渲染回页面、绝不 console.log。新增/轮换的 secret 各自有局部短命 state,提交即清。
 *
 * 破坏性/敏感动作(rotate 替换活跃 secret / delete 删凭证 / state 切换)均 window.confirm,
 * 且各带 reason 输入(进后端审计)。
 */
export function CredentialPanel({ accountId, tenantId }: { accountId: number; tenantId: number }) {
  const [rows, setRows] = useState<CredentialMetadata[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [banner, setBanner] = useState<{ tone: 'ok' | 'danger'; msg: string } | null>(null)

  const reload = useCallback(
    async (signal?: AbortSignal) => {
      if (!Number.isInteger(tenantId) || tenantId <= 0) {
        setLoadError('该账号缺少有效 tenant_id,无法加载凭证')
        setLoading(false)
        return
      }
      setLoading(true)
      setLoadError(null)
      try {
        const data = await listAccountCredentials(accountId, tenantId, signal)
        setRows(data)
      } catch (e) {
        if (signal?.aborted) return
        setLoadError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载凭证失败')
      } finally {
        if (!signal?.aborted) setLoading(false)
      }
    },
    [accountId, tenantId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    void reload(ctrl.signal)
    return () => ctrl.abort()
  }, [reload])

  const flash = (tone: 'ok' | 'danger', msg: string) => setBanner({ tone, msg })

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <h2 style={{ fontSize: 15, color: 'var(--hk-ink-700)' }}>账号凭证</h2>
      <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }}>
        凭证 secret(API Key / OAuth token 等)只写不回显:仅在下方表单提交时随请求发出,
        页面任何位置都不展示已存 secret。下表只展示运维元数据。
      </p>

      {banner && <Banner tone={banner.tone}>{banner.msg}</Banner>}

      {/* 现有凭证表(只 metadata) */}
      <div style={card}>
        <h3 style={cardTitle}>现有凭证</h3>
        {loading && <div style={muted}>加载中…</div>}
        {loadError && <Banner tone="danger">{loadError}</Banner>}
        {!loading && !loadError && rows.length === 0 && <div style={muted}>该账号暂无凭证</div>}
        {!loading && !loadError && rows.length > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
            {rows.map((row) => (
              <CredentialRow
                key={row.id}
                accountId={accountId}
                tenantId={tenantId}
                row={row}
                onChanged={(tone, msg) => {
                  flash(tone, msg)
                  void reload()
                }}
              />
            ))}
          </div>
        )}
      </div>

      {/* 新增凭证表单(secret 只写) */}
      <CreateCredentialForm
        accountId={accountId}
        tenantId={tenantId}
        onCreated={(msg) => {
          flash('ok', msg)
          void reload()
        }}
        onError={(msg) => flash('danger', msg)}
      />
    </section>
  )
}

// ── 单行:metadata 展示 + rotate / state / delete 动作 ─────────────────────────
function CredentialRow({
  accountId,
  tenantId,
  row,
  onChanged,
}: {
  accountId: number
  tenantId: number
  row: CredentialMetadata
  onChanged: (tone: 'ok' | 'danger', msg: string) => void
}) {
  // rotate 的新 secret —— 局部短命 state,提交后立即清空(SECRET-MASK)。
  const [rotateOpen, setRotateOpen] = useState(false)
  const [rotateSecret, setRotateSecret] = useState('')
  const [rotateReason, setRotateReason] = useState('')
  const [nextState, setNextState] = useState<CredentialStateValue>(
    (CREDENTIAL_STATES.find((s) => s !== row.state) ?? 'active') as CredentialStateValue,
  )
  const [stateReason, setStateReason] = useState('')
  const [busy, setBusy] = useState<string | null>(null)

  const fail = (e: unknown, fallback: string) =>
    onChanged('danger', e instanceof ApiError ? `${e.message}(${e.code})` : fallback)

  const doRotate = async () => {
    const built = buildRotateBody({ tenantId, secretJSON: rotateSecret, reason: rotateReason })
    if (!built.ok) {
      onChanged('danger', built.error)
      return
    }
    if (!window.confirm(`轮换凭证 #${row.id}(${row.vendor}/${row.auth_mode})?将替换当前活跃 secret,不可撤销。`)) {
      return
    }
    setBusy('rotate')
    try {
      await rotateAccountCredential(accountId, row.id, built.value)
      // SECRET-MASK:成功后立即清空新 secret 输入。
      setRotateSecret('')
      setRotateReason('')
      setRotateOpen(false)
      onChanged('ok', `凭证 #${row.id} 已轮换`)
    } catch (e) {
      // 失败也清空 secret(不残留在内存/DOM 中)。
      setRotateSecret('')
      fail(e, '轮换失败')
    } finally {
      setBusy(null)
    }
  }

  const doSetState = async () => {
    const built = buildStateBody({ tenantId, state: nextState, reason: stateReason })
    if (!built.ok) {
      onChanged('danger', built.error)
      return
    }
    if (!window.confirm(`将凭证 #${row.id} 状态置为「${credentialStateLabel(nextState)}」?`)) return
    setBusy('state')
    try {
      await setAccountCredentialState(accountId, row.id, built.value)
      setStateReason('')
      onChanged('ok', `凭证 #${row.id} 状态已更新`)
    } catch (e) {
      fail(e, '状态更新失败')
    } finally {
      setBusy(null)
    }
  }

  const doDelete = async () => {
    if (!Number.isInteger(tenantId) || tenantId <= 0) {
      onChanged('danger', 'tenant_id 无效,无法删除')
      return
    }
    const reason = window.prompt(`删除凭证 #${row.id}(${row.vendor}/${row.auth_mode})?\n此操作不可撤销。请输入删除原因(进审计):`, '')
    if (reason === null) return // 取消
    setBusy('delete')
    try {
      await deleteAccountCredential(accountId, row.id, { tenant_id: tenantId, reason: reason.trim() || undefined })
      onChanged('ok', `凭证 #${row.id} 已删除`)
    } catch (e) {
      fail(e, '删除失败')
    } finally {
      setBusy(null)
    }
  }

  return (
    <div style={rowCard}>
      {/* metadata 行(只读展示,无 secret) */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-900)' }}>
          #{row.id} · {row.vendor}/{row.auth_mode}
        </span>
        <StatusBadge tone={credentialStateTone(row.state)}>{credentialStateLabel(row.state)}</StatusBadge>
        <span style={metaChip}>v{row.credential_version}</span>
        {row.failure_count > 0 && (
          <span style={{ ...metaChip, color: 'var(--hk-danger)' }}>失败 {row.failure_count}</span>
        )}
      </div>
      <Grid
        rows={[
          ['上游账号', externalAccountLabel(row)],
          ['access 过期', fmtTime(row.access_expires_at)],
          ['需刷新于', fmtTime(row.refresh_before_at)],
          ['最近刷新', fmtTime(row.last_refresh_at)],
          ['刷新结果', row.last_refresh_outcome || '—'],
          ['失败分类', row.failure_class || '—'],
          ['创建', fmtTime(row.created_at)],
          ['更新', fmtTime(row.updated_at)],
        ]}
      />

      {/* 状态切换 */}
      <Row label="切换状态" hint="active/disabled 等;进审计">
        <select
          value={nextState}
          onChange={(e) => setNextState(e.target.value as CredentialStateValue)}
          style={inp}
        >
          {CREDENTIAL_STATES.map((s) => (
            <option key={s} value={s}>
              {credentialStateLabel(s)}({s})
            </option>
          ))}
        </select>
        <input
          value={stateReason}
          onChange={(e) => setStateReason(e.target.value)}
          placeholder="原因(可选)"
          style={inp}
        />
        <button type="button" disabled={busy !== null} onClick={() => void doSetState()} style={ghostBtn}>
          {busy === 'state' ? '处理中…' : '置状态'}
        </button>
      </Row>

      {/* 轮换(替换活跃 secret) */}
      {!rotateOpen ? (
        <Row label="轮换 secret" hint="替换当前活跃 secret(破坏性)">
          <button type="button" disabled={busy !== null} onClick={() => setRotateOpen(true)} style={dangerGhost}>
            填写新 secret…
          </button>
        </Row>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>
            轮换 secret(JSON 对象,只写)
          </span>
          <textarea
            value={rotateSecret}
            onChange={(e) => setRotateSecret(e.target.value)}
            placeholder='{"api_key":"..."} 或 {"access_token":"...","refresh_token":"..."}'
            spellCheck={false}
            autoComplete="off"
            style={secretArea}
          />
          <input
            value={rotateReason}
            onChange={(e) => setRotateReason(e.target.value)}
            placeholder="轮换原因(可选,进审计)"
            style={inp}
          />
          <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
            <button type="button" disabled={busy !== null} onClick={() => void doRotate()} style={dangerSolid}>
              {busy === 'rotate' ? '轮换中…' : '确认轮换'}
            </button>
            <button
              type="button"
              disabled={busy !== null}
              onClick={() => {
                // 取消也清空 secret(不残留)。
                setRotateSecret('')
                setRotateReason('')
                setRotateOpen(false)
              }}
              style={ghostBtn}
            >
              取消
            </button>
          </div>
        </div>
      )}

      {/* 删除 */}
      <Row label="删除凭证" hint="从该账号移除此凭证(破坏性)">
        <button type="button" disabled={busy !== null} onClick={() => void doDelete()} style={dangerGhost}>
          {busy === 'delete' ? '删除中…' : '删除'}
        </button>
      </Row>
    </div>
  )
}

// ── 新增凭证表单(secret 只写)──────────────────────────────────────────────────
function CreateCredentialForm({
  accountId,
  tenantId,
  onCreated,
  onError,
}: {
  accountId: number
  tenantId: number
  onCreated: (msg: string) => void
  onError: (msg: string) => void
}) {
  const [vendor, setVendor] = useState(VENDOR_OPTIONS[0]?.value ?? '')
  const [authMode, setAuthMode] = useState(AUTH_MODE_OPTIONS[0]?.value ?? '')
  // secret 文本域 —— 局部短命 state,提交后立即清空(SECRET-MASK)。
  const [secret, setSecret] = useState('')
  const [extId, setExtId] = useState('')
  const [extEmail, setExtEmail] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    const built = buildCreateBody({
      tenantId,
      vendor,
      authMode,
      secretJSON: secret,
      externalAccountId: extId,
      externalAccountEmail: extEmail,
      reason,
    })
    if (!built.ok) {
      onError(built.error)
      return
    }
    setBusy(true)
    try {
      await createAccountCredential(accountId, built.value)
      // SECRET-MASK:成功后立即清空 secret(及其它输入)。
      setSecret('')
      setExtId('')
      setExtEmail('')
      setReason('')
      onCreated('凭证已新增')
    } catch (e) {
      // 失败也清空 secret(不残留)。
      setSecret('')
      onError(e instanceof ApiError ? `${e.message}(${e.code})` : '新增失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div style={card}>
      <h3 style={cardTitle}>新增凭证</h3>
      <Row label="厂商 / 鉴权方式" hint="vendor + auth_mode 决定 secret 必填字段">
        <select value={vendor} onChange={(e) => setVendor(e.target.value)} style={inp}>
          {VENDOR_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <select value={authMode} onChange={(e) => setAuthMode(e.target.value)} style={inp}>
          {AUTH_MODE_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </Row>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>
          secret 材料(JSON 对象,只写不回显)
        </span>
        <textarea
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
          placeholder='例如 {"api_key":"sk-..."} 或 {"access_token":"...","refresh_token":"..."}'
          spellCheck={false}
          autoComplete="off"
          style={secretArea}
        />
        <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>
          提交后此框立即清空;系统不会在任何地方回显已存 secret。
        </span>
      </div>

      <Row label="上游账号(可选)" hint="OAuth 流程会自动提取;手动路径可在此填">
        <input value={extId} onChange={(e) => setExtId(e.target.value)} placeholder="external_account_id(可选)" style={inp} />
        <input value={extEmail} onChange={(e) => setExtEmail(e.target.value)} placeholder="external_account_email(可选)" style={inp} />
      </Row>

      <Row label="原因(可选)" hint="进 admin 审计">
        <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="如:运维手动导入" style={inp} />
      </Row>

      <div>
        <button type="button" disabled={busy} onClick={() => void submit()} style={primaryBtn}>
          {busy ? '提交中…' : '新增凭证'}
        </button>
      </div>
    </div>
  )
}

// ── 小组件 / 样式 ─────────────────────────────────────────────────────────────
function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--hk-space-2)' }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>{label}</span>
        {hint && <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{hint}</span>}
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
        {children}
      </div>
    </div>
  )
}

function Grid({ rows }: { rows: Array<[string, string]> }) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '4px var(--hk-space-3)', fontSize: 12 }}>
      {rows.map(([k, v]) => (
        <div key={k} style={{ display: 'contents' }}>
          <span style={{ color: 'var(--hk-ink-500)' }}>{k}</span>
          <span style={{ color: 'var(--hk-ink-900)' }}>{v}</span>
        </div>
      ))}
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
        background: danger ? 'var(--hk-danger-soft)' : 'var(--hk-primary-50, var(--hk-primary-50))',
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
const rowCard: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
  padding: 'var(--hk-space-3)',
  background: 'var(--hk-surface-sunken, #fafafa)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
}
const cardTitle: React.CSSProperties = { fontSize: 13, color: 'var(--hk-ink-700)', margin: 0 }
const muted: React.CSSProperties = { fontSize: 13, color: 'var(--hk-ink-500)' }
const metaChip: React.CSSProperties = { fontSize: 11, color: 'var(--hk-ink-500)', padding: '1px 6px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-pill)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', flex: 1, minWidth: 140 }
const secretArea: React.CSSProperties = { minHeight: 88, padding: 'var(--hk-space-2) var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, fontFamily: 'var(--hk-font-mono, monospace)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', resize: 'vertical' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer', flexShrink: 0 }
const dangerGhost: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #e6b3ab', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-danger)', fontSize: 13, cursor: 'pointer' }
const dangerSolid: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-danger)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-danger)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
