import type { ApiKeyView } from './types'

/*
 * Key 批量撤销纯逻辑(可单测)。POST /v1/api-keys/batch-revoke {ids,reason} → {revoked[],not_found[]}。
 * 后端约束:ids 须 1-200 条。仅活跃 Key 可选。
 */
export const BATCH_REVOKE_MAX = 200

export interface BatchRevokeBody {
  ids: number[]
  reason: string
}

export interface BatchRevokeResult {
  revoked: number[]
  not_found: number[]
}

/** 仅活跃 Key 可被选中(已撤销/过期的不参与批量撤销)。 */
export function isSelectable(k: ApiKeyView): boolean {
  return k.status === 'active'
}

/** 切换选中:返回新 Set(不可变更新,便于 React 状态)。 */
export function toggleSelected(selected: ReadonlySet<number>, id: number): Set<number> {
  const next = new Set(selected)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  return next
}

/** 切换当前页全选；全已选时仅清掉本页，否则补齐本页可选项。 */
export function togglePageSelection(selected: ReadonlySet<number>, pageIds: number[]): Set<number> {
  const next = new Set(selected)
  const allSelected = pageIds.length > 0 && pageIds.every((id) => next.has(id))
  for (const id of pageIds) {
    if (allSelected) next.delete(id)
    else next.add(id)
  }
  return next
}

export type BatchBuildResult = BatchRevokeBody | { error: string }

/** 构造批量撤销请求:守卫数量(1-200);reason 透传(去首尾空白)。 */
export function buildBatchRevoke(ids: number[], reason: string): BatchBuildResult {
  if (ids.length === 0) return { error: '请先勾选要撤销的 Key' }
  if (ids.length > BATCH_REVOKE_MAX) return { error: `单次最多撤销 ${BATCH_REVOKE_MAX} 个,请分批操作` }
  return { ids, reason: reason.trim() }
}

/** 结果汇总文案:撤销 N 个;若有未找到的另行提示。 */
export function summarizeBatchResult(resp: BatchRevokeResult): string {
  const ok = resp.revoked?.length ?? 0
  const miss = resp.not_found?.length ?? 0
  const base = `已撤销 ${ok} 个 Key`
  return miss > 0 ? `${base}(${miss} 个未找到/已失效)` : base
}
