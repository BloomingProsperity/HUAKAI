/*
 * 模型注册纯逻辑(可单测,零副作用、零 console):
 *  - 别名批量导入逐行解析 + 客户端预校验(镜像后端 normalizeModelAliasImport 的同一不变量,
 *    本地先挡掉明显错误,减少无谓往返;后端仍是权威)。
 *  - 能力绑定白名单(镜像 registry.knownModelCapabilityBindings 的子集分组)。
 *  - 能力矩阵 toggle → capabilities map 构造。
 *  - 导入结果汇总(成功/失败计数)。
 */
import type { BadgeTone } from '../../ui/StatusBadge'
import type { AdminModel, AliasImportResult, AliasImportRow, CapabilityBinding } from './types'

// 能力绑定白名单 —— 镜像后端 knownModelCapabilityBindings(PUT capability-bindings 只接受表内能力)。
// 分组仅用于 UI 下拉归类,后端不区分组。任一组外能力提交会被后端 400(invalid_capability)。
export const CAPABILITY_GROUPS: ReadonlyArray<{ label: string; items: readonly string[] }> = [
  { label: '公开发现描述符', items: ['vision', 'function_calling', 'tool_choice', 'reasoning', 'prompt_caching', 'response_schema'] },
  { label: 'HCSF 能力族', items: ['text', 'tool_use', 'tool_result', 'thinking', 'cache_control', 'structured_output', 'computer_use', 'file', 'image', 'audio', 'video', 'live_session', 'batch', 'mcp_server', 'data_retention'] },
  { label: '协议能力矩阵', items: ['text_streaming', 'parallel_tool_calls', 'structured_output_schema', 'image_input', 'audio_input', 'image_output', 'max_tokens_finish_reason', 'max_completion_tokens', 'stop_sequence_emit', 'cache_breakpoints', 'signature_delta', 'system_prompt_array', 'multi_role_messages', 'reasoning_summary'] },
  { label: '注册/同步词表', items: ['stream', 'tools', 'chat', 'messages', 'responses', 'embeddings', 'rerank', 'images', 'audio_speech', 'audio_transcription', 'generateContent', 'countTokens', 'embedContent', 'batchEmbedContents'] },
]

/** 扁平化白名单全集(用于校验某能力名是否在白名单内)。 */
export const KNOWN_CAPABILITIES: ReadonlySet<string> = new Set(CAPABILITY_GROUPS.flatMap((g) => g.items))

export function isKnownCapability(name: string): boolean {
  return KNOWN_CAPABILITIES.has(name.trim())
}

export type ParsedAlias = { ok: true; row: AliasImportRow } | { ok: false; line: number; raw: string; error: string }

/**
 * 解析别名导入文本(逐行,格式:model_id,alias[,scope[,tenant_id[,display]]])。
 * 空行/纯空白行跳过;# 起首为注释行跳过。每行独立校验,返回逐行结果(失败不中断,便于 UI 逐行标红)。
 *
 * 客户端预校验不变量(与后端 normalizeModelAliasImport 对齐):
 *  - model_id 必须为正整数。
 *  - alias 非空(trim 后)。
 *  - scope 缺省 'tenant';只接受 'tenant' | 'global'。
 *  - scope='tenant' 时 tenant_id 必须为正。
 *  - scope='global' 时忽略 tenant_id。
 */
export function parseAliasLines(text: string): ParsedAlias[] {
  const out: ParsedAlias[] = []
  const lines = text.split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i]
    const trimmed = raw.trim()
    if (trimmed === '' || trimmed.startsWith('#')) continue
    out.push(parseOneAliasLine(trimmed, i + 1, raw))
  }
  return out
}

function parseOneAliasLine(trimmed: string, line: number, raw: string): ParsedAlias {
  const fail = (error: string): ParsedAlias => ({ ok: false, line, raw, error })
  const cols = trimmed.split(',').map((c) => c.trim())
  const modelIdStr = cols[0] ?? ''
  const alias = cols[1] ?? ''
  const scopeRaw = cols[2] ?? ''
  const tenantStr = cols[3] ?? ''
  const display = cols[4] ?? ''

  const modelId = Number(modelIdStr)
  if (!Number.isInteger(modelId) || modelId <= 0) return fail('model_id 必须为正整数')
  if (alias === '') return fail('alias 不能为空')

  const scope = scopeRaw === '' ? 'tenant' : scopeRaw
  if (scope !== 'tenant' && scope !== 'global') return fail("scope 只能是 'tenant' 或 'global'")

  const row: AliasImportRow = { model_id: modelId, alias, scope }
  if (scope === 'tenant') {
    const tenantId = Number(tenantStr)
    if (!Number.isInteger(tenantId) || tenantId <= 0) return fail('scope=tenant 时 tenant_id 必须为正整数')
    row.tenant_id = tenantId
  }
  if (display !== '') row.display = display
  return { ok: true, row }
}

