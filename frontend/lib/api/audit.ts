// 审计 & 回执（HUAKAI 护城河）用户面 API 封装。
// 端点形状以 HUAKAI 后端真码为准（读 handler 确认，见每个函数注释）：
//
//  会话鉴权（session bearer，走 userClient.ts，401 自动单飞刷新）：
//   - GET  /v1/receipts/{request_id}              gatewayhttp.NewCostReceiptGetHandler
//   - POST /v1/receipts/{request_id}/verify       gatewayhttp.NewCostReceiptVerifyHandler
//   - POST /v1/receipts/{request_id}/disputes     controlhttp.NewCreateDisputeHandler
//   - GET  /v1/me/disputes                        controlhttp.NewListUserDisputesHandler
//
//  公开（无鉴权，cmd/gateway/routes.go 未挂 SessionMiddleware）：
//   - GET  /v1/audit/pubkey                       gatewayhttp.NewAuditPubkeyHandler
//   - GET  /v1/audit/verify?request_id&tenant_scope_ref   gatewayhttp.NewAuditVerifyHandler
//   - GET  /v1/audit/merkle-tree.json             gatewayhttp.NewAuditMerkleTreeHandler
//
// 这是 HUAKAI 独有面（回执/审计信任链），无需对照 sub2api/new-api/CLIProxyAPI ——
// 三家都「信任商家」，用户看不到 hop chain / 回执验签。
//
// 公开审计端点的浏览器验签 / hop-chain / merkle 类型沿用既有 lib/audit-api.ts
// （已是真端点、非 mock），本模块只新增 session-bound 的回执 + 争议表面，并提供
// 「回执 → tenant_scope_ref → 公开 audit verify」串联的加载器。
import { userGet, userPost } from './userClient';
import {
  fetchAuditMerkleTree,
  verifyAuditProofInBrowser,
  type AuditBundle,
  type AuditVerifyResponse,
  type AuditVerifyResult,
} from '@/lib/audit-api';

// ---- 回执（trust.receipt.v1） ----

// 与 gatewayhttp.UserReceiptCost 对齐（cost_receipt_handler.go）。
export interface UserReceiptCost {
  model: string;
  input_tokens: number;
  output_tokens: number;
  cached_tokens: number;
  cost_total_micro_usd: number;
  rate_table_snapshot_id: number;
}

// 与 gatewayhttp.UserCostReceipt 对齐（cost_receipt_handler.go: userCostReceiptFromAudit）。
// tenant_scope_ref 是后端哈希出的不透明串，前端无法重算 —— 用它去喂公开 audit verify。
export interface UserCostReceipt {
  schema_version: string;
  request_id: string;
  receipt_sequence: number;
  tenant_id?: number;
  tenant_scope_ref: string;
  occurred_at: string;
  cost: UserReceiptCost;
  validation_state: string;
  verdict: string;
  adjustment_refs: string[];
  canonical_hash: string;
  signature: string;
  pubkey_fingerprint: string;
}

// 与 gatewayhttp.receiptVerifyResponse 对齐（cost_receipt_handler.go）。
// status 取值：signed-only（验签通过）/ unverified（未签或 key 撤销/窗口外）/
// mismatch（哈希或字段不符）/ missing（schema 不支持）。
export interface ReceiptVerifyResponse {
  valid: boolean;
  status?: string;
  signature_valid: boolean;
  key_status: string;
  reason?: string;
  canonical_hash?: string;
  schema_version?: string;
  age_seconds: number;
  receipt_sequence: number;
  verdict?: string;
  delta_micro_usd?: number;
  fields_mismatch?: string[];
  refund_event_id?: number;
  supported_versions?: string[];
}

// 拉取某条回执（session）。404 = receipt_not_found；202 = receipt_unavailable（尚未定稿）；
// 503 = gateway_not_configured / receipt_read_failed（dev 装配 receiptStore 为 nil 时）。
export function fetchReceipt(requestId: string): Promise<UserCostReceipt> {
  return userGet<UserCostReceipt>(`/v1/receipts/${encodeURIComponent(requestId)}`);
}

// 校验已存回执签名（session）。空 body → 后端按 request_id 取已存回执验签
// （verifyStoredCostReceiptByID）。返回 signed-only / unverified / mismatch 等。
export function verifyStoredReceipt(requestId: string): Promise<ReceiptVerifyResponse> {
  return userPost<ReceiptVerifyResponse>(`/v1/receipts/${encodeURIComponent(requestId)}/verify`, {});
}

// ---- 争议（cost dispute，F-AUDIT-001） ----

// 与 controlhttp.disputeView 对齐（dispute_handler.go）。
// status：open / reviewing / resolved / rejected（audit.DisputeStatus*）。
export interface CostDispute {
  id: number;
  dispute_id: string;
  tenant_id: number;
  user_id: number;
  request_id: string;
  reason: string;
  status: string;
  operator_note?: string;
  created_at: string;
  resolved_at?: string;
}

interface DisputeListResponse {
  disputes: CostDispute[] | null;
}

interface DisputeCreateResponse {
  dispute: CostDispute;
}

