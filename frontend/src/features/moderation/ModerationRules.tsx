import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  bulkCreateHashes,
  bulkCreateKeywords,
  createHash,
  createKeyword,
  deleteHash,
  deleteKeyword,
  listBannedAPIKeys,
  listHashes,
  listKeywords,
  unbanAPIKey,
} from './api'
import {
  normalizeHash,
  parseBulkLines,
  shortHash,
  validateBulkCount,
  validateHash,
  validateKeyword,
} from './moderation'
import type { BannedAPIKey, BulkCreateResult, HashRule, KeywordRule } from './types'

/*
 * 内容审核 —— 关键词/哈希黑名单管理 + 被封 Key 解封(Wave B 接线)。
 * 后端 moderationhttp/mount.go:39-51,均挂 /admin/v1/moderation(admin token)。
 * 列表/新增/批量导入(≤1000)/删除;哈希须 64 位小写 hex(前端 validateHash 镜像后端)。
 * 解封是恢复服务的破坏性动作,带二次确认。所有读写都需 tenant_id(platform_admin 必填)。
 */

const PAGE_SIZE = 100

// ── 关键词卡 / 哈希卡:同一套 UI,差异(字段名/校验/归一/接口)由 props 注入 ──────────
interface RuleManagerProps<T> {
  title: string
  valueLabel: string
  placeholder: string
  list: (tenantId: number, limit: number, offset: number, signal?: AbortSignal) => Promise<{ items: T[] }>
  create: (tenantId: number, value: string, reasonCode: string, enabled: boolean) => Promise<unknown>
  bulk: (tenantId: number, values: string[], reasonCode: string, enabled: boolean) => Promise<BulkCreateResult>
  remove: (id: number, tenantId: number) => Promise<void>
  rowId: (row: T) => number
  rowValue: (row: T) => string
  rowEnabled: (row: T) => boolean
  /** 单条/批量值的前端校验(返回错误文案或 null)。 */
  validate: (value: string) => string | null
  /** 列表展示时的值格式化(如哈希缩写)。 */
  display?: (value: string) => string
}

