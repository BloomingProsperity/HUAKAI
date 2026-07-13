import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { listFingerprintProfileOptions, setAccountFingerprintProfile } from './api'
import {
  UNBIND_VALUE,
  bindResultMessage,
  currentSelectionValue,
  optionText,
  selectionToProfileId,
} from './fingerprint'
import type { FingerprintProfileOption } from './types'

/*
 * 账号「指纹 Profile 绑定」区(详情页)。出口拟真,非 money。
 *
 * - 列租户可绑定的 TLS 指纹 profile:GET /v1/admin/tls-fingerprint-profiles?tenant_id=N
 *   (tlsfphttp/handler.go:96)。
 * - 选「解绑(回内置默认)」或某个 profile → 保存:
 *   PATCH /admin/v1/provider-accounts/{id}/fingerprint-profile,body {profile_id|null}
 *   (accountfphttp/fingerprint_handler.go:48)。
 *
 * 重要事实(已核后端源码):账号详情/列表 DTO **不**暴露当前绑定的 tls_fingerprint_profile_id,
 * 故进入时"当前绑定"未知,下拉默认停在「解绑」项并提示;仅本次保存成功后回显新值。
 * 与 AccountDiagnosticsCard 一样独立成卡,不改动诊断卡。
 */
export function AccountFingerprintBind({ accountId, tenantId }: { accountId: number; tenantId: number }) {
  const [options, setOptions] = useState<FingerprintProfileOption[]>([])
  const [loadingOptions, setLoadingOptions] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  // 下拉当前值。初次进入"当前绑定未知"→ 默认停在解绑项(currentSelectionValue(undefined))。
  const [selection, setSelection] = useState<string>(currentSelectionValue(undefined))
  // 保存成功后回显的已知绑定 id(null=已解绑);用于把下拉同步到真实状态。
  const [knownBound, setKnownBound] = useState<number | null | undefined>(undefined)
  const [busy, setBusy] = useState(false)
  const [flash, setFlash] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  const loadOptions = useCallback(
    (signal?: AbortSignal) => {
      setLoadingOptions(true)
      setLoadError(null)
      listFingerprintProfileOptions(tenantId, signal)
        .then((items) => setOptions(items))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setLoadError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载指纹 profile 列表失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoadingOptions(false)
        })
    },
    [tenantId],
  )

  useEffect(() => {
    if (!Number.isInteger(tenantId) || tenantId <= 0) {
      setLoadError('租户 ID 非法,无法列出指纹 profile')
      setLoadingOptions(false)
      return
    }
    const ctrl = new AbortController()
    loadOptions(ctrl.signal)
    return () => ctrl.abort()
  }, [tenantId, loadOptions])

  async function onSave() {
    let profileId: number | null
    try {
      profileId = selectionToProfileId(selection)
    } catch {
      setError('请选择一个有效的指纹 profile 或选择解绑')
      return
    }
    // 解绑是改动型动作(会从拟真 profile 回落内置默认),二次确认。
    if (profileId === null) {
      if (!window.confirm('确认解绑该账号的指纹 profile?账号出口将回落到内置默认拟真姿态。')) {
        return
      }
    }
    setBusy(true)
    setFlash(null)
    setError(null)
    try {
      const res = await setAccountFingerprintProfile(tenantId, accountId, profileId)
      setKnownBound(res.tls_fingerprint_profile_id)
      setSelection(currentSelectionValue(res.tls_fingerprint_profile_id))
      setFlash(bindResultMessage(res))
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存指纹 profile 绑定失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section style={card}>
      <h2 style={{ fontSize: 14, color: 'var(--hk-ink-500)' }}>指纹 Profile 绑定</h2>
      <p style={hint}>
        为该账号绑定一个 TLS 指纹 profile(出口拟真姿态);选「解绑」回落到内置默认。
        {knownBound === undefined && '当前绑定未知(账号详情未暴露该字段),保存后才会生效并回显。'}
      </p>

      {flash && <Banner tone="ok">{flash}</Banner>}
      {error && <Banner tone="danger">{error}</Banner>}
      {loadError && <Banner tone="danger">{loadError}</Banner>}

      <div style={row}>
        <select
          value={selection}
          disabled={busy || loadingOptions}
          onChange={(e) => setSelection(e.target.value)}
          style={selectStyle}
        >
          <option value={UNBIND_VALUE}>解绑(回内置默认)</option>
          {options.map((o) => (
            <option key={o.id} value={String(o.id)}>
              {optionText(o)}
            </option>
          ))}
        </select>
        <button type="button" disabled={busy || loadingOptions} onClick={onSave} style={primaryBtn}>
          {busy ? '保存中…' : '保存绑定'}
        </button>
        {loadingOptions && <span style={hint}>加载 profile 列表中…</span>}
        {!loadingOptions && !loadError && options.length === 0 && (
          <span style={hint}>该租户暂无可用指纹 profile(可在「TLS 指纹」页新建)。只能选解绑。</span>
        )}
      </div>
    </section>
  )
}

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
const row: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }
const hint: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-300)', margin: 0 }
const selectStyle: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  minWidth: 240,
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

function Banner({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const ok = tone === 'ok'
  return (
    <div
      style={{
        padding: 'var(--hk-space-3) var(--hk-space-4)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: ok ? 'var(--hk-primary-600)' : 'var(--hk-danger)',
        background: ok ? 'var(--hk-primary-50)' : 'var(--hk-danger-soft)',
        border: `1px solid ${ok ? 'var(--hk-primary-100)' : 'var(--hk-danger-soft)'}`,
      }}
    >
      {children}
    </div>
  )
}
