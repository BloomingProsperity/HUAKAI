/*
 * 管线即导航(pipeline-as-navigation)—— 反克隆签名之一。
 *
 * 不照搬 one-api/new-api 的"功能清单侧栏",而是把导航组织成中转站的真实数据管线顺序:
 *   上游账号池 → 路由与池 → API Key → 用量与计费 → 用户与租户 → 模型与定价 → 系统 → 安全审计
 * 让运维者沿"请求从上游账号流到计费"的链路理解每一站。各 section 对应 docs/frontend
 * 前端编写方案的 9 个功能域。built=false 的项先挂占位页,后续 P0 切片逐个点亮。
 */
export interface NavItem {
  path: string
  label: string
  /** 该域是否已有实现页(false=占位"建设中")。 */
  built: boolean
}

export interface NavSection {
  /** 管线阶段序号(1 起),用于导航上的"管线刻度"视觉。 */
  stage: number
  key: string
  label: string
  hint: string
  items: NavItem[]
}

export const PIPELINE_NAV: NavSection[] = [
  {
    stage: 1,
    key: 'accounts',
    label: '上游账号池',
    hint: '把上游 Claude/OpenAI/Gemini 账号纳入可调度池',
    items: [{ path: '/accounts', label: '账号中心', built: true }],
  },
  {
    stage: 2,
    key: 'routing',
    label: '路由与池',
    hint: '分组、权重、选号策略、健康与亲和',
    items: [{ path: '/routing', label: '路由与池管理', built: true }],
  },
  {
    stage: 3,
    key: 'keys',
    label: 'API Key',
    hint: '把池子签发成可用/可售的密钥',
    items: [{ path: '/keys', label: '我的密钥', built: true }],
  },
  {
    stage: 4,
    key: 'billing',
    label: '用量与计费',
    hint: '余额、充值、订单、兑换、用量统计',
    items: [
      { path: '/usage', label: '用量与日志', built: true },
      { path: '/wallet', label: '钱包与充值', built: false },
    ],
  },
  {
    stage: 5,
    key: 'tenants',
    label: '用户与租户',
    hint: '注册登录、权限、租户作用域',
    items: [{ path: '/users', label: '用户与权限', built: false }],
  },
  {
    stage: 6,
    key: 'models',
    label: '模型与定价',
    hint: '模型目录、倍率定价、公开价目',
    items: [{ path: '/models', label: '模型与定价', built: false }],
  },
  {
    stage: 7,
    key: 'system',
    label: '系统',
    hint: '系统设置、公告、运维诊断',
    items: [{ path: '/system', label: '系统设置', built: false }],
  },
  {
    stage: 8,
    key: 'security',
    label: '安全审计',
    hint: '审计账本、告警、风控',
    items: [{ path: '/security', label: '安全与审计', built: false }],
  },
]
