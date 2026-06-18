// 模型能力 / 别名 admin 写体纯逻辑（能力校验 + 别名逐行校验镜像后端 + 精确 key-set + 错误码映射），
// 零依赖 strip-types 单测。接线 HUAKAI 自有 model registry admin（controlhttp model_admin_*），不读镜像源码。
// 后端真码:
//   controlhttp/model_admin_capabilities_handler.go: PUT /v1/admin/models/{id}/capabilities
//     body{capabilities:map[string]bool, max_output_tokens?:int>0, model_mode?:string}; max_output_tokens<=0 拒。
//   controlhttp/model_admin_aliases_handler.go: POST /v1/admin/models/aliases/bulk-import body{aliases:[...],reason?}
//     (逐行部分成功, per-row result); GET /v1/admin/models/{id}/capability-bindings。
//   registry/model_alias_import.go normalizeModelAliasImport: scope∈{tenant,global}(空→tenant); tenant scope 需
//     tenant_id>0; model_id>0; alias 非空; status∈{active,disabled}(空→active)。这些全局 model registry, 无 tenant_id query。

// ── 能力 PUT ────────────────────────────────────────────────────────────
export interface CapabilitiesInput {
  capabilities: Record<string, boolean>;
  max_output_tokens?: number;
  model_mode?: string;
}

// validateCapabilitiesInput 镜像后端 parseCapabilitiesBody: max_output_tokens(若给)须 >0; 能力 key 非空。
export function validateCapabilitiesInput(input: CapabilitiesInput): string | null {
  if (input.max_output_tokens !== undefined && !(input.max_output_tokens > 0)) {
    return 'max_output_tokens 必须为正整数';
  }
  for (const key of Object.keys(input.capabilities)) {
    if (key.trim() === '') return '能力 key 不能为空';
  }
  return null;
}

// buildCapabilitiesBody: 精确 key-set {capabilities, max_output_tokens?, model_mode?}; 可选项仅在显式给时附带。
export function buildCapabilitiesBody(input: CapabilitiesInput): Record<string, unknown> {
  const body: Record<string, unknown> = { capabilities: input.capabilities };
  if (input.max_output_tokens !== undefined) body.max_output_tokens = input.max_output_tokens;
  if (input.model_mode !== undefined) body.model_mode = input.model_mode;
  return body;
}

// ── 别名批量导入 ────────────────────────────────────────────────────────
export const ALIAS_SCOPES = ['tenant', 'global'] as const;
export type AliasScope = (typeof ALIAS_SCOPES)[number];
export const ALIAS_STATUSES = ['active', 'disabled'] as const;
export type AliasStatus = (typeof ALIAS_STATUSES)[number];

export interface AliasRow {
  model_id: number;
  alias: string;
  scope?: string; // 空→后端默认 tenant
  tenant_id?: number;
  display?: string;
  status?: string; // 空→后端默认 active
  source?: string;
}

// validateAliasRow 镜像后端 normalizeModelAliasImport(发请求前预拒明显错误; 后端仍逐行裁决+部分成功)。
// 返回 null=合法; 否则错误文案。scope/status 空视后端默认(tenant/active), 非法值才拒。
export function validateAliasRow(row: AliasRow): string | null {
  const scope = (row.scope ?? '').trim() || 'tenant';
  if (scope !== 'tenant' && scope !== 'global') return `scope 必须是 tenant 或 global(得到 ${scope})`;
  if (scope === 'tenant' && !((row.tenant_id ?? 0) > 0)) return 'tenant scope 的别名需要正的 tenant_id';
  if (!(row.model_id > 0)) return 'model_id 必须为正整数';
  if ((row.alias ?? '').trim() === '') return 'alias 不能为空';
  const status = (row.status ?? '').trim() || 'active';
  if (status !== 'active' && status !== 'disabled') return `status 必须是 active 或 disabled(得到 ${status})`;
  return null;
}

// validateAliasBulk: 至少一行 + 每行合法。返回 null=合法; 否则带行号的首个错误。
export function validateAliasBulk(rows: AliasRow[]): string | null {
  if (rows.length === 0) return 'aliases 至少需要一行';
  for (let i = 0; i < rows.length; i++) {
    const err = validateAliasRow(rows[i]);
    if (err) return `第 ${i + 1} 行: ${err}`;
  }
  return null;
}

// buildAliasRow: 单行精确 key-set; 仅在显式给时附带可选键(空值交后端默认, 不塞空串)。
function buildAliasRow(row: AliasRow): Record<string, unknown> {
  const out: Record<string, unknown> = { model_id: row.model_id, alias: row.alias };
  if (row.scope !== undefined && row.scope.trim() !== '') out.scope = row.scope;
  if (row.tenant_id !== undefined) out.tenant_id = row.tenant_id;
  if (row.display !== undefined && row.display.trim() !== '') out.display = row.display;
  if (row.status !== undefined && row.status.trim() !== '') out.status = row.status;
  if (row.source !== undefined && row.source.trim() !== '') out.source = row.source;
  return out;
}

// buildAliasBulkBody: {aliases:[...], reason?}。
export function buildAliasBulkBody(rows: AliasRow[], reason?: string): Record<string, unknown> {
  const body: Record<string, unknown> = { aliases: rows.map(buildAliasRow) };
  if (reason !== undefined && reason.trim() !== '') body.reason = reason;
  return body;
}

// ── 错误码映射 ──────────────────────────────────────────────────────────
export const MODEL_ADMIN_ERROR_MESSAGES: Record<string, string> = {
  invalid_model_id: 'model id 非法',
  invalid_json: '请求体不是合法 JSON',
  invalid_max_output_tokens: 'max_output_tokens 必须为正',
  invalid_capabilities: '能力 key 不能为空',
  invalid_aliases: 'aliases 至少需要一行',
  invalid_csv: 'CSV 格式非法',
  invalid_capability: '能力值非法',
  model_not_found: '模型不存在',
  model_capabilities_update_failed: '模型能力更新服务暂不可用',
  model_admin_store_failed: '模型管理后端暂不可用',
  gateway_not_configured: '模型管理未配置',
};

export function modelAdminErrorMessage(code: string): string {
  return MODEL_ADMIN_ERROR_MESSAGES[code] ?? `模型管理操作失败(${code})`;
}
