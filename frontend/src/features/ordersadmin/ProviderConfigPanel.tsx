import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getProviderConfig, putProviderConfig, type PaymentProviderKind } from './api'
import { buildProviderConfig, PROVIDER_KINDS, providerKindLabel, type ProviderConfigForm } from './ordersadmin'
import { errBox, Field, fmt, ghostBtn, inp, panel, primaryBtn, Row } from './ui'
import type { ProviderConfigView } from './types'

/*
 * 支付商配置子区(运营台 · admin)。对每个支付商(manual / taobao)做:
 *   - GET 回填(provider_kind / enabled / checkout_url / source / 最近写入者+时间);
 *   - PUT 保存(只写 enabled + checkout_url)。
 *
 * 【secret 姿态(§4)】后端该端点视图与 PUT body 均不含任何 secret/HMAC 密钥/网关 key:
 *   HMAC 验签密钥由进程 env(ProviderRegistryConfig.HMACSecrets)装载,刻意不经此配置面。
 *   故本面板既不回显 secret,也无 secret 输入框/写入,无需 secret-mask。
 * 端点真码:admin_panel.go:134(GET)/157(PUT);providerKindFromPath 只放行 manual/taobao。
 */
export function ProviderConfigPanel() {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>
        配置各支付商的启用状态与下单跳转链接。验签密钥由部署 env 管理,不在此读写(此处不涉及任何 secret)。
      </p>
      {PROVIDER_KINDS.map((p) => (
        <ProviderConfigCard key={p.value} provider={p.value} />
      ))}
    </div>
  )
}

function ProviderConfigCard({ provider }: { provider: PaymentProviderKind }) {
  const [view, setView] = useState<ProviderConfigView | null>(null)
  const [form, setForm] = useState<ProviderConfigForm>({ enabled: false, checkoutUrl: '' })
  const [loading, setLoading] = useState(true)
  const [loadErr, setLoadErr] = useState<string | null>(null)
  const [saveErr, setSaveErr] = useState<string | null>(null)
  const [ok, setOk] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [nonce, setNonce] = useState(0)

  // applyView:把后端视图回填进编辑草稿。checkout_url 不是 secret,可如实回填。
  const applyView = useCallback((v: ProviderConfigView) => {
    setView(v)
    setForm({ enabled: v.enabled, checkoutUrl: v.checkout_url ?? '' })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setLoadErr(null)
    setOk(null)
    getProviderConfig(provider, ctrl.signal)
      .then((resp) => applyView(resp.provider))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setLoadErr(e instanceof ApiError ? `${e.message}(${e.code})` : '加载支付商配置失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [provider, applyView, nonce])

  const save = () => {
    setSaveErr(null)
    setOk(null)
    const built = buildProviderConfig(form)
    if ('error' in built) {
      setSaveErr(built.error)
      return
    }
    setBusy(true)
    putProviderConfig(provider, built.enabled, built.checkoutUrl)
      .then((resp) => {
        applyView(resp.provider)
        setOk('已保存')
      })
      .catch((e: unknown) => {
        setSaveErr(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
      })
      .finally(() => setBusy(false))
  }

  return (
    <div style={{ ...panel, padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ fontSize: 16, margin: 0 }}>{providerKindLabel(provider)}</h3>
        {view && (
          <StatusBadge tone={view.enabled ? 'ok' : 'muted'}>{view.enabled ? '已启用' : '已停用'}</StatusBadge>
        )}
      </header>

      {loading ? (
        <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>加载中…</div>
      ) : loadErr ? (
        <>
          <div style={errBox}>{loadErr}</div>
          <button type="button" onClick={() => setNonce((n) => n + 1)} style={ghostBtn}>
            重试加载
          </button>
        </>
      ) : (
        <>
          {view && (
            <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
              <Row label="渠道标识">{view.provider_kind}</Row>
              <Row label="配置来源">{view.source || '—'}</Row>
              {view.updated_by ? <Row label="最近写入者">{view.updated_by}</Row> : null}
              {view.updated_at ? <Row label="最近写入时间">{fmt(view.updated_at)}</Row> : null}
            </section>
          )}

          <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-700)' }}>
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
            />
            启用该支付商(用户充值页是否可选此渠道)
          </label>

          <Field label="下单跳转链接 checkout_url(可空)">
            <input
              value={form.checkoutUrl}
              onChange={(e) => setForm((f) => ({ ...f, checkoutUrl: e.target.value }))}
              placeholder="如 https://item.taobao.com/...(留空=不设跳转链接)"
              style={inp}
            />
          </Field>

          {saveErr && <div style={errBox}>{saveErr}</div>}
          {ok && (
            <div style={{ fontSize: 13, color: 'var(--hk-primary-700)' }}>{ok}</div>
          )}

          <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
            <button type="button" disabled={busy} onClick={save} style={primaryBtn}>
              {busy ? '保存中…' : '保存配置'}
            </button>
            {view && (
              <button
                type="button"
                disabled={busy}
                onClick={() => applyView(view)}
                style={ghostBtn}
                title="把表单还原为最近一次后端返回的值"
              >
                还原
              </button>
            )}
          </div>
        </>
      )}
    </div>
  )
}
