/*
 * 上游目录页纯逻辑(可单测,无 DOM/网络副作用):
 *   - tenant_id query 构造(provider/channel 列表共用)
 *   - provider 新建/更新表单的前端校验(镜像后端 validateProviderCatalog*Request)
 *   - 上游协议白名单(镜像后端 knownProviderCatalogProtocols,provider_catalog_mutation_handler.go:489)
 *   - channel 新建/更新表单的前端校验(镜像后端 validateChannelCatalog*Request)
 * 全部为同步纯函数,便于变异测试打红。后端始终是权威,前端先拦以避免无谓 400。
 */

import type {
  ChannelCatalogItem,
  ChannelCatalogMutationRequest,
  ProviderCatalogItem,
  ProviderCatalogMutationRequest,
} from './types'
import type { BadgeTone } from '../../ui/StatusBadge'

export type QueryValue = string | number | undefined

/** 列表 query:tenant_id 必带(platform_admin 必填),limit/offset 透传。 */
export function buildCatalogQuery(
  tenantId: number,
  limit: number,
  offset: number,
): Record<string, QueryValue> {
  return { tenant_id: tenantId, limit, offset }
}

// ── provider 协议白名单(镜像后端 knownProviderCatalogProtocols)─────────────────

/**
 * 受支持的上游协议族。必须与后端 knownProviderCatalogProtocols
 * (provider_catalog_mutation_handler.go:489)严格一致 —— 后端对未列入者返回
 * 400 invalid_upstream_protocol。前端用它渲染下拉并先拦非法值。
 * 顺序按后端注释的批次分组,便于人工核对。
 */
export const UPSTREAM_PROTOCOLS: readonly string[] = [
  'anthropic_messages',
  'openai_chat',
  'openai_responses',
  'openai_codex',
  'gemini',
  'gemini_messages',
  'bedrock',
  'bedrock_invoke',
  'openrouter_chat',
  'grok_chat',
  'deepseek_chat',
  'mistral_chat',
  'groqcloud_chat',
  'together_chat',
  'perplexity_chat',
  'fireworks_chat',
  // 12 家 OpenAI 兼容直通族(国内 + cohere + ollama 兼容模式)。
  'kimi_chat',
  'qwen_chat',
  'glm_chat',
  'yi_chat',
  'baichuan_chat',
  'doubao_chat',
  'ernie_chat',
  'step_chat',
  'hunyuan_chat',
  'minimax_chat',
  'cohere_chat',
  'ollama_chat',
  // 6 个 serving adapter 新族。
  'ollama_native',
  'dify_chat',
  'replicate_image',
  'vertex_gemini',
  'vertex_anthropic',
  'gemini_code_assist',
  // session 族 + antigravity。
  'anthropic_claude_session',
  'cursor_session',
  'copilot_session',
  'gemini_advanced_session',
  'antigravity',
  'antigravity_session',
  'kiro_session',
  'windsurf_session',
]

const PROTOCOL_SET = new Set(UPSTREAM_PROTOCOLS)

/** 判定协议是否在白名单(镜像 isKnownProviderCatalogProtocol)。 */
export function isKnownProtocol(protocol: string): boolean {
  return PROTOCOL_SET.has(protocol.trim())
}

// ── provider 表单校验 ─────────────────────────────────────────────────────────

/** 校验结果:ok 时带可提交请求体,否则带中文错误说明。 */
export type ProviderValidation =
  | { ok: true; value: ProviderCatalogMutationRequest }
  | { ok: false; error: string }

/**
 * 校验 provider 新建表单(镜像 validateProviderCatalogCreateRequest,
 * provider_catalog_mutation_handler.go:328):
 *   - code trim 后非空
 *   - display_name trim 后非空
 *   - upstream_protocol 必须在白名单内
 * 判别核心:任一为空 / 协议非法即拒。reason 可选(空串则不下发)。
 */
export function validateProviderCreate(form: {
  code: string
  displayName: string
  upstreamProtocol: string
  enabled: boolean
  reason: string
}): ProviderValidation {
  const code = form.code.trim()
  if (code === '') return { ok: false, error: 'provider code 不能为空' }
  const displayName = form.displayName.trim()
  if (displayName === '') return { ok: false, error: '展示名(display_name)不能为空' }
  const protocol = form.upstreamProtocol.trim()
  if (!isKnownProtocol(protocol)) return { ok: false, error: '上游协议不在受支持范围内' }
  const reason = form.reason.trim()
  return {
    ok: true,
    value: {
      code,
      display_name: displayName,
      upstream_protocol: protocol,
      enabled: form.enabled,
      ...(reason !== '' ? { reason } : {}),
    },
  }
}

/**
 * 校验 provider 更新表单(镜像 validateProviderCatalogUpdateRequest,
 * provider_catalog_mutation_handler.go:354):code 来自 URL path(不在 body 校验),
 * 只校验 display_name 非空 + 协议白名单。判别核心同上(去掉 code 非空)。
 */
export function validateProviderUpdate(form: {
  displayName: string
  upstreamProtocol: string
  enabled: boolean
  reason: string
}): ProviderValidation {
  const displayName = form.displayName.trim()
  if (displayName === '') return { ok: false, error: '展示名(display_name)不能为空' }
  const protocol = form.upstreamProtocol.trim()
  if (!isKnownProtocol(protocol)) return { ok: false, error: '上游协议不在受支持范围内' }
  const reason = form.reason.trim()
  return {
    ok: true,
    value: {
      display_name: displayName,
      upstream_protocol: protocol,
      enabled: form.enabled,
      ...(reason !== '' ? { reason } : {}),
    },
  }
}

