// 自助 /quota 多维窗口的纯展示逻辑（零依赖 strip-types 单测）。
// 后端 mequotahttp 现按 metric（requests/cost_usd/tokens_estimated）各返一个窗口，
// 所以同一 window_kind 会出现多次：React list key 必须含 metric（否则键冲突），
// 且每行需按 metric 标注以区分三个维度。concurrency 不在内（slot 模型，已延后）。
import type { QuotaWindow } from './usage';

export const QUOTA_METRIC_LABELS: Record<string, string> = {
  requests: '请求数',
  cost_usd: '费用 (USD)',
  tokens_estimated: 'Token',
};

export function quotaMetricLabel(metric: string): string {
  return QUOTA_METRIC_LABELS[metric] ?? metric;
}

// quotaWindowKey：多维下同一 window_kind 重复出现，单用 window_kind 会让 React 键冲突
// （三行只渲一行 / 报 duplicate key 警告）。metric + window_kind 组合保证唯一。
export function quotaWindowKey(w: Pick<QuotaWindow, 'metric' | 'window_kind'>): string {
  return `${w.metric}::${w.window_kind}`;
}
