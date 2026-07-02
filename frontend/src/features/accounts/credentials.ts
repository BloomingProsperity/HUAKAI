import type { BadgeTone } from '../../ui/StatusBadge'

/*
 * 账号凭证子系统纯逻辑 + 类型(可单测,无 DOM/网络副作用):
 *   - 凭证 metadata / 请求体类型(镜像后端 JSON 形态,结构上不含任何 secret 字段)
 *   - 凭证状态 → 徽章语气 + 中文标签
 *   - vendor / auth_mode 下拉选项(镜像后端 credentialstore/types.go 常量)
 *   - secret 文本域(只写)的 JSON 校验 + 新增/轮换请求体构造
 *   - state 切换的合法值校验(镜像后端 credentialstore state 常量)
 *
 * SECRET-MASK 铁律(§4):
 *   secret 材料只在「输入框 → 请求体 credentials 字段」这一条路径上出现。
 *   本文件的 buildCreateBody/buildRotateBody 把用户输入的 JSON 文本解析后塞进
 *   credentials 字段供 POST;此后调用方必须立即清空输入框。本文件绝不持久化、
 *   绝不回显 secret,任何返回值都不携带原始 secret(只回 metadata / 校验结论)。
 *   CredentialMetadata 类型刻意不声明任何 secret 字段——后端响应结构上也不返回 secret。
 *
 * 后端真形态见 backend/internal/credentialstore/types.go(常量)+
 * backend/internal/credentialstore/postgres_store.go:116(CredentialMetadata)+
 * backend/internal/gatewayhttp/admin_credentials_handler.go(请求/响应)。
 */

/**
 * 凭证 metadata DTO —— 严格镜像后端 credentialstore.CredentialMetadata
 * (postgres_store.go:116,JSON tag 一一对应)。
 *
 * SECRET-MASK:此结构里没有任何 secret 字段(api_key/access_token/client_secret/payload 等
 * 一律不存在)。后端 list/create/rotate/status 返回的就是这份 metadata,前端照此建模,
 * 绝不凭空加 secret 字段。所有字段都是可安全展示的运维元数据。
 */
export interface CredentialMetadata {
  id: number
  tenant_id: number
  provider_account_id: number
  vendor: string
  auth_mode: string
  state: string
  /** 凭证版本(轮换递增),JSON tag credential_version。 */
  credential_version: number
  access_expires_at?: string | null
  refresh_before_at?: string | null
  last_refresh_at?: string | null
  last_refresh_outcome?: string | null
  failure_class?: string | null
  failure_count: number
  external_account_id?: string | null
  external_account_email?: string | null
  created_at: string
  updated_at: string
}

/** GET /credentials 响应:{credentials:[CredentialMetadata]}(handler:145)。 */
export interface CredentialListResponse {
  credentials: CredentialMetadata[]
}

/** 凭证状态合法值联合类型(镜像 credentialstore State* 常量)。 */
export type CredentialStateValue =
  | 'active'
  | 'refreshing'
  | 'refreshing_with_grace'
  | 'expired'
  | 'temp_unschedulable'
  | 'needs_rotation'
  | 'revoked'
  | 'operator_attention'

/**
 * 新增/轮换请求体(镜像 credentialWriteRequest,admin_credentials_handler.go:41)。
 * credentials 字段=原始 secret 材料(后端 json.RawMessage)。这是 secret 的唯一出口字段,
 * 只在 POST body 中出现,绝不回显。vendor/auth_mode/external_* 在轮换时可省略。
 */
export interface CredentialWriteBody {
  tenant_id: number
  vendor?: string
  auth_mode?: string
  /** secret 材料(JSON 对象)。SECRET-MASK:只写不回显。 */
  credentials: Record<string, unknown>
  external_account_id?: string
  external_account_email?: string
  reason?: string
}

/** vendor 选项(镜像 credentialstore/types.go 的 Vendor* 常量段,含 2026-07-02 官 key 厂商)。 */
export const VENDOR_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'copilot', label: 'GitHub Copilot' },
  { value: 'antigravity', label: 'Antigravity' },
  { value: 'windsurf', label: 'Windsurf' },
  { value: 'cursor', label: 'Cursor' },
  { value: 'openrouter', label: 'OpenRouter' },
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'grok', label: 'Grok' },
  { value: 'kimi', label: 'Kimi' },
  { value: 'mistral', label: 'Mistral' },
  { value: 'groqcloud', label: 'GroqCloud' },
  { value: 'together', label: 'Together' },
  { value: 'perplexity', label: 'Perplexity' },
  { value: 'fireworks', label: 'Fireworks' },
  // 国内大厂官 key(2026-07-02 接入,迁移 0169 放行存储;镜像 credentialstore Vendor* 新增段)。
  { value: 'qwen', label: '通义千问 Qwen' },
  { value: 'glm', label: '智谱 GLM' },
  { value: 'yi', label: '零一万物 Yi' },
  { value: 'baichuan', label: '百川' },
  { value: 'doubao', label: '豆包 Doubao' },
  { value: 'minimax', label: 'MiniMax' },
  { value: 'ernie', label: '文心 ERNIE' },
  { value: 'hunyuan', label: '腾讯混元' },
  { value: 'step', label: '阶跃 Step' },
]

