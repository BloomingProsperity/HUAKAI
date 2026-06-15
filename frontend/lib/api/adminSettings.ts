// admin 平台设置 API 封装（管理 token 轨）。走 client.ts 的 apiGet/apiPost +（PUT 在本模块内
// 自带 adminPut —— client.ts 未导出 PUT 助手且按硬约束不可改，故复用同一「localStorage
// huakai_admin_token → Bearer」约定 + 复用 client.ts 导出的 ApiError，使 errors.ts friendlyMessage
// 仍可统一翻译，与 lib/api/adminUsers.ts 同法）。
//
// 端点形状全部以 HUAKAI 后端真码为准（读 handler 确认，逐条标注 file:line）：
//   平台设置（controlhttp.MountPlatformSettingsRoutes，挂 /v1/admin/platform-settings —— routes_platformsettings.go:15；
//            handler platformsettings_handler.go）。**需 platform_admin**（resolvePlatformSettingsAdmin
//            硬性要求 RolePlatformAdmin，非 platform_admin → 403 admin_forbidden）。
//     GET  /v1/admin/platform-settings           列出全部 allow-list 设置（List → {items:[{key,value,source,updated_at,updated_by,health?}]}）
//     GET  /v1/admin/platform-settings/{key}      取单个
//     PUT  /v1/admin/platform-settings/{key}      body{value:string, reason?:string} → 单个 setting（DisallowUnknownFields，
//                                                  body 仅接受 value/reason；value 必填非 nil；captcha_enabled=true 但密钥未配 → 400 captcha_secret_required）
//   邮件设置（gatewayhttp.MountAdminEmailSettingsRoutes，挂 /v1/admin/email —— routes.go:773；handler admin_email_settings_handler.go）。
//            **平台/租户管理员均可**（resolveAdminEmailSettings 接受 RolePlatformAdmin 或 RoleTenantOperator）。
//     GET  /v1/admin/email/settings?tenant_id     → {tenant_id, settings:[{key,value,updated_at,updated_by,(configured for password)}]}（密码值被脱敏成空串 + configured 布尔）
//     PUT  /v1/admin/email/settings               body{tenant_id, smtp_host?, smtp_port?, smtp_username?, smtp_password?, smtp_from?,
//                                                  smtp_from_name?, smtp_use_tls?, email_verify_enabled?} → {tenant_id, updated:n}
//                                                  （至少一项；smtp_port 1..65535；DisallowUnknownFields 经 decodeAdminPoolJSON）
//     POST /v1/admin/email/test                   body{tenant_id, to} → {tenant_id, sent:true}（用已存配置发测试信）
//   TLS 指纹画像（tlsfphttp.MountTLSFPAdminRoutes，挂 /v1/admin/tls-fingerprint-profiles —— routes.go:1048；handler tlsfphttp/handler.go）。
//            **需 platform_admin**。本面**只读列表**（CRUD 写入留待后续切片，端点形状已记录在下方注释）。
//     GET  /v1/admin/tls-fingerprint-profiles?tenant_id  → {object, items:[Profile]}（Profile 见 tlsfpadmin/types.go:33）
//
// 鉴权汇总：平台设置 / TLS 列表 → **platform_admin 必需**（403 admin_forbidden 提示）。邮件设置 → platform/tenant 均可。
// tenant_id：平台设置端点**不接受** tenant_id（全局 scope，按 key 取/改）；邮件设置 GET 用 ?tenant_id（tenant_operator 可省、用自身 scope）、
//            PUT/test 在 body 里带 tenant_id；TLS 列表用 ?tenant_id（正整数必需）。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅提取功能/字段/布局形态，未抄码）：
//   - sub2api(LGPL) views/admin/SettingsView.vue + api/admin/settings.ts：设置中心「分 Tab 分组 + 卡片」
//     的 IA 形态（security / registration / 等分区），逐项配置 + 区内保存的运营形态。HUAKAI 后端是
//     per-key get/update（不是 sub2api 的整表 batch save），故本面按「每项独立 get → 改 → PUT 单 key」实现，
//     不照搬 sub2api 的单 form 整体提交。字段集合对齐 HUAKAI platformsettings.SettingKey allow-list（types.go），不照搬上游字段名。
//   - new-api 系统设置页：开关卡 + 文本配置项的分组运营形态（功能形态借鉴）。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';

// ---- 共享：管理 token + PUT（client.ts 未提供 PUT，且不可改它）----

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

