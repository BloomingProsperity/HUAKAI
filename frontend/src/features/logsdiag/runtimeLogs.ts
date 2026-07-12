import type { RuntimeLogRow } from './api'

/*
 * 运行日志面板纯逻辑:增量合并(轮询首页与已有列表按 id 去重)、时间/属性格式化。
 * 与后端键集分页契约(id 降序,next_before_id)一一对应。
 */

/** 轮询到的新首页与既有列表合并:按 id 去重,保持 id 降序,长度封顶 cap。 */
export function mergeRuntimeLogs(existing: RuntimeLogRow[], fresh: RuntimeLogRow[], cap = 500): RuntimeLogRow[] {
  const seen = new Set(existing.map((r) => r.id))
  const newer = fresh.filter((r) => !seen.has(r.id))
  const merged = [...newer, ...existing]
  merged.sort((a, b) => b.id - a.id)
  return merged.slice(0, cap)
}

/** 追加更旧一页(加载更多):同样去重排序,不封顶(用户主动翻页)。 */
export function appendOlderLogs(existing: RuntimeLogRow[], older: RuntimeLogRow[]): RuntimeLogRow[] {
  const seen = new Set(existing.map((r) => r.id))
  return [...existing, ...older.filter((r) => !seen.has(r.id))]
}

export function fmtLogTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** attrs 压缩成单行 k=v 串供表格展示;空对象返回空串。 */
export function fmtAttrs(attrs: Record<string, unknown>): string {
  const parts: string[] = []
  for (const [k, v] of Object.entries(attrs ?? {})) {
    const text = typeof v === 'string' ? v : JSON.stringify(v)
    parts.push(`${k}=${text}`)
  }
  return parts.join(' ')
}

export function levelToneOf(level: string): 'warn' | 'danger' {
  return level === 'error' ? 'danger' : 'warn'
}