/** auth_mode 选项(镜像 credentialstore/types.go 的 AuthMode* 常量段)。 */
export const AUTH_MODE_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'api_key', label: 'API Key' },
  { value: 'claude_ai_oauth', label: 'Claude AI OAuth' },
  { value: 'claude_code', label: 'Claude Code' },
  { value: 'bedrock', label: 'AWS Bedrock' },
  { value: 'vertex_anthropic', label: 'Vertex Anthropic' },
  { value: 'chatgpt_oauth', label: 'ChatGPT OAuth' },
  { value: 'codex_cli_oauth', label: 'Codex CLI OAuth' },
  { value: 'codex_web_oauth', label: 'Codex Web OAuth' },
  { value: 'azure', label: 'Azure OpenAI' },
  { value: 'refresh_token', label: 'Refresh Token' },
  { value: 'aistudio_api_key', label: 'AI Studio API Key' },
  { value: 'vertex_sa', label: 'Vertex Service Account' },
  { value: 'code_assist', label: 'Code Assist' },
  { value: 'google_one', label: 'Google One' },
  { value: 'antigravity', label: 'Antigravity' },
  { value: 'copilot_oauth', label: 'Copilot OAuth' },
  { value: 'xai_oauth', label: 'xAI OAuth' },
  { value: 'oauth', label: 'OAuth' },
  { value: 'kimi_oauth', label: 'Kimi OAuth' },
]

/**
 * 凭证状态合法值(镜像 credentialstore/types.go:51-58 的 State* 常量)。
 * 后端 SetState 会经 Normalize(ToLower+Trim);前端只允许从这组里选,避免发未知态。
 */
export const CREDENTIAL_STATES: ReadonlyArray<CredentialStateValue> = [
  'active',
  'refreshing',
  'refreshing_with_grace',
  'expired',
  'temp_unschedulable',
  'needs_rotation',
  'revoked',
  'operator_attention',
]

const STATE_LABELS: Record<string, string> = {
  active: '活跃',
  refreshing: '刷新中',
  refreshing_with_grace: '宽限刷新中',
  expired: '已过期',
  temp_unschedulable: '临时停调',
  needs_rotation: '待轮换',
  revoked: '已吊销',
  operator_attention: '需人工介入',
}

/** 凭证状态 → 中文标签;未知态回退原值。 */
export function credentialStateLabel(state: string): string {
  return STATE_LABELS[state] ?? (state || '—')
}

/**
 * 凭证状态 → 徽章语气。
 * active=正常(ok);refreshing/宽限刷新=进行中(info);needs_rotation/temp=warn;
 * expired/revoked/operator_attention=需关注(danger);未知=中性。
 */
export function credentialStateTone(state: string): BadgeTone {
  switch (state) {
    case 'active':
      return 'ok'
    case 'refreshing':
    case 'refreshing_with_grace':
      return 'info'
    case 'needs_rotation':
    case 'temp_unschedulable':
      return 'warn'
    case 'expired':
    case 'revoked':
    case 'operator_attention':
      return 'danger'
    default:
      return 'muted'
  }
}

/** state 是否为后端已知合法值。判别核心:未知态一律拒,避免发出无效 PATCH。 */
export function isValidCredentialState(state: string): state is CredentialStateValue {
  return (CREDENTIAL_STATES as ReadonlyArray<string>).includes(state)
}

/** vendor / auth_mode 输入是否非空(后端 Normalize 后空串即 unknown_credential_mode)。 */
export function validateVendorAuthMode(vendor: string, authMode: string): string | null {
  if (vendor.trim() === '') return '请选择 vendor(厂商)'
  if (authMode.trim() === '') return '请选择 auth_mode(鉴权方式)'
  return null
}

/**
 * 校验 secret 文本域内容:必须是非空的 JSON 对象(后端 ValidatePayload 先 json.Unmarshal
 * 成 object,再按 vendor/auth_mode 查必填字段;非对象/空串/数组都会被拒)。
 * 判别核心:空串、非法 JSON、JSON 但非对象(数组/标量)三类都必须拒,
 * 返回错误文案或 null(合法)。绝不把解析出的内容回写或打印。
 */
export function validateSecretJSON(text: string): string | null {
  const trimmed = text.trim()
  if (trimmed === '') return 'secret 不能为空(需 JSON 对象,如 {"api_key":"..."})'
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return 'secret 必须是合法 JSON'
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return 'secret 必须是 JSON 对象(键值对),不能是数组或标量'
  }
  if (Object.keys(parsed as Record<string, unknown>).length === 0) {
    return 'secret JSON 对象不能为空'
  }
  return null
}

/** 新增/轮换请求体构造结果:ok 时携带可提交的 body,否则带中文错误。 */
export type WriteBodyResult =
  | { ok: true; value: CredentialWriteBody }
  | { ok: false; error: string }

