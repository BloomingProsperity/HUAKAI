import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getLogLevel, setLogLevel } from './api'
import { canSubmit, isSelectableLevel, levelLabel, levelTone, normalizeLevel } from './logsdiag'
import { SELECTABLE_LEVELS } from './types'

/*
 * 日志与诊断(运维台,admin)。GET/PUT /v1/admin/loglevel:展示网关当前进程级
 * 日志级别 + 运行时热调(下拉 debug/info/warn/error → 提交,无需重启)。
 * platform_admin 专属。纯运维旋钮:不触碰任何业务数据,只改本进程日志详尽度。
 */
export function LogsDiagPage() {
  const [current, setCurrent] = useState<string>('')
  const [target, setTarget] = useState<string>('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    getLogLevel(signal)
      .then((resp) => {
        const lvl = normalizeLevel(resp.level)
        setCurrent(lvl)
        // 目标默认对齐当前;若当前不在可选档(如 dpanic),目标留空避免误改。
        setTarget(isSelectableLevel(lvl) ? lvl : '')
      })
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '读取日志级别失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const submit = async () => {
    if (!canSubmit(current, target)) return
    setSaving(true)
    setError(null)
    setNotice(null)
    try {
      const resp = await setLogLevel(target)
      const lvl = normalizeLevel(resp.level)
      setCurrent(lvl)
      setTarget(isSelectableLevel(lvl) ? lvl : '')
      setNotice(`已切换至 ${lvl}`)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '切换日志级别失败')
    } finally {
      setSaving(false)
    }
  }

  const dirty = canSubmit(current, target)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>日志与诊断</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          网关运行时日志级别热调 · platform_admin 专属。调整即时生效,无需重启,无需改配置。
        </p>
      </header>

      {error && (
        <div style={errorBox}>{error}</div>
      )}
      {notice && (
        <div style={noticeBox}>{notice}</div>
      )}

      <section style={card}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          <span style={cardLabel}>当前级别</span>
          {loading ? (
            <span style={{ color: 'var(--hk-ink-500)', fontSize: 13 }}>读取中…</span>
          ) : current ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
              <StatusBadge tone={levelTone(current)}>{current}</StatusBadge>
              <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{levelLabel(current)}</span>
            </div>
          ) : (
            <span style={{ color: 'var(--hk-ink-500)', fontSize: 13 }}>未知</span>
          )}
        </div>

        <div style={{ height: 1, background: 'var(--hk-line)', margin: 'var(--hk-space-4) 0' }} />

        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <span style={cardLabel}>调整为</span>
          <div style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'center', flexWrap: 'wrap' }}>
            <select
              value={target}
              disabled={loading || saving}
              onChange={(e) => { setTarget(e.target.value); setNotice(null) }}
              style={select}
              aria-label="目标日志级别"
            >
              {/* 当前级别落在可选集外(如 dpanic)时,给一个占位项提示需手动择一。 */}
              {!isSelectableLevel(target) && <option value="">请选择…</option>}
              {SELECTABLE_LEVELS.map((lvl) => (
                <option key={lvl} value={lvl}>
                  {levelLabel(lvl)}
                </option>
              ))}
            </select>
            <button type="button" disabled={!dirty || saving || loading} onClick={submit} style={dirty && !saving ? primaryBtn : primaryBtnDisabled}>
              {saving ? '切换中…' : '应用'}
            </button>
          </div>
          <p style={{ margin: 0, fontSize: 12, color: 'var(--hk-ink-300)' }}>
            提示:debug 会显著增加日志量与开销,仅事故排查时短开,排查完务必调回 info。
          </p>
        </div>
      </section>
    </div>
  )
}

const card: React.CSSProperties = {
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  padding: 'var(--hk-space-5)',
  maxWidth: 560,
}
const cardLabel: React.CSSProperties = { fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)' }
const select: React.CSSProperties = {
  height: 34,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  minWidth: 300,
}
const primaryBtn: React.CSSProperties = {
  height: 34,
  padding: '0 var(--hk-space-5)',
  border: '1px solid var(--hk-primary-600)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 13,
  fontWeight: 600,
  cursor: 'pointer',
}
const primaryBtnDisabled: React.CSSProperties = {
  ...primaryBtn,
  background: 'var(--hk-surface-sunken)',
  color: 'var(--hk-ink-300)',
  border: '1px solid var(--hk-line)',
  cursor: 'not-allowed',
}
const errorBox: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  color: 'var(--hk-danger)',
  background: 'var(--hk-danger-soft)',
  border: '1px solid var(--hk-danger-soft)',
}
const noticeBox: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  color: '#0b6553',
  background: 'var(--hk-primary-50)',
  border: '1px solid var(--hk-primary-100)',
}
