import type { BadgeTone } from '../../ui/StatusBadge'

/*
 * 凭证获取/导入向导的纯逻辑层(可单测,无 DOM/网络副作用)。
 *
 * 形态全部对照后端真 handler:
 *   - admin_credential_acquisition_handler.go(账号级流 + 导入 helper 的请求/响应字段)
 *   - credentialacq/types.go(FlowKind / FlowStatus / Session JSON 形态)
 *
 * 最高优先级 SECRET-MASK:本文件只产出「请求体构造」和「流状态判定」,
 * 绝不接触/持久化任何 secret 材料。secret(API key / OAuth token / client_secret /
 * 导入原文 content)只在组件输入框 → POST body 这一条路径上流动,提交后即清空,
 * 响应只含 metadata / flow 状态,这里建模的所有响应类型里没有任何 secret 字段。
 */

// ── FlowKind / 向导方式 ───────────────────────────────────────────────────────

/** 后端 credentialacq.FlowKind 取值(types.go:13-23)。前端只用到下列子集。 */
export type FlowKind =
  | 'oauth'
  | 'cli_import'
  | 'paste'
  | 'csv_import'
  | 'json_import'
  | 'cloud_bootstrap'
  | 'token_exchange'
  | 'setup_token'
  | 'manual_first'

/** 后端 credentialacq.FlowStatus 取值(types.go:28-35)。 */
export type FlowStatus =
  | 'started'
  | 'waiting_for_user'
  | 'callback_received'
  | 'validated'
  | 'finalized'
  | 'cancelled'
  | 'expired'
  | 'failed'

/**
 * 向导支持的获取方式。OAuth 走「账号级 start→展示授权 URL→callback→finalize」;
 * 其余四种走 /admin/v1/credentials/{helper} 导入 helper(content 文本域只写)。
 */
export type WizardMethod = 'oauth' | 'paste' | 'cli_import' | 'csv_import' | 'json_import'

/** 各方式对应的导入 helper 子路径(仅非 oauth)。 */
const HELPER_PATH: Record<Exclude<WizardMethod, 'oauth'>, string> = {
  paste: 'paste',
  cli_import: 'cli-import',
  csv_import: 'csv-import',
  json_import: 'json-import',
}

/** 把向导方式映射到导入 helper 子路径;oauth 无 helper,返回 null。 */
export function helperPathForMethod(method: WizardMethod): string | null {
  if (method === 'oauth') return null
  return HELPER_PATH[method]
}

/** 方式中文标签(展示用)。 */
export function methodLabel(method: WizardMethod): string {
  switch (method) {
    case 'oauth':
      return 'OAuth 授权'
    case 'paste':
      return '粘贴凭据(JSON)'
    case 'cli_import':
      return 'CLI 凭据导入'
    case 'csv_import':
      return 'CSV 批量导入'
    case 'json_import':
      return 'JSON 批量导入'
  }
}

// ── 后端响应建模(只含 metadata / flow 状态,绝无 secret)────────────────────────

/**
 * 流状态(Session 的 JSON 投影,credentialacq/types.go:177-206)。
 * StateHash/NonceHash/PKCE/IdempotencyKeyHash/DeviceCodePayload 在后端打了 `json:"-"`,
 * 永不出现在 wire 上;此处也不建模它们。redacted_context 后端已脱敏,仅元数据。
 */
export interface AcquisitionFlow {
  id: string
  tenant_id: number
  provider_account_id: number
  vendor: string
  auth_mode: string
  flow_kind: FlowKind
  status: FlowStatus
  client_identity_source?: string
  auth_type?: string
  redirect_uri?: string
  requested_scopes?: string[]
  redacted_context?: Record<string, unknown>
  long_lived_requested?: boolean
  result_account_credential_id?: number
  error_class?: string
  error_message_redacted?: string
  expires_at?: string
  consumed_at?: string
  cancelled_at?: string
  created_at?: string
  updated_at?: string
}

/**
 * start / oauth-init 的响应:flow + 可选的 OAuth 授权信息。
 * authorize_url/state/code_challenge 仅在 OAuth start 时出现(handler.go:346-350)。
 * 其中 state 是 OAuth 防重放参数,不是 secret(用户授权回来要原样带回),
 * code_challenge 是 PKCE 公开摘要(非 verifier)。两者都可安全展示。
 */
export interface StartFlowResponse {
  flow: AcquisitionFlow
  authorize_url?: string
  state?: string
  code_challenge?: string
}

/** 凭证 metadata(credentialstore.CredentialMetadata,postgres_store.go:116),无 secret。 */
export interface CredentialMetadata {
  id: number
  tenant_id: number
  provider_account_id: number
  vendor: string
  auth_mode: string
  state: string
  credential_version: number
  access_expires_at?: string
  refresh_before_at?: string
  last_refresh_at?: string
  last_refresh_outcome?: string
  failure_class?: string
  failure_count: number
  external_account_id?: string
  external_account_email?: string
  created_at?: string
  updated_at?: string
}

