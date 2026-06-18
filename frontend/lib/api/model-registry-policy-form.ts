// 租户目录继承策略(inherit_global_catalog)admin 写体纯逻辑, 零依赖 strip-types 单测。
// 后端真码 controlhttp/model_registry_policy_handler.go (platform_admin only via adminGate):
//   GET /v1/admin/model-registry-policy?tenant_id → {policy:{tenant_id,inherit_global_catalog,updated_at?,updated_by_actor?}}
//   PUT /v1/admin/model-registry-policy?tenant_id  body{inherit_global_catalog:bool} → {policy}
// tenant 走 query(目标租户, 非 body); body 仅 inherit_global_catalog(*bool 必填, DisallowUnknownFields 拒未知/拒 body tenant_id)。

// validateSetTenantPolicy 发请求前预校验: tenant_id 必须正整数(后端 tenant_id query 必填且 FK 校验)。
export function validateSetTenantPolicy(tenantId: number): string | null {
  if (!(tenantId > 0)) return 'tenant_id 必须是正整数';
  return null;
}

// buildTenantPolicyBody: 精确 key-set —— 仅 { inherit_global_catalog }(布尔)。**无 tenant_id**(走 query, 防 body 走私,
// 后端 DisallowUnknownFields 会拒)。inherit 永远显式带(后端 *bool 必填; 省略会被 400)。
export function buildTenantPolicyBody(inherit: boolean): Record<string, unknown> {
  return { inherit_global_catalog: inherit };
}

// TENANT_POLICY_ERROR_MESSAGES: 后端 writeTenantPolicyError + admin 门错误码 → 中文文案。
export const TENANT_POLICY_ERROR_MESSAGES: Record<string, string> = {
  tenant_id_required: '缺少 tenant_id',
  invalid_tenant_policy: '策略请求非法(inherit_global_catalog 字段必填)',
  invalid_json: '请求体不是合法 JSON',
  tenant_not_found: '目标租户不存在',
  model_admin_store_failed: '模型注册表后端暂不可用',
  gateway_not_configured: '租户策略未配置',
  admin_unauthorized: '管理凭据缺失或无效',
  admin_forbidden_scope: '需要 platform_admin 角色',
  admin_gate_not_configured: '管理鉴权未配置，请联系平台',
  admin_backend_error: '管理鉴权后端暂时不可用，请稍后重试',
};

export function tenantPolicyErrorMessage(code: string): string {
  return TENANT_POLICY_ERROR_MESSAGES[code] ?? `租户策略操作失败(${code})`;
}