export interface ProviderCatalogTableRow {
  id: number
  code: string
  displayName: string
  upstreamProtocol: string
  status: string
  statusTone: BadgeTone
  createdAt: string
  provider: ProviderCatalogItem
}

/** provider 目录 DTO 到列表展示行的纯映射。 */
export function mapProviderCatalogRows(items: ProviderCatalogItem[]): ProviderCatalogTableRow[] {
  return items.map((item) => ({
    id: item.id,
    code: item.code,
    displayName: item.display_name,
    upstreamProtocol: item.upstream_protocol,
    status: item.enabled ? '启用' : '停用',
    statusTone: item.enabled ? 'ok' : 'muted',
    createdAt: formatCatalogTimestamp(item.created_at),
    provider: item,
  }))
}

export interface ChannelCatalogTableRow {
  id: number
  displayId: string
  name: string
  poolGroupId: number
  status: string
  statusTone: BadgeTone
  createdAt: string
  channel: ChannelCatalogItem
}

/** channel 目录 DTO 到列表展示行的纯映射。 */
export function mapChannelCatalogRows(items: ChannelCatalogItem[]): ChannelCatalogTableRow[] {
  return items.map((item) => ({
    id: item.id,
    displayId: `#${item.id}`,
    name: item.name,
    poolGroupId: item.pool_group_id,
    status: item.enabled ? '启用' : '停用',
    statusTone: item.enabled ? 'ok' : 'muted',
    createdAt: formatCatalogTimestamp(item.created_at),
    channel: item,
  }))
}

export function formatCatalogTimestamp(iso?: string): string {
  if (!iso) return '—'
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString('zh-CN', { hour12: false })
}

// ── channel 表单校验 ──────────────────────────────────────────────────────────

export type ChannelValidation =
  | { ok: true; value: ChannelCatalogMutationRequest }
  | { ok: false; error: string }

export interface ChannelFormValues {
  name: string
  poolGroupId: number
  enabled: boolean
  reason: string
  bodyParamStrips: string
  paramOverride: string
  sensitiveWords: string
}

/** 把列表行回显转换为可编辑文本，旧响应缺字段时按三门关闭处理。 */
export function channelFormFromItem(item: ChannelCatalogItem): Omit<ChannelFormValues, 'reason'> {
  return {
    name: item.name,
    poolGroupId: item.pool_group_id,
    enabled: item.enabled,
    bodyParamStrips: (item.body_param_strips ?? []).join(', '),
    paramOverride: JSON.stringify(item.param_override ?? {}, null, 2),
    sensitiveWords: (item.sensitive_words ?? []).join(', '),
  }
}

/**
 * 校验 channel 新建/更新表单，与后端两条写接口的输入约束保持一致：
 *   - name trim 后非空
 *   - pool_group_id 必须为正整数(后端 *PoolGroupID<=0 即 400)
 *   - 两个字符串数组按逗号拆分并去除首尾空白，拒绝空项
 *   - param_override 必须是合法 JSON object，不能是 array/scalar/null
 * 判别核心:name 空 / pool_group_id 非正 / JSON 类型错误即拒。reason 可选。
 * failover_status_codes 仅为旧客户端兼容保留在类型中,当前界面不下发。
 */
export function validateChannel(form: ChannelFormValues): ChannelValidation {
  const name = form.name.trim()
  if (name === '') return { ok: false, error: 'channel 名称不能为空' }
  if (!Number.isInteger(form.poolGroupId) || form.poolGroupId <= 0) {
    return { ok: false, error: 'pool_group_id 必须是正整数' }
  }
  const bodyParamStrips = parseChannelStringList(form.bodyParamStrips, 'body_param_strips')
  if (!bodyParamStrips.ok) return bodyParamStrips
  const sensitiveWords = parseChannelStringList(form.sensitiveWords, 'sensitive_words')
  if (!sensitiveWords.ok) return sensitiveWords
  let paramOverride: unknown
  try {
    paramOverride = JSON.parse(form.paramOverride.trim() === '' ? '{}' : form.paramOverride)
  } catch {
    return { ok: false, error: 'param_override 必须是合法 JSON object' }
  }
  if (paramOverride === null || Array.isArray(paramOverride) || typeof paramOverride !== 'object') {
    return { ok: false, error: 'param_override 必须是 JSON object，不能是 array、scalar 或 null' }
  }
  const reason = form.reason.trim()
  return {
    ok: true,
    value: {
      pool_group_id: form.poolGroupId,
      name,
      enabled: form.enabled,
      body_param_strips: bodyParamStrips.value,
      param_override: paramOverride as Record<string, unknown>,
      sensitive_words: sensitiveWords.value,
      ...(reason !== '' ? { reason } : {}),
    },
  }
}

/** 仅在校验成功时调用写 API，便于测试非法 JSON 不会越过前端边界。 */
export function validateAndDispatchChannel(
  form: ChannelFormValues,
  onValid: (value: ChannelCatalogMutationRequest) => void,
): ChannelValidation {
  const result = validateChannel(form)
  if (result.ok) onValid(result.value)
  return result
}

function parseChannelStringList(
  raw: string,
  field: 'body_param_strips' | 'sensitive_words',
): { ok: true; value: string[] } | { ok: false; error: string } {
  if (raw.trim() === '') return { ok: true, value: [] }
  if (/[\r\n]/.test(raw)) {
    return { ok: false, error: `${field} 请只用逗号分隔` }
  }
  const values = raw.split(',').map((value) => value.trim())
  if (values.some((value) => value === '')) {
    return { ok: false, error: `${field} 不能包含空项` }
  }
  return { ok: true, value: values }
}
