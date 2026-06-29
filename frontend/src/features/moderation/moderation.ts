import type { BadgeTone } from '../../ui/StatusBadge'
import type { LogFilters, ModerationConfig, ModerationConfigUpdate } from './types'

/*
 * 内容审核页纯逻辑(可单测,无 DOM/网络副作用):
 *   - 命中日志 query 构造(tenant_id 必带,api_key_id 空则省略,limit/offset 分页)
 *   - 审核判定 → 徽章语气 + 中文标签
 *   - 配置表单的前端校验(镜像后端 configFromRequest 约束,见 admin_config_handler.go:79)
 *   - 罚款金额展示(裁掉无意义的尾随 0)
 * 全部为同步纯函数,便于变异测试打红。
 */

export type QueryValue = string | number | undefined

/**
 * 构造命中日志 query。tenant_id 必带(后端 platform_admin 角色必填,helpers.go:42);
 * api_key_id 仅在非空且为正整数串时下发(后端 optionalAPIKeyIDFromQuery 要求 >0)。
 * limit/offset 直接透传(调用方保证范围)。
 */
export function buildLogQuery(
  tenantId: number,
  filters: LogFilters,
  limit: number,
  offset: number,
): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = {
    tenant_id: tenantId,
    limit,
    offset,
  }
  const apiKey = filters.apiKeyId.trim()
  // 判别核心:仅当 api_key_id 为正整数串才下发;空串/0/非法一律省略,
  // 避免给后端发 api_key_id=""(会被判 invalid_api_key_id 400)。
  if (apiKey && /^[1-9][0-9]*$/.test(apiKey)) {
    q.api_key_id = apiKey
  }
  return q
}

/** 取配置时的 query:只需 tenant_id。 */
export function buildConfigQuery(tenantId: number): Record<string, QueryValue> {
  return { tenant_id: tenantId }
}

/**
 * 审核判定 → 徽章语气。pass=通过(ok);block_*=拦截(danger);
 * fee_charged=放行但已计违规费(warn);未知=中性。
 */
export function decisionTone(decision: string): BadgeTone {
  switch (decision) {
    case 'pass':
      return 'ok'
    case 'block_keyword':
    case 'block_hash':
    case 'block_external':
    case 'block_backend':
      return 'danger'
    case 'fee_charged':
      return 'warn'
    default:
      return 'muted'
  }
}

/** 审核判定 → 中文标签。 */
export function decisionLabel(decision: string): string {
  switch (decision) {
    case 'pass':
      return '通过'
    case 'block_keyword':
      return '关键词拦截'
    case 'block_hash':
      return '哈希拦截'
    case 'block_external':
      return '外部审核拦截'
    case 'block_backend':
      return '审核后端拦截'
    case 'fee_charged':
      return '已计违规费'
    default:
      return decision || '—'
  }
}

/** 配置校验结果:ok 时携带可提交的请求体,否则带中文错误说明。 */
export type ConfigValidation =
  | { ok: true; value: ModerationConfigUpdate }
  | { ok: false; error: string }

/**
 * 校验配置表单(镜像后端 configFromRequest 的约束 admin_config_handler.go:79):
 *   - sample_rate_pct ∈ [0,100]
 *   - ban_threshold ≥ 0
 *   - ban_window_seconds > 0
 *   - violation_fee_usd 空串或非负十进制
 * 前端先拦,避免无谓 400;后端仍是权威。tenant_id 必须为正(读取时已校验,这里兜底)。
 */
export function validateConfig(
  tenantId: number,
  form: {
    enabled: boolean
    failClosed: boolean
    sampleRatePct: number
    banThreshold: number
    banWindowSeconds: number
    violationFeeUsd: string
  },
): ConfigValidation {
  if (!Number.isInteger(tenantId) || tenantId <= 0) {
    return { ok: false, error: 'tenant_id 必须为正整数' }
  }
  const { sampleRatePct, banThreshold, banWindowSeconds } = form
  if (!Number.isInteger(sampleRatePct) || sampleRatePct < 0 || sampleRatePct > 100) {
    return { ok: false, error: '采样率必须是 0~100 的整数' }
  }
  if (!Number.isInteger(banThreshold) || banThreshold < 0) {
    return { ok: false, error: '封禁阈值必须是非负整数' }
  }
  // 判别核心:窗口必须严格 > 0(后端 BanWindowSeconds <= 0 即 400)。
  if (!Number.isInteger(banWindowSeconds) || banWindowSeconds <= 0) {
    return { ok: false, error: '封禁窗口(秒)必须是正整数' }
  }
  const feeRaw = form.violationFeeUsd.trim()
  if (feeRaw !== '') {
    // 非负十进制:可有小数;负号/非数字一律拒。
    if (!/^[0-9]+(\.[0-9]+)?$/.test(feeRaw)) {
      return { ok: false, error: '违规罚款必须是非负的十进制数(如 0 或 1.50)' }
    }
  }
  return {
    ok: true,
    value: {
      tenant_id: tenantId,
      enabled: form.enabled,
      fail_closed: form.failClosed,
      sample_rate_pct: sampleRatePct,
      ban_threshold: banThreshold,
      ban_window_seconds: banWindowSeconds,
      // 空串归一为 "0"(后端把空串当 0;这里显式化便于回显一致)。
      violation_fee_usd: feeRaw === '' ? '0' : feeRaw,
    },
  }
}

