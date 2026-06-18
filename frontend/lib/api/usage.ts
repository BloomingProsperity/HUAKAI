// 用量 / 额度页数据层。三个端点鉴权方式不同（已读后端 handler 真码确认）：
//
//   GET /v1/me/quota                  —— session 鉴权（routes.go r.Route("/v1/me") 套
//                                        auth.SessionMiddleware；mequotahttp.SessionResolver）。
//                                        走 userClient（自动带 session token + 401 刷新）。
//   GET /v1/me/usage                  —— API-key 鉴权（routes.go 顶层 d.inboundAuth =
//                                        auth.APIKeyResolver，解析 Bearer hk_ key → Identity）。
//   GET /v1/me/analytics/time-series  —— 同样 API-key 鉴权（d.inboundAuth）。
//
// 因此 usage / time-series 必须用客户 API key（hk_live_* / hk_test_*）作 Bearer 拉取，
// 与 chat.ts 共用 localStorage 'huakai_api_key'。quota 用 session。
//
// 后端错误信封统一 {"error":{"code","message"}}，这里解析成 ApiError 以复用 friendlyMessage。
import { ApiError } from './client';
import { userGet } from './userClient';
import { getSessionToken } from '@/lib/auth/session';
import type { APIError } from './types';

// 与 chat.ts 对齐的客户 API key localStorage 约定。
export const API_KEY_STORAGE = 'huakai_api_key';

export function getStoredApiKey(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem(API_KEY_STORAGE) ?? '';
}

export function storeApiKey(key: string): void {
  if (typeof window === 'undefined') return;
  const trimmed = key.trim();
  if (trimmed) localStorage.setItem(API_KEY_STORAGE, trimmed);
  else localStorage.removeItem(API_KEY_STORAGE);
}

// ---- 后端响应类型（snake_case，与 handler JSON 一致）----

// GET /v1/me/quota（mequotahttp.windowView）—— 所有金额/计数为 decimal 字符串。
export type QuotaWindowKind =
  | 'none'
  | 'fixed'
  | 'calendar_day'
  | 'calendar_week'
  | 'calendar_month'
  | (string & {});

// 后端 /quota 现按 metric 各返一个窗口（requests/cost_usd/tokens_estimated）。
// concurrency 不在内（slot 模型，非窗口累积）。
export type QuotaMetric = 'requests' | 'cost_usd' | 'tokens_estimated' | (string & {});

export interface QuotaWindow {
  metric: QuotaMetric;
  window_kind: QuotaWindowKind;
  cap: string;
  consumed: string;
  remaining: string;
  overage: string;
  request_count: number;
  window_start: string;
  window_end: string;
}

export interface QuotaResponse {
  items: QuotaWindow[];
}

// GET /v1/me/analytics/time-series（usageanalyticshttp.timeSeriesResponse）
export interface TimeSeriesTokens {
  input: number;
  output: number;
  cache_read: number;
  cache_creation: number;
}

export interface TimeSeriesPoint {
  day: string; // YYYY-MM-DD（按 granularity 聚合后该桶的起始）
  requested_model: string;
  total_cost: string; // decimal 字符串
  tokens: TimeSeriesTokens;
  request_count: number;
}

export interface TimeSeriesResponse {
  items: TimeSeriesPoint[];
  period: { from: string; to: string };
}

export type UsageGranularity = 'day' | 'week' | 'month';

// GET /v1/me/usage（meusagehttp.listResponse / usageRecord）
export interface MeUsageTokens {
  input: number;
  output: number;
  cache_creation?: number;
  cache_read?: number;
}

export interface MeUsageRecord {
  requested_model: string;
  upstream_model: string;
  actual_cost: string; // decimal 字符串
  tokens: MeUsageTokens;
  provider?: string;
  provider_account_id?: number;
  ledger_id: string;
  created_at: string;
  status: string;
  request_id?: string;
}

export interface MeUsageResponse {
  items: MeUsageRecord[];
  // 空字符串表示没有下一页（后端用 "" 而非 null）。
  next_cursor: string;
}

// ---- quota（session）----