/**
 * 构造「新增凭证」请求体(POST /credentials)。
 * 镜像后端 credentialWriteRequest(admin_credentials_handler.go:41):
 *   {tenant_id, vendor, auth_mode, credentials(原始 secret 材料), external_account_id?,
 *    external_account_email?, reason?}。
 * tenant_id 必须为正(后端无 query tenant 时从 body 取,Create 会校验)。
 * external_account / reason 为空则省略。
 *
 * SECRET-MASK:credentials 字段直接放用户输入的 JSON 文本(后端按 json.RawMessage 透传),
 * 这是 secret 的唯一出口。返回的 body 仅用于一次性 POST,调用方提交后必须清空输入。
 */
export function buildCreateBody(input: {
  tenantId: number
  vendor: string
  authMode: string
  secretJSON: string
  externalAccountId?: string
  externalAccountEmail?: string
  reason?: string
}): WriteBodyResult {
  if (!Number.isInteger(input.tenantId) || input.tenantId <= 0) {
    return { ok: false, error: 'tenant_id 必须为正整数' }
  }
  const modeErr = validateVendorAuthMode(input.vendor, input.authMode)
  if (modeErr) return { ok: false, error: modeErr }
  const secretErr = validateSecretJSON(input.secretJSON)
  if (secretErr) return { ok: false, error: secretErr }
  const body: CredentialWriteBody = {
    tenant_id: input.tenantId,
    vendor: input.vendor.trim(),
    auth_mode: input.authMode.trim(),
    // 后端字段为 json.RawMessage;前端把已校验的 JSON 文本原样作为对象塞入,
    // 由 lib/api 统一 JSON.stringify。这里 parse 一次以保证发出的是嵌套对象而非字符串。
    credentials: JSON.parse(input.secretJSON.trim()) as Record<string, unknown>,
  }
  const extId = input.externalAccountId?.trim()
  if (extId) body.external_account_id = extId
  const extEmail = input.externalAccountEmail?.trim()
  if (extEmail) body.external_account_email = extEmail
  const reason = input.reason?.trim()
  if (reason) body.reason = reason
  return { ok: true, value: body }
}

/**
 * 构造「轮换凭证」请求体(POST /credentials/{id}/rotate)。
 * 同 credentialWriteRequest,但只用 credentials(新 secret)+ tenant_id + reason;
 * vendor/auth_mode 由后端按既有凭证沿用(Rotate 不读 body 的 vendor/auth_mode),
 * 但为对齐 body 结构仍可带上。这里只发 tenant_id + credentials + reason 三个必要字段。
 *
 * SECRET-MASK:同 buildCreateBody,credentials 是新 secret 的唯一出口,提交后清空输入。
 */
export function buildRotateBody(input: {
  tenantId: number
  secretJSON: string
  reason?: string
}): WriteBodyResult {
  if (!Number.isInteger(input.tenantId) || input.tenantId <= 0) {
    return { ok: false, error: 'tenant_id 必须为正整数' }
  }
  const secretErr = validateSecretJSON(input.secretJSON)
  if (secretErr) return { ok: false, error: secretErr }
  const body: CredentialWriteBody = {
    tenant_id: input.tenantId,
    credentials: JSON.parse(input.secretJSON.trim()) as Record<string, unknown>,
  }
  const reason = input.reason?.trim()
  if (reason) body.reason = reason
  return { ok: true, value: body }
}

/** 状态切换请求体校验结果。 */
export type StateBodyResult =
  | { ok: true; value: { tenant_id: number; state: CredentialStateValue; reason?: string } }
  | { ok: false; error: string }

/**
 * 构造「置状态」请求体(PATCH /credentials/{id}/state)。
 * 镜像 credentialStateRequest(admin_credentials_handler.go:54):{tenant_id, state, reason?}。
 * 判别核心:tenant_id 须为正(后端 <=0 即 tenant_id_required 400);
 * state 须是已知合法值(避免发未知态被后端拒)。
 */
export function buildStateBody(input: {
  tenantId: number
  state: string
  reason?: string
}): StateBodyResult {
  if (!Number.isInteger(input.tenantId) || input.tenantId <= 0) {
    return { ok: false, error: 'tenant_id 必须为正整数' }
  }
  if (!isValidCredentialState(input.state)) {
    return { ok: false, error: '未知的凭证状态' }
  }
  const body: { tenant_id: number; state: CredentialStateValue; reason?: string } = {
    tenant_id: input.tenantId,
    state: input.state,
  }
  const reason = input.reason?.trim()
  if (reason) body.reason = reason
  return { ok: true, value: body }
}

/**
 * 上游账号身份展示:优先 email,其次 id,都没有则破折号。
 * 这是 metadata(非 secret),可安全展示。
 */
export function externalAccountLabel(meta: CredentialMetadata): string {
  if (meta.external_account_email) return meta.external_account_email
  if (meta.external_account_id) return meta.external_account_id
  return '—'
}

/** ISO 时间串 → 本地化展示;空/无效则破折号。 */
export function fmtTime(raw?: string | null): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  return d.toLocaleString()
}
