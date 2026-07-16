import type { BadgeTone } from '../../ui/StatusBadge'
import type {
  ProfileCreateRequest,
  ProfileForm,
  ProfileUpdateRequest,
  TLSFingerprintProfile,
} from './types'

/*
 * TLS 指纹 profile 页纯逻辑(可单测,无 DOM/网络副作用):
 *   - 整数数组 / 字符串数组的文本解析(逗号或空白分隔)
 *   - profile 表单校验(name 非空 + 各数值数组合法 + JA3 哈希格式)→ 产出可提交的请求体
 *   - profile DTO ↔ 表单态 互转
 *   - 状态 → 徽章语气 + 中文标签
 * 全部为同步纯函数,便于变异测试打红。校验镜像后端硬约束:
 *   - service.go:63/80:tenant_id>0 且 name(trim 后)非空(否则 ErrInvalidInput)
 *   - JA3 哈希后端不强制格式(可空),但前端做友好校验:为空放行,非空须 32 位 hex(MD5 形态)
 */

export type QueryValue = string | number | undefined

/** 取/删/改单个 profile 时的 query:platform_admin 必带 tenant_id(handler.go:101)。 */
export function buildTenantQuery(tenantId: number): Record<string, QueryValue> {
  return { tenant_id: tenantId }
}

/**
 * 把逗号/空白分隔的文本解析成 int32 数组。
 * 判别核心:
 *   - 空白文本 → 空数组(后端接受 [],序列化非 null)
 *   - 任一 token 非「非负整数」即返回 error(避免给后端发非法码点)
 *   - 解析成功保留原顺序(顺序是指纹的一部分)
 */
export function parseIntList(text: string): { ok: true; value: number[] } | { ok: false; error: string } {
  const tokens = text
    .split(/[\s,]+/)
    .map((t) => t.trim())
    .filter((t) => t !== '')
  const out: number[] = []
  for (const tok of tokens) {
    // 仅接受十进制非负整数(TLS 码点 ∈ [0, 65535],但前端只做格式与范围守卫,后端权威)。
    if (!/^[0-9]+$/.test(tok)) {
      return { ok: false, error: `「${tok}」不是非负整数` }
    }
    const n = Number(tok)
    if (!Number.isSafeInteger(n) || n < 0 || n > 65535) {
      return { ok: false, error: `「${tok}」超出 0~65535 范围` }
    }
    out.push(n)
  }
  return { ok: true, value: out }
}

/**
 * 把逗号/空白分隔的文本解析成字符串数组(ALPN 协议名)。
 * 空白文本 → 空数组;否则按分隔符切分、trim、丢空。不做内容校验(ALPN 名形态自由)。
 */
export function parseStrList(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((t) => t.trim())
    .filter((t) => t !== '')
}

/** 把 int 数组拍成逗号+空格的展示文本(用于表单回填)。 */
export function formatIntList(arr: number[]): string {
  return (arr ?? []).join(', ')
}

/** 把字符串数组拍成逗号+空格的展示文本。 */
export function formatStrList(arr: string[]): string {
  return (arr ?? []).join(', ')
}

/**
 * 校验 JA3 哈希:为空放行(后端不强制);非空须为 32 位十六进制(JA3 是 MD5,32 位 hex)。
 * 判别核心:非空且非 32 位 hex → 返回错误;空串 → null。
 */
export function validateJa3(raw: string): string | null {
  const v = raw.trim()
  if (v === '') return null
  if (!/^[0-9a-fA-F]{32}$/.test(v)) return 'JA3 哈希须为 32 位十六进制(留空表示不设基线)'
  return null
}

/** 校验结果:ok 时携带可提交的内容字段(create/update 共用的内容部分),否则带中文错误。 */
export type FormValidation =
  | { ok: true; value: ProfileUpdateRequest }
  | { ok: false; error: string }

/**
 * 校验 profile 表单并产出内容请求体(create 时再补 tenant_id)。
 * 校验顺序:name → 各整数数组 → ALPN → JA3。镜像后端:name(trim)非空(service.go:63),
 * 数组合法,JA3 友好校验。前端先拦避免无谓 400/500;后端仍是权威。
 */
