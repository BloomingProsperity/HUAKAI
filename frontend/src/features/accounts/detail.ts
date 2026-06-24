import type { ProviderAccount } from './types'

/*
 * 账号详情的纯逻辑(可单测,无 React/网络):据账号当前态推导可用运维动作。
 * 关键契约:清除限流仅在账号【确实处于限流态】时可用——否则按钮误导运维(且后端
 * clear-rate-limit 对非限流账号无意义)。判定为限流态 = 有 rate_limited_at 时间戳,或
 * health_state 标记为 rate_limited。
 */
export interface AccountActions {
  /** 当前是否限流态(决定是否展示"清除限流")。 */
  isRateLimited: boolean
  /** 启停按钮的目标动作:已启用→可停用,已停用→可启用。 */
  toggleTo: 'enable' | 'disable'
}

export function accountAvailableActions(a: ProviderAccount): AccountActions {
  const isRateLimited = a.rate_limited_at != null || a.health_state === 'rate_limited'
  return {
    isRateLimited,
    toggleTo: a.enabled ? 'disable' : 'enable',
  }
}