// ============================================================================
// 平台设置（/v1/admin/platform-settings）—— platform_admin 必需
// ============================================================================

// platformSettingsResponse（platformsettings_handler.go:41）。captcha_enabled 这一项带 health。
export interface PlatformSetting {
  key: string;
  value: string;
  source: string; // "db" | "default"（platformsettings.SourceDB / SourceDefault）
  updated_at: string | null; // RFC3339（来自 db 时非空）
  updated_by: string | null; // actor token id 文本
  health?: PlatformSettingHealth | null;
}

// platformSettingsHealth（platformsettings_handler.go:50），仅 captcha_enabled 项返回。
export interface PlatformSettingHealth {
  status: string; // "ok" | "degraded"
  issue?: string; // 例 "turnstile_secret_missing"
  captcha_secret_configured: boolean;
}

interface PlatformSettingsListResponse {
  items: PlatformSetting[];
}

// listPlatformSettings — GET /v1/admin/platform-settings（返回全部 allow-list key 的当前值/来源）。
export async function listPlatformSettings(): Promise<PlatformSetting[]> {
  const resp = await apiGet<PlatformSettingsListResponse>('/v1/admin/platform-settings');
  return resp.items ?? [];
}

// updatePlatformSetting — PUT /v1/admin/platform-settings/{key}  body{value, reason?}
// value 必填（后端 req.Value==nil → 400 platform_setting_value_required）。reason 可选，进审计。
// 返回更新后的单个 setting（含新 source/updated_at/updated_by/health）。
export function updatePlatformSetting(
  key: string,
  value: string,
  reason?: string,
): Promise<PlatformSetting> {
  const body: { value: string; reason?: string } = { value };
  if (reason && reason.trim() !== '') body.reason = reason.trim();
  return adminPut<PlatformSetting>(`/v1/admin/platform-settings/${encodeURIComponent(key)}`, body);
}

// ---- 设置项元数据（前端展示用；类型与约束镜像后端 platformsettings/types.go ValidateValue 规则，
//      仅用于「渲染什么控件 + 前端轻校验」，后端仍是唯一权威校验源）----

export type SettingControl = 'bool' | 'int' | 'text' | 'textarea';

export interface SettingDescriptor {
  key: string;
  label: string;
  help?: string;
  control: SettingControl;
  // int 控件的非负/正数提示（后端真正校验）。
  intMin?: number;
}

export interface SettingsGroup {
  id: string;
  title: string;
  description?: string;
  keys: SettingDescriptor[];
}

