import type { RiskCard, RiskOverview } from './types'

/*
 * 风控总览纯逻辑(与 React/DOM 解耦,便于 vitest 变异测试)。
 */

// 单租户部署默认租户 1;顶栏可改(平台 admin 看他租户需显式指定)。
export const DEFAULT_TENANT_ID = 1

/**
 * buildRiskCards 把后端计数映射为 4 张卡片。tone 规则:计数 > 0 即 'alert'(需关注),
 * 否则 'ok'。每张卡带一个「去处理」链接,指向已有运维页(本切片只读、不内嵌动作)。
 */
export function buildRiskCards(ov: RiskOverview): RiskCard[] {
  const tone = (n: number): 'ok' | 'alert' => (n > 0 ? 'alert' : 'ok')
  return [
    {
      key: 'disabled_keys',
      label: '已禁用 API Key',
      count: ov.disabled_keys,
      tone: tone(ov.disabled_keys),
      actionPath: '/admin/moderation',
      actionLabel: '内容审核台',
    },
    {
      key: 'firing_alerts',
      label: '触发中告警',
      count: ov.firing_alerts,
      tone: tone(ov.firing_alerts),
      actionPath: '/admin/alerting',
      actionLabel: '告警控制台',
    },
    {
      key: 'disabled_users',
      label: '已封禁用户',
      count: ov.disabled_users,
      tone: tone(ov.disabled_users),
      actionPath: '/users',
      actionLabel: '用户与权限',
    },
    {
      key: 'ip_blacklisted_keys',
      label: 'IP 黑名单 Key',
      count: ov.ip_blacklisted_keys,
      tone: tone(ov.ip_blacklisted_keys),
      actionPath: '/keys',
      actionLabel: '密钥管理',
    },
  ]
}

/** totalRiskSignals 汇总所有风控信号计数(用于页头一眼看总量)。 */
export function totalRiskSignals(ov: RiskOverview): number {
  return ov.disabled_keys + ov.firing_alerts + ov.disabled_users + ov.ip_blacklisted_keys
}

/** 解析顶栏租户输入:正整数则取之,否则回退默认租户。 */
export function parseTenantInput(raw: string): number {
  const v = Number.parseInt(raw, 10)
  return Number.isInteger(v) && v > 0 ? v : DEFAULT_TENANT_ID
}
