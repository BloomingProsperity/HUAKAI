// 运营总览 admin 数据层 —— 全部走管理 token（apiGet from client.ts）。
// 后端: routes_usageadmin /v1/admin/usage/* + routes_alerting /v1/admin/alert-*。
// 形状逐字段对照后端 handler（usageanalyticshttp / alertinghttp）真码。
import { apiGet, apiPostNoContent } from './client';
import {
  buildByBucketParams,
  buildCountsParams,
  type PerfBucket,
} from './usage-analytics-form';

// ---- 时间窗口 ----
// 后端 parseLeaderboardWindow 接受形如 24h / 7d / 30d 的正持续时间，>90d 截断到 90d。
export type UsageWindow = '24h' | '7d' | '30d';

export const USAGE_WINDOWS: { value: UsageWindow; label: string }[] = [
  { value: '24h', label: '近 24 小时' },
  { value: '7d', label: '近 7 天' },
  { value: '30d', label: '近 30 天' },
];

// ---- 用量概览 GET /v1/admin/usage/overview ----
// overview_handler.go: overviewResponse{window, totals, trend}
export interface OverviewTotals {
  requests: number;
  total_cost: string; // StringFixed(8)
  total_tokens: number;
  active_users: number;
  active_api_keys: number;
  success_rate: string; // StringFixed(4)，0~1
}

export interface OverviewTrendPoint {
  day: string; // YYYY-MM-DD
  requests: number;
  cost: string; // StringFixed(8)
}

export interface OverviewResponse {
  window: string;
  totals: OverviewTotals;
  trend: OverviewTrendPoint[];
}

export function getUsageOverview(window: UsageWindow): Promise<OverviewResponse> {
  return apiGet<OverviewResponse>('/v1/admin/usage/overview', { window });
}

// ---- 排行榜 GET /v1/admin/usage/leaderboard ----
// leaderboard_handler.go: by ∈ user|model|provider_account|api_key（api_key 需 tenant_id）。
export type LeaderboardBy = 'user' | 'model' | 'provider_account' | 'api_key';

export interface LeaderboardEntry {
  rank: number;
  key: string;
  total_cost: string; // StringFixed(8)
  total_tokens: number;
  request_count: number;
}

export interface LeaderboardResponse {
  window: string;
  by: string;
  entries: LeaderboardEntry[];
}

export function getUsageLeaderboard(
  by: LeaderboardBy,
  window: UsageWindow,
  opts?: { limit?: number; tenant_id?: number },
): Promise<LeaderboardResponse> {
  return apiGet<LeaderboardResponse>('/v1/admin/usage/leaderboard', {
    by,
    window,
    limit: opts?.limit,
    tenant_id: by === 'api_key' ? opts?.tenant_id : undefined,
  });
}

// ---- 性能排行 GET /v1/admin/usage/performance ----
// performance_handler.go: by ∈ model|provider_account；无成本字段。
export type PerformanceBy = 'model' | 'provider_account';

export interface PerformanceEntry {
  rank: number;
  key: string;
  avg_ttft_ms: string; // StringFixed(4)
  avg_tps: string; // StringFixed(4)
  request_count: number;
  error_rate: string; // StringFixed(4)，0~1
}

export interface PerformanceResponse {
  window: string;
  by: string;
  entries: PerformanceEntry[];
}

export function getUsagePerformance(
  by: PerformanceBy,
  window: UsageWindow,
  opts?: { limit?: number },
): Promise<PerformanceResponse> {
  return apiGet<PerformanceResponse>('/v1/admin/usage/performance', {
    by,
    window,
    limit: opts?.limit,
  });
}

// ---- 性能指标摘要 GET /v1/admin/usage/perf-metrics/summary ----
// perf_metrics_handler.go: latency_percentiles_ms{p50,p95,p99} 为毫秒 float。
export interface PerfMetricsSummary {
  avg_ttft_ms: string;
  avg_tps: string;
  request_count: number;
  error_count: number;
  error_rate: string;
}

export interface LatencyPercentiles {
  p50: number;
  p95: number;
  p99: number;
}

export interface PerfMetricsSummaryResponse {
  window: string;
  model?: string;
  summary: PerfMetricsSummary;
  latency_percentiles_ms: LatencyPercentiles;
}

export function getPerfMetricsSummary(
  window: UsageWindow,
  opts?: { model?: string },
): Promise<PerfMetricsSummaryResponse> {
  return apiGet<PerfMetricsSummaryResponse>('/v1/admin/usage/perf-metrics/summary', {
    window,
    model: opts?.model,
  });
}

// ---- 健康分 GET /v1/admin/usage/health-score ----
// perf_metrics_handler.go: 0-100 综合分，含业务/基础设施子分与信号。
export interface HealthScoreSignals {
  error_rate: string;
  ttft_p99_ms: number;
}

