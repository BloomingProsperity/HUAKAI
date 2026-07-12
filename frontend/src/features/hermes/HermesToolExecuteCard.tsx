import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import type { HermesAuthQuery } from './hermesClient'
import {
  executeReadOnlyTool,
  executeToolConfirm,
  executeToolDryRun,
  listTools,
} from './hermesAdminApi'
import {
  canRunDirectly,
  confirmExecuteMessage,
  parseToolArgs,
  previewEntries,
  toolExecutionMode,
  type PreviewEntry,
} from './hermesAdmin'
import type { HermesToolDescriptor, MutationPreview } from './hermesAdminTypes'

/*
 * Hermes 工具执行卡。
 *
 * 两条路径:
 *   - 只读工具(read_only && !mutating):填 args(JSON 对象)→ 直接执行 → 展示结果。
 *   - 改动型工具(mutating):必须 dry-run(confirm=false)拿 correlation_id + preview →
 *     UI 明示「将对 {target} 执行 {tool}」并列出 preview 改动 → operator 二次强确认 →
 *     带同一 correlation_id + confirm=true 执行(后端一次性消费,陈旧/不匹配即拒)。
 * correlation_id 5 分钟 TTL;dry-run 的 args 必须与 confirm 时一致(改了 args 会让 target
 * 不匹配,后端拒、绝不改错误的行),故确认前不允许再改 args(改 args 即作废本次 dry-run)。
 */
export function HermesToolExecuteCard({ adminToken, auth }: { adminToken: string; auth: HermesAuthQuery }) {
  const [tools, setTools] = useState<HermesToolDescriptor[] | null>(null)
  const [loadErr, setLoadErr] = useState<string | null>(null)
  const [selected, setSelected] = useState<string>('')

  useEffect(() => {
    const ctrl = new AbortController()
    setLoadErr(null)
    void listTools(adminToken, auth, ctrl.signal)
      .then((t) => setTools(t))
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        setLoadErr(describeErr(e))
      })
    return () => ctrl.abort()
  }, [adminToken, auth])

  const selectedTool = useMemo(() => (tools ?? []).find((t) => t.name === selected) ?? null, [tools, selected])

  return (
    <section style={card}>
      <h2 style={h2}>工具执行</h2>
      {loadErr && <Banner tone="danger">{loadErr}</Banner>}

      {tools === null && !loadErr ? (
        <p style={muted}>加载工具清单中…</p>
      ) : (tools ?? []).length === 0 ? (
        <p style={muted}>暂无可用工具。</p>
      ) : (
        <>
          <Row label="选择工具">
            <select value={selected} onChange={(e) => setSelected(e.target.value)} style={{ ...inp, maxWidth: 360 }}>
              <option value="">— 请选择一个工具 —</option>
              {(tools ?? []).map((t) => (
                <option key={t.name} value={t.name}>
                  {t.name}
                  {t.mutating ? '(改动型)' : t.read_only ? '(只读)' : ''}
                </option>
              ))}
            </select>
          </Row>

          {selectedTool && (
            <ToolRunner key={selectedTool.name} adminToken={adminToken} auth={auth} tool={selectedTool} />
          )}
        </>
      )}
    </section>
  )
}

// ── 单个工具的执行子组件 ──

