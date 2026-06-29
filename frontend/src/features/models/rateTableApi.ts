import { apiGet } from '../../lib/api'
import type { RateTable, SnapshotsResponse } from './rateTable'

/*
 * 费率版本 / 快照(公开只读)数据访问层。三个端点均为 /v1/pricing/* 公开读:
 *  - GET /v1/pricing/snapshots             列出历史 version 快照
 *    (backend/internal/gatewayhttp/cost_receipt_handler.go:572,routes.go:242)。
 *  - GET /v1/pricing/rate-table?version=X  按版本号取该版本费率表(version 必填,缺失后端 400)
 *    (handler:548,routes.go:229)。
 *  - GET /v1/pricing/snapshots/{id}        按快照主键 id 取费率表详情
 *    (handler:587,routes.go:243)。
 * 这些路径不带 admin 前缀,apiGet 经 tokenForPath 走 session(或无 token,后端公开放行)。
 */

/** 列出历史费率快照(响应包裹 {snapshots:[...]},无数据时 snapshots 可能为 null)。 */
export async function listRateSnapshots(signal?: AbortSignal): Promise<SnapshotsResponse> {
  return apiGet<SnapshotsResponse>('/v1/pricing/snapshots', { signal })
}

/** 按版本号取费率表(version 必填)。 */
export async function getRateTableByVersion(version: string, signal?: AbortSignal): Promise<RateTable> {
  return apiGet<RateTable>('/v1/pricing/rate-table', { query: { version }, signal })
}

/** 按快照 id 取费率表详情。 */
export async function getRateSnapshot(snapshotID: number, signal?: AbortSignal): Promise<RateTable> {
  return apiGet<RateTable>(`/v1/pricing/snapshots/${snapshotID}`, { signal })
}