// 分组：镜像 platformsettings allow-list（types.go orderedSettingKeys 的关键子集），按运营语义聚类。
// 仅纳入「单值开关 / 整数 / 短文本」这类适合卡片逐项编辑的项；复杂 JSON 项（payment_provider_config /
// model_fallback_chains / budget_limits / moderation_thresholds 等）留待专门切片，避免本面塞难编辑的 JSON。
export const SETTINGS_GROUPS: SettingsGroup[] = [
  {
    id: 'registration',
    title: '注册与登录',
    description: '控制新用户注册、邀请制与密码登录开关。',
    keys: [
      { key: 'registration_enabled', label: '允许注册', control: 'bool', help: '关闭后新用户无法自助注册。' },
      { key: 'invitation_required', label: '需要邀请码', control: 'bool', help: '开启后注册必须携带有效邀请码。' },
      { key: 'password_register_enabled', label: '允许密码注册', control: 'bool' },
      { key: 'password_login_enabled', label: '允许密码登录', control: 'bool' },
    ],
  },
  {
    id: 'security',
    title: '安全与验证',
    description: '两步验证、验证码与 Passkey 等账户安全开关。',
    keys: [
      { key: 'two_factor_enabled', label: '两步验证（2FA）', control: 'bool', help: '开启后登录需二次验证；关闭前请确认后端已装配 2FA 依赖。' },
      { key: 'captcha_enabled', label: '启用验证码', control: 'bool', help: '需先在后端配置 Turnstile 密钥，否则开启会被拒（captcha_secret_required）。' },
      { key: 'captcha_provider', label: '验证码提供商', control: 'text', help: '取值：turnstile / recaptcha / hcaptcha。' },
      { key: 'captcha_site_key', label: '验证码 Site Key', control: 'text', help: '前端公开 site key（非密钥）。' },
      { key: 'passkey_enabled', label: '启用 Passkey 登录', control: 'bool' },
      { key: 'passkey_registration_enabled', label: '允许 Passkey 注册', control: 'bool' },
    ],
  },
  {
    id: 'site',
    title: '站点信息',
    description: '面向用户的站点名称、标语与公开链接（均为公开展示文本，非密钥）。',
    keys: [
      { key: 'site_name', label: '站点名称', control: 'text' },
      { key: 'site_subtitle', label: '站点标语', control: 'text' },
      { key: 'site_contact_info', label: '联系方式', control: 'text', help: '邮箱 / IM / 自由文本，展示给用户。' },
      { key: 'site_doc_url', label: '文档链接', control: 'text', help: 'http(s) URL。' },
      { key: 'site_api_base_url', label: 'API 基础地址', control: 'text', help: '用户客户端接入用的公开网关地址（http(s)）。' },
      { key: 'site_footer', label: '页脚文本', control: 'textarea' },
    ],
  },
  {
    id: 'reliability',
    title: '稳定性与冷却',
    description: '流式超时与上游限流（429/529）冷却窗口（单位：秒，正整数）。',
    keys: [
      { key: 'stream_timeout_seconds', label: '流式超时（秒）', control: 'int', intMin: 1 },
      { key: 'cooldown_429_seconds', label: '429 冷却（秒）', control: 'int', intMin: 1 },
      { key: 'cooldown_529_seconds', label: '529 冷却（秒）', control: 'int', intMin: 1 },
    ],
  },
  {
    id: 'growth',
    title: '签到与推荐',
    description: '每日签到奖励与推荐返利开关（金额单位：分）。',
    keys: [
      { key: 'promo_enabled', label: '启用促销码', control: 'bool' },
      { key: 'checkin_enabled', label: '启用每日签到', control: 'bool' },
      { key: 'checkin_min_cents', label: '签到最小奖励（分）', control: 'int', intMin: 1 },
      { key: 'checkin_max_cents', label: '签到最大奖励（分）', control: 'int', intMin: 1 },
      { key: 'referral_reward_enabled', label: '启用推荐返利', control: 'bool' },
      { key: 'referral_reward_cents', label: '推荐返利（分）', control: 'int', intMin: 0 },
    ],
  },
  {
    id: 'ops',
    title: '运维通知',
    description: '每日巡检报告收件地址（留空则巡检 worker 关闭，故障安全）。',
    keys: [
      { key: 'admin_notification_email', label: '运维通知邮箱', control: 'text', help: '单个邮箱地址；留空关闭每日巡检报告。' },
    ],
  },
];

// 本面纳入编辑的所有 key 集合（用于过滤后端返回的 items，未纳入的项只读忽略）。
export const MANAGED_SETTING_KEYS: ReadonlySet<string> = new Set(
  SETTINGS_GROUPS.flatMap((g) => g.keys.map((k) => k.key)),
);

// ============================================================================
// 邮件 SMTP 设置（/v1/admin/email）—— platform/tenant 管理员均可
// ============================================================================

// maskEmailSettings 行（admin_email_settings_handler.go:223）。密码项 value 被脱敏成空串 + configured。
export interface EmailSettingRow {
  key: string; // mailinfra.SettingMail*（如 smtp_host / smtp_port / smtp_password …）
  value: string; // 密码项恒为空串
  updated_at: string | null;
  updated_by: string | null;
  configured?: boolean; // 仅密码项：true=已设置（值非空）
}

export interface EmailSettingsResponse {
  tenant_id: number;
  settings: EmailSettingRow[];
}

// 后端 mailinfra 的设置 key（admin_email_settings_handler.go adminEmailSettingsValues 写入的键）。
// 仅用于把扁平 settings 行映射回表单字段；真实键名以后端 mailinfra.SettingMail* 常量为准。
export const EMAIL_KEYS = {
  host: 'smtp_host',
  port: 'smtp_port',
  username: 'smtp_username',
  password: 'smtp_password',
  from: 'smtp_from',
  fromName: 'smtp_from_name',
  tls: 'smtp_use_tls',
  verify: 'email_verify_enabled',
} as const;

// getEmailSettings — GET /v1/admin/email/settings?tenant_id
export function getEmailSettings(tenantId: number): Promise<EmailSettingsResponse> {
  return apiGet<EmailSettingsResponse>('/v1/admin/email/settings', { tenant_id: tenantId });
}

