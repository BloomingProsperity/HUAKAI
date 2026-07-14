import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  createChannel,
  createProvider,
  deleteChannel,
  deleteProvider,
  listChannels,
  listProviders,
  updateChannel,
  updateProvider,
} from './api'
import {
  UPSTREAM_PROTOCOLS,
  mapChannelCatalogRows,
  mapProviderCatalogRows,
  validateChannel,
  validateProviderCreate,
  validateProviderUpdate,
} from './catalogs'
import type { ChannelCatalogTableRow, ProviderCatalogTableRow } from './catalogs'
import type { ChannelCatalogItem, ProviderCatalogItem } from './types'

/*
 * 上游目录运营台。管线「模型与定价」分组下的目录写侧管理面:
 *   - provider 目录:上游账号所属的供应商条目(code / 展示名 / 上游协议 / 启用)
 *   - channel 目录:池组路由条目(pool_group_id / 名称 / 启用)
 * 后端 /admin/v1/{providers,channels}(admin token),见 cmd/gateway/routes.go:888-900。
 * 注意:platform_admin 角色下后端 tenant_id 必填,故本页先要租户 ID 再加载。
 * 删除是软删但属破坏性(provider 删除后该供应商不可再新建账号),带二次确认。
 * money 说明:两份目录均不含任何计费/倍率字段,本页不触碰计费面;
 * 也不触碰任何 pool/registry/gateway 等碰撞包模块(仅调 adminhttp 目录端点)。
 */

const PAGE_SIZE = 100

export function CatalogsPage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantId, setTenantId] = useState<number | null>(null)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>上游目录</h1>
          <p className="hk-sub">
            provider 目录(供应商条目)与 channel 目录(池组路由条目)的增删改。先指定租户 ID。
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
        <>
          <ProvidersCard tenantId={tenantId} />
          <ChannelsCard tenantId={tenantId} />
        </>
      )}
    </div>
  )
}

// ── provider 目录卡 ───────────────────────────────────────────────────────────

