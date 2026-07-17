import rawSpecs from './advancedFields.json'
import type {
  ProviderAccount,
  ProviderAccountProxyBinding,
  ProviderAccountTempRule,
} from './types'

export type AdvancedFieldKind =
  | 'integer'
  | 'boolean'
  | 'datetime'
  | 'integer_array'
  | 'rule_array'
  | 'proxy_binding'

export interface AdvancedFieldSpec {
  key: string
  kind: AdvancedFieldKind
  format: string
  nullable: boolean
  minimum: string
  maximum: string
  create: boolean
  update: boolean
  label: string
  help: string
}

/** 该静态 mirror 直接驱动 create/edit 的高级字段渲染；Go 门会逐项核对后端规格。 */
export const ACCOUNT_ADVANCED_FIELD_SPECS = rawSpecs as AdvancedFieldSpec[]

export type ProxyBindingMode = 'direct' | 'proxy' | 'group'
export type PoolModeChoice = 'unchanged' | 'enabled' | 'disabled'
export type NullableFieldMode = 'unchanged' | 'value' | 'clear'
export type TempRulesMode = 'unchanged' | 'replace'

export interface TempUnschedulableRuleForm {
  errorCode: string
  keywords: string
  durationMinutes: string
  description: string
}

export interface AccountAdvancedFormState {
  rpmLimit: string
  tpmLimit: string
  windowCostLimitCents: string
  maxSessions: string
  disableCooling: boolean
  refreshLeadMode: NullableFieldMode
  refreshLeadSeconds: string
  expiresAtMode: NullableFieldMode
  expiresAt: string
  tlsFingerprintRotate: boolean
  customErrorCodesEnabled: boolean
  customErrorCodes: string
  poolMode: PoolModeChoice
  tempUnschedulableEnabled: boolean
  tempRulesMode: TempRulesMode
  tempUnschedulableRules: TempUnschedulableRuleForm[]
  proxyMode: ProxyBindingMode
  proxyId: string
  proxyGroupId: string
}

export interface AccountAdvancedPayload {
  rpm_limit?: number
  tpm_limit?: number
  window_cost_limit_cents?: number
  max_sessions?: number
  disable_cooling?: boolean
  refresh_lead_seconds?: number | null
  expires_at?: string | null
  tls_fingerprint_rotate?: boolean
  custom_error_codes_enabled?: boolean
  custom_error_codes?: number[]
  pool_mode?: boolean
  temp_unschedulable_enabled?: boolean
  temp_unschedulable_rules?: ProviderAccountTempRule[]
  proxy_binding?: ProviderAccountProxyBinding
}

export type AdvancedBuildResult = AccountAdvancedPayload | { error: string }

export const ADVANCED_NUMBER_FORM_KEYS = {
  rpm_limit: 'rpmLimit',
  tpm_limit: 'tpmLimit',
  window_cost_limit_cents: 'windowCostLimitCents',
  max_sessions: 'maxSessions',
} as const

export const ADVANCED_BOOLEAN_FORM_KEYS = {
  disable_cooling: 'disableCooling',
  tls_fingerprint_rotate: 'tlsFingerprintRotate',
  custom_error_codes_enabled: 'customErrorCodesEnabled',
  temp_unschedulable_enabled: 'tempUnschedulableEnabled',
} as const

export function emptyAdvancedForm(): AccountAdvancedFormState {
  return {
    rpmLimit: '',
    tpmLimit: '',
    windowCostLimitCents: '',
    maxSessions: '',
    disableCooling: false,
    refreshLeadMode: 'unchanged',
    refreshLeadSeconds: '',
    expiresAtMode: 'unchanged',
    expiresAt: '',
    tlsFingerprintRotate: false,
    customErrorCodesEnabled: false,
    customErrorCodes: '',
    poolMode: 'unchanged',
    tempUnschedulableEnabled: false,
    tempRulesMode: 'unchanged',
    tempUnschedulableRules: [],
    proxyMode: 'direct',
    proxyId: '',
    proxyGroupId: '',
  }
}

export function advancedFormFromAccount(account: ProviderAccount): AccountAdvancedFormState {
  return {
    rpmLimit: String(account.rpm_limit ?? 0),
    tpmLimit: String(account.tpm_limit ?? 0),
    windowCostLimitCents: String(account.window_cost_limit_cents ?? 0),
    maxSessions: String(account.max_sessions ?? 0),
    disableCooling: account.disable_cooling ?? false,
    refreshLeadMode: 'unchanged',
    refreshLeadSeconds: account.refresh_lead_seconds == null ? '' : String(account.refresh_lead_seconds),
    expiresAtMode: 'unchanged',
    expiresAt: dateTimeLocalValue(account.expires_at),
    tlsFingerprintRotate: account.tls_fingerprint_rotate ?? false,
    customErrorCodesEnabled: account.custom_error_codes_enabled ?? false,
    customErrorCodes: (account.custom_error_codes ?? []).join(', '),
    poolMode: 'unchanged',
    tempUnschedulableEnabled: account.temp_unschedulable_enabled ?? false,
    tempRulesMode: 'unchanged',
    tempUnschedulableRules: rulesToForm(account.temp_unschedulable_rules),
    proxyMode: proxyModeFromAccount(account),
    proxyId: account.proxy_id == null ? '' : String(account.proxy_id),
    proxyGroupId: account.proxy_group_id ?? '',
  }
}

