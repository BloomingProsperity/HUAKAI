import { apiSend } from '../../lib/api'
import type { ModelSyncRequest, ModelSyncResult } from './types'

/*
 * 厂商模型同步数据访问层。端点 POST /admin/v1/model-sync。
 * 路径以 /admin/ 开头,api.ts 的 tokenForPath 会自动注入 admin token(无需手动传 bearer)。
 * 请求体 reason 可选;后端约束 ≤200 字符,空串时后端兜底 admin_manual。
 */
const PATH = '/admin/v1/model-sync'

export async function triggerModelSync(
  body: ModelSyncRequest,
  signal?: AbortSignal,
): Promise<ModelSyncResult> {
  return apiSend<ModelSyncResult>('POST', PATH, body, { signal })
}
