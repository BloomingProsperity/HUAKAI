/*
 * TLS 指纹 profile(出口拟真)运营台前端类型 —— 镜像后端 internal/tlsfphttp 的 JSON 形态。
 *
 * 端点(均 platform_admin token 鉴权,挂在 /v1/admin/tls-fingerprint-profiles,
 * 见 cmd/gateway/routes.go:1104 + tlsfphttp/handler.go MountTLSFPAdminRoutes:87):
 *   GET    /v1/admin/tls-fingerprint-profiles?tenant_id=N        列租户的 profile(handler.go:96,resp object="tls_fingerprint_profiles_list")
 *   POST   /v1/admin/tls-fingerprint-profiles                    新建(body 带 tenant_id,handler.go:136,resp {profile})
 *   GET    /v1/admin/tls-fingerprint-profiles/{id}?tenant_id=N   取单个(handler.go:114,resp {profile})
 *   PUT    /v1/admin/tls-fingerprint-profiles/{id}?tenant_id=N   全字段内容更新(handler.go:160,DisallowUnknownFields:不可夹带 status)
 *   POST   /v1/admin/tls-fingerprint-profiles/{id}/status?tenant_id=N  改状态(handler.go:192,body {status})
 *   DELETE /v1/admin/tls-fingerprint-profiles/{id}?tenant_id=N   软删除(handler.go:218,resp {deleted,id})
 *
 * 注意:platform_admin 角色下 tenant_id 必填(handler.go:101 parsePositiveQuery,
 * 必须为正整数;create 时从 body 取)。状态变更只走 /{id}/status,且管理员仅可设
 * "active" / "disabled"(service.go:27 adminSettableStatuses);"drift_detected" 是
 * drift 检测 worker 专属写入,只读暴露。drift 元数据(expected_ja3_hash 可写、
 * last_validated_at 只读)见 tlsfpadmin/types.go:29。
 */

/** 管理员可设置的 profile 状态(service.go:27 adminSettableStatuses)。 */
export type SettableStatus = 'active' | 'disabled'

/**
 * TLS 指纹 profile DTO(镜像 tlsfpadmin.Profile,types.go:31)。
 * 数值数组(cipher_suites 等)是 TLS 扩展/算法的 IANA 码点;服务端绝不为 null(序列化为 [])。
 */
export interface TLSFingerprintProfile {
  id: number
  tenant_id: number
  name: string
  description?: string | null
  /** GREASE:是否在 ClientHello 注入 GREASE 占位值(拟真主流浏览器/客户端)。 */
  grease_enabled: boolean
  /** 加密套件码点序列(IANA cipher suite 码,顺序敏感)。 */
  cipher_suites: number[]
  /** 支持的椭圆曲线/分组(supported_groups 扩展)。 */
  supported_curves: number[]
  /** EC 点格式(ec_point_formats 扩展)。 */
  ec_point_formats: number[]
  /** 签名算法(signature_algorithms 扩展)。 */
  signature_algorithms: number[]
  /** ALPN 协议(如 h2 / http/1.1),字符串数组。 */
  alpn_protocols: string[]
  /** 支持的 TLS 版本(supported_versions 扩展)。 */
  tls_supported_versions: number[]
  /** TLS 1.3 key_share 分组。 */
  key_share_groups: number[]
  /** PSK 交换模式(psk_key_exchange_modes 扩展)。 */
  psk_modes: number[]
  /** 扩展出现顺序(顺序本身是指纹的一部分)。 */
  extensions_order: number[]
  /** 期望的 JA3 哈希(drift 检测的基线,可在 create/update 设置)。 */
  expected_ja3_hash: string
  /** 状态:active / disabled / drift_detected(后者只读,由 drift worker 设置)。 */
  status: string
  /** drift worker 最近校验时间(只读)。 */
  last_validated_at?: string | null
  created_at?: string
  updated_at?: string
}

/** 列表响应(object="tls_fingerprint_profiles_list",handler.go:110)。 */
export interface ProfileListResponse {
  object: string
  items: TLSFingerprintProfile[]
}

/** 单个 profile 响应(get/create/update/status 均回 {profile})。 */
export interface ProfileResponse {
  profile: TLSFingerprintProfile
}

/** 删除响应({deleted,id},handler.go:235)。 */
export interface DeleteResponse {
  deleted: boolean
  id: number
}

/**
 * 新建 profile 请求体(镜像 createRequest,handler.go:47)。
 * tenant_id 在 body;status 不可设(DB 层默认 active)。
 */
export interface ProfileCreateRequest {
  tenant_id: number
  name: string
  description?: string | null
  grease_enabled: boolean
  cipher_suites: number[]
  supported_curves: number[]
  ec_point_formats: number[]
  signature_algorithms: number[]
  alpn_protocols: string[]
  tls_supported_versions: number[]
  key_share_groups: number[]
  psk_modes: number[]
  extensions_order: number[]
  expected_ja3_hash: string
}

/**
 * 更新 profile 请求体(镜像 updateRequest,handler.go:66)。
 * 刻意省略 tenant_id(走 query)、id(走 path)、status(走 /{id}/status);
 * 后端 DisallowUnknownFields 会拒绝任何额外字段(含 status)。
 */
export interface ProfileUpdateRequest {
  name: string
  description?: string | null
  grease_enabled: boolean
  cipher_suites: number[]
  supported_curves: number[]
  ec_point_formats: number[]
  signature_algorithms: number[]
  alpn_protocols: string[]
  tls_supported_versions: number[]
  key_share_groups: number[]
  psk_modes: number[]
  extensions_order: number[]
  expected_ja3_hash: string
}

/**
 * profile 编辑表单态(供页面 useState)。数值数组在 UI 里以逗号/空白分隔的文本编辑,
 * ALPN 同理用文本;提交前由 tlsfp.ts 的 parse* 解析回数组。
 */
export interface ProfileForm {
  name: string
  description: string
  greaseEnabled: boolean
  /** 逗号/空白分隔的整数文本。 */
  cipherSuites: string
  supportedCurves: string
  ecPointFormats: string
  signatureAlgorithms: string
  /** 逗号/空白分隔的字符串(如 "h2, http/1.1")。 */
  alpnProtocols: string
  tlsSupportedVersions: string
  keyShareGroups: string
  pskModes: string
  extensionsOrder: string
  expectedJa3Hash: string
}

/** 空白表单初值(新建用)。 */
export const EMPTY_FORM: ProfileForm = {
  name: '',
  description: '',
  greaseEnabled: true,
  cipherSuites: '',
  supportedCurves: '',
  ecPointFormats: '',
  signatureAlgorithms: '',
  alpnProtocols: '',
  tlsSupportedVersions: '',
  keyShareGroups: '',
  pskModes: '',
  extensionsOrder: '',
  expectedJa3Hash: '',
}
