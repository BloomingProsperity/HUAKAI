import type { AccountState } from './types'

/*
 * 列表查询的纯逻辑(可单测,无 React/网络依赖):把筛选状态构造成后端
 * GET /admin/v1/provider-accounts 的 query 参数。后端只认 limit/cursor/pool_group_id/
 * state_filter/tag;空筛选项必须【省略】(不能传空串,否则触发后端 invalid_state_filter
 * 之类的 400)。这是本模块唯一值得单测的逻辑——故抽成纯函数。
 */

/** state_filter 下拉选项(值与后端枚举一致;空=全部)。 */
export const ACCOUNT_STATE_OPTIONS: ReadonlyArray<{ value: '' | AccountState; label: string }> = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '正常' },
  { value: 'error', label: '异常' },
  { value: 'disabled', label: '已禁用' },
  { value: 'rate_limited', label: '限流中' },
  { value: 'overloaded', label: '过载' },
  { value: 'temp_unschedulable', label: '临时停调' },
]

export const ACCOUNTS_PAGE_LIMIT = 50
export const ACCOUNTS_PAGE_LIMIT_MAX = 200

export interface AccountListFilters {
  stateFilter: '' | AccountState
  poolGroupId: string
  tag: string
  cursor: string | null
  limit: number
}

export const EMPTY_ACCOUNT_FILTERS: AccountListFilters = {
  stateFilter: '',
  poolGroupId: '',
  tag: '',
  cursor: null,
  limit: ACCOUNTS_PAGE_LIMIT,
}

/**
 * 把筛选状态构造成后端 query。空项一律省略;limit 夹紧到 [1, 200];cursor 仅在非空时带。
 * 返回的对象直接喂给 apiGet 的 query 选项(undefined 值会被 apiGet 跳过)。
 */
export function buildAccountListQuery(
  f: AccountListFilters,
): Record<string, string | number | undefined> {
  const limit = Math.min(Math.max(Math.trunc(f.limit) || ACCOUNTS_PAGE_LIMIT, 1), ACCOUNTS_PAGE_LIMIT_MAX)
  return {
    limit,
    state_filter: f.stateFilter || undefined,
    pool_group_id: f.poolGroupId.trim() || undefined,
    tag: f.tag.trim() || undefined,
    cursor: f.cursor || undefined,
  }
}
