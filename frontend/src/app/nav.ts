/*
 * 三壳导航(登录后按壳分组)。Owner 定的 IA:公开壳(壳外路由)/ 用户门户壳 / 运营台壳。
 * 这里只列登录后两壳;公开页(落地/登录/找回/排行)是壳外独立路由,不在侧导航。
 * built=false 的项先挂占位"建设中",后续切片点亮。
 */
export interface NavItem {
  path: string
  label: string
  /** 该域是否已有实现页(false=占位"建设中")。 */
  built: boolean
}

export type Shell = 'user' | 'operator'

export interface NavSection {
  /** 壳内分组序号(每壳自 1 起),用于导航上的刻度视觉。 */
  stage: number
  key: string
  /** 所属壳:用户门户 / 运营台。 */
  shell: Shell
  label: string
  hint: string
  items: NavItem[]
}

export const SHELL_LABEL: Record<Shell, string> = {
  user: '用户门户',
  operator: '运营台',
}

export const PIPELINE_NAV: NavSection[] = [
  // ── 用户门户壳(session token,卖额度的客户自助) ──
  {
    stage: 1,
    key: 'overview',
    shell: 'user',
    label: '概览',
    hint: '余额 · 用量 · Key 速览',
    items: [{ path: '/overview', label: '我的概览', built: true }],
  },
  {
    stage: 2,
    key: 'keys',
    shell: 'user',
    label: 'API Key',
    hint: '签发、接入客户端、撤销',
    items: [{ path: '/keys', label: '我的密钥', built: true }],
  },
  {
    stage: 3,
    key: 'usage',
    shell: 'user',
    label: '用量与配额',
    hint: '调用日志、配额窗口',
    items: [{ path: '/usage', label: '用量与日志', built: true }],
  },
  {
    stage: 4,
    key: 'wallet',
    shell: 'user',
    label: '充值与权益',
    hint: '钱包、订单、订阅、兑换、签到、推广',
    items: [
      { path: '/wallet', label: '钱包与充值', built: false },
      { path: '/orders', label: '我的订单', built: false },
      { path: '/subscriptions', label: '订阅套餐', built: true },
      { path: '/redeem', label: '兑换码', built: true },
      { path: '/checkin', label: '每日签到', built: true },
      { path: '/affiliate', label: '推广返利', built: true },
    ],
  },
  // ── 运营台壳(admin token,平台运营) ──
  {
    stage: 1,
    key: 'accounts',
    shell: 'operator',
    label: '上游账号池',
    hint: '把上游 Claude/OpenAI/Gemini 账号纳入可调度池',
    items: [{ path: '/accounts', label: '账号中心', built: true }],
  },
  {
    stage: 2,
    key: 'routing',
    shell: 'operator',
    label: '路由与池',
    hint: '分组、权重、选号策略、健康与亲和',
    items: [{ path: '/routing', label: '路由与池管理', built: true }],
  },
  {
    stage: 3,
    key: 'tenants',
    shell: 'operator',
    label: '用户与租户',
    hint: '注册登录、权限、租户作用域',
    items: [{ path: '/users', label: '用户与权限', built: true }],
  },
  {
    stage: 4,
    key: 'models',
    shell: 'operator',
    label: '模型与定价',
    hint: '模型目录、倍率定价、公开价目',
    items: [{ path: '/models', label: '模型与定价', built: true }],
  },
  {
    stage: 5,
    key: 'commerce',
    shell: 'operator',
    label: '计费运营',
    hint: '订单、套餐、兑换码、分销',
    items: [
      { path: '/admin/orders', label: '订单管理台', built: true },
      { path: '/admin/subscriptions', label: '套餐管理', built: true },
      { path: '/admin/vouchers', label: '兑换码管理', built: true },
      { path: '/admin/affiliates', label: '分销管理', built: true },
    ],
  },
  {
    stage: 6,
    key: 'content',
    shell: 'operator',
    label: '内容与公告',
    hint: '公告、内容审核风控',
    items: [
      { path: '/admin/announcements', label: '公告管理', built: true },
      { path: '/admin/moderation', label: '内容审核', built: true },
    ],
  },
  {
    stage: 7,
    key: 'system',
    shell: 'operator',
    label: '系统',
    hint: '系统设置、运维大屏、系统健康',
    items: [
      { path: '/system', label: '系统设置', built: true },
      { path: '/ops', label: '运维大屏', built: true },
      { path: '/health', label: '系统健康', built: true },
    ],
  },
  {
    stage: 8,
    key: 'security',
    shell: 'operator',
    label: '安全审计',
    hint: '审计账本、告警、风控',
    items: [{ path: '/security', label: '安全与审计', built: true }],
  },
]
