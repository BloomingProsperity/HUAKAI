import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import {
  createChannelTestTemplate,
  deleteChannelTestTemplate,
  listChannelTestTemplates,
  updateChannelTestTemplate,
} from './api'
import {
  emptyForm,
  mapChannelTemplateRows,
  templateToForm,
  validateForm,
  type ChannelTemplateTableRow,
} from './channeltesttemplates'
import { ALLOWED_METHODS } from './types'
import type { ChannelTestTemplate, TemplateForm } from './types'

/*
 * 渠道测试模板运营台。管线第 4 站(模型与定价)下:运营者为渠道(上游账号)
 * 连通性测试预存的 HTTP 请求模板(方法 / 路径 / 请求体模板 / 自定义请求头),按租户隔离。
 * 后端 /admin/v1/channel-test-templates(admin token):列表 + 增删改。
 * 注意:platform_admin 角色下后端要求 tenant_id 必填,故本页先要租户 ID 再加载。
 * 安全:请求头中禁止凭证类 header(authorization 等),前端先拦,后端也会拒。
 * 删除为破坏性动作,UI 二次确认。本页不碰任何 pool/registry/gateway 等碰撞包模块。
 */

const PAGE_SIZE = 50

export function ChannelTestTemplatesPage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantId, setTenantId] = useState<number | null>(null)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>渠道测试模板</h1>
          <p className="hk-sub">
            为渠道连通性测试预存的 HTTP 请求模板(方法 / 路径 / 请求体 / 自定义请求头),按租户隔离。
            请求头禁止填凭证类(authorization 等)。先指定租户 ID。
          </p>
        </div>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          const v = Number(tenantInput.trim())
          setTenantId(Number.isInteger(v) && v > 0 ? v : null)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="租户 ID(tenant_id)">
          <input
            value={tenantInput}
            onChange={(e) => setTenantInput(e.target.value)}
            inputMode="numeric"
            placeholder="如 1"
            style={{ ...inp, width: 160 }}
          />
        </Field>
        <button type="submit" className="hk-btn hk-btn--green">
          加载
        </button>
      </form>

      {tenantId == null ? (
        <EmptyState title="尚未选择租户" hint="请输入正整数租户 ID 后点击「加载」。" />
      ) : (
        <TemplatesCard key={tenantId} tenantId={tenantId} />
      )}
    </div>
  )
}