/** finalize / callback 的响应(FinalizeResult,types.go:208-212)。 */
export interface FinalizeResponse {
  flow: AcquisitionFlow
  credential: CredentialMetadata
  already_finalized?: boolean
}

/** 导入 helper 的响应(handler.go:275:{flows, finalized})。 */
export interface ImportHelperResponse {
  flows: AcquisitionFlow[]
  finalized?: FinalizeResponse[]
}

// ── 请求体构造(纯函数,便于变异测试)─────────────────────────────────────────

/** OAuth 客户端配置输入(可选;client_secret 只写,绝不回显)。 */
export interface OAuthClientInput {
  clientId: string
  clientSecret: string
  authUrl: string
  tokenUrl: string
  redirectUri: string
  scopes: string
  source: string
}

/** 向导用户在表单里填的字段(secret 材料在 content / clientSecret 里)。 */
export interface WizardForm {
  vendor: string
  authMode: string
  redirectUri: string
  requestedScopes: string
  reason: string
  /** 导入方式的原文(secret 材料,只写)。 */
  content: string
  /** OAuth 自定义 client(可选)。 */
  oauthClient: OAuthClientInput
  /** 是否填了自定义 OAuth client(否则后端走默认 public client)。 */
  useCustomOAuthClient: boolean
}

export const EMPTY_OAUTH_CLIENT: OAuthClientInput = {
  clientId: '',
  clientSecret: '',
  authUrl: '',
  tokenUrl: '',
  redirectUri: '',
  scopes: '',
  source: '',
}

export const EMPTY_WIZARD_FORM: WizardForm = {
  vendor: '',
  authMode: '',
  redirectUri: '',
  requestedScopes: '',
  reason: '',
  content: '',
  oauthClient: { ...EMPTY_OAUTH_CLIENT },
  useCustomOAuthClient: false,
}

/** 把逗号/空白分隔的 scope 串切成数组(空则省略)。 */
export function parseScopes(raw: string): string[] {
  return raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

/**
 * 构造账号级 OAuth start 请求体(POST /{id}/credential-acquisitions)。
 * tenant_id 必带且为正(后端 createOrStartCredentialAcqSession 校验 tenant_id<=0 报错);
 * provider_account_id 由后端从 path 注入,这里也带上以镜像 body 形态。
 * 关键判别:oauth_client 仅在 useCustomOAuthClient 时下发,且其中 client_secret 只写。
 */
export function buildOAuthStartBody(
  tenantId: number,
  providerAccountId: number,
  form: WizardForm,
): Record<string, unknown> {
  const body: Record<string, unknown> = {
    tenant_id: tenantId,
    provider_account_id: providerAccountId,
    vendor: form.vendor.trim(),
    auth_mode: form.authMode.trim(),
    flow_kind: 'oauth',
  }
  const redirect = form.redirectUri.trim()
  if (redirect) body.redirect_uri = redirect
  const scopes = parseScopes(form.requestedScopes)
  if (scopes.length > 0) body.requested_scopes = scopes
  const reason = form.reason.trim()
  if (reason) body.reason = reason
  if (form.useCustomOAuthClient) {
    body.oauth_client = buildOAuthClientPayload(form.oauthClient)
  }
  return body
}

/**
 * 构造 oauth_client 子对象。仅下发非空字段;client_secret 原样进 body(只写,
 * 后端对 Gemini / ChatGPT/Codex 等 PKCE 模式会忽略它,见 handler.go:374-383)。
 */
export function buildOAuthClientPayload(client: OAuthClientInput): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (client.clientId.trim()) out.client_id = client.clientId.trim()
  // client_secret 去首尾空白(防误粘贴的空白),保留内部内容;空则不下发。只写,绝不回显。
  const secret = client.clientSecret.trim()
  if (secret) out.client_secret = secret
  if (client.authUrl.trim()) out.auth_url = client.authUrl.trim()
  if (client.tokenUrl.trim()) out.token_url = client.tokenUrl.trim()
  if (client.redirectUri.trim()) out.redirect_uri = client.redirectUri.trim()
  const scopes = parseScopes(client.scopes)
  if (scopes.length > 0) out.scopes = scopes
  if (client.source.trim()) out.source = client.source.trim()
  return out
}

/**
 * 构造 OAuth callback 请求体(POST /{id}/credential-acquisitions/{flowID}/callback)。
 * state 必带(后端比对防重放),code 是授权码。两者非 secret 但属敏感凭据交换输入,
 * 提交后由组件清空。
 */
export function buildCallbackBody(state: string, code: string): Record<string, unknown> {
  return { state: state.trim(), code: code.trim() }
}

