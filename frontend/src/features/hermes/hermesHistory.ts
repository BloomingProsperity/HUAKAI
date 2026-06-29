/*
 * Hermes 历史回看 / 模块上下文的纯逻辑层(无 React、无网络),便于做 §14 变异测试。
 *
 * 后端 message.content 是 json.RawMessage(backend/internal/hermes/types.go:108),原样回传,
 * 形态不固定:可能是
 *   - 纯字符串 "你好"
 *   - 对象 { "text": "..." } 或 { "content": "..." }
 *   - Anthropic 风格内容块数组 [{ "type": "text", "text": "..." }, ...]
 * messageText 把这些归一化成一段可渲染文本,既保证只读回看可用,也避免把整坨 JSON 直接抛给用户。
 */

import type { HermesConversation, HermesMessage, HermesModuleView } from './hermesClient'

/** conversationTitle 给会话列表项算显示标题:有 title 用 title,否则回退「会话 #id」。 */
export function conversationTitle(c: Pick<HermesConversation, 'id' | 'title'>): string {
  const t = c.title?.trim()
  return t ? t : `会话 #${c.id}`
}

/**
 * messageText 从一条消息的 content 中提取可读文本。
 * 顺序:字符串原样 → 内容块数组拼接其 text → 对象取 text/content 字段 → 兜底 JSON 串化。
 * 兜底串化保证「任何形态都不会渲染成空白」,从而不会把一条有内容的消息显示成空气。
 */
export function messageText(content: unknown): string {
  if (content === null || content === undefined) return ''
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    const parts: string[] = []
    for (const block of content) {
      const t = blockText(block)
      if (t) parts.push(t)
    }
    return parts.join('\n')
  }
  if (typeof content === 'object') {
    const obj = content as Record<string, unknown>
    if (typeof obj.text === 'string') return obj.text
    if (typeof obj.content === 'string') return obj.content
  }
  // 兜底:未知形态串化,确保有内容的消息绝不渲染成空白。
  try {
    return JSON.stringify(content)
  } catch {
    return ''
  }
}

/** blockText 从单个内容块里取文本(Anthropic 风格 {type:'text', text:'...'} 或纯字符串块)。 */
function blockText(block: unknown): string {
  if (typeof block === 'string') return block
  if (block && typeof block === 'object') {
    const b = block as Record<string, unknown>
    if (typeof b.text === 'string') return b.text
  }
  return ''
}

/** roleLabel 把后端 role 转成中文展示标签;未知 role 原样返回(不吞数据)。 */
export function roleLabel(role: string): string {
  switch (role) {
    case 'user':
      return '用户'
    case 'assistant':
      return 'Hermes'
    case 'system':
      return '系统'
    case 'tool':
      return '工具'
    default:
      return role
  }
}

/** sortMessagesByCreatedAt 按 created_at 升序稳定排序(回看须时间正序);缺时间的排在最后但保持相对顺序。 */
export function sortMessagesByCreatedAt(messages: HermesMessage[]): HermesMessage[] {
  return messages
    .map((m, i) => ({ m, i }))
    .sort((a, b) => {
      const ta = parseTime(a.m.created_at)
      const tb = parseTime(b.m.created_at)
      if (ta !== tb) return ta - tb
      return a.i - b.i // 时间相同(或都缺失)保持原相对顺序,稳定排序
    })
    .map((x) => x.m)
}

function parseTime(iso?: string): number {
  if (!iso) return Number.POSITIVE_INFINITY // 缺时间排最后
  const t = new Date(iso).getTime()
  return Number.isNaN(t) ? Number.POSITIVE_INFINITY : t
}

/** ModuleCategoryGroup 是模块上下文按 category 聚合后的一组。 */
export interface ModuleCategoryGroup {
  category: string
  modules: HermesModuleView[]
}

/**
 * groupModulesByCategory 把模块视图按 category 聚合,组内保持后端给定顺序,
 * 组的出现顺序按各 category「首次出现」的先后(不重排,贴合后端按 ID 的稳定序)。
 */
export function groupModulesByCategory(modules: HermesModuleView[]): ModuleCategoryGroup[] {
  const order: string[] = []
  const byCat = new Map<string, HermesModuleView[]>()
  for (const m of modules) {
    const cat = m.category || '未分类'
    if (!byCat.has(cat)) {
      byCat.set(cat, [])
      order.push(cat)
    }
    byCat.get(cat)!.push(m)
  }
  return order.map((cat) => ({ category: cat, modules: byCat.get(cat)! }))
}

/**
 * probeTone 把探针 status 映射成 UI 语义色调键(ok/warn/danger/muted),供面板着色。
 * 健康相关枚举(healthy/up/ready)→ ok;degraded/warn → warn;down/unhealthy/error/fail → danger;其余 → muted。
 */
export function probeTone(status: string): 'ok' | 'warn' | 'danger' | 'muted' {
  const s = status.toLowerCase()
  if (s === 'healthy' || s === 'up' || s === 'ready' || s === 'ok') return 'ok'
  if (s === 'degraded' || s === 'warn' || s === 'warning') return 'warn'
  if (s === 'down' || s === 'unhealthy' || s === 'error' || s === 'fail' || s === 'failed') {
    return 'danger'
  }
  return 'muted'
}
