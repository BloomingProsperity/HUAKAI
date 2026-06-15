// admin 系统 / 审核 API 封装（管理 token 轨，走 client.ts 的 apiGet/apiPost + 本模块内自带的
// adminPut —— client.ts 未导出 PUT/DELETE 助手且按硬约束不可改它，故在本模块内复用同一
// 「localStorage huakai_admin_token → Bearer」约定 + 复用 client.ts 导出的 ApiError，使
// errors.ts friendlyMessage 仍可统一翻译。参考 lib/api/adminUsers.ts 的做法。
//
// 端点形状全部以 HUAKAI 后端真码为准（读 handler 确认，逐条标注）：
//   GET  /admin/v1/system/health          systemhealthhttp.NewSystemHealthHandler（adminGate→platform_admin）
//   GET  /admin/v1/modules[?category=]      modulehttp.NewModulesHandler（adminGate→platform_admin）
//   GET  /admin/v1/version                  adminhttp.newVersionHandler（tenant_operator | platform_admin 可读）
//   GET  /admin/v1/loglevel                 adminhttp.newLogLevelHandler（GET 任意已认证；但 PUT platform_admin only，
//                                            实际 GET 也走 zap AtomicLevel.ServeHTTP，handler 未对 GET 限角色，
//                                            仅 PUT 前置 RolePlatformAdmin 检查 —— 见下注）
//   PUT  /admin/v1/loglevel  {level}        adminhttp.newLogLevelHandler（platform_admin only：ident.Role!=RolePlatformAdmin → 403 admin_forbidden）
//   GET  /admin/v1/moderation/config?tenant_id=   moderationhttp.newConfigGetHandler
//   PUT  /admin/v1/moderation/config  {tenant_id,...}  moderationhttp.newConfigPutHandler
//   GET  /admin/v1/moderation/logs?tenant_id=     moderationhttp.newLogListHandler
//   GET  /admin/v1/moderation/banned?tenant_id=   moderationhttp.newBannedListHandler
//   GET  /admin/v1/billing/settings?tenant_id=    gatewayhttp.newAdminBillingSettingsGetHandler
//
// 鉴权 / 角色（读后端真码）：
//   · /system/health、/modules：adminGate 包装 → 仅 platform_admin（middleware.go adminGate：
//     id.Role != admin.RolePlatformAdmin → 403 admin_forbidden_scope）。tenant_operator 即便持合法
//     凭据也被拒。本面这两区需 platform_admin。
//   · /version：tenant_operator | platform_admin 均可读。
//   · /loglevel：GET 走 zap AtomicLevel（handler 未限角色）；PUT 仅 platform_admin。
//   · /moderation/*、/billing/settings：moderationhttp / gatewayhttp 自有 auth.Resolve（非 adminGate），
//     platform_admin 必带 ?tenant_id；tenant_operator 可省（用自身 scope）。单租户部署默认 tenant=1。
//
// ?tenant_id 需求：moderation config/logs/banned + billing settings 走 tenantFromQuery —— platform_admin
// 必带；tenant_operator 省略时用 ScopeTenantID。本面统一传入（默认 1）以兼容两种角色。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅提取功能/字段/布局形态，未抄码）：
//   - sub2api(LGPL) views/admin/RiskControlView.vue：内容审核「采样率 sampleRate / 自动封禁 autoBan +
//     封禁阈值 banThreshold / 关键词模式 keywordBlockingMode / 屏蔽关键词」配置面 + 审核日志列表 +
//     自动封禁 / 解封形态。本面对齐 HUAKAI moderation handler 字段（enabled / fail_closed /
//     sample_rate_pct / ban_threshold / ban_window_seconds / violation_fee_usd），不照搬上游字段名。
//   - sub2api(LGPL) views/admin/SettingsView.vue：系统设置「分区卡片」运营布局形态。
//   - new-api(AGPL) pages/Setting/index.jsx（多 itemKey Tab 分区）+ pages/Log（日志列「类型/模型/额度/
//     时间/IP/内容」形态）+ components/settings/OperationSetting：系统设置分区 + 日志列表运营形态。
//   日志/健康/模块字段集合完全对齐 HUAKAI 后端 HealthResponse / ModuleView / moderationLogResponse /
//   bannedAPIKeyResponse / configResponse / buildinfo.Info，不照搬上游字段名。

import { ApiError, apiGet } from './client';
import type { APIError } from './types';

// ---- 共享：管理 token 取用 + PUT（client.ts 未提供该动词，且不可改它）----

function adminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

