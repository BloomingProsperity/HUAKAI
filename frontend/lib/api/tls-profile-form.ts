// TLS 指纹画像（反 ban / mimicry uTLS 配置）admin 写体的纯逻辑（状态 allowlist + name 校验 +
// 精确 key-set 构造），零依赖 strip-types 单测。本面是运维对上游连接 TLS 指纹的配置 CRUD，
// 不改任何 mimicry 策略/姿态（策略在后端 + 运维运行时选择）。
// 后端真码: internal/tlsfphttp/handler.go(createRequest:48 / updateRequest:67 / setStatusRequest:83 /
//          decodeJSON:280 DisallowUnknownFields) + tlsfpadmin/service.go(name 非空必填) +
//          tlsfpadmin/types.go:29 adminSettableStatuses = {active, disabled}。

// admin 可设置的状态（后端 adminSettableStatuses）。
export const TLS_PROFILE_STATUSES = ['active', 'disabled'] as const;
export type TLSProfileStatus = (typeof TLS_PROFILE_STATUSES)[number];

export function isValidTLSProfileStatus(s: string): s is TLSProfileStatus {
  return s === 'active' || s === 'disabled';
}

// 画像可写字段：create 与 update 共享这 13 字段；create 另需 tenant_id（在 body）。
// 字段为 TLS ClientHello 各扩展列表的原始值，由运维提供；后端按 JA3 期望等校验。
export interface TLSProfileInput {
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
}

// validateTLSProfileInput 镜像后端 service：name 非空必填（tenant_id 由调用方另校验）。
export function validateTLSProfileInput(input: TLSProfileInput): string | null {
  if (!input.name.trim()) return 'name 必填';
  return null;
}

// 共享 13 字段的精确映射（后端 DisallowUnknownFields，多余键 → 400）。
function profileFields(input: TLSProfileInput): Record<string, unknown> {
  return {
    name: input.name,
    description: input.description ?? null,
    grease_enabled: input.grease_enabled,
    cipher_suites: input.cipher_suites,
    supported_curves: input.supported_curves,
    ec_point_formats: input.ec_point_formats,
    signature_algorithms: input.signature_algorithms,
    alpn_protocols: input.alpn_protocols,
    tls_supported_versions: input.tls_supported_versions,
    key_share_groups: input.key_share_groups,
    psk_modes: input.psk_modes,
    extensions_order: input.extensions_order,
    expected_ja3_hash: input.expected_ja3_hash,
  };
}

// POST / 创建体：tenant_id（body）+ 13 字段。
export function buildTLSProfileCreateBody(tenantId: number, input: TLSProfileInput): Record<string, unknown> {
  return { tenant_id: tenantId, ...profileFields(input) };
}

// PUT /{id} 更新体：仅 13 字段——**不含 tenant_id、不含 status**。
// 后端注释明确 PUT 经 DisallowUnknownFields 拒 status：状态只能经 POST /{id}/status 改。
// 漏掉这个纪律会让 update 误带 status（被 400 拒，或混淆状态修改路径）——属安全相关 key-set 约束。
export function buildTLSProfileUpdateBody(input: TLSProfileInput): Record<string, unknown> {
  return profileFields(input);
}
