// 订阅档→pool_group 路由规则(routes 表)admin 写体的纯逻辑：model_pattern 校验(镜像后端单一来源)
// + 精确 key-set 构造 + 错误码映射，零依赖 strip-types 单测。本面只接线 HUAKAI 自有后端的 routes
// CRUD(controlhttp/routeadmin_handler.go + routeadmin/validate.go)，不读任何镜像源码。
// 后端真码:
//   routeadmin/validate.go ValidateModelPattern: '' 或 '*'=全匹配; 'prefix*'(唯一一个 '*' 在末尾)=前缀;
//     无 '*'=精确; 中段/多 '*'(a*b/*x/a**)拒 → ErrInvalidModelPattern。
//   controlhttp/routeadmin_handler.go: createRouteRequest{tenant_id,name,user_group_match,model_pattern_match,
//     pool_group_id,match_priority}; updateRouteRequest 同但【无 tenant_id】(防经更新跨租户走私);
//     DisallowUnknownFields → 多余键 400。PUT 全替换: 省略 match_priority → 后端 COALESCE 回落默认 100。

export interface RouteInput {
  name: string;
  user_group_match: string;
  model_pattern_match?: string;
  pool_group_id: number;
  match_priority?: number;
}

// 更新输入: match_priority 必填(number, 非可选) —— PUT 全替换语义下省略它后端会静默重置为 100,
// 故编辑必须 read-modify-write 显式带原值。类型层强制, 配合 buildUpdateRouteBody 永远输出该键。
export type RouteUpdateInput = Omit<RouteInput, 'match_priority'> & { match_priority: number };

// validateModelPattern 镜像后端 routeadmin/validate.go 的单一来源语义。
// 返回 null=合法; 否则错误文案(对齐后端 invalid_model_pattern 的语义)。
export function validateModelPattern(p: string): string | null {
  if (p === '' || p === '*') return null; // 全匹配
  const first = p.indexOf('*');
  if (first === -1) return null; // 无通配 = 精确串
  // 含 '*': 仅当恰好一个且位于末尾才合法(prefix*)。
  const count = (p.match(/\*/g) ?? []).length;
  if (count === 1 && first === p.length - 1) return null;
  return "model_pattern_match 的通配 '*' 只能作整串或末尾后缀(不可中段/多个)";
}

// validateRouteInput 镜像后端 Service.Create/Update 的必填 + 形态校验(发请求前预拒, 文案先于后端)。
// 必填: name/user_group_match 非空, pool_group_id>0; match_priority(若给)>=0; model_pattern 形态。
export function validateRouteInput(input: RouteInput): string | null {
  if (!input.name.trim()) return 'name 必填';
  if (!input.user_group_match.trim()) return 'user_group_match 必填';
  if (!(input.pool_group_id > 0)) return 'pool_group_id 必须是正整数';
  if (input.match_priority !== undefined && input.match_priority < 0) return 'match_priority 必须 >= 0';
  return validateModelPattern(input.model_pattern_match ?? '');
}

// validateRouteUpdateInput 在 validateRouteInput 基础上额外强制 match_priority 必须显式给(number)。
// 防御纵深(不仅靠类型): PUT 全替换省略 match_priority 后端会静默重置为 100; 若类型被 cast(as any)/
// 重构绕过, 这里在发请求前拦住, 避免静默重置。updateRoute 调本函数(而非 validateRouteInput)。
export function validateRouteUpdateInput(input: RouteUpdateInput): string | null {
  if (typeof input.match_priority !== 'number') {
    return 'match_priority 必填(PUT 全替换, 省略会被后端静默重置为 100)';
  }
  return validateRouteInput(input);
}

// buildCreateRouteBody: 创建体精确 key-set —— tenant_id(body) + name + user_group_match +
// model_pattern_match(默认 '' = 全匹配) + pool_group_id, 仅当显式给 match_priority 时附带它(否则后端默认 100)。
export function buildCreateRouteBody(tenantId: number, input: RouteInput): Record<string, unknown> {
  const body: Record<string, unknown> = {
    tenant_id: tenantId,
    name: input.name,
    user_group_match: input.user_group_match,
    model_pattern_match: input.model_pattern_match ?? '',
    pool_group_id: input.pool_group_id,
  };
  if (input.match_priority !== undefined) body.match_priority = input.match_priority;
  return body;
}

// buildUpdateRouteBody: 更新体精确 key-set —— 【无 tenant_id】(防跨租户走私, 后端 DisallowUnknownFields
// 会拒 body 内 tenant_id) 且 match_priority 永远显式带(防 PUT 全替换静默重置为 100)。
export function buildUpdateRouteBody(input: RouteUpdateInput): Record<string, unknown> {
  return {
    name: input.name,
    user_group_match: input.user_group_match,
    model_pattern_match: input.model_pattern_match ?? '',
    pool_group_id: input.pool_group_id,
    match_priority: input.match_priority,
  };
}

// buildSetEnabledBody: 启停体精确 key-set —— 仅 { enabled }(布尔), 【无 tenant_id】(防跨租户走私, 后端
// DisallowUnknownFields 会拒)。enabled 显式带且保真布尔: 后端 *bool 强制存在, 漏键会被拒(防空 body 静默停用)。
export function buildSetEnabledBody(enabled: boolean): Record<string, unknown> {
  return { enabled };
}

// ROUTE_ERROR_MESSAGES: 后端 routeAdminWriteRouteError + admin 门的错误码 → 中文文案(UI 展示用)。
export const ROUTE_ERROR_MESSAGES: Record<string, string> = {
  invalid_model_pattern: "model_pattern 通配只能作整串或末尾后缀",
  invalid_route_request: "路由请求非法(字段缺失或格式错误)",
  route_not_found: "路由不存在或已删除",
  pool_group_not_found: "目标 pool_group 不存在或不属于本租户",
  route_name_conflict: "同租户下已存在同名路由",
  route_admin_backend_error: "路由管理服务暂不可用",
  invalid_route_id: "路由 id 非法",
  tenant_id_required: "缺少 tenant_id",
  admin_unauthorized: "管理凭据缺失或无效",
  admin_forbidden: "需要 platform_admin 角色",
  gateway_not_configured: "路由管理未配置",
};

export function routeErrorMessage(code: string): string {
  return ROUTE_ERROR_MESSAGES[code] ?? `路由操作失败(${code})`;
}
