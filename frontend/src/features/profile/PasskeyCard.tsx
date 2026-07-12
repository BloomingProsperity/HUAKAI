import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { deletePasskey, listPasskeys, registerPasskeyBegin, registerPasskeyFinish } from './api'
import { passkeyLabel } from './profile'
import {
  buildPasskeyStepUp,
  passkeyRegistrationSupported,
  serializeAttestation,
  toPublicKeyCreationOptions,
  type PasskeyStepUpMethod,
} from './passkeyRegistration'
import type { PasskeyItem } from './types'

interface PendingRegistration {
  sessionId: string
  name: string
  credential: Record<string, unknown>
}

export function PasskeyCard() {
  const [items, setItems] = useState<PasskeyItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [registering, setRegistering] = useState(false)
  const [name, setName] = useState('')
  const [method, setMethod] = useState<PasskeyStepUpMethod>('password')
  const [stepUpValue, setStepUpValue] = useState('')
  const [pending, setPending] = useState<PendingRegistration | null>(null)
  const supported = passkeyRegistrationSupported()

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listPasskeys(signal)
      .then((response) => setItems(response.passkeys ?? []))
      .catch((cause: unknown) => {
        if (!signal.aborted) setError(errorText(cause, '加载通行密钥失败'))
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    load(controller.signal)
    return () => controller.abort()
  }, [load, refreshNonce])

  const markRegistered = () => {
    setPending(null)
    setName('')
    setStepUpValue('')
    setFlash('Passkey 已添加')
    setRefreshNonce((value) => value + 1)
  }

  const beginRegistration = async () => {
    if (!supported) {
      setError('当前浏览器不支持 WebAuthn Passkey 注册')
      return
    }
    const built = buildPasskeyStepUp(method, stepUpValue)
    if (!built.ok) {
      setError(built.error)
      return
    }
    const cleanName = name.trim()
    setRegistering(true)
    setError(null)
    setFlash(null)
    try {
      const begin = await registerPasskeyBegin(cleanName, built.proof)
      const publicKey = toPublicKeyCreationOptions(begin.public_key)
      const created = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null
      if (!created) {
        setError('Passkey 创建已取消')
        return
      }
      const next = {
        sessionId: begin.session_id,
        name: cleanName,
        credential: serializeAttestation(created),
      }
      if (method === 'two_factor_code') {
        setPending(next)
        setStepUpValue('')
        setFlash('设备凭据已创建，请输入新的两步验证码完成注册')
        return
      }
      await registerPasskeyFinish(next.sessionId, next.name, built.proof, next.credential)
      markRegistered()
    } catch (cause) {
      setError(
        cause instanceof DOMException
          ? 'Passkey 创建未完成'
          : errorText(cause, '添加 Passkey 失败'),
      )
    } finally {
      if (method === 'password') setStepUpValue('')
      setRegistering(false)
    }
  }

  const finishTwoFactorRegistration = async () => {
    if (!pending) return
    const built = buildPasskeyStepUp('two_factor_code', stepUpValue)
    if (!built.ok) {
      setError(built.error)
      return
    }
    setRegistering(true)
    setError(null)
    try {
      await registerPasskeyFinish(
        pending.sessionId,
        pending.name,
        built.proof,
        pending.credential,
      )
      markRegistered()
    } catch (cause) {
      setError(errorText(cause, '完成 Passkey 注册失败'))
    } finally {
      setStepUpValue('')
      setRegistering(false)
    }
  }

  const remove = async (passkey: PasskeyItem) => {
    const built = buildPasskeyStepUp(method, stepUpValue)
    if (!built.ok) {
      setError(`删除前${built.error}`)
      return
    }
    setBusyId(passkey.id)
    setError(null)
    setFlash(null)
    try {
      await deletePasskey(passkey.id, built.proof)
      setFlash('通行密钥已删除')
      setRefreshNonce((value) => value + 1)
    } catch (cause) {
      setError(errorText(cause, '删除失败'))
    } finally {
      setStepUpValue('')
      setBusyId(null)
    }
  }

  return (
    <section className="hk-card">
      <div className="hk-card__head"><h3>通行密钥(Passkey)</h3></div>
      <div className="hk-card__body" style={body}>
        <p style={hint}>通行密钥用设备生物识别或硬件密钥免密登录。敏感动作需要当前密码或两步验证。</p>
        {error && <Notice tone="danger">{error}</Notice>}
        {flash && <Notice tone="ok">{flash}</Notice>}

        <PasskeyRegistrationControls
          supported={supported}
          pending={pending !== null}
          busy={registering}
          name={name}
          method={method}
          stepUpValue={stepUpValue}
          onNameChange={setName}
          onMethodChange={(next) => { setMethod(next); setStepUpValue(''); setPending(null); setError(null) }}
          onStepUpChange={setStepUpValue}
          onBegin={() => void beginRegistration()}
          onFinish={() => void finishTwoFactorRegistration()}
          onCancelPending={() => { setPending(null); setStepUpValue(''); setFlash(null) }}
        />

        <div style={{ borderTop: '1px solid var(--hk-line-soft)', paddingTop: 'var(--hk-space-3)' }}>
          {loading && items.length === 0 ? (
            <div className="hk-empty">加载中…</div>
          ) : items.length === 0 ? (
            <div className="hk-empty">尚未添加任何通行密钥。</div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
              {items.map((passkey) => (
                <div key={passkey.id} style={listRow}>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                    <span style={{ fontWeight: 600 }}>{passkeyLabel(passkey)}</span>
                    <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
                      添加于 {formatTime(passkey.created_at)}
                      {passkey.last_used_at ? ` · 最近使用 ${formatTime(passkey.last_used_at)}` : ''}
                      {passkey.clone_warning ? ' · ⚠ 检测到克隆风险' : ''}
                    </span>
                  </div>
                  <button
                    type="button"
                    disabled={busyId === passkey.id || registering}
                    onClick={() => void remove(passkey)}
                    className="hk-btn hk-btn--sm"
                    style={{ color: 'var(--hk-danger)' }}
                  >
                    删除
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </section>
  )
}

interface RegistrationControlsProps {
  supported: boolean
  pending: boolean
  busy: boolean
  name: string
  method: PasskeyStepUpMethod
  stepUpValue: string
  onNameChange: (value: string) => void
  onMethodChange: (value: PasskeyStepUpMethod) => void
  onStepUpChange: (value: string) => void
  onBegin: () => void
  onFinish: () => void
  onCancelPending: () => void
}

/** 无状态控件便于覆盖支持、不支持与两步验证待完成分支。 */
export function PasskeyRegistrationControls(props: RegistrationControlsProps) {
  const inputType = props.method === 'password' ? 'password' : 'text'
  const proofPlaceholder =
    props.method === 'password'
      ? '当前密码'
      : props.pending
        ? '新的动态码 / 另一枚备用码'
        : '动态码 / 备用码'
  return (
    <div style={registrationBox}>
      {props.pending ? (
        <p style={{ ...hint, margin: 0 }}>
          第一次两步验证码已被安全消费。请使用下一时间步的新动态码或另一枚备用码完成注册。
        </p>
      ) : (
        <label style={field}>
          <span style={label}>Passkey 名称（可选）</span>
          <input
            value={props.name}
            onChange={(event) => props.onNameChange(event.target.value)}
            placeholder="例如：MacBook Touch ID"
            autoComplete="off"
            style={input}
          />
        </label>
      )}
      {!props.pending && (
        <label style={field}>
          <span style={label}>二次验证方式</span>
          <select
            value={props.method}
            onChange={(event) => props.onMethodChange(event.target.value as PasskeyStepUpMethod)}
            style={input}
          >
            <option value="password">当前密码</option>
            <option value="two_factor_code">两步验证码 / 备用码</option>
          </select>
        </label>
      )}
      <label style={field}>
        <span style={label}>{props.pending ? '完成验证' : '验证凭据'}</span>
        <input
          type={inputType}
          value={props.stepUpValue}
          onChange={(event) => props.onStepUpChange(event.target.value)}
          placeholder={proofPlaceholder}
          autoComplete={props.method === 'password' ? 'current-password' : 'one-time-code'}
          style={input}
        />
      </label>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
        {props.pending ? (
          <>
            <button type="button" disabled={props.busy} onClick={props.onFinish} className="hk-btn hk-btn--green">
              {props.busy ? '完成中…' : '完成 Passkey 注册'}
            </button>
            <button type="button" disabled={props.busy} onClick={props.onCancelPending} className="hk-btn">取消</button>
          </>
        ) : (
          <button
            type="button"
            disabled={props.busy || !props.supported}
            onClick={props.onBegin}
            title={props.supported ? '' : '当前浏览器不支持 WebAuthn Passkey 注册'}
            className="hk-btn hk-btn--green"
          >
            {props.busy ? '创建中…' : '添加 Passkey'}
          </button>
        )}
        {!props.supported && <span style={{ fontSize: 12, color: 'var(--hk-warn)' }}>当前浏览器不支持 WebAuthn 注册</span>}
      </div>
    </div>
  )
}

function Notice({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const color = tone === 'ok' ? 'var(--hk-success)' : 'var(--hk-danger)'
  const background = tone === 'ok' ? 'var(--hk-success-soft)' : 'var(--hk-danger-soft)'
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)', color, background, fontSize: 13 }}>{children}</div>
}

function errorText(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? `${cause.message}(${cause.code})` : fallback
}

function formatTime(value: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

const body: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
const hint: React.CSSProperties = { margin: 0, fontSize: 13, color: 'var(--hk-ink-500)', lineHeight: 1.6 }
const registrationBox: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 'var(--hk-space-3)', alignItems: 'end', padding: 'var(--hk-space-3)', background: 'var(--hk-surface-sunken)', border: '1px solid var(--hk-line-soft)', borderRadius: 'var(--hk-radius-md)' }
const field: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }
const label: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)' }
const input: React.CSSProperties = { width: '100%', minWidth: 0, padding: '7px 9px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontFamily: 'inherit' }
const listRow: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line-soft)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)' }
