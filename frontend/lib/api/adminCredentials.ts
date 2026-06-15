// admin 凭证 + 出站代理 API 封装（管理 token 轨：从 localStorage huakai_admin_token 取 Bearer，
// 非 session 用户面）。client.ts 只导出 apiGet/apiPost/apiPatch，未提供 PUT/DELETE 助手且按硬约束
// 不可改它 —— 故本模块内复用同一「localStorage huakai_admin_token → Bearer」约定自带私有
// adminPut/adminDelete，并复用 client.ts 导出的 ApiError，使 errors.ts friendlyMessage 仍可统一翻译
//（与 lib/api/adminUsers.ts 的做法一致）。
//
// 端点形状全部以 HUAKAI 后端真码为准（读 handler 确认，逐条标注）：
//
//   【凭证续期状态（读）】—— 复用既有 lib/api/renew.ts（不重复封装同一端点）：
//     GET    /admin/v1/credentials/renew-status   gatewayhttp.newListCredentialRenewStatusHandler
//            响应 {items: RenewStatusMetadata[], next_cursor}；游标分页；limit 1..500（默认 100）。
//            platform_admin 无 scope 时可省 tenant_id（列全部租户）或传指定租户；
//            tenant_operator / 有 scope 的 platform_admin 用自身 scope（传不匹配的 tenant_id → 403）。
//
//   【凭证导入 / OAuth 起步（写）—— 均 platform_admin 限定（resolveCredentialAcqAdmin）】：
//     POST   /admin/v1/credentials/paste         gatewayhttp.newCredentialAcqImportHelperHandler(FlowKindPaste)
//     POST   /admin/v1/credentials/cli-import    （FlowKindCLIImport）
//     POST   /admin/v1/credentials/csv-import    （FlowKindCSVImport）
//     POST   /admin/v1/credentials/json-import   （FlowKindJSONImport）
//            body credentialAcqHelperRequest{tenant_id, provider_account_id, vendor?, auth_mode?,
//            content?|credentials?, finalize?, reason?, redacted_context?}；
//            响应 {flows: Session[], finalized: FinalizeResult[]}（HTTP 201）。
//            paste 走 credentials(JSON 对象)；cli/csv/json-import 走 content(原始文本)。
//     POST   /admin/v1/credentials/oauth-init    gatewayhttp.newCredentialAcqOAuthInitHelperHandler
//            body credentialAcqStartRequest{tenant_id, provider_account_id, vendor, auth_mode,
//            redirect_uri?, requested_scopes?, oauth_client?, reason?}；
//            响应 {flow: Session, authorize_url?, state?, code_challenge?}（HTTP 201）。
//
//   【出站代理池 CRUD / 测试态（proxyadminhttp.MountRoutes，挂 /admin/v1/proxies）】：
//     GET    /admin/v1/proxies            newListHandler        → {items: proxyResponse[]}
//     POST   /admin/v1/proxies            newCreateHandler      → proxyResponse（201）
//     GET    /admin/v1/proxies/{id}       newGetHandler         → proxyResponse
//     PATCH  /admin/v1/proxies/{id}       newUpdateHandler      → proxyResponse
//     DELETE /admin/v1/proxies/{id}       newDeleteHandler      → 204
//     PUT    /admin/v1/proxies/{id}/status newSetStatusHandler  → {id, status}
//            读 DTO 无 auth_secret（写入用，永不回读）。create/update body DisallowUnknownFields：
//            {name, protocol, host, port, auth_username?, auth_secret?, status?(create only)}；
//            status ∈ {active, disabled, dead}；protocol 仅校验非空（http/https/socks5 等自由文本）。
//            tenant_operator 可省 ?tenant_id（用自身 scope）；platform_admin 必带（CanIssueForTenant 闸）。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12 — 仅提取功能/字段/动作/布局形态，未抄码；逐源注明）：
//   - sub2api(LGPL)@e34ad2b src/api/admin/proxies.ts + views/admin/ProxiesView.vue：
//       代理列表「protocol + status 过滤」形态、列集合（protocol / 地址 host:port / 状态徽章 / 操作）、
//       行动作（测试连通 / 编辑 / 删除）、create 表单字段（name/protocol/host/port/username/password）。
//       注：sub2api 代理状态枚举是 active/inactive/expired 且带 expires_at 过期窗 + test/quality 探测；
//       HUAKAI 后端代理状态是 active/disabled/dead、无过期列、无 test 端点（仅 set-status），
//       故本面照 HUAKAI 真形态，不照搬 sub2api 的 expires_at / test / quality 字段。
//   - sub2api(LGPL)@e34ad2b src/views/admin/AccountsView.vue + components/account/CreateAccountModal.vue：
//       凭证导入入口（paste / cli / csv / json 多法）+ 按 vendor（anthropic/openai/gemini）起 OAuth 的形态、
//       账号凭证「平台/状态/过期」列形态 —— 映射到 HUAKAI renew-status 的
//       vendor/auth_mode/state/access_expires_at/refresh_before_at/failure_class 字段。
//   字段集合完全对齐 HUAKAI 后端 credentialAcqHelperRequest / credentialAcqStartRequest / proxyResponse，
//   不照搬上游字段名（如 sub2api 的 platform/expires_at/latency_status 概念在 HUAKAI 不存在）。

