// 定价页数据层。三个端点都是 **public**（无鉴权）——后端 cmd/gateway/routes.go 顶层
// 直接 r.Get 挂载，无 SessionMiddleware 包裹（已读真码确认）：
//
//   GET /v1/pricing/page                  —— public。pricingpublichttp.NewHandler，
//                                            返回每模型客户面单位价数组（已剔除内部成本/倍率）。
//   GET /v1/pricing/snapshots             —— public。gatewayhttp.NewPricingSnapshotsHandler，
//                                            返回 {"snapshots": [...]} 历史 version 列表（轻量行）。
//   GET /v1/pricing/rate-table?version=X  —— public。gatewayhttp.NewPricingRateTableHandler，
//                                            返回某 version 的完整费率表（含原始 pricing_data，
//                                            带 cache 价 / model_multiplier 等 page 端点不暴露的字段）。
//
// 三个端点都公开,但仍走 userClient（带 session token 也无妨,且复用 401 刷新与错误信封解析）。
//
// 503 风险（dev 装配）：
//   - /v1/pricing/page    Catalog/Pricing 任一 nil → 503 gateway_not_configured；
//                         PublicModelPrices 后端报错 → 503 pricing_backend_error；
//                         registry 后端瞬时故障 → 503 registry_backend_error。
//                         **无公开费率表 / 无匹配模型时返回 [] 而非 503**（需正常容错为空态）。
//   - /v1/pricing/snapshots  RateTables nil → 503 gateway_not_configured；读失败 → 503 rate_table_read_failed。
//                            无快照时返回 {"snapshots": []}。
//   - /v1/pricing/rate-table RateTables nil → 503；缺 version → 400 version_required；
//                            version 不存在 → 404 rate_table_not_found；读失败 → 503 rate_table_read_failed。
//   正常 Postgres dev 装配里 modelRegistry / rateTableSource 都是无条件构造的非 nil 指针,
//   故 503 主要来自瞬时后端故障或裁剪装配；每 section 独立容错。
//
// 后端错误信封统一 {"error":{"code","message"}}，userClient 解析成 ApiError 复用 friendlyMessage。
//
// 对照（clean-room，仅提取功能/字段形态，未抄码）：
//   - sub2api src/api/channels.ts：UserSupportedModelPricing 暴露 input_price / output_price /
//     cache_write_price / cache_read_price / per_request_price + 分组 rate_multiplier（倍率）+ billing_mode。
//   - new-api web/default features/pricing：model 名 + vendor/owner + group_ratio（分组倍率）+
//     搜索/筛选 toolbar + input/output/cache 价 + 表格视图。
//   HUAKAI 公开 page 端点形态更窄（仅每模型单位输入/输出价 + context_length，故意剔除 cache/倍率/成本）；
//   cache 价与版本经 rate-table/snapshots 暴露,本页据此做「当前价表 + 可选版本切换」两段。

import { userGet } from './userClient';

// ---- GET /v1/pricing/page（pricingpublichttp.pricingItem）----
// 字段与 handler.go JSON tag 完全一致（omitempty：缺价/缺 context 时该字段不出现）。
// 价格为「每 token 美元」的 decimal 字符串（如 "0.0000004"）。
export interface PricingPageItem {
  model: string;
  canonical_id?: string;
  input_price_per_token?: string;
  output_price_per_token?: string;
  context_length?: number;
}

export function fetchPricingPage(): Promise<PricingPageItem[]> {
  return userGet<PricingPageItem[]>('/v1/pricing/page');
}

// ---- GET /v1/pricing/snapshots（billing.RateTableSnapshot 轻量行）----
export interface RateTableSnapshot {
  id: number;
  version: string;
  effective_from: string; // RFC3339
  effective_to?: string | null; // null/缺省 = 当前生效
  created_at: string;
}

interface SnapshotsResponse {
  snapshots: RateTableSnapshot[] | null;
}

export async function fetchPricingSnapshots(): Promise<RateTableSnapshot[]> {
  const resp = await userGet<SnapshotsResponse>('/v1/pricing/snapshots');
  return resp.snapshots ?? [];
}

// ---- GET /v1/pricing/rate-table?version=X（billing.RateTable）----
// pricing_data 是原始 JSON：顶层可能有 "models" map，每模型项带 *_micro_usd 字段。
// 这里弱类型解析,只在能识别出 input/output/cache micro 价时展示,识别不出就跳过该行。
export interface RateTable {
  id: number;
  version: string;
  pricing_data: unknown; // 原始 JSON,见 parseRateTableModels
  effective_from: string;
  effective_to?: string | null;
  created_at: string;
}

export function fetchRateTable(version: string): Promise<RateTable> {
  return userGet<RateTable>('/v1/pricing/rate-table', { version });
}