export function proxyModeFromAccount(account: ProviderAccount): ProxyBindingMode {
  if (account.proxy_id != null) return 'proxy'
  if (account.proxy_group_id) return 'group'
  return 'direct'
}

export function rulesToForm(rules?: ProviderAccountTempRule[]): TempUnschedulableRuleForm[] {
  if (!Array.isArray(rules)) return []
  return rules.map((rule) => ({
    errorCode: String(rule.error_code ?? ''),
    keywords: (rule.keywords ?? []).join(', '),
    durationMinutes: String(rule.duration_minutes ?? ''),
    description: rule.description ?? '',
  }))
}

export function parseErrorCodes(raw: string): number[] | { error: string } {
  const parts = raw.split(/[,，\s]+/).map((value) => value.trim()).filter(Boolean)
  const values: number[] = []
  for (const part of parts) {
    const value = Number(part)
    if (!Number.isInteger(value) || value < 100 || value > 599) {
      return { error: `自定义错误码须为 100-599 的整数:${part}` }
    }
    values.push(value)
  }
  return values
}

export function buildTempUnschedulableRules(
  rows: TempUnschedulableRuleForm[],
): ProviderAccountTempRule[] | { error: string } {
  const rules: ProviderAccountTempRule[] = []
  for (const [index, row] of rows.entries()) {
    const errorCode = Number(row.errorCode.trim())
    if (!Number.isInteger(errorCode) || errorCode < 100 || errorCode > 599) {
      return { error: `第 ${index + 1} 条规则的错误码须为 100-599 的整数` }
    }
    const durationMinutes = Number(row.durationMinutes.trim())
    if (!Number.isInteger(durationMinutes) || durationMinutes < 1) {
      return { error: `第 ${index + 1} 条规则的停调时长须为正整数分钟` }
    }
    if (durationMinutes > 2147483647) {
      return { error: `第 ${index + 1} 条规则的停调时长超出 int32 范围` }
    }
    const keywords = row.keywords.split(/[,，]+/).map((value) => value.trim()).filter(Boolean)
    const description = row.description.trim()
    rules.push({
      error_code: errorCode,
      keywords,
      duration_minutes: durationMinutes,
      ...(description ? { description } : {}),
    })
  }
  return rules
}

export function buildAdvancedCreate(form: AccountAdvancedFormState): AdvancedBuildResult {
  const body: AccountAdvancedPayload = {}
  const numberError = addNumberFields(body, form)
  if (numberError) return { error: numberError }
  if (form.disableCooling) body.disable_cooling = true
  if (form.tlsFingerprintRotate) body.tls_fingerprint_rotate = true
  if (form.customErrorCodesEnabled) body.custom_error_codes_enabled = true
  if (form.tempUnschedulableEnabled) body.temp_unschedulable_enabled = true
  const nullableError = addNullableFields(body, form)
  if (nullableError) return { error: nullableError }
  if (form.customErrorCodes.trim()) {
    const codes = parseErrorCodes(form.customErrorCodes)
    if (!Array.isArray(codes)) return codes
    body.custom_error_codes = codes
  }
  if (form.poolMode !== 'unchanged') body.pool_mode = form.poolMode === 'enabled'
  const rulesError = addRules(body, form)
  if (rulesError) return { error: rulesError }
  const proxy = buildProxyBinding(form)
  if ('error' in proxy) return proxy
  if (proxy.mode !== 'direct') body.proxy_binding = proxy
  return body
}

export function buildAdvancedUpdate(
  original: ProviderAccount,
  form: AccountAdvancedFormState,
): AdvancedBuildResult {
  const body: AccountAdvancedPayload = {}
  const numberError = addNumberFields(body, form, original)
  if (numberError) return { error: numberError }
  addChangedBoolean(body, 'disable_cooling', form.disableCooling, original.disable_cooling ?? false)
  addChangedBoolean(body, 'tls_fingerprint_rotate', form.tlsFingerprintRotate, original.tls_fingerprint_rotate ?? false)
  addChangedBoolean(body, 'custom_error_codes_enabled', form.customErrorCodesEnabled, original.custom_error_codes_enabled ?? false)
  addChangedBoolean(body, 'temp_unschedulable_enabled', form.tempUnschedulableEnabled, original.temp_unschedulable_enabled ?? false)
  const nullableError = addNullableFields(body, form)
  if (nullableError) return { error: nullableError }
  const codes = parseErrorCodes(form.customErrorCodes)
  if (!Array.isArray(codes)) return codes
  if (!listEqual(codes, original.custom_error_codes ?? [])) body.custom_error_codes = codes
  if (form.poolMode !== 'unchanged') {
    const value = form.poolMode === 'enabled'
    if (value !== original.pool_mode) body.pool_mode = value
  }
  const rulesError = addRules(body, form)
  if (rulesError) return { error: rulesError }
  const proxy = buildProxyBinding(form)
  if ('error' in proxy) return proxy
  if (!proxyBindingEqual(proxy, original)) body.proxy_binding = proxy
  return body
}