function TemplatesCard({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<ChannelTestTemplate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  // editing: null=未打开;{id:null}=新建;{id:number}=编辑已有。
  const [editing, setEditing] = useState<{ id: number | null; form: TemplateForm } | null>(null)
  const [saving, setSaving] = useState(false)
  const [deletingId, setDeletingId] = useState<number | null>(null)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listChannelTestTemplates(tenantId, PAGE_SIZE, 0, signal)
        .then((resp) => setRows(resp.items ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载测试模板失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [tenantId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const startCreate = () => {
    setNotice(null)
    setError(null)
    setEditing({ id: null, form: emptyForm() })
  }
  const startEdit = (t: ChannelTestTemplate) => {
    setNotice(null)
    setError(null)
    setEditing({ id: t.id, form: templateToForm(t) })
  }

  const save = async () => {
    if (!editing) return
    const v = validateForm(editing.form)
    if (!v.ok) {
      setError(v.error)
      return
    }
    setSaving(true)
    setError(null)
    setNotice(null)
    try {
      if (editing.id == null) {
        await createChannelTestTemplate(tenantId, v.value)
        setNotice(`已新建模板「${v.value.name}」。`)
      } else {
        await updateChannelTestTemplate(editing.id, tenantId, v.value)
        setNotice(`已更新模板「${v.value.name}」。`)
      }
      setEditing(null)
      load()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const remove = async (t: ChannelTestTemplate) => {
    // 破坏性动作:二次确认。
    if (!window.confirm(`确认删除测试模板「${t.name}」(#${t.id})?此操作不可撤销。`)) {
      return
    }
    setDeletingId(t.id)
    setError(null)
    setNotice(null)
    try {
      await deleteChannelTestTemplate(t.id, tenantId)
      setNotice(`已删除模板「${t.name}」。`)
      if (editing?.id === t.id) setEditing(null)
      load()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '删除失败')
    } finally {
      setDeletingId(null)
    }
  }
  const tableRows = mapChannelTemplateRows(rows)

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>测试模板列表</h3>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
          <button type="button" onClick={startCreate} className="hk-btn hk-btn--green hk-btn--sm">
            新建模板
          </button>
          <button type="button" disabled={loading} onClick={() => load()} className="hk-btn hk-btn--sm">
            刷新
          </button>
        </div>
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {editing && (
        <TemplateEditor
          form={editing.form}
          isNew={editing.id == null}
          saving={saving}
          onChange={(form) => setEditing((e) => (e ? { ...e, form } : e))}
          onSave={save}
          onCancel={() => setEditing(null)}
        />
      )}

      {loading && rows.length === 0 ? (
        <EmptyState title="正在加载测试模板" hint="请稍候。" />
      ) : rows.length === 0 ? (
        <EmptyState title="暂无测试模板" hint="点击「新建模板」开始配置。" />
      ) : (
        <DataListTable
          label="渠道测试模板"
          rows={tableRows}
          rowKey={(row) => row.id}
          columns={templateColumns}
          actions={[
            { label: '编辑', onClick: (row) => startEdit(row.template) },
            { label: (row) => deletingId === row.id ? '删除中…' : '删除', tone: 'danger', disabled: (row) => deletingId === row.id, onClick: (row) => void remove(row.template) },
          ]}
        />
      )}
    </section>
  )
}

const templateColumns: DataListColumn<ChannelTemplateTableRow>[] = [
  { key: 'name', label: '名称', render: (row) => <span style={{ fontWeight: 600 }}>{row.name}</span> },
  { key: 'method', label: '方法', render: (row) => <span className="hk-mono">{row.method}</span> },
  { key: 'path', label: '路径', render: (row) => <span className="hk-mono" style={{ display: 'block', maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis' }}>{row.path}</span> },
  { key: 'header-count', label: '请求头数', render: (row) => <span className="hk-mono">{row.headerCount}</span> },
  { key: 'body', label: '请求体', render: (row) => <span style={{ color: 'var(--hk-ink-300)' }}>{row.body}</span> },
  { key: 'created-at', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
]

function TemplateEditor({
  form,
  isNew,
  saving,
  onChange,
  onSave,
  onCancel,
}: {
  form: TemplateForm
  isNew: boolean
  saving: boolean
  onChange: (form: TemplateForm) => void
  onSave: () => void
  onCancel: () => void
}) {
  const setF = <K extends keyof TemplateForm>(k: K, v: TemplateForm[K]) =>
    onChange({ ...form, [k]: v })

  return (
    <div style={{ padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <h3 style={{ fontSize: 14, margin: 0 }}>{isNew ? '新建测试模板' : '编辑测试模板'}</h3>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 'var(--hk-space-3)' }}>
        <Field label="名称(≤128 字符)">
          <input value={form.name} onChange={(e) => setF('name', e.target.value)} placeholder="如 Claude 连通性探测" style={inp} />
        </Field>
        <Field label="方法">
          <select value={form.method} onChange={(e) => setF('method', e.target.value)} style={inp}>
            {ALLOWED_METHODS.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </Field>
        <Field label="路径(须以 / 开头)">
          <input value={form.path} onChange={(e) => setF('path', e.target.value)} placeholder="如 /v1/messages" style={inp} />
        </Field>
      </div>
      <Field label="请求体模板(可选,任意文本)">
        <textarea
          value={form.bodyTemplate}
          onChange={(e) => setF('bodyTemplate', e.target.value)}
          rows={4}
          placeholder='如 {"model":"claude-3-5-sonnet","max_tokens":1}'
          style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', fontFamily: 'var(--hk-font-mono)', resize: 'vertical' }}
        />
      </Field>
      <Field label="自定义请求头(可选,JSON 对象;禁止 authorization 等凭证 header)">
        <textarea
          value={form.headersText}
          onChange={(e) => setF('headersText', e.target.value)}
          rows={3}
          placeholder='如 {"X-Trace":"diag","Accept":"application/json"}'
          style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', fontFamily: 'var(--hk-font-mono)', resize: 'vertical' }}
        />
      </Field>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
        <button type="button" disabled={saving} onClick={onSave} className="hk-btn hk-btn--green">
          {saving ? '保存中…' : isNew ? '创建' : '保存'}
        </button>
        <button type="button" disabled={saving} onClick={onCancel} className="hk-btn">
          取消
        </button>
      </div>
    </div>
  )
}

/* ——— 小工具组件 / 样式(本页私有) ——— */

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

function Banner({ kind, children }: { kind: 'error' | 'ok'; children: React.ReactNode }) {
  const palette =
    kind === 'error'
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return (
    <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>
      {children}
    </div>
  )
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