// 后端只接受 {reason}（disputeCreateRequest）；tenant/user 由 session 注入，前端不上送。
// reason 必填、TrimSpace 后 1..4000（dispute_store.go validateCreate）。
export interface CreateDisputeRequest {
  reason: string;
}

// 列出当前用户的争议（session）。limit 缺省 100，范围 1..500。
export async function fetchMyDisputes(limit = 100): Promise<CostDispute[]> {
  const resp = await userGet<DisputeListResponse>('/v1/me/disputes', { limit });
  return resp.disputes ?? [];
}

// 对某条回执发起争议（session）。201 → {dispute}。
// 409 dispute_duplicate（同回执已存在争议）；404 receipt_not_found；400 invalid_dispute_request。
export async function createDispute(requestId: string, body: CreateDisputeRequest): Promise<CostDispute> {
  const resp = await userPost<DisputeCreateResponse>(
    `/v1/receipts/${encodeURIComponent(requestId)}/disputes`,
    body,
  );
  return resp.dispute;
}

// ---- 公开审计签名密钥 ----

// 与 gatewayhttp.AuditPubkeyResponse 对齐（audit_pubkey_handler.go）。公开端点。
export interface AuditPubkey {
  algorithm: string;
  fingerprint: string;
  pubkey_fingerprint: string;
  public_key_base64: string;
  key_status?: string;
  effective_from?: string;
  effective_to?: string;
}

// 拉取当前活跃审计公钥（公开，无鉴权）。
// dev 装配 auditSigner 为 nil 时 503 gateway_not_configured。
export function fetchAuditPubkey(): Promise<AuditPubkey> {
  // 公开端点不带 session header；普通 fetch 即可，但复用 userGet 也无害（仅多一个 Bearer）。
  return userGet<AuditPubkey>('/v1/audit/pubkey');
}

// ---- 公开 audit verify（带 tenant_scope_ref） ----

// 公开 /v1/audit/verify 强制要求 tenant_scope_ref（否则 400 missing_tenant_scope_ref），
// 而既有 lib/audit-api.ts 的 fetchAuditVerify 不带该参数，且该文件不在本页可编辑范围内，
// 故本模块自带一份带 tenant_scope_ref 的查询封装。公开端点无鉴权，普通 fetch 即可。
async function fetchAuditVerifyScoped(
  requestId: string,
  tenantScopeRef: string,
): Promise<AuditVerifyResponse> {
  const query = new URLSearchParams({
    request_id: requestId,
    tenant_scope_ref: tenantScopeRef,
  });
  const resp = await fetch(`/v1/audit/verify?${query.toString()}`, { cache: 'no-store' });
  if (resp.ok) return resp.json() as Promise<AuditVerifyResponse>;
  let message = `HTTP ${resp.status}`;
  try {
    const payload = (await resp.json()) as { error?: { message?: string } };
    message = payload.error?.message ?? message;
  } catch {
    // 保持 HTTP 状态作为错误信息。
  }
  throw new Error(message);
}

// ---- 回执 → tenant_scope_ref → 公开 audit verify 串联 ----

// 加载一条 request_id 的完整审计束：先用 session 拉回执拿到 tenant_scope_ref，
// 再用公开 audit verify 取 hop_chain / model_chain / merkle chain proof，并在浏览器内验签。
// 任一步失败抛错，由调用方按 section 容错（503/404 不拖垮整页）。
export async function loadAuditBundleForRequest(requestId: string): Promise<{
  receipt: UserCostReceipt;
  bundle: AuditBundle;
  result: AuditVerifyResult;
}> {
  const receipt = await fetchReceipt(requestId);
  const tenantScopeRef = receipt.tenant_scope_ref;
  const [verify, tree] = await Promise.all([
    fetchAuditVerifyScoped(requestId, tenantScopeRef),
    fetchAuditMerkleTree(),
  ]);
  const bundle: AuditBundle = { verify, tree, source: 'backend' };
  const result = await verifyAuditProofInBrowser(bundle);
  return { receipt, bundle, result };
}

// ---- 展示辅助 ----

// micro-USD（百万分之一美元）→ 人类可读 USD 串。
export function formatMicroUSD(micros: number): string {
  const usd = (micros ?? 0) / 1_000_000;
  return `$${usd.toLocaleString('en-US', { minimumFractionDigits: 4, maximumFractionDigits: 6 })}`;
}

// 回执 verify status → 中文标签 + 语义。
export function receiptStatusLabel(status?: string): string {
  switch (status) {
    case 'signed-only':
      return '已验签';
    case 'unverified':
      return '未验签 / 已撤销';
    case 'mismatch':
      return '不匹配';
    case 'missing':
      return '不支持的版本';
    default:
      return status || '未知';
  }
}

// 争议 status → 中文标签。
export function disputeStatusLabel(status: string): string {
  switch (status) {
    case 'open':
      return '待处理';
    case 'reviewing':
      return '审核中';
    case 'resolved':
      return '已解决';
    case 'rejected':
      return '已驳回';
    default:
      return status || '未知';
  }
}