/** 把逐行解析结果拆成「可提交的行」与「本地校验失败的行」。 */
export function splitParsedAliases(parsed: ParsedAlias[]): { rows: AliasImportRow[]; invalid: Extract<ParsedAlias, { ok: false }>[] } {
  const rows: AliasImportRow[] = []
  const invalid: Extract<ParsedAlias, { ok: false }>[] = []
  for (const p of parsed) {
    if (p.ok) rows.push(p.row)
    else invalid.push(p)
  }
  return { rows, invalid }
}

/** 汇总后端逐行导入结果:upserted 计成功,其余(含 failed)计失败。 */
export function summarizeImportResults(results: AliasImportResult[]): { upserted: number; failed: number } {
  let upserted = 0
  let failed = 0
  for (const r of results) {
    if (r.status === 'upserted') upserted++
    else failed++
  }
  return { upserted, failed }
}

/**
 * 把能力矩阵(能力名→开关)构造成 capabilities map。仅保留 trim 后非空的 key
 * (后端 parseCapabilitiesBody 会 400 空 key);值原样下发(true/false 都是有效断言)。
 */
export function buildCapabilitiesMap(toggles: Record<string, boolean>): Record<string, boolean> {
  const out: Record<string, boolean> = {}
  for (const [k, v] of Object.entries(toggles)) {
    const key = k.trim()
    if (key === '') continue
    out[key] = v
  }
  return out
}

/** 导入结果行配色档(供 StatusBadge):upserted→ok,failed→danger,其余→muted。 */
export function importResultTone(status: string): 'ok' | 'danger' | 'muted' {
  if (status === 'upserted') return 'ok'
  if (status === 'failed') return 'danger'
  return 'muted'
}

export interface CapabilityBindingTableRow {
  key: string
  capability: string
  scope: string
  tenant: number | string
  value: string
  enabled: string
  enabledTone: BadgeTone
  source: string
}

/** 能力绑定 DTO 到列表展示行的纯映射。 */
export function mapCapabilityBindingRows(bindings: CapabilityBinding[]): CapabilityBindingTableRow[] {
  return bindings.map((binding, index) => ({
    key: `${binding.scope}-${binding.capability}-${index}`,
    capability: binding.capability,
    scope: binding.scope,
    tenant: binding.tenant_id ?? '—',
    value: binding.capability_value ?? '—',
    enabled: binding.enabled ? '启用' : '停用',
    enabledTone: binding.enabled ? 'ok' : 'muted',
    source: binding.source,
  }))
}

export interface AliasValidationTableRow {
  line: number
  raw: string
  error: string
}

/** 本地校验失败项到列表展示行的纯映射。 */
export function mapAliasValidationRows(rows: AliasValidationTableRow[]): AliasValidationTableRow[] {
  return rows.map((row) => ({ line: row.line, raw: row.raw, error: row.error }))
}

export interface AliasResultTableRow {
  index: number
  alias: string
  modelId: number | string
  status: string
  statusTone: BadgeTone
  error: string
  hasError: boolean
}

/** 后端逐行导入结果到列表展示行的纯映射。 */
export function mapAliasResultRows(results: AliasImportResult[]): AliasResultTableRow[] {
  return results.map((result) => ({
    index: result.index,
    alias: result.alias,
    modelId: result.model_id ?? '—',
    status: result.status,
    statusTone: importResultTone(result.status),
    error: result.error ?? '—',
    hasError: Boolean(result.error),
  }))
}

export interface AdminModelTableRow {
  id: number
  canonicalId: string
  providerModelId: string
  protocolFamily: string
  scope: string
  tenant: number | string
  contextWindow: number
  status: string
  statusTone: BadgeTone
}

/** 模型主体 DTO 到列表展示行的纯映射，数字 id 原样保留供后续运维卡回填。 */
export function mapAdminModelRows(models: AdminModel[]): AdminModelTableRow[] {
  return models.map((model) => ({
    id: model.id,
    canonicalId: model.canonical_id,
    providerModelId: model.default_provider_model_id,
    protocolFamily: model.protocol_family,
    scope: model.scope,
    tenant: model.tenant_id ?? '全局',
    contextWindow: model.default_context_window,
    status: model.status === 'active' ? '启用' : '停用',
    statusTone: model.status === 'active' ? 'ok' : 'muted',
  }))
}