export function fetchQuota(): Promise<QuotaResponse> {
  return userGet<QuotaResponse>('/v1/me/quota');
}

// ---- API-key 鉴权的 fetch helper ----

function buildQuery(params: Record<string, string | undefined>): string {
  const entries = Object.entries(params).filter(([, v]) => v !== undefined && v !== '') as [string, string][];
  if (entries.length === 0) return '';
  return `?${new URLSearchParams(entries).toString()}`;
}

async function apiKeyGet<T>(path: string, apiKey: string): Promise<T> {
  const resp = await fetch(path, {
    method: 'GET',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey}`,
    },
    cache: 'no-store',
  });
  if (resp.ok) {
    return resp.json() as Promise<T>;
  }
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new ApiError(resp.status, { error: { code: 'http_error', message: `HTTP ${resp.status}` } });
  }
  throw new ApiError(resp.status, payload);
}

// ---- time-series（API-key）----
// from / to 必填（RFC3339），窗口 <= 31 天，granularity ∈ day|week|month。
export function fetchTimeSeries(
  apiKey: string,
  opts: { from: string; to: string; granularity?: UsageGranularity },
): Promise<TimeSeriesResponse> {
  const query = buildQuery({ from: opts.from, to: opts.to, granularity: opts.granularity });
  return apiKeyGet<TimeSeriesResponse>(`/v1/me/analytics/time-series${query}`, apiKey);
}

// ---- usage 明细（API-key，cursor 分页）----
// limit 1..200；cursor 为上一页返回的 next_cursor（不透明 base64）。
export function fetchUsage(
  apiKey: string,
  opts?: { limit?: number; cursor?: string; from?: string; to?: string },
): Promise<MeUsageResponse> {
  const query = buildQuery({
    limit: opts?.limit !== undefined ? String(opts.limit) : undefined,
    cursor: opts?.cursor,
    from: opts?.from,
    to: opts?.to,
  });
  return apiKeyGet<MeUsageResponse>(`/v1/me/usage${query}`, apiKey);
}

// ---- 派生：把 time-series 聚合成图表点 + 汇总 ----

export interface TrendPoint {
  label: string; // X 轴标签（day 字符串）
  cost: number;
  tokens: number;
  requests: number;
}

export interface UsageTotals {
  total_cost: number;
  total_tokens: number;
  total_requests: number;
  today_cost: number;
}

// time-series 的每个桶可能按 model 拆多行，这里按 day 合并成单条折线点。
export function aggregateTimeSeries(resp: TimeSeriesResponse): {
  points: TrendPoint[];
  totals: UsageTotals;
} {
  const byDay = new Map<string, TrendPoint>();
  let totalCost = 0;
  let totalTokens = 0;
  let totalRequests = 0;

  for (const item of resp.items) {
    const cost = Number.parseFloat(item.total_cost) || 0;
    const tokens =
      (item.tokens.input || 0) +
      (item.tokens.output || 0) +
      (item.tokens.cache_read || 0) +
      (item.tokens.cache_creation || 0);
    const requests = item.request_count || 0;

    totalCost += cost;
    totalTokens += tokens;
    totalRequests += requests;

    const existing = byDay.get(item.day);
    if (existing) {
      existing.cost += cost;
      existing.tokens += tokens;
      existing.requests += requests;
    } else {
      byDay.set(item.day, { label: item.day, cost, tokens, requests });
    }
  }

  const points = Array.from(byDay.values()).sort((a, b) => a.label.localeCompare(b.label));
  const todayKey = new Date().toISOString().slice(0, 10);
  const today = byDay.get(todayKey);

  return {
    points,
    totals: {
      total_cost: totalCost,
      total_tokens: totalTokens,
      total_requests: totalRequests,
      today_cost: today ? today.cost : 0,
    },
  };
}

// 默认拉最近 30 天（贴近后端 31 天窗口上限），RFC3339。
export function defaultWindow(days = 30): { from: string; to: string } {
  const to = new Date();
  const from = new Date(to.getTime() - days * 24 * 60 * 60 * 1000);
  return { from: from.toISOString(), to: to.toISOString() };
}

// time-series 后端窗口上限 31 天；UI 选择器据此夹紧（design risk: window_too_large=400）。
export const MAX_TIMESERIES_DAYS = 31;

// 由 YYYY-MM-DD 的本地日期边界算 RFC3339 窗口（from 取当天 00:00，to 取当天 23:59:59.999）。
// 用本地时区起止，避免用户选「今天」却因 UTC 偏移漏掉当天记录。
export function rangeFromDates(fromDate: string, toDate: string): { from: string; to: string } {
  const from = new Date(`${fromDate}T00:00:00`);
  const to = new Date(`${toDate}T23:59:59.999`);
  return { from: from.toISOString(), to: to.toISOString() };
}

// 把 Date 格式成 input[type=date] 需要的 YYYY-MM-DD（本地时区）。
export function toDateInput(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

// ---- 派生：按 requested_model 聚合成「模型维度」统计（Top 模型 + 占比）----
// 来源行为：sub2api getDashboardModels(ModelStat 表) + new-api 消耗分布图（按模型配额占比）。
export interface ModelStatRow {
  model: string;
  cost: number;
  tokens: number;
  requests: number;
  costShare: number; // 0..1，占总花费比例
}

export function aggregateByModel(resp: TimeSeriesResponse): { rows: ModelStatRow[]; totalCost: number } {
  const byModel = new Map<string, { cost: number; tokens: number; requests: number }>();
  let totalCost = 0;
  for (const item of resp.items) {
    const model = item.requested_model || '—';
    const cost = Number.parseFloat(item.total_cost) || 0;
    const tokens =
      (item.tokens.input || 0) +
      (item.tokens.output || 0) +
      (item.tokens.cache_read || 0) +
      (item.tokens.cache_creation || 0);
    const requests = item.request_count || 0;
    totalCost += cost;
    const cur = byModel.get(model);
    if (cur) {
      cur.cost += cost;
      cur.tokens += tokens;
      cur.requests += requests;
    } else {
      byModel.set(model, { cost, tokens, requests });
    }
  }
  const rows = Array.from(byModel.entries())
    .map(([model, v]) => ({
      model,
      cost: v.cost,
      tokens: v.tokens,
      requests: v.requests,
      costShare: totalCost > 0 ? v.cost / totalCost : 0,
    }))
    .sort((a, b) => b.cost - a.cost || b.requests - a.requests);
  return { rows, totalCost };
}

// ---- 导出（session 鉴权）----
// GET /v1/me/usage/export.csv 挂在 r.Route("/v1/me") 下，用 SessionMiddleware（与 quota 一样），
// 不是 API-key；故按账户范围（tenant+user）导出，与所选 hk_ key 无关。
// 参数：format=csv|json（默认 csv）、from+to 必填 RFC3339、窗口 <= 366 天。
export type ExportFormat = 'csv' | 'json';

// 触发浏览器下载。session token 需放进 Authorization 头，无法用裸 <a href>，故走 blob。
export async function downloadUsageExport(opts: { from: string; to: string; format?: ExportFormat }): Promise<void> {
  const format = opts.format ?? 'csv';
  const query = buildQuery({ from: opts.from, to: opts.to, format });
  const token = getSessionToken();
  const resp = await fetch(`/v1/me/usage/export.csv${query}`, {
    method: 'GET',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    cache: 'no-store',
  });
  if (!resp.ok) {
    let payload: APIError;
    try {
      payload = (await resp.json()) as APIError;
    } catch {
      throw new ApiError(resp.status, { error: { code: 'http_error', message: `HTTP ${resp.status}` } });
    }
    throw new ApiError(resp.status, payload);
  }
  const truncated = resp.headers.get('X-Truncated') === 'true';
  const blob = await resp.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  const stamp = new Date().toISOString().slice(0, 10);
  a.download = `huakai-usage-${stamp}.${format}`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
  if (truncated) {
    // 行数被后端截断（达到 maxRows）——抛出可识别错误让页面提示，但文件已下载。
    throw new ApiError(200, {
      error: { code: 'export_truncated', message: '导出行数已达上限，文件仅包含部分记录。请缩小时间范围。' },
    });
  }
}
