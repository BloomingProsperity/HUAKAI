import { useCallback, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  EMPTY_WIZARD_FORM,
  buildCallbackBody,
  buildImportBody,
  buildOAuthStartBody,
  canCancel,
  canDeliverCallback,
  methodLabel,
  statusLabel,
  statusTone,
  validateCallback,
  validateImport,
  validateOAuthStart,
  type AcquisitionFlow,
  type CredentialMetadata,
  type WizardForm,
  type WizardMethod,
} from './acquisition'
import {
  cancelAcquisition,
  deliverCallback,
  getAcquisitionFlow,
  importCredentials,
  startAcquisition,
} from './acquisitionApi'

/*
 * 凭证获取/导入向导(账号详情页用)。
 *
 * 两条主路径:
 *   ① OAuth:start → 展示授权 URL(operator 让用户去授权)→ 回填 code/state → callback(后端自动 finalize)。
 *   ② 导入:把凭据原文粘进 content 文本域 → POST helper(finalize=true 直接落库)。
 *
 * 最高优先级 SECRET-MASK(§4 硬规则):
 *   - content / client_secret / code / state 等 secret/敏感材料只存在于受控输入框 → 请求体 → POST 这一路径;
 *   - 永不渲染回页面、永不 console.log、永不进任何持久态;
 *   - 每次提交成功后立即清空对应输入框(clearSecrets);
 *   - 页面只展示后端返回的 flow 状态 + 凭证 metadata(无 secret 字段)。
 *
 * 破坏性/敏感动作(start 替换性获取 / callback 凭据交换 / cancel)均带 reason 输入 + window.confirm。
 */

/** 向导的方式选项(展示顺序)。 */
const METHODS: WizardMethod[] = ['oauth', 'paste', 'cli_import', 'csv_import', 'json_import']