function addNumberFields(
  body: AccountAdvancedPayload,
  form: AccountAdvancedFormState,
  original?: ProviderAccount,
): string | null {
  for (const spec of ACCOUNT_ADVANCED_FIELD_SPECS.filter((item) => item.kind === 'integer' && !item.nullable)) {
    const formKey = ADVANCED_NUMBER_FORM_KEYS[spec.key as keyof typeof ADVANCED_NUMBER_FORM_KEYS]
    if (!formKey) continue
    const raw = form[formKey].trim()
    if (!raw) continue
    const parsed = parseBoundedInteger(raw, spec)
    if ('error' in parsed) return parsed.error
    const apiKey = spec.key as keyof Pick<AccountAdvancedPayload, 'rpm_limit' | 'tpm_limit' | 'window_cost_limit_cents' | 'max_sessions'>
    if (!original || parsed.value !== (original[apiKey] ?? 0)) body[apiKey] = parsed.value
  }
  return null
}

function parseBoundedInteger(raw: string, spec: AdvancedFieldSpec): { value: number } | { error: string } {
  const value = Number(raw)
  const maximum = spec.format === 'int64' ? Number.MAX_SAFE_INTEGER : Number(spec.maximum)
  if (!Number.isSafeInteger(value) || value < Number(spec.minimum) || value > maximum) {
    return { error: `${spec.label}须为 ${spec.minimum} 到 ${maximum} 的安全整数` }
  }
  return { value }
}

function addNullableFields(body: AccountAdvancedPayload, form: AccountAdvancedFormState): string | null {
  if (form.refreshLeadMode === 'clear') body.refresh_lead_seconds = null
  if (form.refreshLeadMode === 'value') {
    const spec = ACCOUNT_ADVANCED_FIELD_SPECS.find((item) => item.key === 'refresh_lead_seconds')!
    const parsed = parseBoundedInteger(form.refreshLeadSeconds.trim(), spec)
    if ('error' in parsed) return parsed.error
    body.refresh_lead_seconds = parsed.value
  }
  if (form.expiresAtMode === 'clear') body.expires_at = null
  if (form.expiresAtMode === 'value') {
    const parsed = new Date(form.expiresAt)
    if (!form.expiresAt.trim() || Number.isNaN(parsed.getTime())) return '账号过期时间格式无效'
    body.expires_at = parsed.toISOString()
  }
  return null
}

function addRules(body: AccountAdvancedPayload, form: AccountAdvancedFormState): string | null {
  if (form.tempRulesMode !== 'replace') return null
  const rules = buildTempUnschedulableRules(form.tempUnschedulableRules)
  if (!Array.isArray(rules)) return rules.error
  body.temp_unschedulable_rules = rules
  return null
}

function buildProxyBinding(form: AccountAdvancedFormState): ProviderAccountProxyBinding | { error: string } {
  if (form.proxyMode === 'direct') return { mode: 'direct' }
  if (form.proxyMode === 'proxy') {
    const proxyID = Number(form.proxyId.trim())
    if (!Number.isSafeInteger(proxyID) || proxyID <= 0) return { error: '请选择一个出站代理' }
    return { mode: 'proxy', proxy_id: proxyID }
  }
  const groupID = form.proxyGroupId.trim()
  if (!groupID) return { error: '请填写代理组标识(proxy_group_id)' }
  return { mode: 'group', proxy_group_id: groupID }
}

function proxyBindingEqual(next: ProviderAccountProxyBinding, original: ProviderAccount): boolean {
  const mode = proxyModeFromAccount(original)
  if (next.mode !== mode) return false
  if (mode === 'proxy') return next.proxy_id === original.proxy_id
  if (mode === 'group') return next.proxy_group_id === original.proxy_group_id
  return true
}

function addChangedBoolean(
  body: AccountAdvancedPayload,
  key: 'disable_cooling' | 'tls_fingerprint_rotate' | 'custom_error_codes_enabled' | 'temp_unschedulable_enabled',
  next: boolean,
  original: boolean,
) {
  if (next !== original) body[key] = next
}

function listEqual<T>(left: T[], right: T[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index])
}

function dateTimeLocalValue(value: string | null): string {
  if (!value) return ''
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return ''
  return parsed.toISOString().slice(0, 16)
}
