/*
 * 退款/扣费争议台前端类型 —— 镜像后端 internal/controlhttp 的 dispute JSON 形态。
 *
 * 端点(均 admin token 鉴权,挂在 /v1/admin/disputes,见 cmd/gateway/routes.go:1088-1091):
 *   GET  /v1/admin/disputes?tenant_id=N&status=&limit=&offset=  列争议(dispute_handler.go:139)
 *   POST /v1/admin/disputes/{id}/resolve                        裁决争议(dispute_handler.go:172)
 *
 * 注意:platform_admin 角色下 tenant_id query 必填(dispute_handler.go:280),tenant_operator
 * 自动用其 scope 租户。裁决体里仍要带 tenant_id(后端 CanIssueForTenant 二次校验,
 * dispute_handler.go:191),并以 path {id} 定位争议。
 *
 * 这是 money 敏感台:争议针对的是一笔已计费请求(request_id 对应费用回执),
 * 裁决结果(resolved/rejected)是运营对该笔费用是否退款/维持的人工决断。
 */

/** 争议状态:镜像后端 audit.DisputeStatus*(dispute_store.go:28-31)。 */
export type DisputeStatus = 'open' | 'reviewing' | 'resolved' | 'rejected'

/** 后端认可的全部状态值(裁决下拉与前端校验都用它)。 */
export const DISPUTE_STATUSES: DisputeStatus[] = ['open', 'reviewing', 'resolved', 'rejected']

/**
 * 争议视图 DTO(镜像 disputeView,dispute_handler.go:62-73)。
 * 时间为后端 RFC3339 字符串;operator_note / resolved_at 在未裁决时可能缺省。
 */
export interface DisputeView {
  id: number
  dispute_id: string
  tenant_id: number
  user_id: number
  request_id: string
  reason: string
  status: string
  /** 运营备注(omitempty:open 态可能没有)。 */
  operator_note?: string
  created_at: string
  /** 裁决落定时间(omitempty:未裁决为 null/缺省)。 */
  resolved_at?: string
}

/** 列表响应(镜像 {"disputes": []},dispute_handler.go:168)。 */
export interface DisputeListResponse {
  disputes: DisputeView[]
}

/** 裁决请求体(镜像 disputeResolveRequest,dispute_handler.go:56-60)。 */
export interface DisputeResolveRequest {
  tenant_id: number
  status: DisputeStatus
  operator_note: string
}

/** 裁决响应(镜像 {"dispute": {...}},dispute_handler.go:205)。 */
export interface DisputeResolveResponse {
  dispute: DisputeView
}

/** 列表过滤草稿(状态空串=不下发 status,取全部)。 */
export interface DisputeFilters {
  status: '' | DisputeStatus
}

export const EMPTY_DISPUTE_FILTERS: DisputeFilters = { status: '' }