export function CredentialAcquisitionWizard({
  accountId,
  tenantId,
}: {
  accountId: number
  tenantId: number
}) {
  const [method, setMethod] = useState<WizardMethod>('oauth')
  const [formState, setFormState] = useState<WizardForm>({ ...EMPTY_WIZARD_FORM })

  // OAuth 进行中的流。authorizeUrl/state 来自 start 响应(state 非 secret,授权回来要原样带回)。
  const [flow, setFlow] = useState<AcquisitionFlow | null>(null)
  const [authorizeUrl, setAuthorizeUrl] = useState<string | null>(null)
  // callback 输入(敏感,提交后清空)。state 预填 start 返回值以减少 operator 抄写。
  const [callbackState, setCallbackState] = useState('')
  const [callbackCode, setCallbackCode] = useState('')

  // 最近一次成功落库的凭证 metadata(只展示元数据,无 secret)。
  const [lastCredential, setLastCredential] = useState<CredentialMetadata | null>(null)

  const [busy, setBusy] = useState(false)
  const [flash, setFlash] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const tenantOk = Number.isInteger(tenantId) && tenantId > 0

  /** 清空所有 secret/敏感输入(content / client_secret / callback code-state)。 */
  const clearSecrets = useCallback(() => {
    setFormState((prev) => ({
      ...prev,
      content: '',
      oauthClient: { ...prev.oauthClient, clientSecret: '' },
    }))
    setCallbackCode('')
    setCallbackState('')
  }, [])

  function patch(p: Partial<WizardForm>) {
    setFormState((prev) => ({ ...prev, ...p }))
  }
  function patchClient(p: Partial<WizardForm['oauthClient']>) {
    setFormState((prev) => ({ ...prev, oauthClient: { ...prev.oauthClient, ...p } }))
  }

  function fail(e: unknown, fallback: string) {
    setError(e instanceof ApiError ? `${e.message}(${e.code})` : fallback)
  }

  // ── OAuth:发起流 ────────────────────────────────────────────────────────────
  async function onStartOAuth() {
    setError(null)
    setFlash(null)
    const v = validateOAuthStart(tenantId, formState)
    if (!v.ok) {
      setError(v.message ?? '校验失败')
      return
    }
    // start 是敏感的获取动作(会替换/新建活跃凭证),二次确认。
    if (!window.confirm('确认发起 OAuth 获取流?将引导用户完成上游账号授权并落库新凭证。')) {
      return
    }
    setBusy(true)
    try {
      const res = await startAcquisition(accountId, buildOAuthStartBody(tenantId, accountId, formState))
      setFlow(res.flow)
      setAuthorizeUrl(res.authorize_url ?? null)
      // 预填 state(非 secret),减少 operator 手抄。
      setCallbackState(res.state ?? '')
      setFlash('OAuth 流已发起。请让用户打开下方授权链接完成授权,再回填 code/state。')
    } catch (e) {
      fail(e, '发起 OAuth 流失败')
    } finally {
      setBusy(false)
      // client_secret 无论成功失败都清空(secret 不滞留输入态)。
      patchClient({ clientSecret: '' })
    }
  }

  // ── OAuth:投递回调(后端自动 finalize)────────────────────────────────────────
  async function onDeliverCallback() {
    if (!flow) return
    setError(null)
    setFlash(null)
    const v = validateCallback(callbackState, callbackCode)
    if (!v.ok) {
      setError(v.message ?? '校验失败')
      return
    }
    if (!window.confirm('确认投递授权回调?后端将用 code 交换上游凭证并落库。')) {
      return
    }
    setBusy(true)
    try {
      const res = await deliverCallback(accountId, flow.id, buildCallbackBody(callbackState, callbackCode))
      setFlow(res.flow)
      setLastCredential(res.credential ?? null)
      setFlash(res.already_finalized ? '该流此前已落库(幂等)。' : '凭证已成功落库。')
    } catch (e) {
      fail(e, '投递回调失败')
    } finally {
      setBusy(false)
      // code 是 secret 材料,无论成功失败都清空。
      clearSecrets()
    }
  }

  // ── OAuth:轮询流状态 ─────────────────────────────────────────────────────────
  async function onRefreshFlow() {
    if (!flow) return
    setError(null)
    setBusy(true)
    try {
      const res = await getAcquisitionFlow(accountId, flow.id)
      setFlow(res.flow)
    } catch (e) {
      fail(e, '刷新流状态失败')
    } finally {
      setBusy(false)
    }
  }

  // ── OAuth:取消流 ────────────────────────────────────────────────────────────
  async function onCancel() {
    if (!flow) return
    if (!window.confirm('确认取消该获取流?未完成的授权将作废。')) {
      return
    }
    setError(null)
    setFlash(null)
    setBusy(true)
    try {
      const res = await cancelAcquisition(accountId, flow.id)
      setFlow(res.flow)
      clearSecrets()
      setFlash('获取流已取消。')
    } catch (e) {
      fail(e, '取消流失败')
    } finally {
      setBusy(false)
    }
  }

  // ── 导入:粘贴 / CLI / CSV / JSON(content 只写)────────────────────────────────
  async function onImport() {
    setError(null)
    setFlash(null)
    const v = validateImport(tenantId, formState)
    if (!v.ok) {
      setError(v.message ?? '校验失败')
      return
    }
    if (!window.confirm(`确认以「${methodLabel(method)}」方式导入并落库凭证?`)) {
      return
    }
    setBusy(true)
    try {
      // finalize=true:导入即落库(一步到位)。content 是 secret 材料,提交后立即清空。
      const res = await importCredentials(method, buildImportBody(tenantId, accountId, formState, true))
      const finalized = res.finalized && res.finalized.length > 0 ? res.finalized[0] : null
      setLastCredential(finalized?.credential ?? null)
      const n = res.finalized?.length ?? 0
      setFlash(n > 0 ? `导入成功,已落库 ${n} 条凭证。` : '导入流已建立(未落库)。')
    } catch (e) {
      fail(e, '导入凭证失败')
    } finally {
      setBusy(false)
      // content 是 secret 材料,无论成功失败都清空。
      clearSecrets()
    }
  }

  const isOAuth = method === 'oauth'

  return (
    <section style={card}>
      <h2 style={{ fontSize: 14, color: 'var(--hk-ink-500)', margin: 0 }}>凭证获取 / 导入</h2>
      <p style={hint}>
        为该账号(#{accountId})获取上游凭证。OAuth 走授权流;其余走凭据导入。
        <strong>所有密钥/令牌只写不回显:提交后立即清空,页面只展示流状态与元数据。</strong>
      </p>

      {!tenantOk && <Banner tone="danger">租户 ID 非法,无法发起获取流。</Banner>}
      {flash && <Banner tone="ok">{flash}</Banner>}
      {error && <Banner tone="danger">{error}</Banner>}

      {/* ① 选方式 */}
      <div style={row}>
        <label style={fieldLabel}>方式</label>
        <select
          value={method}
          disabled={busy}
          onChange={(e) => {
            setMethod(e.target.value as WizardMethod)
            // 切换方式时清空 secret 输入,避免把上一种方式的 content/secret 误带过去。
            clearSecrets()
            setError(null)
          }}
          style={selectStyle}
        >
          {METHODS.map((m) => (
            <option key={m} value={m}>
              {methodLabel(m)}
            </option>
          ))}
        </select>
      </div>

      {/* 公共字段:vendor / auth_mode / reason */}
      <div style={grid}>
        <Field label="vendor" hintText="如 anthropic / openai / gemini">
          <input
            style={inputStyle}
            value={formState.vendor}
            disabled={busy}
            onChange={(e) => patch({ vendor: e.target.value })}
            placeholder="anthropic"
          />
        </Field>
        <Field label="auth_mode" hintText="如 claude_ai_oauth / api_key">
          <input
            style={inputStyle}
            value={formState.authMode}
            disabled={busy}
            onChange={(e) => patch({ authMode: e.target.value })}
            placeholder="claude_ai_oauth"
          />
        </Field>
        <Field label="reason(审计)" hintText="本次操作原因,写入审计">
          <input
            style={inputStyle}
            value={formState.reason}
            disabled={busy}
            onChange={(e) => patch({ reason: e.target.value })}
            placeholder="运维补录凭证"
          />
        </Field>
      </div>

      {isOAuth ? (
        <OAuthPanel
          form={formState}
          busy={busy}
          flow={flow}
          authorizeUrl={authorizeUrl}
          callbackState={callbackState}
          callbackCode={callbackCode}
          onPatch={patch}
          onPatchClient={patchClient}
          onSetCallbackState={setCallbackState}
          onSetCallbackCode={setCallbackCode}
          onStart={onStartOAuth}
          onDeliver={onDeliverCallback}
          onRefresh={onRefreshFlow}
          onCancel={onCancel}
        />
      ) : (
        <ImportPanel
          method={method}
          content={formState.content}
          busy={busy}
          onContent={(content) => patch({ content })}
          onImport={onImport}
        />
      )}

      {lastCredential && <CredentialCard meta={lastCredential} />}
    </section>
  )
}

// ── OAuth 子面板 ───────────────────────────────────────────────────────────────

function OAuthPanel(props: {
  form: WizardForm
  busy: boolean
  flow: AcquisitionFlow | null
  authorizeUrl: string | null
  callbackState: string
  callbackCode: string
  onPatch: (p: Partial<WizardForm>) => void
  onPatchClient: (p: Partial<WizardForm['oauthClient']>) => void
  onSetCallbackState: (v: string) => void
  onSetCallbackCode: (v: string) => void
  onStart: () => void
  onDeliver: () => void
  onRefresh: () => void
  onCancel: () => void
}) {
  const {
    form,
    busy,
    flow,
    authorizeUrl,
    callbackState,
    callbackCode,
    onPatch,
    onPatchClient,
    onSetCallbackState,
    onSetCallbackCode,
    onStart,
    onDeliver,
    onRefresh,
    onCancel,
  } = props

  return (
    <div style={panel}>
      <div style={row}>
        <label style={{ ...fieldLabel, display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <input
            type="checkbox"
            checked={form.useCustomOAuthClient}
            disabled={busy}
            onChange={(e) => onPatch({ useCustomOAuthClient: e.target.checked })}
          />
          使用自定义 OAuth client(否则走后端默认 public client)
        </label>
      </div>

      {form.useCustomOAuthClient && (
        <div style={grid}>
          <Field label="client_id">
            <input
              style={inputStyle}
              value={form.oauthClient.clientId}
              disabled={busy}
              onChange={(e) => onPatchClient({ clientId: e.target.value })}
            />
          </Field>
          <Field label="client_secret(只写)" hintText="提交后清空,绝不回显">
            <input
              style={inputStyle}
              type="password"
              autoComplete="off"
              value={form.oauthClient.clientSecret}
              disabled={busy}
              onChange={(e) => onPatchClient({ clientSecret: e.target.value })}
            />
          </Field>
          <Field label="auth_url">
            <input
              style={inputStyle}
              value={form.oauthClient.authUrl}
              disabled={busy}
              onChange={(e) => onPatchClient({ authUrl: e.target.value })}
            />
          </Field>
          <Field label="token_url">
            <input
              style={inputStyle}
              value={form.oauthClient.tokenUrl}
              disabled={busy}
              onChange={(e) => onPatchClient({ tokenUrl: e.target.value })}
            />
          </Field>
          <Field label="redirect_uri">
            <input
              style={inputStyle}
              value={form.oauthClient.redirectUri}
              disabled={busy}
              onChange={(e) => onPatchClient({ redirectUri: e.target.value })}
            />
          </Field>
          <Field label="scopes(空格/逗号分隔)">
            <input
              style={inputStyle}
              value={form.oauthClient.scopes}
              disabled={busy}
              onChange={(e) => onPatchClient({ scopes: e.target.value })}
            />
          </Field>
        </div>
      )}

      <div style={row}>
        <button type="button" disabled={busy} onClick={onStart} style={primaryBtn}>
          {busy ? '处理中…' : '发起 OAuth 流'}
        </button>
      </div>

      {flow && (
        <div style={panel}>
          <div style={row}>
            <span style={hint}>流 ID</span>
            <code style={{ fontSize: 12 }}>{flow.id}</code>
            <StatusBadge tone={statusTone(flow.status)}>{statusLabel(flow.status)}</StatusBadge>
          </div>

          {authorizeUrl && (
            <p style={hint}>
              授权链接:
              <a href={authorizeUrl} target="_blank" rel="noreferrer" style={{ marginLeft: 6 }}>
                打开上游授权页
              </a>
            </p>
          )}

          {canDeliverCallback(flow) && (
            <div style={grid}>
              <Field label="回调 state" hintText="授权回调里返回的 state">
                <input
                  style={inputStyle}
                  value={callbackState}
                  disabled={busy}
                  onChange={(e) => onSetCallbackState(e.target.value)}
                />
              </Field>
              <Field label="回调 code" hintText="授权回调里返回的 code(提交后清空)">
                <input
                  style={inputStyle}
                  autoComplete="off"
                  value={callbackCode}
                  disabled={busy}
                  onChange={(e) => onSetCallbackCode(e.target.value)}
                />
              </Field>
            </div>
          )}

          <div style={row}>
            {canDeliverCallback(flow) && (
              <button type="button" disabled={busy} onClick={onDeliver} style={primaryBtn}>
                投递回调并落库
              </button>
            )}
            <button type="button" disabled={busy} onClick={onRefresh} style={ghostBtn}>
              刷新状态
            </button>
            {canCancel(flow.status) && (
              <button type="button" disabled={busy} onClick={onCancel} style={dangerBtn}>
                取消流
              </button>
            )}
          </div>
          {flow.error_class && (
            <Banner tone="danger">
              失败类别:{flow.error_class}
              {flow.error_message_redacted ? `(${flow.error_message_redacted})` : ''}
            </Banner>
          )}
        </div>
      )}
    </div>
  )
}

// ── 导入子面板 ─────────────────────────────────────────────────────────────────

function ImportPanel(props: {
  method: WizardMethod
  content: string
  busy: boolean
  onContent: (v: string) => void
  onImport: () => void
}) {
  const { method, content, busy, onContent, onImport } = props
  return (
    <div style={panel}>
      <Field
        label="凭据原文(只写)"
        hintText={
          method === 'csv_import'
            ? 'CSV:每行一条凭据。提交后立即清空,绝不回显。'
            : method === 'json_import'
              ? 'JSON 数组或对象。提交后立即清空,绝不回显。'
              : '凭据 JSON / CLI 凭据文件内容。提交后立即清空,绝不回显。'
        }
      >
        <textarea
          style={textareaStyle}
          value={content}
          disabled={busy}
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => onContent(e.target.value)}
          placeholder="粘贴要导入的凭据内容…"
          rows={8}
        />
      </Field>
      <div style={row}>
        <button type="button" disabled={busy} onClick={onImport} style={primaryBtn}>
          {busy ? '导入中…' : '导入并落库'}
        </button>
      </div>
    </div>
  )
}

// ── 凭证 metadata 卡(只展示元数据,无 secret)──────────────────────────────────

function CredentialCard({ meta }: { meta: CredentialMetadata }) {
  return (
    <div style={panel}>
      <h3 style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>已落库凭证(仅元数据)</h3>
      <dl style={metaGrid}>
        <Meta k="ID" v={String(meta.id)} />
        <Meta k="vendor" v={meta.vendor} />
        <Meta k="auth_mode" v={meta.auth_mode} />
        <Meta k="state" v={meta.state} />
        <Meta k="版本" v={String(meta.credential_version)} />
        <Meta k="access_expires_at" v={meta.access_expires_at ?? '—'} />
        <Meta k="refresh_before_at" v={meta.refresh_before_at ?? '—'} />
        <Meta k="last_refresh_outcome" v={meta.last_refresh_outcome ?? '—'} />
        <Meta k="failure_class" v={meta.failure_class ?? '—'} />
        <Meta k="failure_count" v={String(meta.failure_count)} />
        <Meta k="external_account_id" v={meta.external_account_id ?? '—'} />
        <Meta k="external_account_email" v={meta.external_account_email ?? '—'} />
      </dl>
    </div>
  )
}

function Meta({ k, v }: { k: string; v: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <dt style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{k}</dt>
      <dd style={{ margin: 0, fontSize: 12, color: 'var(--hk-ink-900)', wordBreak: 'break-all' }}>{v}</dd>
    </div>
  )
}

function Field({
  label,
  hintText,
  children,
}: {
  label: string
  hintText?: string
  children: React.ReactNode
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <label style={fieldLabel}>{label}</label>
      {children}
      {hintText && <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{hintText}</span>}
    </div>
  )
}

function Banner({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const ok = tone === 'ok'
  return (
    <div
      style={{
        padding: 'var(--hk-space-3) var(--hk-space-4)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: ok ? '#0b6553' : '#8f322a',
        background: ok ? 'var(--hk-primary-50)' : '#fbe9e7',
        border: `1px solid ${ok ? 'var(--hk-primary-100)' : '#f2cdc8'}`,
      }}
    >
      {children}
    </div>
  )
}

// ── 样式(只引设计 token)─────────────────────────────────────────────────────

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
const panel: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
  padding: 'var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
}
const row: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--hk-space-3)',
  flexWrap: 'wrap',
}
const grid: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))',
  gap: 'var(--hk-space-3)',
}
const metaGrid: React.CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))',
  gap: 'var(--hk-space-3)',
  margin: 0,
}
const hint: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-300)', margin: 0 }
const fieldLabel: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)', fontWeight: 600 }
const inputStyle: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
const textareaStyle: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 12,
  fontFamily: 'var(--hk-font-mono, monospace)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  resize: 'vertical',
  minHeight: 140,
}
const selectStyle: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  minWidth: 220,
}
const baseBtn: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-4)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  fontWeight: 600,
  cursor: 'pointer',
}
const primaryBtn: React.CSSProperties = {
  ...baseBtn,
  border: '1px solid var(--hk-primary-600)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
}
const ghostBtn: React.CSSProperties = {
  ...baseBtn,
  border: '1px solid var(--hk-line)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
const dangerBtn: React.CSSProperties = {
  ...baseBtn,
  border: '1px solid #f2cdc8',
  background: '#fbe9e7',
  color: '#8f322a',
}
