import type { AccountMode, CreateAccountRequest, FieldSpec } from './createTypes'

/*
 * 新建账号的纯逻辑(可单测):把向导表单态构造成后端创建请求体。后端契约关键点:
 *  - account_type 必选且为枚举(oauth/api_key/service_account/upstream_static/session/aws_sigv4);
 *  - credentials 必须是非空 JSON 对象,键 = required_fields 的 name,值 = 运维输入;
 *  - vendor/auth_mode 非空 → 走 V2 凭据库(credentials 按 mode required_fields 校验);
 *  - 可选参数(priority/static_weight/cap_concurrency/probe_model/tags)留空必须省略;
 *  - 混合渠道风险时二次提交带 confirm=true。
 */

/** account_type 枚举(与后端 validateCreateProviderAccount 一致)。 */
export const ACCOUNT_TYPE_OPTIONS = [
  'oauth',
  'api_key',
  'service_account',
  'upstream_static',
  'session',
  'aws_sigv4',
] as const

export type AccountType = (typeof ACCOUNT_TYPE_OPTIONS)[number]

export interface CreateAccountForm {
  providerId: string
  channelId: string
  name: string
  accountType: string
  /** "vendor::auth_mode" 复合键,空=未选模式。 */
  modeKey: string
  /** required_fields 的输入值,键=field.name。 */
  credentialValues: Record<string, string>
  enabled: boolean
  priority: string
  staticWeight: string
  capConcurrency: string
  probeModel: string
  tags: string
  reason: string
}

export const EMPTY_CREATE_FORM: CreateAccountForm = {
  providerId: '',
  channelId: '',
  name: '',
  accountType: '',
  modeKey: '',
  credentialValues: {},
  enabled: true,
  priority: '',
  staticWeight: '',
  capConcurrency: '',
  probeModel: '',
  tags: '',
  reason: '',
}

export function modeKey(m: Pick<AccountMode, 'vendor' | 'auth_mode'>): string {
  return `${m.vendor}::${m.auth_mode}`
}

function parsePositiveInt(s: string): number | undefined {
  const n = Number(s.trim())
  return Number.isInteger(n) && n > 0 ? n : undefined
}

function splitTags(s: string): string[] | undefined {
  const tags = s
    .split(/[,，\s]+/)
    .map((t) => t.trim())
    .filter(Boolean)
  return tags.length ? tags : undefined
}

/**
 * 把凭据输入收成 credentials 对象:只收【非空】值(去空白)。required_fields 用于决定渲染,
 * 但这里按实际输入收集(避免把空可选字段塞成空串)。
 */
export function assembleCredentials(
  fields: FieldSpec[],
  values: Record<string, string>,
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const f of fields) {
    const v = (values[f.name] ?? '').trim()
    if (v) out[f.name] = v
  }
  return out
}

/**
 * 构造创建请求体。selectedMode 为 null 时不带 vendor/auth_mode(非 V2);confirm 用于混合风险二次提交。
 */
export function buildCreateRequest(
  form: CreateAccountForm,
  selectedMode: AccountMode | null,
  confirm: boolean,
): CreateAccountRequest {
  const credentials = assembleCredentials(selectedMode?.required_fields ?? [], form.credentialValues)
  const req: CreateAccountRequest = {
    provider_id: parsePositiveInt(form.providerId) ?? 0,
    channel_id: parsePositiveInt(form.channelId) ?? 0,
    name: form.name.trim(),
    account_type: form.accountType,
    credentials,
    enabled: form.enabled,
  }
  if (selectedMode) {
    req.vendor = selectedMode.vendor
    req.auth_mode = selectedMode.auth_mode
  }
  const priority = parsePositiveInt(form.priority)
  if (priority !== undefined) req.priority = priority
  const staticWeight = parsePositiveInt(form.staticWeight)
  if (staticWeight !== undefined) req.static_weight = staticWeight
  const capConcurrency = parsePositiveInt(form.capConcurrency)
  if (capConcurrency !== undefined) req.cap_concurrency = capConcurrency
  const probe = form.probeModel.trim()
  if (probe) req.probe_model = probe
  const tags = splitTags(form.tags)
  if (tags) req.tags = tags
  const reason = form.reason.trim()
  if (reason) req.reason = reason
  if (confirm) req.confirm = true
  return req
}

/** 前端先校验必填,避免明显无效请求空跑后端。返回第一条错误或 null。 */
export function validateCreateForm(form: CreateAccountForm, selectedMode: AccountMode | null): string | null {
  if (!parsePositiveInt(form.providerId)) return '请选择上游(provider)'
  if (!parsePositiveInt(form.channelId)) return '请选择渠道(channel)'
  if (!form.name.trim()) return '请填写账号名称'
  if (!ACCOUNT_TYPE_OPTIONS.includes(form.accountType as AccountType)) return '请选择账号类型'
  // 选了模式时,必填的凭据字段必须有值。
  if (selectedMode) {
    for (const f of selectedMode.required_fields) {
      if (f.required && !f.one_of_group && !(form.credentialValues[f.name] ?? '').trim()) {
        return `请填写凭据字段:${f.name}`
      }
    }
  }
  const creds = assembleCredentials(selectedMode?.required_fields ?? [], form.credentialValues)
  if (Object.keys(creds).length === 0) return '请填写至少一项凭据'
  return null
}
