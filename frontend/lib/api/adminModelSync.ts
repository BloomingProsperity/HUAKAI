// admin 模型目录同步触发 API 封装（管理 token 轨）。
// 端点形状以后端真码 model_sync_handler.go / cmd/gateway/routes.go 为准（禁止凭记忆）：
//   POST /admin/v1/model-sync   body {reason?}（≤200 码点，空→后端默认 admin_manual）
//     → 200 {object,completed_at,total_added,total_updated,total_disabled,results[]}
//   鉴权 platform_admin only（全局目录，影响所有继承 global catalog 的租户）；【无 tenant_id】。
//   错误：503 gateway_not_configured / model_sync_failed、400 invalid_json / invalid_reason、401/403。
//
// 仅需 POST，故直接复用 client.ts 的 apiPost（其 buildHeaders 已注入 huakai_admin_token → Bearer，
// 与 /admin/v1 轨一致），不自带 PUT/DELETE 助手、不拼 tenantQuery。

import { apiPost } from './client';
import { buildModelSyncBody } from './model-sync-form';

// ---- 类型（对齐 modelSyncResponseBody / modelSyncResultItemBody）----

export interface ModelSyncResultItem {
  vendor: string;
  added: number;
  updated: number;
  reactivated: number;
  disabled: number;
  unchanged: number;
  snapshot_bumps: number;
}

export interface ModelSyncResult {
  object: string;
  completed_at: string; // RFC3339 UTC
  total_added: number;
  total_updated: number;
  total_disabled: number;
  results: ModelSyncResultItem[];
}

// ---- 触发 ----

// triggerModelSync — POST /admin/v1/model-sync。reason 经 buildModelSyncBody 构造（空则省略键）。
export function triggerModelSync(reason: string): Promise<ModelSyncResult> {
  return apiPost<ModelSyncResult>('/admin/v1/model-sync', buildModelSyncBody(reason));
}