export function validateForm(form: ProfileForm): FormValidation {
  if (form.name.trim() === '') {
    return { ok: false, error: 'profile 名称不能为空' }
  }
  // 逐个整数数组解析,任一失败即带字段名返回。
  const intFields: Array<[keyof ProfileForm, string]> = [
    ['cipherSuites', '加密套件'],
    ['supportedCurves', '支持曲线'],
    ['ecPointFormats', 'EC 点格式'],
    ['signatureAlgorithms', '签名算法'],
    ['tlsSupportedVersions', 'TLS 版本'],
    ['keyShareGroups', 'key_share 分组'],
    ['pskModes', 'PSK 模式'],
    ['extensionsOrder', '扩展顺序'],
  ]
  const parsed: Record<string, number[]> = {}
  for (const [key, label] of intFields) {
    const r = parseIntList(form[key] as string)
    if (!r.ok) {
      return { ok: false, error: `${label}:${r.error}` }
    }
    parsed[key] = r.value
  }
  const ja3Err = validateJa3(form.expectedJa3Hash)
  if (ja3Err) {
    return { ok: false, error: ja3Err }
  }
  const desc = form.description.trim()
  return {
    ok: true,
    value: {
      name: form.name.trim(),
      // 空描述归一为 null(后端 Description 为 *string,可空)。
      description: desc === '' ? null : desc,
      grease_enabled: form.greaseEnabled,
      cipher_suites: parsed.cipherSuites,
      supported_curves: parsed.supportedCurves,
      ec_point_formats: parsed.ecPointFormats,
      signature_algorithms: parsed.signatureAlgorithms,
      alpn_protocols: parseStrList(form.alpnProtocols),
      tls_supported_versions: parsed.tlsSupportedVersions,
      key_share_groups: parsed.keyShareGroups,
      psk_modes: parsed.pskModes,
      extensions_order: parsed.extensionsOrder,
      expected_ja3_hash: form.expectedJa3Hash.trim(),
    },
  }
}

/** 把校验产出的内容体 + tenant_id 组装成 create 请求体。 */
export function toCreateRequest(tenantId: number, content: ProfileUpdateRequest): ProfileCreateRequest {
  return { tenant_id: tenantId, ...content }
}

/** 把 profile DTO 拍平成表单初值(供编辑时 useState 初始化)。 */
export function profileToForm(p: TLSFingerprintProfile): ProfileForm {
  return {
    name: p.name,
    description: p.description ?? '',
    greaseEnabled: p.grease_enabled,
    cipherSuites: formatIntList(p.cipher_suites),
    supportedCurves: formatIntList(p.supported_curves),
    ecPointFormats: formatIntList(p.ec_point_formats),
    signatureAlgorithms: formatIntList(p.signature_algorithms),
    alpnProtocols: formatStrList(p.alpn_protocols),
    tlsSupportedVersions: formatIntList(p.tls_supported_versions),
    keyShareGroups: formatIntList(p.key_share_groups),
    pskModes: formatIntList(p.psk_modes),
    extensionsOrder: formatIntList(p.extensions_order),
    expectedJa3Hash: p.expected_ja3_hash,
  }
}

/**
 * 状态 → 徽章语气。active=ok;disabled=muted;
 * drift_detected=danger(指纹漂移,拟真可能已暴露,需运维关注)。
 */
export function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'active':
      return 'ok'
    case 'disabled':
      return 'muted'
    case 'drift_detected':
      return 'danger'
    default:
      return 'info'
  }
}

/** 状态 → 中文标签。 */
export function statusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '启用'
    case 'disabled':
      return '停用'
    case 'drift_detected':
      return '指纹漂移'
    default:
      return status || '—'
  }
}

/**
 * 计算状态切换的目标值:active→disabled、disabled→active。
 * 判别核心:drift_detected 也可被管理员覆盖为 active(service.go:23 注释:
 * 管理员把 drift_detected 设为 active 是「清除 drift」路径),故 drift_detected 的
 * 切换目标也是 active(回到启用)。只有这两个目标在 adminSettableStatuses 内。
 */
export function nextStatus(current: string): 'active' | 'disabled' {
  return current === 'active' ? 'disabled' : 'active'
}

export interface TLSProfileTableRow {
  id: number
  name: string
  description: string
  status: string
  statusTone: BadgeTone
  grease: string
  cipherSuiteCount: number
  alpn: string
  ja3: string
  lastValidatedAt: string
  profile: TLSFingerprintProfile
}

/** TLS 指纹 DTO 到列表展示行的纯映射。 */
export function mapTLSProfileRows(profiles: TLSFingerprintProfile[]): TLSProfileTableRow[] {
  return profiles.map((profile) => ({
    id: profile.id,
    name: profile.name,
    description: profile.description ?? '',
    status: statusLabel(profile.status),
    statusTone: statusTone(profile.status),
    grease: profile.grease_enabled ? '开' : '关',
    cipherSuiteCount: (profile.cipher_suites ?? []).length,
    alpn: (profile.alpn_protocols ?? []).join(', ') || '—',
    ja3: shortenJa3(profile.expected_ja3_hash),
    lastValidatedAt: formatProfileTimestamp(profile.last_validated_at),
    profile,
  }))
}

export function shortenJa3(value: string): string {
  if (!value) return '—'
  return value.length > 14 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value
}

export function formatProfileTimestamp(iso?: string | null): string {
  if (!iso) return '—'
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString('zh-CN', { hour12: false })
}
