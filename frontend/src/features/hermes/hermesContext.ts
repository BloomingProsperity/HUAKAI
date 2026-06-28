/*
 * Hermes 面板的纯逻辑:壳判定 + 当前页上下文前缀 + 操作身份持久化。
 * 全部抽成不依赖 React/DOM 的纯函数(persist 仅触碰 localStorage,容错处理),便于 vitest 变异测试。
 */

import { PIPELINE_NAV, type NavSection, type Shell } from '../../app/nav'

/** 一次 nav 匹配的结果:命中的壳 + label。无匹配为 null。 */
export interface NavMatch {
  shell: Shell
  label: string
}

/**
 * resolveNavMatch 在给定 nav sections 里为 path 找"最长匹配前缀"的那条 item,返回其壳 + label。
 * 用最长前缀是因为详情路由(/accounts/:id)在 nav 里登记的是父路径(/accounts);若将来出现某 path
 * 是另一 path 的严格前缀(如 /admin 与 /admin/x 并存),也要命中更具体的那条而非更短的。无匹配返回 null。
 * sections 参数默认用真实 PIPELINE_NAV,允许测试注入合成 nav 来验证"最长前缀"这条判定本身。
 */
export function resolveNavMatch(pathname: string, sections: NavSection[] = PIPELINE_NAV): NavMatch | null {
  const path = normalizePath(pathname)
  let best: { match: NavMatch; len: number } | null = null
  for (const section of sections) {
    for (const item of section.items) {
      if (isPathUnder(path, item.path)) {
        const len = item.path.length
        if (!best || len > best.len) best = { match: { shell: section.shell, label: item.label }, len }
      }
    }
  }
  return best ? best.match : null
}

/**
 * getCurrentShell 由 pathname 判定当前处于哪个壳(运营台 operator / 用户门户 user),无匹配返回 null。
 * 面板只在 operator 壳渲染;非 operator(含 null)一律不渲染。
 */
export function getCurrentShell(pathname: string): Shell | null {
  const m = resolveNavMatch(pathname)
  return m ? m.shell : null
}

/** isPathUnder 判断 path 是否等于 base 或为其子路径(以 base + '/' 开头),避免 /accounts 误配 /accounts-x。 */
function isPathUnder(path: string, base: string): boolean {
  if (path === base) return true
  return path.startsWith(base.endsWith('/') ? base : base + '/')
}

/** normalizePath 去掉查询串/哈希,并裁掉非根路径的尾斜杠,使匹配稳定。 */
function normalizePath(pathname: string): string {
  let p = pathname.split('?')[0].split('#')[0]
  if (p.length > 1 && p.endsWith('/')) p = p.slice(0, -1)
  return p || '/'
}

/**
 * getCurrentPageLabel 取当前路径对应 nav item 的中文 label,用于上下文 chip 与前缀文案;
 * 无匹配返回回退文案。与 getCurrentShell 走同一 resolveNavMatch,保证壳与 label 判定一致。
 */
export function getCurrentPageLabel(pathname: string, fallback = '运营台'): string {
  const m = resolveNavMatch(pathname)
  return m ? m.label : fallback
}

/**
 * extractEntityId 从详情类路径(/accounts/:id、/users/:id)提取实体 id 文本;非详情路径返回 null。
 * 只认这两条已知带详情子路由的页;其余返回 null(避免把 /admin/groups 的 groups 误当 id)。
 */
export function extractEntityId(pathname: string): string | null {
  const path = normalizePath(pathname)
  for (const base of ['/accounts/', '/users/']) {
    if (path.startsWith(base)) {
      const rest = path.slice(base.length)
      // 只取第一段(/accounts/123/whatever → 123),且必须是纯数字 id。
      const seg = rest.split('/')[0]
      if (seg !== '' && /^\d+$/.test(seg)) return seg
    }
  }
  return null
}

/**
 * buildPageContextPrefix 组装注入到消息 content 最前面的"当前页上下文"前缀(中文括注 + 双换行)。
 * UI 只展示用户真正输入的文本,这段前缀只随请求发出、不回显。label 为空时退回"运营台"。
 * 返回串总是以 \n\n 结尾,便于与用户输入拼接。
 */
export function buildPageContextPrefix(label: string, entityId: string | null): string {
  const safeLabel = label.trim() === '' ? '运营台' : label.trim()
  const idPart = entityId ? `,实体 #${entityId}` : ''
  return `(上下文:我正在运营台“${safeLabel}”页${idPart})\n\n`
}

/** composeUserContent 把上下文前缀拼到用户输入前,作为真正发给后端的 content。 */
export function composeUserContent(prefix: string, userInput: string): string {
  return prefix + userInput
}

// ── 操作身份(as_user_id 必填 / tenant_id 可选)持久化 ──

/** 操作身份:面板代某个 tenant user 与 Hermes 对话所需的上下文。 */
export interface HermesActor {
  /** ?as_user_id,必填正整数。 */
  asUserId: number | null
  /** ?tenant_id,可选(platform_admin 必填、tenant_operator 可省)。 */
  tenantId: number | null
}

const LS_ACTOR = 'hk_hermes_actor'

/** emptyActor 返回未设置的操作身份(用于无持久值/解析失败时)。 */
export function emptyActor(): HermesActor {
  return { asUserId: null, tenantId: null }
}

/** loadActor 从 localStorage 读操作身份;无/坏数据返回 emptyActor(容错,不抛)。 */
export function loadActor(): HermesActor {
  try {
    const raw = localStorage.getItem(LS_ACTOR)
    if (!raw) return emptyActor()
    return parseActor(JSON.parse(raw))
  } catch {
    return emptyActor()
  }
}

/** saveActor 持久化操作身份;localStorage 不可用时静默忽略(仅内存态)。 */
export function saveActor(actor: HermesActor): void {
  try {
    localStorage.setItem(LS_ACTOR, JSON.stringify({ as_user_id: actor.asUserId, tenant_id: actor.tenantId }))
  } catch {
    /* localStorage 不可用,忽略 */
  }
}

/** parseActor 把任意已解析对象归一为 HermesActor:只接受正整数,否则置 null。导出供测试。 */
export function parseActor(obj: unknown): HermesActor {
  if (!obj || typeof obj !== 'object') return emptyActor()
  const o = obj as Record<string, unknown>
  return {
    asUserId: toPositiveInt(o.as_user_id),
    tenantId: toPositiveInt(o.tenant_id),
  }
}

/** toPositiveInt 把输入解析为正整数;非正整数/非数字字符串一律 null(防把 0/负数/小数当合法身份)。 */
export function toPositiveInt(v: unknown): number | null {
  let n: number
  if (typeof v === 'number') n = v
  else if (typeof v === 'string' && v.trim() !== '') n = Number(v)
  else return null
  if (!Number.isInteger(n) || n <= 0) return null
  return n
}

/** actorReady 判断操作身份是否可发起对话:as_user_id 必须为正整数(tenant_id 可缺)。 */
export function actorReady(actor: HermesActor): boolean {
  return actor.asUserId !== null && actor.asUserId > 0
}
