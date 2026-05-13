/**
 * HUAKAI 仪表盘 P1 模拟数据
 * 基于 2026-05-12 简报
 */

export interface UsageSummary {
  input_tokens: number;
  output_tokens: number;
  cache_tokens: number;
  cost_usd: number;
  cost_rmb: number;
  request_count: number;
  latency_p50: number;
  latency_p95: number;
  latency_p99: number;
  in_flight: number;
  total_cap_concurrency: number;
  cache_hit_ratio: number;
  health_stats: {
    healthy: number;
    degraded: number;
    failed: number;
    total: number;
  };
}

export type QuotaStatus = 'active' | 'exhausted';

export interface ProviderAccountMock {
  id: string;
  name: string;
  provider: string;
  health_state: 'operational' | 'degraded' | 'failed' | 'cooling_down';
  in_flight: number;
  cap: number;
  quota_status: QuotaStatus;
  last_dispatch_at: string;
}

export interface ChartDataPoint {
  time: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  requests: number;
  hit_rate: number;
  ratio: number;
  latency_p95: number;
}

export const MOCK_USAGE: UsageSummary = {
  input_tokens: 1245080,
  output_tokens: 854200,
  cache_tokens: 320100,
  cost_usd: 42.58,
  cost_rmb: 304.45,
  request_count: 8542,
  latency_p50: 450,
  latency_p95: 1200,
  latency_p99: 2500,
  in_flight: 12,
  total_cap_concurrency: 40,
  cache_hit_ratio: 0.38,
  health_stats: {
    healthy: 18,
    degraded: 2,
    failed: 1,
    total: 21,
  },
};

export const MOCK_PROVIDER_ACCOUNTS: ProviderAccountMock[] = [
  {
    id: 'pa-001',
    name: 'anthropic-pro-01',
    provider: 'anthropic',
    health_state: 'operational',
    in_flight: 2,
    cap: 4,
    quota_status: 'active',
    last_dispatch_at: '2026-05-12T10:30:00Z',
  },
  {
    id: 'pa-002',
    name: 'openai-plus-team',
    provider: 'openai',
    health_state: 'degraded',
    in_flight: 4,
    cap: 4,
    quota_status: 'active',
    last_dispatch_at: '2026-05-12T10:31:05Z',
  },
  {
    id: 'pa-003',
    name: 'gemini-adv-01',
    provider: 'google',
    health_state: 'operational',
    in_flight: 0,
    cap: 2,
    quota_status: 'active',
    last_dispatch_at: '2026-05-12T10:28:45Z',
  },
  {
    id: 'pa-004',
    name: 'azure-gpt4-east',
    provider: 'azure',
    health_state: 'failed',
    in_flight: 0,
    cap: 10,
    quota_status: 'exhausted',
    last_dispatch_at: '2026-05-12T09:15:20Z',
  },
  {
    id: 'pa-005',
    name: 'anthropic-pro-02',
    provider: 'anthropic',
    health_state: 'operational',
    in_flight: 1,
    cap: 4,
    quota_status: 'active',
    last_dispatch_at: '2026-05-12T10:32:12Z',
  },
];

const MOCK_CHART_POINTS: Array<Omit<ChartDataPoint, 'ratio'>> = [
  { time: '00:00', input_tokens: 42000, output_tokens: 28000, cache_read_tokens: 9000, requests: 210, hit_rate: 31, latency_p95: 1420 },
  { time: '01:00', input_tokens: 38000, output_tokens: 24000, cache_read_tokens: 8600, requests: 184, hit_rate: 34, latency_p95: 1360 },
  { time: '02:00', input_tokens: 31000, output_tokens: 21000, cache_read_tokens: 7200, requests: 152, hit_rate: 33, latency_p95: 1290 },
  { time: '03:00', input_tokens: 29000, output_tokens: 19000, cache_read_tokens: 6800, requests: 139, hit_rate: 35, latency_p95: 1240 },
  { time: '04:00', input_tokens: 33000, output_tokens: 22000, cache_read_tokens: 7600, requests: 160, hit_rate: 36, latency_p95: 1180 },
  { time: '05:00', input_tokens: 46000, output_tokens: 30000, cache_read_tokens: 11200, requests: 228, hit_rate: 38, latency_p95: 1160 },
  { time: '06:00', input_tokens: 61000, output_tokens: 39000, cache_read_tokens: 15800, requests: 310, hit_rate: 42, latency_p95: 1210 },
  { time: '07:00', input_tokens: 76000, output_tokens: 51000, cache_read_tokens: 19100, requests: 408, hit_rate: 41, latency_p95: 1260 },
  { time: '08:00', input_tokens: 93000, output_tokens: 65000, cache_read_tokens: 24400, requests: 520, hit_rate: 43, latency_p95: 1320 },
  { time: '09:00', input_tokens: 118000, output_tokens: 83000, cache_read_tokens: 31800, requests: 642, hit_rate: 45, latency_p95: 1380 },
  { time: '10:00', input_tokens: 137000, output_tokens: 96000, cache_read_tokens: 37000, requests: 728, hit_rate: 46, latency_p95: 1440 },
  { time: '11:00', input_tokens: 149000, output_tokens: 103000, cache_read_tokens: 39800, requests: 760, hit_rate: 44, latency_p95: 1490 },
  { time: '12:00', input_tokens: 141000, output_tokens: 99000, cache_read_tokens: 38900, requests: 706, hit_rate: 43, latency_p95: 1460 },
  { time: '13:00', input_tokens: 152000, output_tokens: 108000, cache_read_tokens: 42600, requests: 782, hit_rate: 45, latency_p95: 1410 },
  { time: '14:00', input_tokens: 166000, output_tokens: 119000, cache_read_tokens: 47400, requests: 846, hit_rate: 48, latency_p95: 1370 },
  { time: '15:00', input_tokens: 159000, output_tokens: 113000, cache_read_tokens: 46100, requests: 810, hit_rate: 47, latency_p95: 1330 },
  { time: '16:00', input_tokens: 174000, output_tokens: 124000, cache_read_tokens: 50600, requests: 872, hit_rate: 49, latency_p95: 1300 },
  { time: '17:00', input_tokens: 161000, output_tokens: 116000, cache_read_tokens: 48200, requests: 830, hit_rate: 46, latency_p95: 1280 },
  { time: '18:00', input_tokens: 132000, output_tokens: 91000, cache_read_tokens: 35200, requests: 690, hit_rate: 42, latency_p95: 1230 },
  { time: '19:00', input_tokens: 116000, output_tokens: 78000, cache_read_tokens: 28600, requests: 604, hit_rate: 39, latency_p95: 1200 },
  { time: '20:00', input_tokens: 98000, output_tokens: 66000, cache_read_tokens: 23800, requests: 512, hit_rate: 38, latency_p95: 1170 },
  { time: '21:00', input_tokens: 86000, output_tokens: 56000, cache_read_tokens: 20600, requests: 458, hit_rate: 37, latency_p95: 1150 },
  { time: '22:00', input_tokens: 70000, output_tokens: 47000, cache_read_tokens: 16600, requests: 370, hit_rate: 36, latency_p95: 1180 },
  { time: '23:00', input_tokens: 52000, output_tokens: 35000, cache_read_tokens: 12200, requests: 282, hit_rate: 35, latency_p95: 1210 },
];

export const MOCK_CHART_DATA: ChartDataPoint[] = MOCK_CHART_POINTS.map((point) => ({
  ...point,
  ratio: point.hit_rate / 100,
}));