function ToolRunner({
  adminToken,
  auth,
  tool,
}: {
  adminToken: string
  auth: HermesAuthQuery
  tool: HermesToolDescriptor
}) {
  const mode = toolExecutionMode(tool)
  const direct = canRunDirectly(tool)

  const [argsText, setArgsText] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // 只读结果 / 改动结果的展示文本。
  const [resultEntries, setResultEntries] = useState<PreviewEntry[] | null>(null)
  const [resultLabel, setResultLabel] = useState<string | null>(null)
  // 改动型 dry-run 的待确认状态。
  const [pending, setPending] = useState<MutationPreview | null>(null)

  const reset = () => {
    setError(null)
    setResultEntries(null)
    setResultLabel(null)
    setPending(null)
  }

  // 改 args 即作废上一次 dry-run(避免确认时 args 与 preview 锚定的 target 不一致)。
  const onArgsChange = (v: string) => {
    setArgsText(v)
    if (pending) setPending(null)
  }

  const runReadOnly = () => {
    reset()
    const parsed = parseToolArgs(argsText)
    if (!parsed.ok) {
      setError(parsed.error)
      return
    }
    setBusy(true)
    executeReadOnlyTool(adminToken, auth, tool.name, parsed.args)
      .then((r) => {
        setResultLabel('只读执行完成')
        setResultEntries(previewEntries((r.result ?? {}) as Record<string, unknown>))
      })
      .catch((e) => setError(describeErr(e)))
      .finally(() => setBusy(false))
  }

  const runDryRun = () => {
    reset()
    const parsed = parseToolArgs(argsText)
    if (!parsed.ok) {
      setError(parsed.error)
      return
    }
    setBusy(true)
    executeToolDryRun(adminToken, auth, tool.name, parsed.args)
      .then((p) => setPending(p))
      .catch((e) => setError(describeErr(e)))
      .finally(() => setBusy(false))
  }

  const confirmRun = () => {
    if (!pending) return
    // 必须用 dry-run 当时的 args(此刻 argsText 与 dry-run 时一致,因为改 args 已清空 pending)。
    const parsed = parseToolArgs(argsText)
    if (!parsed.ok) {
      setError(parsed.error)
      return
    }
    // 强二次确认:明示「将执行改动型工具」+ preview 改动明细。
    if (!window.confirm(confirmExecuteMessage(tool.name, pending.preview))) return
    setBusy(true)
    executeToolConfirm(adminToken, auth, tool.name, parsed.args, pending.correlation_id)
      .then((r) => {
        setPending(null)
        setResultLabel(
          `改动已执行${r.target_type ? ` · 目标 ${r.target_type}#${r.target_id ?? ''}` : ''}`,
        )
        setResultEntries(previewEntries((r.result ?? {}) as Record<string, unknown>))
      })
      .catch((e) => {
        // 失败(含 correlation_id 过期/不匹配):清掉待确认,operator 须重新 dry-run。
        setPending(null)
        setError(describeErr(e))
      })
      .finally(() => setBusy(false))
  }

  return (
    <div style={runnerBox}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
        <strong style={{ fontSize: 13, color: 'var(--hk-ink-900)' }}>{tool.name}</strong>
        <span style={mode === 'mutating' ? badgeMut : badgeRO}>{mode === 'mutating' ? '改动型' : '只读'}</span>
        <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{tool.category}</span>
      </div>
      {tool.description && <p style={{ ...muted, fontSize: 12 }}>{tool.description}</p>}
      {tool.input_schema && Object.keys(tool.input_schema).length > 0 && (
        <p style={{ ...muted, fontSize: 11 }}>
          入参:{Object.entries(tool.input_schema).map(([k, v]) => `${k}(${v})`).join('、')}
        </p>
      )}

      <Row label="args(JSON 对象,无参可留空)">
        <textarea
          value={argsText}
          onChange={(e) => onArgsChange(e.target.value)}
          placeholder='如 {"account_id": 1}'
          rows={3}
          style={textArea}
        />
      </Row>

      {error && <Banner tone="danger">{error}</Banner>}

      {/* 改动型:dry-run 后展示 preview + 确认/取消 */}
      {pending && (
        <div style={previewBox}>
          <strong style={{ fontSize: 13, color: 'var(--hk-ink-900)' }}>
            预览(dry-run · 未执行):将对该工具执行改动
          </strong>
          <p style={{ ...muted, fontSize: 11, margin: '4px 0 0' }}>
            correlation_id {pending.correlation_id.slice(0, 8)}… · 有效 {pending.expires_in_seconds}s,过期需重新预览。
          </p>
          <PreviewList entries={previewEntries(pending.preview)} />
          <div style={{ display: 'flex', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-2)' }}>
            <button type="button" disabled={busy} onClick={confirmRun} style={dangerBtn}>
              {busy ? '执行中…' : '确认执行改动'}
            </button>
            <button type="button" disabled={busy} onClick={() => setPending(null)} style={ghostBtn}>
              取消
            </button>
          </div>
        </div>
      )}

      {/* 操作按钮:只读直接跑;改动型先 dry-run */}
      {!pending && (
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          {direct ? (
            <button type="button" disabled={busy} onClick={runReadOnly} style={primaryBtn}>
              {busy ? '执行中…' : '执行(只读)'}
            </button>
          ) : (
            <button type="button" disabled={busy} onClick={runDryRun} style={primaryBtn}>
              {busy ? '预览中…' : '预览改动(dry-run)'}
            </button>
          )}
        </div>
      )}

      {/* 执行结果 */}
      {resultLabel && (
        <div style={resultBox}>
          <strong style={{ fontSize: 13, color: 'var(--hk-primary-700)' }}>{resultLabel}</strong>
          {resultEntries && resultEntries.length > 0 && <PreviewList entries={resultEntries} />}
        </div>
      )}
    </div>
  )
}

function PreviewList({ entries }: { entries: PreviewEntry[] }) {
  if (entries.length === 0) return <p style={{ ...muted, fontSize: 11, margin: '4px 0 0' }}>(无明细)</p>
  return (
    <div style={{ marginTop: 'var(--hk-space-2)', display: 'flex', flexDirection: 'column', gap: 2 }}>
      {entries.map((e) => (
        <div key={e.key} style={{ display: 'flex', gap: 'var(--hk-space-2)', fontSize: 12 }}>
          <span style={{ color: 'var(--hk-ink-500)', minWidth: 140 }}>{e.key}</span>
          <span style={{ color: 'var(--hk-ink-900)', wordBreak: 'break-all' }}>{e.value}</span>
        </div>
      ))}
    </div>
  )
}

// ── 小组件 + 样式 ──

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>{label}</span>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>{children}</div>
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
        background: danger ? 'var(--hk-danger-soft)' : 'var(--hk-primary-50, #eef7f2)',
        border: `1px solid ${danger ? 'var(--hk-danger-soft)' : 'var(--hk-line)'}`,
      }}
    >
      {children}
    </div>
  )
}