function RuleManager<T>({
  tenantId,
  title,
  valueLabel,
  placeholder,
  list,
  create,
  bulk,
  remove,
  rowId,
  rowValue,
  rowEnabled,
  validate,
  display,
}: RuleManagerProps<T> & { tenantId: number }) {
  const [rows, setRows] = useState<T[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [value, setValue] = useState('')
  const [reasonCode, setReasonCode] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [bulkText, setBulkText] = useState('')

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      list(tenantId, PAGE_SIZE, 0, signal)
        .then((resp) => setRows(resp.items ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : `加载${title}失败`)
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [tenantId, list, title],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const run = async (fn: () => Promise<unknown>, okMsg: string) => {
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      await fn()
      setNotice(okMsg)
      load()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const addSingle = () => {
    const err = validate(value)
    if (err) {
      setError(err)
      return
    }
    void run(() => create(tenantId, value, reasonCode.trim(), enabled), '已新增').then(() => {
      setValue('')
    })
  }

  const addBulk = () => {
    const lines = parseBulkLines(bulkText)
    const countErr = validateBulkCount(lines.length)
    if (!countErr.ok) {
      setError(countErr.error)
      return
    }
    // 逐行先做格式校验,任何一行非法即拦(给出行号),避免整批被后端逐条记错。
    for (let i = 0; i < lines.length; i++) {
      const err = validate(lines[i])
      if (err) {
        setError(`第 ${i + 1} 行:${err}`)
        return
      }
    }
    void run(
      () =>
        bulk(tenantId, lines, reasonCode.trim(), enabled).then((res: BulkCreateResult) => {
          setNotice(`导入完成:接受 ${res.accepted}、跳过重复 ${res.skipped_duplicate}、错误 ${res.errors.length}`)
          return res
        }),
      '导入完成',
    ).then(() => setBulkText(''))
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>{title}</h2>
        <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {/* 新增 + 批量导入 */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)' }}>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
          <Field label={valueLabel}>
            <input value={value} onChange={(e) => setValue(e.target.value)} placeholder={placeholder} style={{ ...inp, width: 280 }} />
          </Field>
          <Field label="原因码(reason_code,可选)">
            <input value={reasonCode} onChange={(e) => setReasonCode(e.target.value)} placeholder="如 policy" style={{ ...inp, width: 160 }} />
          </Field>
          <label style={{ display: 'flex', alignItems: 'center', gap: 4, fontSize: 12, color: 'var(--hk-ink-700)', height: 32 }}>
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> 启用
          </label>
          <button type="button" disabled={busy} onClick={addSingle} style={primaryBtn}>
            新增
          </button>
        </div>
        <details>
          <summary style={{ fontSize: 12, color: 'var(--hk-ink-500)', cursor: 'pointer' }}>批量导入(每行一条,≤1000;复用上方原因码/启用)</summary>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-2)' }}>
            <textarea value={bulkText} onChange={(e) => setBulkText(e.target.value)} rows={5} placeholder={`每行一个${valueLabel}`} style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', fontFamily: 'var(--hk-font-mono)' }} />
            <button type="button" disabled={busy} onClick={addBulk} style={{ ...ghostBtn, alignSelf: 'flex-start' }}>
              批量导入
            </button>
          </div>
        </details>
      </div>

      {/* 列表 */}
      {loading && rows.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : rows.length === 0 ? (
        <Empty>该租户暂无{title}。</Empty>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr>
                {[valueLabel, '状态', ''].map((h) => (
                  <th key={h} style={th}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const id = rowId(row)
                const v = rowValue(row)
                return (
                  <tr key={id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={tdMono}>{display ? display(v) : v}</td>
                    <td style={td}>
                      <StatusBadge tone={rowEnabled(row) ? 'ok' : 'muted'}>{rowEnabled(row) ? '启用' : '停用'}</StatusBadge>
                    </td>
                    <td style={{ ...td, textAlign: 'right' }}>
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          if (!window.confirm(`删除该${valueLabel}「${display ? display(v) : v}」?`)) return
                          void run(() => remove(id, tenantId), '已删除')
                        }}
                        style={dangerLink}
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

export function KeywordsCard({ tenantId }: { tenantId: number }) {
  return (
    <RuleManager<KeywordRule>
      tenantId={tenantId}
      title="关键词黑名单"
      valueLabel="关键词"
      placeholder="命中即按配置处置"
      list={(t, l, o, s) => listKeywords(t, l, o, s)}
      create={(t, v, rc, en) => createKeyword({ tenant_id: t, keyword: v.trim(), reason_code: rc, enabled: en })}
      bulk={(t, vals, rc, en) => bulkCreateKeywords(t, vals.map((v) => ({ keyword: v, reason_code: rc, enabled: en })))}
      remove={(id, t) => deleteKeyword(id, t)}
      rowId={(r) => r.id}
      rowValue={(r) => r.keyword}
      rowEnabled={(r) => r.enabled}
      validate={validateKeyword}
    />
  )
}

export function HashesCard({ tenantId }: { tenantId: number }) {
  return (
    <RuleManager<HashRule>
      tenantId={tenantId}
      title="内容哈希黑名单"
      valueLabel="哈希(SHA-256)"
      placeholder="64 位十六进制"
      list={(t, l, o, s) => listHashes(t, l, o, s)}
      create={(t, v, rc, en) => createHash({ tenant_id: t, hash_hex: normalizeHash(v), reason_code: rc, enabled: en })}
      bulk={(t, vals, rc, en) => bulkCreateHashes(t, vals.map((v) => ({ hash_hex: normalizeHash(v), reason_code: rc, enabled: en })))}
      remove={(id, t) => deleteHash(id, t)}
      rowId={(r) => r.id}
      rowValue={(r) => r.hash_hex}
      rowEnabled={(r) => r.enabled}
      validate={validateHash}
      display={shortHash}
    />
  )
}

export function BannedKeysCard({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<BannedAPIKey[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState<number | null>(null)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listBannedAPIKeys(tenantId, PAGE_SIZE, 0, signal)
        .then((resp) => setRows(resp.items ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载被封 Key 失败')
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

  const unban = (k: BannedAPIKey) => {
    const reason = window.prompt(`解封 Key「${k.name || k.key_prefix}」会恢复其访问能力。可填解封原因(供审计):`, '')
    if (reason === null) return // 取消
    setBusy(k.id)
    setError(null)
    setNotice(null)
    unbanAPIKey(k.id, tenantId, reason.trim())
      .then(() => {
        setNotice(`已解封 #${k.id}`)
        load()
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? `${e.message}(${e.code})` : '解封失败'))
      .finally(() => setBusy(null))
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>被封 API Key</h2>
        <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
      </div>
      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}
      {loading && rows.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : rows.length === 0 ? (
        <Empty>该租户暂无被封 Key。</Empty>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr>
                {['名称', 'Key 前缀', '用户', '违规次数', '最近违规', ''].map((h) => (
                  <th key={h} style={th}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((k) => (
                <tr key={k.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                  <td style={td}>{k.name || '—'}</td>
                  <td style={tdMono}>{k.key_prefix}</td>
                  <td style={tdMono}>#{k.user_id}</td>
                  <td style={tdMono}>{k.violation_count}</td>
                  <td style={tdMono}>{fmt(k.last_violation_at)}</td>
                  <td style={{ ...td, textAlign: 'right' }}>
                    <button type="button" disabled={busy === k.id} onClick={() => unban(k)} style={primaryBtn}>
                      {busy === k.id ? '解封中…' : '解封'}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
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
      ? { color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
      : { color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}
function fmt(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const dangerLink: React.CSSProperties = { border: 'none', background: 'transparent', color: '#8f322a', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