/**
 * 罚款金额展示:后端回的是 StringFixed(8)(如 "0.00000000")。
 * 裁掉小数部分无意义的尾随 0,纯整数则不留小数点;非法值原样返回。
 */
export function formatFee(raw: string): string {
  const v = raw.trim()
  if (!/^[0-9]+(\.[0-9]+)?$/.test(v)) return v
  if (!v.includes('.')) return v
  const trimmed = v.replace(/0+$/, '').replace(/\.$/, '')
  return trimmed === '' ? '0' : trimmed
}

// ── 关键词/哈希规则的前端校验与批量解析(镜像后端硬约束,可单测)────────────────

/** 批量导入项数上限,镜像后端 moderation.BulkImportMaxItems(types.go:188)。 */
export const BULK_MAX_ITEMS = 1000

/** 校验关键词:trim 后非空。返回错误文案或 null(合法)。 */
export function validateKeyword(keyword: string): string | null {
  if (keyword.trim() === '') return '关键词不能为空'
  return null
}

/**
 * 归一化哈希:trim + 转小写(后端单条 create 先 ToLower 再校验,bulk 不转,
 * 故前端统一转小写后下发,避免大写输入被 bulk 逐条记 invalid_hash_hex)。
 */
export function normalizeHash(hashHex: string): string {
  return hashHex.trim().toLowerCase()
}

/**
 * 校验哈希:归一化后须为恰好 64 位小写 hex(镜像 moderation.ValidSHA256Hex,bulk_store.go:90)。
 * 判别核心:长度必须 ==64 且字符集 ∈ [0-9a-f];任一不满足即拒。返回错误文案或 null。
 */
export function validateHash(hashHex: string): string | null {
  const h = normalizeHash(hashHex)
  if (!/^[0-9a-f]{64}$/.test(h)) return '须为 64 位十六进制(SHA-256)'
  return null
}

/** 把多行文本拆成非空行(trim 每行、丢弃空行),用于批量导入框。 */
export function parseBulkLines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l !== '')
}

/** 批量个数校验结果。 */
export type BulkCountValidation = { ok: true } | { ok: false; error: string }

/**
 * 校验批量条数 ∈ [1, BULK_MAX_ITEMS](镜像后端 validateBulkItemCount:0 拒、>1000 拒)。
 * 判别核心:0 条与 >1000 条都必须拒(前端先拦,避免无谓 400)。
 */
export function validateBulkCount(n: number): BulkCountValidation {
  if (n <= 0) return { ok: false, error: '请至少导入 1 条' }
  if (n > BULK_MAX_ITEMS) return { ok: false, error: `单次最多 ${BULK_MAX_ITEMS} 条` }
  return { ok: true }
}

/** 哈希缩写展示(头 8 + 尾 4),用于列表。 */
export function shortHash(h: string): string {
  if (!h) return '—'
  return h.length > 14 ? `${h.slice(0, 8)}…${h.slice(-4)}` : h
}

/** 把后端配置 DTO 拍平成表单初值(供页面 useState 初始化)。 */
export function configToForm(cfg: ModerationConfig): {
  enabled: boolean
  failClosed: boolean
  sampleRatePct: number
  banThreshold: number
  banWindowSeconds: number
  violationFeeUsd: string
} {
  return {
    enabled: cfg.enabled,
    failClosed: cfg.fail_closed,
    sampleRatePct: cfg.sample_rate_pct,
    banThreshold: cfg.ban_threshold,
    banWindowSeconds: cfg.ban_window_seconds,
    violationFeeUsd: formatFee(cfg.violation_fee_usd),
  }
}