function describeErr(e: unknown): string {
  if (e instanceof ApiError) return `${e.message}(${e.code})`
  if (e instanceof Error) return e.message
  return '执行失败'
}

const card: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-4)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)' }
const h2: React.CSSProperties = { fontSize: 15, color: 'var(--hk-ink-700)', margin: 0 }
const muted: React.CSSProperties = { fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }
const runnerBox: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }
const previewBox: React.CSSProperties = { padding: 'var(--hk-space-3)', border: '1px solid #f0d9a8', background: '#fff8e8', borderRadius: 'var(--hk-radius-md)' }
const resultBox: React.CSSProperties = { padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line)', background: 'var(--hk-primary-50, #eef7f2)', borderRadius: 'var(--hk-radius-md)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', flex: 1, minWidth: 120 }
const textArea: React.CSSProperties = { width: '100%', padding: 'var(--hk-space-2) var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, fontFamily: 'monospace', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', resize: 'vertical' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const dangerBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #c0392b', borderRadius: 'var(--hk-radius-md)', background: '#c0392b', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const badgeRO: React.CSSProperties = { fontSize: 10, fontWeight: 600, color: 'var(--hk-primary-700)', padding: '1px 6px', border: '1px solid var(--hk-line)', borderRadius: 999 }
const badgeMut: React.CSSProperties = { fontSize: 10, fontWeight: 600, color: 'var(--hk-danger)', padding: '1px 6px', border: '1px solid var(--hk-danger-soft)', background: 'var(--hk-danger-soft)', borderRadius: 999 }
