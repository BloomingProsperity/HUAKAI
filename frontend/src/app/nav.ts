/*
 * 登录后三壳导航。公开页是壳外独立路由，不进入侧栏。
 * 导航只负责信息架构与展示元数据；路由注册和后端授权边界保持独立。
 */
export interface NavItem {
  path: string
  label: string
  icon: string
  /** 该域是否已有实现页(false=占位“建设中”)。 */
  built: boolean
}

export type Shell = 'user' | 'operator'

export interface NavSection {
  key: string
  /** 仅兼容旧占位页；侧栏不生成或展示分组编号。 */
  stage?: number
  /** 所属壳：用户门户或运营台。 */
  shell: Shell
  label: string
  /** 独立入口显示在所有分组标题之前。 */
  standalone?: boolean
  items: NavItem[]
}

export const SHELL_LABEL: Record<Shell, string> = {
  user: '用户门户',
  operator: '运营台',
}

export const PIPELINE_NAV: NavSection[] = [
  // ── 用户门户壳(session token，客户自助) ──
  {
    key: 'user-overview',
    shell: 'user',
    label: '概览',
    standalone: true,
    items: [{ path: '/overview', label: '概览', icon: '⌂', built: true }],
  },
  {
    key: 'user-account',
    shell: 'user',
    label: '我的账户',
    items: [
      { path: '/keys', label: '我的密钥', icon: '◇', built: true },
      { path: '/profile', label: '个人资料与安全', icon: '◉', built: true },
      { path: '/notifications', label: '站内信', icon: '◌', built: true },
      { path: '/activity', label: '安全日志', icon: '≋', built: true },
    ],
  },
  {
    key: 'user-usage-billing',
    shell: 'user',
    label: '用量与计费',
    items: [
      { path: '/usage', label: '用量与日志', icon: '▥', built: true },
      { path: '/usage-records', label: '用量明细', icon: '≡', built: true },
      { path: '/wallet', label: '钱包与充值', icon: '◈', built: true },
      { path: '/orders', label: '我的订单', icon: '□', built: true },
      { path: '/subscriptions', label: '订阅套餐', icon: '◆', built: true },
      { path: '/redeem', label: '兑换码', icon: '◇', built: true },
      { path: '/my-groups', label: '分组与倍率', icon: '⊙', built: true },
    ],
  },
  {
    key: 'user-more',
    shell: 'user',
    label: '更多',
    items: [
      { path: '/integration', label: '接入指引', icon: '↗', built: true },
      { path: '/playground', label: '在线调试台', icon: '▷', built: true },
      { path: '/media-tasks', label: '媒体任务', icon: '▣', built: true },
      { path: '/available-channels', label: '可用渠道', icon: '⌁', built: true },
      { path: '/trust', label: '信任验证', icon: '✓', built: true },
      { path: '/checkin', label: '每日签到', icon: '☑', built: true },
      { path: '/affiliate', label: '推广返利', icon: '♧', built: true },
    ],
  },

  // ── 运营台壳(admin token，平台运营) ──
  {
    key: 'operator-overview',
    shell: 'operator',
    label: '概览',
    standalone: true,
    items: [{ path: '/ops', label: '概览', icon: '⌂', built: true }],
  },
  {
    key: 'gateway-resources',
    shell: 'operator',
    label: '网关资源',
    items: [
      { path: '/models', label: '模型服务', icon: '◇', built: true },
      { path: '/admin/model-registry', label: '模型注册', icon: '◆', built: true },
      { path: '/admin/channel-health', label: '渠道健康', icon: '♥', built: true },
      { path: '/accounts', label: '上游账号', icon: '◉', built: true },
      { path: '/routing', label: '账号池', icon: '⊙', built: true },
      { path: '/admin/route-rules', label: '路由规则', icon: '⇄', built: true },
      { path: '/admin/quota-policies', label: '流量控制', icon: '◫', built: true },
      { path: '/admin/groups', label: '分组管理', icon: '▦', built: true },
      { path: '/admin/model-sync', label: '厂商同步', icon: '↻', built: true },
      { path: '/admin/catalogs', label: '上游目录', icon: '▤', built: true },
      { path: '/admin/proxies', label: '出口代理池', icon: '⌁', built: true },
      { path: '/admin/tls-fingerprints', label: 'TLS 指纹配置', icon: '⌘', built: true },
      { path: '/admin/channel-test-templates', label: '渠道测试模板', icon: '✓', built: true },
    ],
  },
  {
    key: 'users-finance',
    shell: 'operator',
    label: '用户与财务',
    items: [
      { path: '/users', label: '用户管理', icon: '♙', built: true },
      { path: '/admin/orders', label: '订单管理', icon: '□', built: true },
      { path: '/admin/subscriptions', label: '订阅管理', icon: '◆', built: true },
      { path: '/admin/pricing', label: '定价设置', icon: '＄', built: true },
      { path: '/admin/vouchers', label: '兑换码管理', icon: '◇', built: true },
      { path: '/admin/billing-claims', label: '用量与计费台账', icon: '▥', built: true },
      { path: '/admin/disputes', label: '退款与扣费争议', icon: '!', built: true },
      { path: '/admin/affiliates', label: '分销管理', icon: '♧', built: true },
    ],
  },
  {
    key: 'security-audit',
    shell: 'operator',
    label: '安全与审计',
    items: [
      { path: '/security', label: '审计日志', icon: '≋', built: true },
      { path: '/admin/risk', label: '风控总览', icon: '⛨', built: true },
      { path: '/admin/moderation', label: '内容审核', icon: '✓', built: true },
      { path: '/admin/credential-renew', label: '凭证续期', icon: '↻', built: true },
    ],
  },
  {
    key: 'observability-ops',
    shell: 'operator',
    label: '观测与运维',
    items: [
      { path: '/health', label: '系统监控', icon: '⌁', built: true },
      { path: '/admin/alerting', label: '告警中心', icon: '!', built: true },
      { path: '/admin/logs', label: '日志与诊断', icon: '≡', built: true },
      { path: '/admin/dlq', label: '死信队列', icon: '□', built: true },
      { path: '/admin/orphan-reconcile', label: '孤儿对账', icon: '⇄', built: true },
    ],
  },
  {
    key: 'settings',
    shell: 'operator',
    label: '设置',
    items: [
      { path: '/system', label: '系统设置', icon: '⚙', built: true },
      { path: '/admin/modules', label: '模块开关', icon: '▦', built: true },
      { path: '/admin/platform-credentials', label: '平台凭证', icon: '◇', built: true },
      { path: '/admin/cache', label: '缓存管理', icon: '▤', built: true },
      { path: '/admin/backup', label: '备份与恢复', icon: '↥', built: true },
      { path: '/admin/version', label: '版本与维护', icon: 'ⓘ', built: true },
      { path: '/admin/announcements', label: '公告管理', icon: '◫', built: true },
      { path: '/admin/broadcast', label: '站内信广播', icon: '◌', built: true },
      { path: '/admin/hermes', label: 'Hermes 配置与工具', icon: '⌘', built: true },
    ],
  },
]
