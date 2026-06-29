/*
 * Hermes 运营台「改动型」子系统的纯逻辑层(无 React、无网络),便于做 §14 变异测试。
 *
 * 这里集中了所有「判别核心」:
 *   - profile kind 校验(managed 禁 pool_group_id;dedicated 必需 pool_group_id)——镜像后端
 *     validateProfileSpecWithStore(backend/internal/hermes/profiles.go:196)。
 *   - 工具执行路径分类(只读直接跑 vs mutating 走 dry-run→confirm)。
 *   - 工具 args 的 JSON 解析(空串=空 args;非法 JSON / 非对象一律拒)。
 *   - preview / settings / profile 的展示归一化。
 * 全部为同步纯函数,删守卫 / 翻条件时断言必须转红。
 */

import {
  API_SOURCE_DEDICATED,
  API_SOURCE_MANAGED,
  type HermesAPISource,
  type HermesSettings,
  type HermesToolDescriptor,
} from './hermesAdminTypes'

// ── api_source 展示 ────────────────────────────────────────────────────────────────

/** api_source → 中文标签。 */
export function apiSourceLabel(source: string): string {
  switch (source) {
    case API_SOURCE_MANAGED:
      return '托管 HUAKAI API'
    case API_SOURCE_DEDICATED:
      return '专用分组'
    default:
      return source || '—'
  }
}

/** kind → 中文标签(profile 的 kind 取值与 api_source 同集)。 */
export function profileKindLabel(kind: string): string {
  return apiSourceLabel(kind)
}

// ── 启用配置校验 ────────────────────────────────────────────────────────────────────

/** 启用配置表单的输入(profileId 为空表示不绑定具体 profile)。 */
export interface EnableForm {
  apiSource: HermesAPISource
  profileId: number | null
}

/** 校验结果:ok 时带可提交的请求体片段。 */
export type EnableValidation =
  | { ok: true; apiSource: HermesAPISource; profileId?: number }
  | { ok: false; error: string }

/**
 * 校验启用配置表单(镜像后端 validateSettingsSourceWithStore,settings.go:141):
 *   - 判别核心:managed 禁带 profile_id(后端 settings.go:144 直接 400);
 *   - 判别核心:dedicated_group 必须选定一个 profile(且其 kind 须为 dedicated_group 并归本人
 *     所有,后端 settings.go:148-161 校验);
 *   - profileId 若给出必须为正整数。
 */
export function validateEnable(form: EnableForm): EnableValidation {
  if (form.apiSource !== API_SOURCE_MANAGED && form.apiSource !== API_SOURCE_DEDICATED) {
    return { ok: false, error: 'api_source 取值非法' }
  }
  if (form.profileId !== null && (!Number.isInteger(form.profileId) || form.profileId <= 0)) {
    return { ok: false, error: 'profile_id 必须是正整数' }
  }
  // 判别核心:托管型禁止绑定 profile,否则后端会拒。
  if (form.apiSource === API_SOURCE_MANAGED && form.profileId !== null) {
    return { ok: false, error: '托管 HUAKAI API 不能绑定 API profile' }
  }
  // 判别核心:专用分组必须绑定一个 profile,否则后端会拒。
  if (form.apiSource === API_SOURCE_DEDICATED && form.profileId === null) {
    return { ok: false, error: '专用分组必须选定一个 API profile' }
  }
  const out: { ok: true; apiSource: HermesAPISource; profileId?: number } = {
    ok: true,
    apiSource: form.apiSource,
  }
  if (form.profileId !== null) out.profileId = form.profileId
  return out
}

// ── 新建 profile 校验 ──────────────────────────────────────────────────────────────

/** 新建 profile 表单输入(均为字符串,便于直接绑定 input)。 */
export interface ProfileForm {
  name: string
  kind: HermesAPISource
  apiKeyId: string
  poolGroupId: string
}

/** 校验结果:ok 时带可提交的请求体。 */
export type ProfileValidation =
  | { ok: true; value: { name: string; kind: HermesAPISource; api_key_id?: number; pool_group_id?: number } }
  | { ok: false; error: string }

/** 解析可选的正整数 ID 串:空串=未填(返回 null);非正整数=非法(返回 undefined)。 */
function parseOptionalId(raw: string): number | null | undefined {
  const t = raw.trim()
  if (t === '') return null
  if (!/^[1-9][0-9]*$/.test(t)) return undefined
  return Number(t)
}

/**
 * 校验新建 profile 表单(镜像后端 validateProfileSpecWithStore,profiles.go:189):
 *   - name trim 后非空
 *   - kind ∈ {managed_huakai_api, dedicated_group}
 *   - 判别核心:managed 禁 pool_group_id;dedicated 必需 pool_group_id(>0)
 *   - api_key_id / pool_group_id 若填须为正整数
 * 前端先拦避免无谓 400 / 403;后端仍是权威(还会校验 owner 一致性)。
 */
