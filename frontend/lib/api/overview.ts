// 用户门户「概览」落地页数据层。聚合多个已封装端点，**不新增端点**，
// 只 import 复用前批次 lib（usage / account / billing / apiKeys）里已按后端 handler 真码确认的函数。
//
// 鉴权轨混合（与各源 lib 一致，已读 handler 真码确认）：
//   余额   GET /v1/users/me/payments/balance   —— session（paymenthttp newUserBalanceHandler）→ billing.getBalance
//   额度   GET /v1/me/quota                     —— session（mequotahttp）→ account.fetchQuota
//   Key 数 GET /v1/api-keys                     —— session（userkeyhttp listResponse）→ apiKeys.listApiKeys
//   签到   GET /v1/me/checkin                    —— session（checkinhttp newStatusHandler）→ account.fetchCheckinStatus
//   POST  /v1/me/checkin                        —— session（checkinhttp newPostHandler）→ account.doCheckin
//   趋势   GET /v1/me/analytics/time-series      —— API-key（hk_）（usageanalyticshttp）→ usage.fetchTimeSeries
//
// 因此趋势这一路必须用客户 hk_ key 作 Bearer（与 Playground / 用量页共用 localStorage huakai_api_key）；
// 其余四路走 session。任一 session 路失败不连累其它路：各路独立 try/catch，缺失即降级为「未取到」。
//
// CLEAN-ROOM（CLAUDE.md §11/§12）：落地页的「Stats 行 + 趋势图 + 最近用量 + 快捷入口」聚合形态
// 借鉴 sub2api 用户 DashboardView（views/user/DashboardView.vue 组装 UserDashboardStats /
// UserDashboardCharts / UserDashboardRecentUsage / UserDashboardQuickActions 的布局；
// 其 getDashboardStats 暴露 balance/total_api_keys/today_requests/today_cost/today_tokens 这组字段）。
// 仅提取功能/字段/布局形态，未抄码；字段名/单位一律以 HUAKAI handler 真码为准。
// Source files read: refs/sub2api/frontend/src/views/user/DashboardView.vue、
//   components/user/dashboard/{UserDashboardStats,UserDashboardQuickActions}.vue（功能/布局借鉴，非抄码）。

import { getBalance, type Balance } from './billing';
import { fetchQuota, fetchCheckinStatus, type MeQuotaWindow, type CheckinStatus } from './account';
import { listApiKeys, type ApiKeyView } from './apiKeys';
import {
  aggregateTimeSeries,
  defaultWindow,
  fetchTimeSeries,
  getStoredApiKey,
  type TrendPoint,
  type UsageTotals,
} from './usage';

// 趋势默认窗口（天）。贴近后端 31 天上限，落地页只看近 7 天更轻量。
const TREND_DAYS = 7;

// 单路结果信封：拿到即 data，失败即 error（友好信息留给页面 friendlyMessage 渲染前的占位）。
// 用 ok 区分「真未取到」与「取到空值（如余额 0）」，空态展示才不会误判。
export interface OverviewSection<T> {
  ok: boolean;
  data: T | null;
}

export interface OverviewSnapshot {
  balance: OverviewSection<Balance>;
  quota: OverviewSection<MeQuotaWindow[]>;
  apiKeys: OverviewSection<{ total: number; active: number }>;
  checkin: OverviewSection<CheckinStatus>;
  // 趋势依赖 hk_ key；hasKey=false 时不视为错误，仅提示去填 key。
  trend: OverviewSection<{ points: TrendPoint[]; totals: UsageTotals }> & { hasKey: boolean };
}

async function section<T>(fn: () => Promise<T>): Promise<OverviewSection<T>> {
  try {
    return { ok: true, data: await fn() };
  } catch {
    return { ok: false, data: null };
  }
}

// 拉取全部概览数据。各路独立失败隔离，任何一路挂掉不影响其它卡片显示真实数据。
export async function loadOverview(): Promise<OverviewSnapshot> {
  const apiKey = getStoredApiKey();

  const [balance, quota, apiKeys, checkin, trend] = await Promise.all([
    section(async () => (await getBalance()).balance),
    section(async () => (await fetchQuota()).items),
    section(async () => {
      const resp = await listApiKeys();
      const active = resp.api_keys.filter((k: ApiKeyView) => k.status === 'active').length;
      return { total: resp.count, active };
    }),
    section(() => fetchCheckinStatus()),
    loadTrendSection(apiKey),
  ]);

  return { balance, quota, apiKeys, checkin, trend };
}

async function loadTrendSection(
  apiKey: string,
): Promise<OverviewSection<{ points: TrendPoint[]; totals: UsageTotals }> & { hasKey: boolean }> {
  if (!apiKey) {
    return { ok: true, hasKey: false, data: null };
  }
  try {
    const win = defaultWindow(TREND_DAYS);
    const ts = await fetchTimeSeries(apiKey, { ...win, granularity: 'day' });
    const agg = aggregateTimeSeries(ts);
    return { ok: true, hasKey: true, data: { points: agg.points, totals: agg.totals } };
  } catch {
    return { ok: false, hasKey: true, data: null };
  }
}

// ---- 展示派生（纯前端，不新增端点） ----

// 取「总额」窗口的剩余额度（window_kind=total）；没有 total 窗口时回退首个有上限的窗口。
export function pickPrimaryQuota(windows: MeQuotaWindow[]): MeQuotaWindow | null {
  if (windows.length === 0) return null;
  const total = windows.find((w) => w.window_kind === 'total');
  if (total) return total;
  const withCap = windows.find((w) => Number.parseFloat(w.cap) > 0);
  return withCap ?? windows[0];
}