export interface HealthScoreResponse {
  window: string;
  overall_score: number;
  business_score: number;
  infra_score: number;
  signals: HealthScoreSignals;
}

export function getHealthScore(window: UsageWindow): Promise<HealthScoreResponse> {
  return apiGet<HealthScoreResponse>('/v1/admin/usage/health-score', { window });
}

// ---- 性能分桶 GET /v1/admin/usage/perf-metrics/by-bucket ----
// perf_metrics_handler.go: 按 hour|day 桶 + 按模型聚合 TTFT/TPS/错误率。
export interface PerfMetricsBucketEntry {
  bucket: string; // 桶时间戳标签
  key: string; // 模型名
  avg_ttft_ms: string;
  avg_tps: string;
  request_count: number;
  error_count: number;
  error_rate: string;
}

export interface PerfMetricsBucketResponse {
  window: string;
  bucket: string; // hour|day
  by: string; // model
  entries: PerfMetricsBucketEntry[];
}

export function getPerfMetricsByBucket(
  bucket: PerfBucket,
  window: UsageWindow,
  opts?: { limit?: number },
): Promise<PerfMetricsBucketResponse> {
  return apiGet<PerfMetricsBucketResponse>(
    '/v1/admin/usage/perf-metrics/by-bucket',
    buildByBucketParams(bucket, window, opts),
  );
}

// ---- 按提供商账号用量 GET /v1/admin/usage/provider-account-counts ----
// provider_account_counts_handler.go: from/to(RFC3339)必填、tenant_id 可选；只读聚合，不触结算。
export interface ProviderAccountCount {
  provider_account_id: number;
  request_count: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cost: string;
}

export interface ProviderAccountCountsResponse {
  from: string;
  to: string;
  counts: ProviderAccountCount[];
}

export function getProviderAccountCounts(
  from: string,
  to: string,
  opts?: { tenant_id?: number },
): Promise<ProviderAccountCountsResponse> {
  return apiGet<ProviderAccountCountsResponse>(
    '/v1/admin/usage/provider-account-counts',
    buildCountsParams(from, to, opts),
  );
}

// ---- 告警事件 GET /v1/admin/alert-events ----
// alertinghttp event_handlers.go: eventListResponse{object, items, limit, offset}。
// tenant_id：tenant-operator 可省（用其 scope），平台 admin 必填正整数。
export type AlertEventState = 'firing' | 'resolved' | string;

export interface AlertEvent {
  id: number;
  tenant_id: number;
  rule_id: number;
  state: string;
  observed_value: number;
  threshold_value?: number;
  metric_value?: number;
  dimensions?: Record<string, string>;
  fired_at: string;
  resolved_at?: string;
  email_sent: boolean;
}

export interface AlertEventListResponse {
  object: string;
  items: AlertEvent[];
  limit: number;
  offset: number;
}

export function listAlertEvents(opts?: {
  tenant_id?: number;
  rule_id?: number;
  state?: AlertEventState;
  limit?: number;
  offset?: number;
}): Promise<AlertEventListResponse> {
  return apiGet<AlertEventListResponse>('/v1/admin/alert-events', {
    tenant_id: opts?.tenant_id,
    rule_id: opts?.rule_id,
    state: opts?.state,
    limit: opts?.limit,
    offset: opts?.offset,
  });
}

// ---- 告警规则 GET /v1/admin/alert-rules ----
// alertinghttp rule_handlers.go: ruleListResponse{object, items, limit, offset}。
export interface AlertRule {
  id: number;
  tenant_id: number;
  name: string;
  metric: string;
  metric_type?: string;
  comparator: string;
  threshold: number;
  severity: string;
  window_seconds: number;
  sustained_seconds: number;
  cooldown_seconds: number;
  notify_email: boolean;
  filters?: Record<string, string>;
  last_triggered_at?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface AlertRuleListResponse {
  object: string;
  items: AlertRule[];
  limit: number;
  offset: number;
}

export function listAlertRules(opts?: {
  tenant_id?: number;
  limit?: number;
  offset?: number;
}): Promise<AlertRuleListResponse> {
  return apiGet<AlertRuleListResponse>('/v1/admin/alert-rules', {
    tenant_id: opts?.tenant_id,
    limit: opts?.limit,
    offset: opts?.offset,
  });
}

// ---- 手动消解告警事件 POST /v1/admin/alert-events/{id}/manual-resolve ----
// 返回更新后的 event（200）；这里仅用 NoContent 包装触发，调用方刷新列表。
export function manualResolveAlertEvent(id: number, opts?: { tenant_id?: number }): Promise<void> {
  const qs = opts?.tenant_id ? `?tenant_id=${opts.tenant_id}` : '';
  return apiPostNoContent(`/v1/admin/alert-events/${id}/manual-resolve${qs}`);
}
