import type { CreateRouteRequest, Route, UpdateRouteRequest } from './types'

/*
 * 请求路由规则页的纯逻辑(可单测,无 DOM/网络副作用):
 *   - 表单校验(镜像后端 service.Create/Update 约束 service.go:30/59 + ValidateModelPattern validate.go:18)
 *   - 模型模式合法性(通配 '*' 仅允许整串或末尾后缀)
 *   - 列表按 match_priority 升序排序(同优先级以 id 兜底稳定)
 *   - 模型模式 / 默认优先级的展示文案
 * 全部同步纯函数,便于 §14 变异测试打红。后端仍是权威,前端先拦以避免无谓 400。
 */

/** 路由规则的 DB 默认优先级(后端 routes.match_priority DEFAULT 100,见 types.go:43 注释)。 */
export const DEFAULT_MATCH_PRIORITY = 100

/**
 * 校验模型模式(镜像后端 ValidateModelPattern,validate.go:18-32):
 *   ''        → 全匹配(合法)
 *   '*'       → 全匹配(合法)
 *   'prefix*' → 前缀匹配:恰好一个 '*' 且在末尾(合法)
 *   'exact'   → 不含 '*' 的精确串(合法)
 * 判别核心:含 '*' 但不是「恰好一个且在末尾」(如 'a*b'、'*x'、'a**')一律拒——
 * 这些会被后端选路语义静默错配。返回错误文案或 null(合法)。
 */
export function validateModelPattern(pattern: string): string | null {
  const p = pattern.trim()
  if (p === '' || p === '*') return null
  const idx = p.indexOf('*')
  if (idx === -1) return null // 纯精确串
  // 含 '*':仅当恰好一个且位于末尾才合法。
  const count = (p.match(/\*/g) ?? []).length
  if (count === 1 && idx === p.length - 1) return null
  return "模型模式的通配 '*' 只能作为整串或末尾后缀(如 '*'、'claude-*');不支持中段或多个 '*'"
}

/** 表单原始输入(字符串态,来自受控 input)。 */
export interface RouteForm {
  name: string
  userGroupMatch: string
  modelPatternMatch: string
  poolGroupId: string
  /** 优先级输入;空串表示用默认 100。 */
  matchPriority: string
}

/** 校验结果:create / update 各返回可提交的请求体,否则带中文错误说明。 */
export type CreateValidation =
  | { ok: true; value: CreateRouteRequest }
  | { ok: false; error: string }
export type UpdateValidation =
  | { ok: true; value: UpdateRouteRequest }
  | { ok: false; error: string }

// parsePositiveInt 解析正整数串:仅接受 [1-9][0-9]*,否则返回 null。
function parsePositiveInt(raw: string): number | null {
  const s = raw.trim()
  if (!/^[1-9][0-9]*$/.test(s)) return null
  return Number(s)
}

// parsePriority 解析优先级:空串=默认 100;否则须为非负整数(0 合法)。null=非法。
function parsePriority(raw: string): number | null {
  const s = raw.trim()
  if (s === '') return DEFAULT_MATCH_PRIORITY
  // 判别核心:非负整数(允许 0);负号/小数/非数字一律拒。
  if (!/^[0-9]+$/.test(s)) return null
  return Number(s)
}

// 共享字段校验:镜像后端 service.go:30/59(TrimSpace 后 name/user_group_match 非空、pool_group_id>0)
// + ValidateModelPattern。返回 { error } 或归一化后的核心字段。
function validateCore(
  form: RouteForm,
):
  | { ok: false; error: string }
  | {
      ok: true
      name: string
      userGroupMatch: string
      modelPatternMatch: string
      poolGroupId: number
      matchPriority: number
    } {
  const name = form.name.trim()
  if (name === '') return { ok: false, error: '规则名(name)不能为空' }
  const userGroupMatch = form.userGroupMatch.trim()
  if (userGroupMatch === '') return { ok: false, error: '用户组匹配(user_group_match)不能为空' }
  const poolGroupId = parsePositiveInt(form.poolGroupId)
  if (poolGroupId === null) return { ok: false, error: '目标 pool_group_id 必须是正整数' }
  const modelPatternMatch = form.modelPatternMatch.trim()
  const patternErr = validateModelPattern(modelPatternMatch)
  if (patternErr) return { ok: false, error: patternErr }
  const matchPriority = parsePriority(form.matchPriority)
  if (matchPriority === null) return { ok: false, error: '优先级(match_priority)必须是非负整数,留空则用默认 100' }
  return { ok: true, name, userGroupMatch, modelPatternMatch, poolGroupId, matchPriority }
}

/**
 * 校验新建表单。tenant_id 由页面单独提供(读取时已校验为正)。
 * model_pattern_match 为空串时省略字段(后端把缺省/空串都当全匹配)。
 */
export function validateCreate(tenantId: number, form: RouteForm): CreateValidation {
  if (!Number.isInteger(tenantId) || tenantId <= 0) {
    return { ok: false, error: 'tenant_id 必须为正整数' }
  }
  const core = validateCore(form)
  if (!core.ok) return core
  const value: CreateRouteRequest = {
    tenant_id: tenantId,
    name: core.name,
    user_group_match: core.userGroupMatch,
    pool_group_id: core.poolGroupId,
    match_priority: core.matchPriority,
  }
  if (core.modelPatternMatch !== '') value.model_pattern_match = core.modelPatternMatch
  return { ok: true, value }
}

/**
 * 校验编辑表单(全替换语义,始终显式带 match_priority,防 read-omit-write 静默重置到 100)。
 * 编辑请求体不含 tenant_id(后端 query 取租户、DisallowUnknownFields 拒 body 内 tenant_id)。
 */
export function validateUpdate(form: RouteForm): UpdateValidation {
  const core = validateCore(form)
  if (!core.ok) return core
  const value: UpdateRouteRequest = {
    name: core.name,
    user_group_match: core.userGroupMatch,
    pool_group_id: core.poolGroupId,
    // 全替换:始终显式带优先级(即便等于默认 100)。
    match_priority: core.matchPriority,
  }
  if (core.modelPatternMatch !== '') value.model_pattern_match = core.modelPatternMatch
  return { ok: true, value }
}

/**
 * 列表按 match_priority 升序排序(数值小=优先级高,先生效)。
 * 判别核心:必须按 match_priority 排,同优先级以 id 升序兜底稳定;不可改原数组。
 */
export function sortRoutes(routes: Route[]): Route[] {
  return [...routes].sort((a, b) => {
    if (a.match_priority !== b.match_priority) return a.match_priority - b.match_priority
    return a.id - b.id
  })
}

/** 模型模式展示文案:空串/'*' → 「全部模型」;其余原样(前缀模式保留尾随 '*')。 */
export function displayModelPattern(pattern: string): string {
  const p = pattern.trim()
  if (p === '' || p === '*') return '全部模型'
  return p
}

/** 把后端 Route DTO 拍平成编辑表单初值(供页面 useState 初始化)。 */
export function routeToForm(r: Route): RouteForm {
  return {
    name: r.name,
    userGroupMatch: r.user_group_match,
    modelPatternMatch: r.model_pattern_match,
    poolGroupId: String(r.pool_group_id),
    matchPriority: String(r.match_priority),
  }
}

/** 空白新建表单初值。 */
export function emptyForm(): RouteForm {
  return { name: '', userGroupMatch: '', modelPatternMatch: '', poolGroupId: '', matchPriority: '' }
}