import { ApiError, apiGet, apiPost, apiPatch } from './client';
import type { APIError, AuthCredentialRenewState } from './types';
// 续期状态读路径直接复用既有封装，避免重复封装同一端点。
import { listRenewStatus, RenewCredentialsForbiddenError } from './renew';

export { listRenewStatus, RenewCredentialsForbiddenError };
export type {
  AuthCredentialRenewStatus,
  AuthCredentialRenewStatusList,
  AuthCredentialRenewState,
} from './types';

// ---- 共享：管理 token 取用 + PUT/DELETE（client.ts 未提供这两个动词，且不可改它）----

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

async function adminDelete<T>(path: string): Promise<T> {
  const resp = await fetch(path, { method: 'DELETE', headers: adminHeaders() });
  return parse<T>(resp);
}

// 拼 ?tenant_id（platform_admin 必带；tenant_operator 省略用自身 scope）。
function tenantQuery(tenantId?: number): string {
  return tenantId != null ? `?tenant_id=${tenantId}` : '';
}

// =====================================================================================
//  凭证导入 / OAuth 起步（写）—— 均 platform_admin 限定
// =====================================================================================

// 后端 vendor 常量（credentialstore/types.go）。下拉枚举展示用，后端 Normalize 容错。
export const VENDORS = [
  'anthropic',
  'openai',
  'gemini',
  'copilot',
  'antigravity',
  'windsurf',
  'cursor',
  'openrouter',
  'deepseek',
  'grok',
  'kimi',
  'mistral',
  'groqcloud',
  'together',
  'perplexity',
  'fireworks',
] as const;

// 后端 import helper 的 FlowKind 子集（仅手工导入四法暴露到 UI）。
export type ImportKind = 'paste' | 'cli-import' | 'csv-import' | 'json-import';

// credentialacq.Session（部分字段；后端序列化省略加密内部字段）。
export interface CredentialAcqSession {
  id: string;
  tenant_id: number;
  provider_account_id: number;
  vendor: string;
  auth_mode: string;
  flow_kind: string;
  status: string;
  actor_id: string;
  actor_role: string;
  redacted_context: Record<string, unknown> | null;
  long_lived_requested: boolean;
  result_account_credential_id?: number;
  error_class?: string;
  error_message_redacted?: string;
  expires_at: string;
  created_at: string;
  updated_at: string;
}

// credentialstore.CredentialMetadata（finalize 后落库的凭证元数据；secret-free）。
export interface CredentialMetadata {
  id: number;
  tenant_id: number;
  provider_account_id: number;
  vendor: string;
  auth_mode: string;
  state: string;
  credential_version: number;
  created_at?: string;
  updated_at?: string;
}

// credentialacq.FinalizeResult（finalize=true 时返回；含落库凭证元数据 + 会话快照）。
export interface CredentialAcqFinalizeResult {
  session: CredentialAcqSession;
  credential?: CredentialMetadata;
}

// 导入 helper 响应（newCredentialAcqImportHelperHandler）：{flows, finalized}。
export interface CredentialImportResult {
  flows: CredentialAcqSession[];
  finalized: CredentialAcqFinalizeResult[];
}

// OAuth init 响应（newCredentialAcqOAuthInitHelperHandler / startCredentialAcqFlow）：
// {flow, authorize_url?, state?, code_challenge?}。
export interface CredentialOAuthInitResult {
  flow: CredentialAcqSession;
  authorize_url?: string;
  state?: string;
  code_challenge?: string;
}

