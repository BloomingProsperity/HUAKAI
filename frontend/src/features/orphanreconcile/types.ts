/*
 * 媒体任务孤儿对账(money 敏感)运营台前端类型 —— 镜像后端
 * internal/orphanreconcilehttp 的 JSON 形态。
 *
 * 端点(均 admin token 鉴权,挂在 /admin/v1,见 cmd/gateway/routes.go:914-915):
 *   GET  /admin/v1/media-task-orphans?tenant_id=&limit=        列 pending 孤儿(routes.go:85 newListHandler)
 *   POST /admin/v1/media-task-orphans/{id}/reconcile          显式对账一个孤儿(reconcile.go:42 newReconcileHandler)
 *
 * 孤儿 = 上游已创建任务(provider_task_id)却因租约在 Submit 期间被抢走而未落回 media_tasks;
 * 上游可能已计费,本平台却无对应扣费,需 admin 手动对账。**追扣(back_charge=true)走既有
 * billing.Capture 真实扣余额,是 money 敏感动作**。
 *
 * 注意:platform_admin 角色下 tenant_id 为【可选收窄】(缺省=跨租户全局扫,routes.go:134);
 * tenant_operator 强制限自己租户(routes.go:144)。前端列表的 tenant_id 输入仅作可选过滤。
 */

/** 单条孤儿线索 DTO(镜像 orphanItem,routes.go:69)。 */
export interface OrphanItem {
  id: number
  task_id: number
  tenant_id: number
  user_id: number
  provider: string
  provider_task_id: string
  /**
   * 预估漏扣金额(分)。注意后端 toItem(routes.go:112)说明:OrphanRecord 不带
   * estimated_cents(它在 media_tasks 行上),列表恒为 0 占位;真金额以追扣返回的
   * captured_cents 为准。前端展示时按此口径标注。
   */
  estimated_cents: number
  /** 对账状态:pending(待处置)/ reconciled / cancelled / ignored。 */
  reconcile_status: string
  /** 上报时间(RFC3339)。 */
  observed_at: string
}

/** 孤儿列表响应(routes.go:81 listResponse)。 */
export interface OrphanListResponse {
  items: OrphanItem[]
}

/**
 * 对账请求体(镜像 reconcileRequest,reconcile.go:26)。
 *   - status:目标终态 reconciled / cancelled / ignored。默认 reconciled。
 *   - back_charge:【money】是否追扣漏掉的费用。默认 false=仅标记不扣钱;
 *     仅当 status=reconciled 时合法(后端硬约束 reconcile.go:177)。
 *   - reason:可选备注,落审计。
 */
export interface ReconcileRequest {
  status: string
  back_charge: boolean
  reason?: string
}

/**
 * 对账响应(镜像 reconcileResponse,reconcile.go:32)。
 *   - advanced:状态是否真的从 pending 推进到终态(幂等:重复对账已终态的孤儿为 false)。
 *   - back_charged:本次是否走了 settle 追扣路径。
 *   - captured_cents:【money】真实扣到的金额(分);仅真追扣时为正,仅标记时为 0。
 *   - back_charge_outcome:仅追扣请求时回显。captured=真扣到;其余值
 *     (hold_not_held / task_archived / no_estimate / holdref_unparseable)=未扣到,
 *     孤儿保持 pending(后端此时返回 409,reconcile.go:90)。
 */
export interface ReconcileResponse {
  orphan_id: number
  status: string
  advanced: boolean
  back_charged: boolean
  captured_cents: number
  back_charge_outcome?: string
}

/** 终态选项(镜像后端 auditAction 接受的 status 集合,reconcile.go:152)。 */
export const RECONCILE_STATUSES = ['reconciled', 'cancelled', 'ignored'] as const
export type ReconcileStatus = (typeof RECONCILE_STATUSES)[number]