export function validateProfile(form: ProfileForm): ProfileValidation {
  const name = form.name.trim()
  if (name === '') return { ok: false, error: 'profile 名称不能为空' }
  if (form.kind !== API_SOURCE_MANAGED && form.kind !== API_SOURCE_DEDICATED) {
    return { ok: false, error: 'kind 取值非法' }
  }
  const apiKeyId = parseOptionalId(form.apiKeyId)
  if (apiKeyId === undefined) return { ok: false, error: 'api_key_id 必须是正整数' }
  const poolGroupId = parseOptionalId(form.poolGroupId)
  if (poolGroupId === undefined) return { ok: false, error: 'pool_group_id 必须是正整数' }

  if (form.kind === API_SOURCE_MANAGED && poolGroupId !== null) {
    // 判别核心:托管型禁止设 pool_group_id(后端 profiles.go:198 直接 400)。
    return { ok: false, error: '托管 HUAKAI API 不能设置 pool_group_id' }
  }
  if (form.kind === API_SOURCE_DEDICATED && poolGroupId === null) {
    // 判别核心:专用分组必须设 pool_group_id(后端 profiles.go:202 直接 400)。
    return { ok: false, error: '专用分组必须设置 pool_group_id' }
  }

  const value: { name: string; kind: HermesAPISource; api_key_id?: number; pool_group_id?: number } = {
    name,
    kind: form.kind,
  }
  if (apiKeyId !== null) value.api_key_id = apiKeyId
  if (poolGroupId !== null) value.pool_group_id = poolGroupId
  return { ok: true, value }
}

/** 某个 profile 是否正被当前配置引用(用于删除前提示「将断开当前绑定」)。 */
export function isProfileInUse(settings: HermesSettings | null, profileId: number): boolean {
  return settings != null && settings.profile_id != null && settings.profile_id === profileId
}

// ── 工具执行路径分类 ────────────────────────────────────────────────────────────────

export type ToolExecutionMode = 'read_only' | 'mutating'

/**
 * 判定一个工具的执行模式。
 * 判别核心:只要 mutating=true 就归为 mutating(必须走 dry-run→confirm),即便它也标了
 * read_only/requires_confirmation——绝不能因为某个标记把改动型工具误判成可直接执行。
 * 只有「read_only 且非 mutating」才允许直接执行。
 */
export function toolExecutionMode(tool: Pick<HermesToolDescriptor, 'read_only' | 'mutating'>): ToolExecutionMode {
  if (tool.mutating) return 'mutating'
  return tool.read_only ? 'read_only' : 'mutating'
}

/** 是否允许直接执行(不经确认)。等价于 toolExecutionMode==='read_only'。 */
export function canRunDirectly(tool: Pick<HermesToolDescriptor, 'read_only' | 'mutating'>): boolean {
  return toolExecutionMode(tool) === 'read_only'
}

// ── 工具 args(JSON 文本)解析 ──────────────────────────────────────────────────────

export type ArgsParse =
  | { ok: true; args: Record<string, unknown> }
  | { ok: false; error: string }

/**
 * 解析工具 args 的 JSON 文本输入。
 * 判别核心:空串 / 全空白 ⇒ 空 args(合法);非法 JSON ⇒ 拒;合法 JSON 但不是「对象」
 * (数组 / 数字 / 字符串 / null)⇒ 拒——后端 args 必须是一个 JSON 对象(map[string]any)。
 */
export function parseToolArgs(text: string): ArgsParse {
  const t = text.trim()
  if (t === '') return { ok: true, args: {} }
  let parsed: unknown
  try {
    parsed = JSON.parse(t)
  } catch {
    return { ok: false, error: 'args 必须是合法 JSON' }
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false, error: 'args 必须是一个 JSON 对象(如 {"account_id": 1})' }
  }
  return { ok: true, args: parsed as Record<string, unknown> }
}

// ── preview 展示归一化 ──────────────────────────────────────────────────────────────

/** 一条 preview 键值对(用于把 preview 对象拍平成可渲染的「字段 → 值」列表)。 */
export interface PreviewEntry {
  key: string
  value: string
}

/**
 * 把 mutating 工具 dry-run 返回的 preview 对象拍平成有序可渲染列表。
 * preview 是后端给的「将改动什么」(枚举/id/状态名),无 secret(后端在 plan 层已脱敏)。
 * null/undefined/非对象一律得空列表;对象按 key 排序保证渲染稳定。值为对象/数组时 JSON 串化。
 */
export function previewEntries(preview: Record<string, unknown> | null | undefined): PreviewEntry[] {
  if (preview == null || typeof preview !== 'object' || Array.isArray(preview)) return []
  return Object.keys(preview)
    .sort()
    .map((key) => ({ key, value: stringifyPreviewValue((preview as Record<string, unknown>)[key]) }))
}

function stringifyPreviewValue(v: unknown): string {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  try {
    return JSON.stringify(v)
  } catch {
    return String(v)
  }
}

/** 二次确认文案:明示「将对 {target} 执行 {tool}」。target 缺失时退化为工具名提示。 */
export function confirmExecuteMessage(
  toolName: string,
  preview: Record<string, unknown> | null | undefined,
): string {
  const entries = previewEntries(preview)
  const detail =
    entries.length > 0
      ? '\n\n将应用以下改动:\n' + entries.map((e) => `· ${e.key}:${e.value}`).join('\n')
      : ''
  return `确认执行改动型工具「${toolName}」?\n此操作会真实改变系统状态,不可仅凭直觉确认。${detail}`
}