// updateEmailSettings — PUT /v1/admin/email/settings  body{tenant_id, ...至少一项}
// 仅提交有变更的字段；smtp_password 省略=保留原值（后端 *string nil 不更新），传空串=显式清空。
export interface EmailSettingsUpdate {
  tenant_id: number;
  smtp_host?: string;
  smtp_port?: number;
  smtp_username?: string;
  smtp_password?: string; // 省略则不动；传值（含空串）则覆盖
  smtp_from?: string;
  smtp_from_name?: string;
  smtp_use_tls?: boolean;
  email_verify_enabled?: boolean;
}

export interface EmailSettingsUpdateResult {
  tenant_id: number;
  updated: number;
}

export function updateEmailSettings(input: EmailSettingsUpdate): Promise<EmailSettingsUpdateResult> {
  // 不传 undefined 字段（后端 DisallowUnknownFields 只对未知键报错，但留空字段也无意义）。
  const body: Record<string, unknown> = { tenant_id: input.tenant_id };
  if (input.smtp_host !== undefined) body.smtp_host = input.smtp_host;
  if (input.smtp_port !== undefined) body.smtp_port = input.smtp_port;
  if (input.smtp_username !== undefined) body.smtp_username = input.smtp_username;
  if (input.smtp_password !== undefined) body.smtp_password = input.smtp_password;
  if (input.smtp_from !== undefined) body.smtp_from = input.smtp_from;
  if (input.smtp_from_name !== undefined) body.smtp_from_name = input.smtp_from_name;
  if (input.smtp_use_tls !== undefined) body.smtp_use_tls = input.smtp_use_tls;
  if (input.email_verify_enabled !== undefined) body.email_verify_enabled = input.email_verify_enabled;
  return adminPut<EmailSettingsUpdateResult>('/v1/admin/email/settings', body);
}

// sendEmailTest — POST /v1/admin/email/test  body{tenant_id, to}
export interface EmailTestResult {
  tenant_id: number;
  sent: boolean;
}

export function sendEmailTest(tenantId: number, to: string): Promise<EmailTestResult> {
  return apiPost<EmailTestResult>('/v1/admin/email/test', { tenant_id: tenantId, to });
}

// ============================================================================
// TLS 指纹画像（/v1/admin/tls-fingerprint-profiles）—— platform_admin 必需，本面只读列表
// ============================================================================

// tlsfpadmin.Profile（tlsfpadmin/types.go:33）。列表只展示核心标识/状态字段；CRUD 写入留待后续切片。
export interface TLSFingerprintProfile {
  id: number;
  tenant_id: number;
  name: string;
  description?: string | null;
  grease_enabled: boolean;
  cipher_suites: number[];
  supported_curves: number[];
  ec_point_formats: number[];
  signature_algorithms: number[];
  alpn_protocols: string[];
  tls_supported_versions: number[];
  key_share_groups: number[];
  psk_modes: number[];
  extensions_order: number[];
  expected_ja3_hash: string;
  status: string; // active / disabled
  last_validated_at?: string | null;
  created_at: string;
  updated_at: string;
}

interface TLSProfileListResponse {
  object: string;
  items: TLSFingerprintProfile[];
}

// listTLSProfiles — GET /v1/admin/tls-fingerprint-profiles?tenant_id（正整数必需）。
export async function listTLSProfiles(tenantId: number): Promise<TLSFingerprintProfile[]> {
  const resp = await apiGet<TLSProfileListResponse>('/v1/admin/tls-fingerprint-profiles', {
    tenant_id: tenantId,
  });
  return resp.items ?? [];
}

// ============================================================================
// 展示辅助
// ============================================================================

export function isTrue(value: string | undefined | null): boolean {
  return value === 'true';
}

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

// source 文本 -> 中文标签。
export function sourceLabel(source: string): string {
  if (source === 'db') return '已自定义';
  if (source === 'default') return '默认值';
  return source || '—';
}

// 前端轻校验：int 控件值是否满足 min（后端仍是权威校验）。返回错误文案或 null。
export function validateIntInput(value: string, min: number | undefined): string | null {
  const trimmed = value.trim();
  if (trimmed === '') return '请输入数值。';
  if (!/^-?\d+$/.test(trimmed)) return '请输入整数。';
  const n = Number(trimmed);
  if (min !== undefined && n < min) return `不能小于 ${min}。`;
  return null;
}
