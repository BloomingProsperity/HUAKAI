/*
 * API Key 细粒度控制 · 前端类型 —— 镜像 userkeycontrolshttp / userkeycontrols 的 JSON 形态。
 * 端点全部挂在 /v1/api-keys/{id}/...(session 鉴权,用户管理自己名下 key 的控制项)。
 * 路由真实性见 backend/internal/userkeycontrolshttp/mount.go:30-39 + cmd/gateway/routes.go:328-332。
 */

// ── 配额 GET 响应(KeyQuotaView,types.go:40)──────────────────────────────────
export interface KeyQuotaView {
  api_key_id: number
  policy_id: number
  /** 非负十进制字符串(numeric(20,8));"0" 表示无限额。 */
  limit_usd: string
  scope_kind: string
  scope_id: string
  /** quota.Metric:cost_usd / requests / ... 。 */
  metric: string
  window_kind: string
  window_seconds: number
  mode: string
  priority: number
  valid_from: string
  /** KEY-007:当前窗口已用(已结算+已预留)。 */
  used_usd: string
  /** 仅 limit_usd 非零时返回。 */
  remaining_usd?: string
  /** 当前窗口重置时刻;无窗口时缺省。 */
  window_end?: string
}

/** 配额 PUT 体(setQuotaRequest,quota_group_handlers.go:15)。 */
export interface SetQuotaBody {
  /** 非负十进制字符串;必填(后端 parseLimitUSD 拒空)。 */
  limit_usd: string
  /** cost-usd | request-count(可省,默认 cost-usd)。 */
  metric?: string
  window_kind?: string
  window_seconds?: number
  mode?: string
}

// ── 分组 GET/PUT(SetKeyGroupResult / KeyGroupView,types.go:72)─────────────────
export interface KeyGroupView {
  api_key_id: number
  /** 未绑定时缺省(omitempty)。 */
  group_id?: number | null
  group_name?: string
  group_description?: string
  group_enabled?: boolean
}

/** 分组 PUT 体(setGroupRequest,quota_group_handlers.go:23);group_id 为正 int64 或 null。 */
export interface SetGroupBody {
  group_id: number | null
}

// ── IP 白/黑名单 GET/PUT(types.go:90/106)──────────────────────────────────────
/** 白名单与黑名单视图同构,字段名不同;统一用一个可选并存的形态承接。 */
export interface KeyIPListView {
  api_key_id: number
  ip_allowlist?: string[]
  ip_blacklist?: string[]
}

/** IP 白名单 PUT 体(setIPAllowlistRequest,quota_group_handlers.go:27)。 */
export interface SetIPAllowlistBody {
  ip_allowlist: string[]
}

/** IP 黑名单 PUT 体(setIPBlacklistRequest,ip_blacklist_handlers.go:9)。 */
export interface SetIPBlacklistBody {
  ip_blacklist: string[]
}

// ── 模型白名单 GET/PUT(SetKeyModelAllowlistResult,types.go:121)───────────────
export interface KeyModelAllowlistView {
  api_key_id: number
  allowed_models?: string[]
}

/** 模型白名单 PUT 体(setModelAllowlistRequest,quota_group_handlers.go:31)。 */
export interface SetModelAllowlistBody {
  allowed_models: string[]
}