/**
 * 构造导入 helper 请求体(POST /admin/v1/credentials/{helper})。
 * content 是 secret 材料(导入原文),只写。finalize=true 让后端在同一请求里落库,
 * 否则只建流不落库。tenant_id/provider_account_id 必带(后端 startInput 校验 >0)。
 */
export function buildImportBody(
  tenantId: number,
  providerAccountId: number,
  form: WizardForm,
  finalize: boolean,
): Record<string, unknown> {
  const body: Record<string, unknown> = {
    tenant_id: tenantId,
    provider_account_id: providerAccountId,
    content: form.content,
    finalize,
  }
  const vendor = form.vendor.trim()
  if (vendor) body.vendor = vendor
  const authMode = form.authMode.trim()
  if (authMode) body.auth_mode = authMode
  const reason = form.reason.trim()
  if (reason) body.reason = reason
  return body
}

// ── 表单校验(镜像后端约束,在网络前拦截)──────────────────────────────────────

export interface ValidationResult {
  ok: boolean
  message?: string
}

/** 校验 tenant_id 必须为正整数(后端硬约束 tenant_id<=0 报错)。 */
function validTenant(tenantId: number): ValidationResult {
  if (!Number.isInteger(tenantId) || tenantId <= 0) {
    return { ok: false, message: '租户 ID 非法(必须为正整数)' }
  }
  return { ok: true }
}

/** OAuth start 前置校验:vendor / auth_mode 必填,tenant 合法。 */
export function validateOAuthStart(tenantId: number, form: WizardForm): ValidationResult {
  const t = validTenant(tenantId)
  if (!t.ok) return t
  if (!form.vendor.trim()) return { ok: false, message: '请填写 vendor(如 anthropic / openai / gemini)' }
  if (!form.authMode.trim()) return { ok: false, message: '请填写 auth_mode(如 claude_ai_oauth)' }
  return { ok: true }
}

/**
 * 导入 helper 前置校验:tenant 合法 + content 非空。
 * 判别核心:content 为空(或仅空白)必须拦下——空导入对后端无意义且会触发
 * invalid_import_body,且会让用户误以为提交了内容。
 */
export function validateImport(tenantId: number, form: WizardForm): ValidationResult {
  const t = validTenant(tenantId)
  if (!t.ok) return t
  if (!form.content.trim()) return { ok: false, message: '请粘贴要导入的凭据内容' }
  return { ok: true }
}

/**
 * callback 前置校验:state / code 均非空。
 * 判别核心:state 缺失会被后端判 oauth_state_mismatch,提前拦下给清晰提示。
 */
export function validateCallback(state: string, code: string): ValidationResult {
  if (!state.trim()) return { ok: false, message: '请填写授权回调返回的 state' }
  if (!code.trim()) return { ok: false, message: '请填写授权回调返回的 code' }
  return { ok: true }
}

// ── 流状态机(判定下一步可做什么)─────────────────────────────────────────────

/** 终态:不再可推进的状态。 */
const TERMINAL: ReadonlySet<FlowStatus> = new Set<FlowStatus>([
  'finalized',
  'cancelled',
  'expired',
  'failed',
])

/** 流是否已到终态。 */
export function isTerminal(status: FlowStatus): boolean {
  return TERMINAL.has(status)
}

/** 是否已成功落库(finalized)。 */
export function isFinalized(status: FlowStatus): boolean {
  return status === 'finalized'
}

/**
 * 是否还能投递 OAuth callback。
 * 判别核心:只有未到终态的 OAuth 流(started/waiting_for_user)才允许 callback;
 * 已 finalized/cancelled 后再投递会被后端拒(replay/已消费),前端先禁掉按钮。
 */
export function canDeliverCallback(flow: AcquisitionFlow): boolean {
  if (flow.flow_kind !== 'oauth') return false
  return !isTerminal(flow.status)
}

/**
 * 是否还能取消该流。终态(尤其 finalized/cancelled)不可再取消。
 */
export function canCancel(status: FlowStatus): boolean {
  return !isTerminal(status)
}

/** 流状态 → 徽章语气。 */
export function statusTone(status: FlowStatus): BadgeTone {
  switch (status) {
    case 'finalized':
      return 'ok'
    case 'failed':
    case 'expired':
      return 'danger'
    case 'cancelled':
      return 'muted'
    case 'callback_received':
    case 'validated':
      return 'info'
    case 'started':
    case 'waiting_for_user':
      return 'warn'
    default:
      return 'muted'
  }
}

/** 流状态中文标签。 */
export function statusLabel(status: FlowStatus): string {
  switch (status) {
    case 'started':
      return '已发起'
    case 'waiting_for_user':
      return '等待用户授权'
    case 'callback_received':
      return '已收到回调'
    case 'validated':
      return '已校验'
    case 'finalized':
      return '已落库'
    case 'cancelled':
      return '已取消'
    case 'expired':
      return '已过期'
    case 'failed':
      return '失败'
    default:
      return status
  }
}