function adminHeaders(): Record<string, string> {
  const token = adminToken();
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

async function parse<T>(resp: Response): Promise<T> {
  if (resp.ok) {
    if (resp.status === 204) return undefined as T;
    return (await resp.json()) as T;
  }
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

async function adminPut<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(path, { method: 'PUT', headers: adminHeaders(), body: JSON.stringify(body) });
  return parse<T>(resp);
}

// ============================================================
// 系统健康（GET /admin/v1/system/health —— systemhealthhttp.HealthResponse）
// ============================================================

export type HealthStatus = 'healthy' | 'degraded' | 'unhealthy';

// systemhealthhttp.Component。Detail 是简短运营诊断串（如 "unhealthy_channels=2 total=10"）。
export interface HealthComponent {
  name: string;
  status: HealthStatus;
  detail?: string;
}

// systemhealthhttp.HealthResponse。status = 各 component 中最差者。
export interface HealthResponse {
  status: HealthStatus;
  components: HealthComponent[];
}

export function getSystemHealth(): Promise<HealthResponse> {
  return apiGet<HealthResponse>('/admin/v1/system/health');
}

// ============================================================
// 模块清单（GET /admin/v1/modules —— modulehttp.ModulesResponse）
// ============================================================

export type ProbeStatus = 'ok' | 'degraded' | 'unknown' | 'error';

// modulehttp.ProbeResult（moduleregistry.ProbeResult）。
export interface ModuleProbe {
  status: ProbeStatus;
  detail?: string;
}

// modulehttp.CatalogOverlay（静态特征树叠加，无匹配时缺省）。
export interface ModuleCatalog {
  section?: string;
  feature_id?: string;
  status?: string;
  parity?: string;
  pkgs?: string[];
}

// modulehttp.ModuleView。live_probe 为运行时探针；catalog 为静态身份叠加（可缺）。
export interface ModuleView {
  id: string;
  category: string;
  title: string;
  capabilities?: string[];
  catalog?: ModuleCatalog;
  live_probe: ModuleProbe;
}

// modulehttp.ModulesResponse。
export interface ModulesResponse {
  modules: ModuleView[];
}

// 只读：合并后的模块身份 + 能力 + 静态目录 + 运行时探针。注：后端无启停端点（纯只读知识面），
// 故本面以「探针状态」呈现各模块健康，而非提供开关动作（后端 modulehttp 仅 GET）。
export function listModules(category?: string): Promise<ModulesResponse> {
  return apiGet<ModulesResponse>('/admin/v1/modules', category ? { category } : undefined);
}

// ============================================================
// 版本（GET /admin/v1/version —— buildinfo.Info）
// ============================================================

// buildinfo.Info。tenant_operator | platform_admin 均可读。
export interface BuildInfo {
  version: string;
  commit: string;
  build_time: string;
  go_version: string;
}

export function getVersion(): Promise<BuildInfo> {
  return apiGet<BuildInfo>('/admin/v1/version');
}

// ============================================================
// 日志级别（GET/PUT /admin/v1/loglevel —— zap AtomicLevel.ServeHTTP）
// ============================================================

// zap AtomicLevel 的 JSON 契约：{"level":"info"}。可设值见 LOG_LEVELS。
export interface LogLevelResponse {
  level: string;
}

export const LOG_LEVELS = ['debug', 'info', 'warn', 'error', 'dpanic', 'panic', 'fatal'] as const;
export type LogLevel = (typeof LOG_LEVELS)[number];

export function getLogLevel(): Promise<LogLevelResponse> {
  return apiGet<LogLevelResponse>('/admin/v1/loglevel');
}

// PUT 仅 platform_admin（非 platform_admin → 403 admin_forbidden）。
export function setLogLevel(level: LogLevel): Promise<LogLevelResponse> {
  return adminPut<LogLevelResponse>('/admin/v1/loglevel', { level });
}

// ============================================================
// 内容审核配置（GET/PUT /admin/v1/moderation/config —— moderationhttp.configResponse）
// ============================================================

// moderationhttp.configResponse。violation_fee_usd 是文本十进制（StringFixed(8)）。
export interface ModerationConfig {
  tenant_id: number;
  enabled: boolean;
  fail_closed: boolean;
  sample_rate_pct: number; // 0..100
  ban_threshold: number; // ≥0
  ban_window_seconds: number; // >0
  violation_fee_usd: string; // 文本十进制，例 "0.00000000"
  updated_by?: string;
  updated_at?: string; // RFC3339
}

// 提交体（moderationhttp.configRequest）。需 ?无 —— tenant_id 在 body 里（requireTenant）。
export interface ModerationConfigUpdate {
  tenant_id: number;
  enabled: boolean;
  fail_closed: boolean;
  sample_rate_pct: number;
  ban_threshold: number;
  ban_window_seconds: number;
  violation_fee_usd: string;
}

// GET 走 tenantFromQuery（platform_admin 必带 ?tenant_id；tenant_operator 省略用 scope）。
export function getModerationConfig(tenantId: number): Promise<ModerationConfig> {
  return apiGet<ModerationConfig>('/admin/v1/moderation/config', { tenant_id: tenantId });
}

// PUT body 含 tenant_id（requireTenant）。后端校验：sample_rate_pct 0..100、ban_threshold≥0、
// ban_window_seconds>0、violation_fee_usd 非负十进制。
export function updateModerationConfig(body: ModerationConfigUpdate): Promise<ModerationConfig> {
  return adminPut<ModerationConfig>('/admin/v1/moderation/config', body);
}

// ============================================================
// 审核日志（GET /admin/v1/moderation/logs —— moderationhttp.moderationLogResponse）
// ============================================================

// moderationhttp.moderationLogResponse。decision: pass/flag/...（后端枚举字符串）。
export interface ModerationLog {
  id: number;
  tenant_id: number;
  api_key_id: number;
  user_id: number;
  request_id?: string;
  payload_hash: string;
  decision: string;
  reason_code: string;
  matched_keyword_id?: number;
  matched_hash_id?: number;
  violation_fee_usd: string; // 文本十进制
  billing_event_id?: number;
  occurred_at?: string; // RFC3339
}

// moderationhttp.moderationLogListResponse（object/items/limit/offset）。
export interface ModerationLogListResponse {
  object: string;
  items: ModerationLog[];
  limit: number;
  offset: number;
}

// tenantFromQuery + parsePage（limit 1..500）。
export function listModerationLogs(params: {
  tenant_id: number;
  limit?: number;
  offset?: number;
}): Promise<ModerationLogListResponse> {
  return apiGet<ModerationLogListResponse>('/admin/v1/moderation/logs', {
    tenant_id: params.tenant_id,
    limit: params.limit,
    offset: params.offset,
  });
}

// ============================================================
// 被封 API Key（GET /admin/v1/moderation/banned —— moderationhttp.bannedAPIKeyResponse，只读展示）
// ============================================================

// moderationhttp.bannedAPIKeyResponse。
export interface BannedAPIKey {
  id: number;
  tenant_id: number;
  user_id: number;
  name: string;
  key_prefix: string;
  status: string;
  violation_count: number;
  last_violation_at?: string;
  created_at?: string;
  updated_at?: string;
}

// moderationhttp.bannedAPIKeyListResponse。
export interface BannedAPIKeyListResponse {
  object: string;
  items: BannedAPIKey[];
  limit: number;
  offset: number;
}

export function listBannedApiKeys(params: {
  tenant_id: number;
  limit?: number;
  offset?: number;
}): Promise<BannedAPIKeyListResponse> {
  return apiGet<BannedAPIKeyListResponse>('/admin/v1/moderation/banned', {
    tenant_id: params.tenant_id,
    limit: params.limit,
    offset: params.offset,
  });
}

// ============================================================
// 计费设置（GET /admin/v1/billing/settings —— gatewayhttp.adminBillingSettingsResponse，只读展示）
// ============================================================

// gatewayhttp.adminBillingSettingsResponse。source: default | tenant。
// 当前后端只暴露一个键：stream_input_only_interrupted_policy（流式仅输入被中断的计费策略）。
export interface BillingSetting {
  tenant_id: number;
  key: string;
  value: string;
  source: string; // default | tenant
  allowed_values: string[];
  roadmap_values: string[];
  updated_at: string | null;
  updated_by: string | null;
}

// GET 走 resolveAdminBillingTenantFromQuery（platform_admin 必带 ?tenant_id；tenant_operator 省略用 scope）。
// 注：本面只读展示该计费策略；PUT 需 reason 且后端做租户存在性校验，本面不提供写入（避免误改计费）。
export function getBillingSettings(tenantId: number): Promise<BillingSetting> {
  return apiGet<BillingSetting>('/admin/v1/billing/settings', { tenant_id: tenantId });
}

// ============================================================
// 展示助手
// ============================================================

export function formatDateTime(iso?: string | null): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString('zh-CN', { hour12: false });
}

// 健康 / 探针 / 状态 → 中文标签
export function healthStatusLabel(s: HealthStatus): string {
  switch (s) {
    case 'healthy':
      return '健康';
    case 'degraded':
      return '降级';
    case 'unhealthy':
      return '异常';
    default:
      return s;
  }
}

export function probeStatusLabel(s: ProbeStatus): string {
  switch (s) {
    case 'ok':
      return '正常';
    case 'degraded':
      return '降级';
    case 'unknown':
      return '未知';
    case 'error':
      return '故障';
    default:
      return s;
  }
}

// 审核判定 decision → 中文（后端枚举：pass/flag/...，未知透传）。
export function decisionLabel(d: string): string {
  switch (d) {
    case 'pass':
    case 'allow':
      return '放行';
    case 'flag':
    case 'block':
      return '拦截';
    case 'review':
      return '待审';
    default:
      return d;
  }
}

// 把文本十进制按需裁掉尾随 0（仅展示，不做精度运算）。
export function trimDecimal(v: string): string {
  if (!v) return '0';
  if (!v.includes('.')) return v;
  return v.replace(/\.?0+$/, '') || '0';
}
