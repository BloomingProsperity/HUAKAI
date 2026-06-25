import type { CacheOverrideScope, ToolSurchargeDefault } from './types'

/*
 * 模型定价设置的纯逻辑(可单测)。把后端的计费约束投影成前端校验,提交前先挡住非法值,
 * 既给用户清晰提示、又减少打到 money-gated 写端点的无效请求。
 *
 * 约束来源(核源码):
 *  - 分组倍率   pricing_ratio_handler.go:parseRatioBody —— 正小数,范围 [0.01, 100](默认上限,
 *    可被 HUAKAI_PRICING_RATIO_MAX 环境变量调大,前端按默认上限校验,真正上限以后端为准)。
 *  - 缓存价覆盖 cache_price_override_handler.go —— multiplier 必须为正小数;
 *    model scope 需非空 model,tenant scope 需正整数 tenant_id。
 *  - 工具附加费 toolpricing.DefaultToolPrices —— 无 admin 写端点(env HUAKAI_TOOL_SURCHARGE_ENABLED 控开关),
 *    前端只读展示默认价。
 */

/** 倍率下限(含),对齐后端 pricingRatioMin。 */
export const RATIO_MIN = 0.01
/** 倍率默认上限(含),对齐后端 defaultPricingRatioMax;运维可经环境变量调高。 */
export const RATIO_MAX_DEFAULT = 100

/**
 * 解析并校验正小数字符串。返回数值或错误信息。
 * 拒绝:空、非数字、NaN、非正(<=0)。允许小数。
 */
export function parsePositiveDecimal(raw: string): { value: number } | { error: string } {
  const s = raw.trim()
  if (!s) return { error: '请填写数值' }
  // 仅允许十进制正小数写法(可带前导 0 / 小数点),不接受科学计数法/符号/千分位。
  if (!/^\d+(\.\d+)?$/.test(s)) return { error: '必须是十进制正小数(如 1.5)' }
  const n = Number(s)
  if (!Number.isFinite(n) || n <= 0) return { error: '数值必须大于 0' }
  return { value: n }
}

/**
 * 校验分组倍率输入。判别核心:必须为正小数且落在 [RATIO_MIN, RATIO_MAX_DEFAULT] 闭区间内,
 * 否则后端会回 422 ratio_out_of_range。
 */
export function validateRatio(raw: string, max: number = RATIO_MAX_DEFAULT): { value: string } | { error: string } {
  const parsed = parsePositiveDecimal(raw)
  if ('error' in parsed) return parsed
  if (parsed.value < RATIO_MIN || parsed.value > max) {
    return { error: `倍率必须在 ${RATIO_MIN} 到 ${max} 之间` }
  }
  // 回传去空白后的原始字符串(保留用户精度,避免 Number 往返丢精度)。
  return { value: raw.trim() }
}

/** 校验缓存价倍率输入(正小数,无上限约束,后端只要求 IsPositive)。 */
export function validateMultiplier(raw: string): { value: string } | { error: string } {
  const parsed = parsePositiveDecimal(raw)
  if ('error' in parsed) return parsed
  return { value: raw.trim() }
}

export interface CacheOverrideQualifier {
  model?: string
  tenantId?: string
}

/**
 * 校验缓存价覆盖的 scope 限定值。判别核心:
 *  - global:无需限定值
 *  - model:需非空 model
 *  - tenant:需正整数 tenant_id
 * 返回归一化后的限定对象(给 api 层),或错误信息。
 */
export function validateCacheQualifier(
  scope: CacheOverrideScope,
  q: CacheOverrideQualifier,
): { model?: string; tenantId?: number } | { error: string } {
  if (scope === 'global') return {}
  if (scope === 'model') {
    const model = (q.model ?? '').trim()
    if (!model) return { error: 'model 范围需填写模型名' }
    return { model }
  }
  // tenant
  const raw = (q.tenantId ?? '').trim()
  if (!/^\d+$/.test(raw)) return { error: 'tenant 范围需填写正整数租户 id' }
  const id = Number(raw)
  if (id <= 0) return { error: 'tenant 范围需填写正整数租户 id' }
  return { tenantId: id }
}

/** scope 中文展示名。 */
export function scopeLabel(scope: string): string {
  switch (scope) {
    case 'global':
      return '全局'
    case 'model':
      return '按模型'
    case 'tenant':
      return '按租户'
    default:
      return scope
  }
}

export const CACHE_SCOPES: ReadonlyArray<{ value: CacheOverrideScope; label: string }> = [
  { value: 'global', label: '全局' },
  { value: 'model', label: '按模型' },
  { value: 'tenant', label: '按租户' },
]

/** 校验租户 id 输入(分组倍率列表必填,后端要求正整数)。 */
export function validateTenantId(raw: string): { value: number } | { error: string } {
  const s = raw.trim()
  if (!/^\d+$/.test(s)) return { error: '租户 id 必须是正整数' }
  const id = Number(s)
  if (id <= 0) return { error: '租户 id 必须是正整数' }
  return { value: id }
}

/**
 * 工具附加费默认价(只读)。镜像后端 toolpricing.DefaultToolPrices:USD / 1000 次调用。
 * image_generation 价表延期(Stage D,按模型/尺寸定价),当前默认 $0。
 * 该表无 admin 写端点,启停由环境变量 HUAKAI_TOOL_SURCHARGE_ENABLED 控制(默认开)。
 */
export const TOOL_SURCHARGE_DEFAULTS: ReadonlyArray<ToolSurchargeDefault> = [
  { tool: 'web_search', label: '联网搜索', perThousandUSD: '10.00', note: '官方平台默认价' },
  { tool: 'file_search', label: '文件检索', perThousandUSD: '2.50', note: '官方平台默认价' },
  { tool: 'image_generation', label: '图像生成', perThousandUSD: '0.00', note: 'Stage D 延期(按模型/尺寸定价)' },
]