// importCredentials — POST /admin/v1/credentials/{paste|cli-import|csv-import|json-import}
// paste：传 credentials（JSON 对象字符串，后端按 json_object 入会话）；
// cli/csv/json-import：传 content（原始 CLI 文件 / CSV / JSON 文本，后端解析多候选）。
// finalize=true 直接落库（否则只建会话，需后续 finalize）。
export function importCredentials(input: {
  kind: ImportKind;
  tenant_id: number;
  provider_account_id: number;
  vendor?: string;
  auth_mode?: string;
  content?: string;
  credentials?: string; // JSON 对象字符串（paste）
  finalize?: boolean;
  reason?: string;
}): Promise<CredentialImportResult> {
  const body: Record<string, unknown> = {
    tenant_id: input.tenant_id,
    provider_account_id: input.provider_account_id,
  };
  if (input.vendor && input.vendor.trim() !== '') body.vendor = input.vendor.trim();
  if (input.auth_mode && input.auth_mode.trim() !== '') body.auth_mode = input.auth_mode.trim();
  if (input.kind === 'paste') {
    // paste 走 credentials（json.RawMessage）—— 直接放解析后的对象，client.ts 会序列化。
    if (input.credentials && input.credentials.trim() !== '') {
      body.credentials = JSON.parse(input.credentials);
    }
  } else if (input.content && input.content.trim() !== '') {
    body.content = input.content;
  }
  if (input.finalize) body.finalize = true;
  if (input.reason && input.reason.trim() !== '') body.reason = input.reason.trim();
  return apiPost<CredentialImportResult>(`/admin/v1/credentials/${input.kind}`, body);
}

// oauthClient — credentialAcqStartRequest.oauth_client（可选；Gemini / ChatGPT 的 client_secret
// 由后端环境注入并忽略 body 内的 secret，故 UI 一般只填 client_id / scopes）。
export interface OAuthClientInput {
  client_id?: string;
  client_secret?: string;
  auth_url?: string;
  token_url?: string;
  redirect_uri?: string;
  scopes?: string[];
  source?: string;
}

// initOAuth — POST /admin/v1/credentials/oauth-init
// 返回 authorize_url（前端引导运营在浏览器打开授权），随后回调由后端 /oauth-callback 处理。
export function initOAuth(input: {
  tenant_id: number;
  provider_account_id: number;
  vendor: string;
  auth_mode: string;
  redirect_uri?: string;
  requested_scopes?: string[];
  oauth_client?: OAuthClientInput;
  reason?: string;
}): Promise<CredentialOAuthInitResult> {
  const body: Record<string, unknown> = {
    tenant_id: input.tenant_id,
    provider_account_id: input.provider_account_id,
    vendor: input.vendor,
    auth_mode: input.auth_mode,
  };
  if (input.redirect_uri && input.redirect_uri.trim() !== '') body.redirect_uri = input.redirect_uri.trim();
  if (input.requested_scopes && input.requested_scopes.length > 0) body.requested_scopes = input.requested_scopes;
  if (input.oauth_client) body.oauth_client = input.oauth_client;
  if (input.reason && input.reason.trim() !== '') body.reason = input.reason.trim();
  return apiPost<CredentialOAuthInitResult>('/admin/v1/credentials/oauth-init', body);
}

// =====================================================================================
//  出站代理池 CRUD / 状态
// =====================================================================================

