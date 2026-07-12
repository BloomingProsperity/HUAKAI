import { useCallback, useEffect, useState } from 'react'
import type { CSSProperties } from 'react'
import { ApiError } from '../../lib/api'
import { getEmailSettings, previewEmailTemplate, updateEmailSettings } from './emailApi'
import {
  TEMPLATE_KINDS,
  credentialPlaceholder,
  overrideFromRows,
  validateTemplateDraft,
} from './emailTemplates'
import type { TemplateKind, TemplateOverride } from './emailTemplates'

/*
 * 鉴权邮件模板编辑分区。挂在设置中心「邮件」tab 下,与 SMTP 分区同页聚合。
 * 覆盖存租户邮件设置键(email_template.<kind>.subject/.body);清空保存=恢复内置默认。
 * 预览走服务端渲染(样例值,不发信),iframe srcDoc 沙箱展示,不在管理页直挂 HTML。
 */

const TENANT_ID = 1

export function EmailTemplatesSection() {
  const [kind, setKind] = useState<TemplateKind>('verification')
  const [drafts, setDrafts] = useState<Record<string, TemplateOverride>>({})
  const [loading, setLoading] = useState(true)
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [msg, setMsg] = useState<{ tone: 'ok' | 'err'; text: string } | null>(null)
  const [preview, setPreview] = useState<{ subject: string; html: string } | null>(null)
  const [previewing, setPreviewing] = useState(false)

  const load = useCallback((signal?: AbortSignal) => {
    setLoading(true)
    setLoadError(null)
    getEmailSettings(TENANT_ID, signal)
      .then((resp) => {
        const next: Record<string, TemplateOverride> = {}
        for (const spec of TEMPLATE_KINDS) {
          next[spec.kind] = overrideFromRows(resp.settings, spec.kind)
        }
        setDrafts(next)
        setLoaded(true)
      })
      .catch((e: unknown) => {
        if (signal?.aborted) return
        setLoadError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载模板失败')
      })
      .finally(() => {
        if (!signal?.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const spec = TEMPLATE_KINDS.find((k) => k.kind === kind) ?? TEMPLATE_KINDS[0]
  const draft = drafts[kind] ?? { subject: '', body: '' }
  const setDraft = (patch: Partial<TemplateOverride>) => {
    setDrafts((prev) => ({ ...prev, [kind]: { ...(prev[kind] ?? { subject: '', body: '' }), ...patch } }))
    setMsg(null)
  }

  const save = async () => {
    const invalid = validateTemplateDraft(kind, draft.subject, draft.body)
    if (invalid) {
      setMsg({ tone: 'err', text: invalid })
      return
    }
    setSaving(true)
    setMsg(null)
    try {
      await updateEmailSettings({
        tenant_id: TENANT_ID,
        templates: { [kind]: { subject: draft.subject, body: draft.body } },
      })
      setMsg({ tone: 'ok', text: draft.subject.trim() === '' && draft.body.trim() === '' ? '已恢复内置默认模板' : '模板已保存' })
    } catch (e) {
      setMsg({ tone: 'err', text: e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败' })
    } finally {
      setSaving(false)
    }
  }

  const restoreDefault = async () => {
    setDraft({ subject: '', body: '' })
    setSaving(true)
    setMsg(null)
    try {
      await updateEmailSettings({ tenant_id: TENANT_ID, templates: { [kind]: { subject: '', body: '' } } })
      setMsg({ tone: 'ok', text: '已恢复内置默认模板' })
    } catch (e) {
      setMsg({ tone: 'err', text: e instanceof ApiError ? `${e.message}(${e.code})` : '恢复失败' })
    } finally {
      setSaving(false)
    }
  }

  const doPreview = async () => {
    const invalid = validateTemplateDraft(kind, draft.subject, draft.body)
    if (invalid) {
      setMsg({ tone: 'err', text: invalid })
      return
    }
    setPreviewing(true)
    setMsg(null)
    try {
      const res = await previewEmailTemplate(TENANT_ID, kind, draft.subject, draft.body)
      setPreview({ subject: res.subject, html: res.html })
    } catch (e) {
      setMsg({ tone: 'err', text: e instanceof ApiError ? `${e.message}(${e.code})` : '预览失败' })
    } finally {
      setPreviewing(false)
    }
  }

  return (
    <section className="hk-card" style={{ marginTop: 'var(--hk-space-4)' }}>
      <h2 style={{ fontSize: 15, margin: 0 }}>鉴权邮件模板</h2>
      <p className="hk-sub" style={{ marginTop: 4 }}>
        自定义四类鉴权邮件的主题与 HTML 正文;留空保存 = 恢复内置默认。占位符按类型注入真实值;渲染异常时自动回退内置模板,不影响邮件送达。
      </p>

      {loadError && <div style={errBox}>{loadError} <button type="button" style={linkBtn} onClick={() => load()}>重试</button></div>}

      <div role="tablist" aria-label="模板类型" style={{ display: 'flex', gap: 'var(--hk-space-1)', flexWrap: 'wrap', marginTop: 'var(--hk-space-3)' }}>
        {TEMPLATE_KINDS.map((k) => {
          const active = k.kind === kind
          const overridden = (drafts[k.kind]?.subject?.trim() ?? '') !== '' || (drafts[k.kind]?.body?.trim() ?? '') !== ''
          return (
            <button
              key={k.kind}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => { setKind(k.kind); setPreview(null); setMsg(null) }}
              style={{
                ...kindBtn,
                background: active ? 'var(--hk-primary-50)' : 'transparent',
                borderColor: active ? 'var(--hk-primary-500)' : 'var(--hk-line)',
                color: active ? 'var(--hk-primary-700)' : 'var(--hk-ink-500)',
              }}
            >
              {k.label}
              {overridden && <span title="已自定义" style={{ marginLeft: 4, color: 'var(--hk-primary-500)' }}>●</span>}
            </button>
          )
        })}
      </div>

      <div style={{ fontSize: 12, color: 'var(--hk-ink-500)', marginTop: 'var(--hk-space-2)' }}>
        可用占位符:{spec.placeholders.map((p) => (
          <code key={p} style={phChip}>{`{{${p}}}`}</code>
        ))}
        <span style={{ marginLeft: 6 }}>正文必须包含 <code style={phChip}>{`{{${credentialPlaceholder(kind)}}}`}</code></span>
      </div>

      <label style={fieldLabel}>
        主题(留空 = 用内置默认)
        <input
          value={draft.subject}
          onChange={(e) => setDraft({ subject: e.target.value })}
          disabled={loading}
          placeholder="例:【HUAKAI】请验证你的邮箱"
          style={inp}
        />
      </label>
      <label style={fieldLabel}>
        HTML 正文(留空 = 用内置默认)
        <textarea
          value={draft.body}
          onChange={(e) => setDraft({ body: e.target.value })}
          disabled={loading}
          rows={8}
          placeholder={`例:<p>点击 <a href="{{link}}">这里</a> 完成验证,或使用一次性令牌 {{token}}</p>`}
          style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', fontFamily: 'var(--hk-font-mono)', resize: 'vertical' }}
        />
      </label>

      {msg && <div style={msg.tone === 'ok' ? okBox : errBox}>{msg.text}</div>}

      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <button type="button" className="hk-btn hk-btn--green" disabled={saving || loading || !loaded} onClick={save}>
          {saving ? '保存中…' : '保存模板'}
        </button>
        <button type="button" className="hk-btn" disabled={previewing || loading || !loaded} onClick={doPreview}>
          {previewing ? '渲染中…' : '预览(样例值)'}
        </button>
        <button type="button" className="hk-btn" disabled={saving || loading || !loaded} onClick={restoreDefault}>
          恢复内置默认
        </button>
      </div>

      {preview && (
        <div style={{ marginTop: 'var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', overflow: 'hidden' }}>
          <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderBottom: '1px solid var(--hk-line)', fontSize: 13, background: 'var(--hk-surface-sunken)' }}>
            主题预览:<strong>{preview.subject || '(用内置默认主题)'}</strong>
          </div>
          {preview.html ? (
            <iframe
              title="邮件正文预览"
              sandbox=""
              srcDoc={preview.html}
              style={{ width: '100%', height: 260, border: 'none', background: '#fff' }}
            />
          ) : (
            <div className="hk-empty">正文留空,发送时将使用内置默认正文。</div>
          )}
        </div>
      )}
    </section>
  )
}

const inp: CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const fieldLabel: CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)', marginTop: 'var(--hk-space-3)' }
const kindBtn: CSSProperties = { padding: 'var(--hk-space-1) var(--hk-space-3)', border: '1px solid', borderRadius: 'var(--hk-radius-pill)', fontSize: 13, cursor: 'pointer', background: 'transparent' }
const phChip: CSSProperties = { fontFamily: 'var(--hk-font-mono)', fontSize: 11, background: 'var(--hk-surface-sunken)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', padding: '1px 5px', marginRight: 4 }
const errBox: CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', marginTop: 'var(--hk-space-2)' }
const okBox: CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-primary-700)', background: 'var(--hk-primary-50)', marginTop: 'var(--hk-space-2)' }
const linkBtn: CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer' }