function ProvidersCard({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<ProviderCatalogItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // 编辑态:null=新建模式;非 null=正在编辑该 code(code 不可改,只能改其余字段)。
  const [editCode, setEditCode] = useState<string | null>(null)
  const [code, setCode] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [protocol, setProtocol] = useState<string>(UPSTREAM_PROTOCOLS[0])
  const [enabled, setEnabled] = useState(true)
  const [reason, setReason] = useState('')
  const tableRows = mapProviderCatalogRows(rows)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listProviders(tenantId, PAGE_SIZE, 0, signal)
        .then((resp) => setRows(resp.items ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载 provider 目录失败')
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

  const resetForm = () => {
    setEditCode(null)
    setCode('')
    setDisplayName('')
    setProtocol(UPSTREAM_PROTOCOLS[0])
    setEnabled(true)
    setReason('')
  }

  const startEdit = (row: ProviderCatalogItem) => {
    setEditCode(row.code)
    setCode(row.code)
    setDisplayName(row.display_name)
    setProtocol(row.upstream_protocol)
    setEnabled(row.enabled)
    setReason('')
    setNotice(null)
    setError(null)
  }

  const submit = () => {
    setError(null)
    setNotice(null)
    if (editCode == null) {
      const v = validateProviderCreate({ code, displayName, upstreamProtocol: protocol, enabled, reason })
      if (!v.ok) {
        setError(v.error)
        return
      }
      runOp(() => createProvider(tenantId, v.value), `已新建 provider「${v.value.code}」`)
    } else {
      const v = validateProviderUpdate({ displayName, upstreamProtocol: protocol, enabled, reason })
      if (!v.ok) {
        setError(v.error)
        return
      }
      runOp(() => updateProvider(tenantId, editCode, v.value), `已更新 provider「${editCode}」`)
    }
  }

  const runOp = (fn: () => Promise<unknown>, okMsg: string) => {
    setBusy(true)
    setError(null)
    setNotice(null)
    fn()
      .then(() => {
        setNotice(okMsg)
        resetForm()
        load()
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败'))
      .finally(() => setBusy(false))
  }

  const remove = (row: ProviderCatalogItem) => {
    // 破坏性:provider 软删后该供应商不可再新建账号(后端还有 active-account 守卫)。
    if (!window.confirm(`删除 provider「${row.code}」(${row.display_name})?\n该供应商将不可再用于新建账号。`)) {
      return
    }
    const r = window.prompt('可填删除原因(供审计):', '') ?? ''
    runOp(() => deleteProvider(tenantId, row.code, r), `已删除 provider「${row.code}」`)
  }

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>provider 目录</h3>
        <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {/* 新建 / 编辑表单 */}
      <div style={formWrap}>
        <div style={formRow}>
          <Field label="code(唯一标识)">
            <input
              value={code}
              onChange={(e) => setCode(e.target.value)}
              disabled={editCode != null}
              placeholder="如 anthropic"
              style={{ ...inp, width: 180, ...(editCode != null ? disabledInp : {}) }}
            />
          </Field>
          <Field label="展示名(display_name)">
            <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="如 Anthropic" style={{ ...inp, width: 200 }} />
          </Field>
          <Field label="上游协议(upstream_protocol)">
            <select value={protocol} onChange={(e) => setProtocol(e.target.value)} style={{ ...inp, width: 220 }}>
              {UPSTREAM_PROTOCOLS.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </Field>
          <label style={chk}>
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> 启用
          </label>
        </div>
        <div style={formRow}>
          <Field label="原因(reason,可选,写入审计)">
            <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="可选" style={{ ...inp, width: 260 }} />
          </Field>
          <button type="button" disabled={busy} onClick={submit} className="hk-btn hk-btn--green">
            {editCode == null ? '新建' : '保存修改'}
          </button>
          {editCode != null && (
            <button type="button" disabled={busy} onClick={resetForm} className="hk-btn">
              取消编辑
            </button>
          )}
        </div>
      </div>

      {/* 列表 */}
      {loading && rows.length === 0 ? (
        <EmptyState title="正在加载 provider 目录" hint="请稍候。" />
      ) : rows.length === 0 ? (
        <EmptyState title="暂无 provider 目录条目" hint="可使用上方表单新建供应商条目。" />
      ) : (
        <DataListTable
          label="provider 目录"
          rows={tableRows}
          rowKey={(row) => row.id}
          columns={providerColumns}
          actions={[
            { label: '编辑', disabled: busy, onClick: (row) => startEdit(row.provider) },
            { label: '删除', tone: 'danger', disabled: busy, onClick: (row) => remove(row.provider) },
          ]}
        />
      )}
    </section>
  )
}

// ── channel 目录卡 ────────────────────────────────────────────────────────────

export function ChannelsCard({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<ChannelCatalogItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // 编辑态:null=新建模式;非 null=正在编辑该 id。
  const [editId, setEditId] = useState<number | null>(null)
  const [name, setName] = useState('')
  const [poolGroupId, setPoolGroupId] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [reason, setReason] = useState('')
  const tableRows = mapChannelCatalogRows(rows)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listChannels(tenantId, PAGE_SIZE, 0, signal)
        .then((resp) => setRows(resp.items ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载 channel 目录失败')
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

  const resetForm = () => {
    setEditId(null)
    setName('')
    setPoolGroupId('')
    setEnabled(true)
    setReason('')
  }

  const startEdit = (row: ChannelCatalogItem) => {
    setEditId(row.id)
    setName(row.name)
    setPoolGroupId(String(row.pool_group_id))
    setEnabled(row.enabled)
    setReason('')
    setNotice(null)
    setError(null)
  }

  const runOp = (fn: () => Promise<unknown>, okMsg: string) => {
    setBusy(true)
    setError(null)
    setNotice(null)
    fn()
      .then(() => {
        setNotice(okMsg)
        resetForm()
        load()
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败'))
      .finally(() => setBusy(false))
  }

  const submit = () => {
    setError(null)
    setNotice(null)
    const pg = Number(poolGroupId.trim())
    const v = validateChannel({ name, poolGroupId: pg, enabled, reason })
    if (!v.ok) {
      setError(v.error)
      return
    }
    if (editId == null) {
      runOp(() => createChannel(tenantId, v.value), `已新建 channel「${v.value.name}」`)
    } else {
      runOp(() => updateChannel(tenantId, editId, v.value), `已更新 channel #${editId}`)
    }
  }

  const remove = (row: ChannelCatalogItem) => {
    if (!window.confirm(`删除 channel「${row.name}」(#${row.id})?`)) return
    const r = window.prompt('可填删除原因(供审计):', '') ?? ''
    runOp(() => deleteChannel(tenantId, row.id, r), `已删除 channel #${row.id}`)
  }

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>channel 目录</h3>
        <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {/* 新建 / 编辑表单 */}
      <div style={formWrap}>
        <div style={formRow}>
          <Field label="名称(name)">
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="如 主通道" style={{ ...inp, width: 200 }} />
          </Field>
          <Field label="pool_group_id(正整数)">
            <input value={poolGroupId} onChange={(e) => setPoolGroupId(e.target.value)} inputMode="numeric" placeholder="如 1" style={{ ...inp, width: 160 }} />
          </Field>
          <label style={chk}>
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> 启用
          </label>
        </div>
        <div style={formRow}>
          <Field label="原因(reason,可选,写入审计)">
            <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="可选" style={{ ...inp, width: 220 }} />
          </Field>
        </div>
        <div style={formRow}>
          <button type="button" disabled={busy} onClick={submit} className="hk-btn hk-btn--green">
            {editId == null ? '新建' : '保存修改'}
          </button>
          {editId != null && (
            <button type="button" disabled={busy} onClick={resetForm} className="hk-btn">
              取消编辑
            </button>
          )}
        </div>
      </div>

      {/* 列表 */}
      {loading && rows.length === 0 ? (
        <EmptyState title="正在加载 channel 目录" hint="请稍候。" />
      ) : rows.length === 0 ? (
        <EmptyState title="暂无 channel 目录条目" hint="可使用上方表单新建池组路由条目。" />
      ) : (
        <DataListTable
          label="channel 目录"
          rows={tableRows}
          rowKey={(row) => row.id}
          columns={channelColumns}
          actions={[
            { label: '编辑', disabled: busy, onClick: (row) => startEdit(row.channel) },
            { label: '删除', tone: 'danger', disabled: busy, onClick: (row) => remove(row.channel) },
          ]}
        />
      )}
    </section>
  )
}

/* ——— 本文件私有小组件 / 样式 ——— */
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
  return <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}

const providerColumns: DataListColumn<ProviderCatalogTableRow>[] = [
  { key: 'code', label: 'code', render: (row) => <span className="hk-mono">{row.code}</span> },
  { key: 'name', label: '展示名', render: (row) => row.displayName },
  { key: 'protocol', label: '上游协议', render: (row) => <span className="hk-mono">{row.upstreamProtocol}</span> },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
  { key: 'created-at', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
]

export const channelColumns: DataListColumn<ChannelCatalogTableRow>[] = [
  { key: 'id', label: '#', render: (row) => <span className="hk-mono">{row.displayId}</span> },
  { key: 'name', label: '名称', render: (row) => row.name },
  { key: 'pool-group', label: 'pool_group_id', render: (row) => <span className="hk-mono">{row.poolGroupId}</span> },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
  { key: 'created-at', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
]

const formWrap: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)' }
const formRow: React.CSSProperties = { display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap' }
const chk: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 4, fontSize: 12, color: 'var(--hk-ink-700)', height: 32 }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const disabledInp: React.CSSProperties = { background: 'var(--hk-surface-sunken)', color: 'var(--hk-ink-500)' }