// proxyadminhttp.proxyResponse（secret-free 读 DTO）。
export interface Proxy {
  id: number;
  name: string;
  protocol: string;
  host: string;
  port: number;
  auth_username: string | null;
  status: string; // active / disabled / dead
  last_check_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface ProxyListResponse {
  items: Proxy[];
}

export type ProxyStatus = 'active' | 'disabled' | 'dead';

// listProxies — GET /admin/v1/proxies?tenant_id
export function listProxies(tenantId?: number): Promise<ProxyListResponse> {
  return apiGet<ProxyListResponse>('/admin/v1/proxies', { tenant_id: tenantId });
}

// createProxy — POST /admin/v1/proxies?tenant_id  body{name, protocol, host, port, auth_username?, auth_secret?, status?}
// 后端 DisallowUnknownFields：仅这些字段；name/protocol/host 非空、port 1..65535。
export function createProxy(input: {
  name: string;
  protocol: string;
  host: string;
  port: number;
  auth_username?: string;
  auth_secret?: string;
  status?: ProxyStatus;
  tenant_id?: number;
}): Promise<Proxy> {
  const body: Record<string, unknown> = {
    name: input.name.trim(),
    protocol: input.protocol.trim(),
    host: input.host.trim(),
    port: input.port,
  };
  if (input.auth_username && input.auth_username.trim() !== '') body.auth_username = input.auth_username.trim();
  if (input.auth_secret && input.auth_secret !== '') body.auth_secret = input.auth_secret;
  if (input.status) body.status = input.status;
  return apiPost<Proxy>(`/admin/v1/proxies${tenantQuery(input.tenant_id)}`, body);
}

// updateProxy — PATCH /admin/v1/proxies/{id}?tenant_id  body{name, protocol, host, port, auth_username?, auth_secret?}
// 注意：update body 无 status 字段（状态走独立 set-status 端点）。auth_secret 留空表示不改密钥。
export function updateProxy(
  id: number,
  input: {
    name: string;
    protocol: string;
    host: string;
    port: number;
    auth_username?: string;
    auth_secret?: string;
    tenant_id?: number;
  },
): Promise<Proxy> {
  const body: Record<string, unknown> = {
    name: input.name.trim(),
    protocol: input.protocol.trim(),
    host: input.host.trim(),
    port: input.port,
  };
  if (input.auth_username && input.auth_username.trim() !== '') body.auth_username = input.auth_username.trim();
  if (input.auth_secret && input.auth_secret !== '') body.auth_secret = input.auth_secret;
  return apiPatch<Proxy>(`/admin/v1/proxies/${id}${tenantQuery(input.tenant_id)}`, body);
}

// deleteProxy — DELETE /admin/v1/proxies/{id}?tenant_id（204 无返回体）
export function deleteProxy(id: number, tenantId?: number): Promise<void> {
  return adminDelete<void>(`/admin/v1/proxies/${id}${tenantQuery(tenantId)}`);
}

// setProxyStatus — PUT /admin/v1/proxies/{id}/status?tenant_id  body{status}
// status ∈ {active, disabled, dead}；响应 {id, status}。
export function setProxyStatus(
  id: number,
  status: ProxyStatus,
  tenantId?: number,
): Promise<{ id: number; status: string }> {
  return adminPut<{ id: number; status: string }>(
    `/admin/v1/proxies/${id}/status${tenantQuery(tenantId)}`,
    { status },
  );
}

// =====================================================================================
//  展示辅助
// =====================================================================================

// 凭证续期状态 -> 中文标签。
export function renewStateLabel(state: AuthCredentialRenewState | string): string {
  switch (state) {
    case 'active':
      return '正常';
    case 'refreshing':
      return '续期中';
    case 'refreshing_with_grace':
      return '续期中（宽限）';
    case 'expired':
      return '已过期';
    case 'temp_unschedulable':
      return '临时不可调度';
    case 'needs_rotation':
      return '待轮换';
    case 'revoked':
      return '已吊销';
    case 'operator_attention':
      return '需人工处理';
    default:
      return state || '未知';
  }
}

// 凭证续期状态 -> Badge variant。
export function renewStateBadgeVariant(
  state: AuthCredentialRenewState | string,
): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (state === 'active') return 'default';
  if (state === 'expired' || state === 'revoked' || state === 'operator_attention') return 'destructive';
  if (state === 'refreshing' || state === 'refreshing_with_grace') return 'secondary';
  if (state === 'needs_rotation' || state === 'temp_unschedulable') return 'outline';
  return 'outline';
}

// 代理状态 -> 中文标签。
export function proxyStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '启用';
    case 'disabled':
      return '已停用';
    case 'dead':
      return '已失效';
    default:
      return status || '未知';
  }
}

// 代理状态 -> Badge variant。
export function proxyStatusBadgeVariant(
  status: string,
): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (status === 'active') return 'default';
  if (status === 'dead') return 'destructive';
  if (status === 'disabled') return 'secondary';
  return 'outline';
}

// RFC3339 -> 本地时间显示。
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

// 过期窗口的剩余天数提示（access_expires_at / refresh_before_at）。
// 返回 {label, urgent}：urgent 用于把临期/已过期标红。
export function expiryHint(value: string | null | undefined): { label: string; urgent: boolean } | null {
  if (!value) return null;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return null;
  const ms = d.getTime() - Date.now();
  const days = Math.floor(ms / 86_400_000);
  if (ms <= 0) return { label: '已过期', urgent: true };
  if (days === 0) return { label: '今日内到期', urgent: true };
  if (days <= 3) return { label: `${days} 天后到期`, urgent: true };
  return { label: `${days} 天后到期`, urgent: false };
}