// ---- 派生：从 rate-table 的 pricing_data 解析出每模型的「内部费率行」----
// 后端 billing.parsePublicPriceTable 接受 models map 或顶层模型项；这里对齐其字段名做弱解析。
// 单位：*_micro_usd 是「每 1M token 的美元」（micro-usd-per-token 语义下乘以 1e6 即 per-1M）；
// 我们直接展示原始 micro 值并标注单位,避免与 page 端点的 per-token 字符串混淆。
export interface RateTableModelRow {
  model: string;
  inputMicroUsd?: number;
  outputMicroUsd?: number;
  cacheReadMicroUsd?: number;
  cacheWriteMicroUsd?: number;
  multiplier?: number;
}

const INPUT_KEYS = ['input_micro_usd', 'input_rate_micro', 'input_cost_micro_usd', 'input_per_token_micro_usd'] as const;
const OUTPUT_KEYS = ['output_micro_usd', 'output_rate_micro', 'output_cost_micro_usd', 'output_per_token_micro_usd'] as const;
const CACHE_READ_KEYS = ['cache_read_micro_usd', 'cache_read_rate_micro'] as const;
const CACHE_WRITE_KEYS = ['cache_write_micro_usd', 'cache_creation_micro_usd', 'cache_write_rate_micro'] as const;
const MULTIPLIER_KEYS = ['model_multiplier', 'multiplier'] as const;

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

function numField(obj: Record<string, unknown>, keys: readonly string[]): number | undefined {
  for (const k of keys) {
    const raw = obj[k];
    if (raw === undefined || raw === null) continue;
    const n = typeof raw === 'number' ? raw : Number.parseFloat(String(raw));
    if (Number.isFinite(n)) return n;
  }
  return undefined;
}

export function parseRateTableModels(data: unknown): RateTableModelRow[] {
  if (!isObject(data)) return [];
  // 后端优先看 "models" map；没有则把顶层每个对象项当模型项尝试。
  const modelsContainer = isObject(data.models) ? data.models : data;
  const rows: RateTableModelRow[] = [];
  for (const [model, value] of Object.entries(modelsContainer)) {
    if (model === 'models') continue;
    if (!isObject(value)) continue;
    const inputMicroUsd = numField(value, INPUT_KEYS);
    const outputMicroUsd = numField(value, OUTPUT_KEYS);
    const cacheReadMicroUsd = numField(value, CACHE_READ_KEYS);
    const cacheWriteMicroUsd = numField(value, CACHE_WRITE_KEYS);
    const multiplier = numField(value, MULTIPLIER_KEYS);
    // 至少有一个可识别价格字段才入表,否则跳过（避免把无关 JSON 节点当模型行）。
    if (
      inputMicroUsd === undefined &&
      outputMicroUsd === undefined &&
      cacheReadMicroUsd === undefined &&
      cacheWriteMicroUsd === undefined
    ) {
      continue;
    }
    rows.push({ model, inputMicroUsd, outputMicroUsd, cacheReadMicroUsd, cacheWriteMicroUsd, multiplier });
  }
  rows.sort((a, b) => a.model.localeCompare(b.model));
  return rows;
}

// ---- 展示格式化 ----

// page 端点的 per-token 字符串价 → 「每 1M token 美元」展示（更贴近行业惯例,便于横比）。
export function perTokenToPerMillion(perToken?: string): string {
  if (!perToken) return '—';
  const n = Number.parseFloat(perToken);
  if (!Number.isFinite(n)) return '—';
  const perMillion = n * 1_000_000;
  // 小数位按量级自适应,避免极小价显示成 0。
  const digits = perMillion >= 1 ? 2 : perMillion >= 0.01 ? 4 : 6;
  return `$${perMillion.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: digits })}`;
}

// rate-table 的 micro-usd 值（语义为「每 1M token 美元」）直接展示。
export function microUsdPerMillion(micro?: number): string {
  if (micro === undefined) return '—';
  const digits = micro >= 1 ? 2 : micro >= 0.01 ? 4 : 6;
  return `$${micro.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: digits })}`;
}

export function fmtContext(ctx?: number): string {
  if (!ctx || ctx <= 0) return '—';
  if (ctx >= 1000) return `${Math.round(ctx / 1000)}K`;
  return String(ctx);
}

export function fmtSnapshotTime(iso?: string | null): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString('zh-CN');
}

// 模型 owner 推断：canonical_id 形如 "openai/gpt-4.1-mini",取斜杠前段作为提供方标签。
export function ownerFromCanonical(canonicalId?: string, model?: string): string {
  if (canonicalId && canonicalId.includes('/')) {
    return canonicalId.split('/')[0];
  }
  if (model && model.includes('/')) return model.split('/')[0];
  return '—';
}
