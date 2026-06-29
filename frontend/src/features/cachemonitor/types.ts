/*
 * L2 响应缓存监控的前端 DTO,镜像后端 gatewayhttp/admin_cache_l2_handler.go
 * 的 adminL2StatsResponse(:24)与 cache/store.go 的 EntryStats(:26)、
 * cachemetrics/l2.go 的 L2SnapshotRow(:15)。
 * 仅暴露安全元数据,不含 response body 明文(后端 EntryStats 注释 store.go:25)。
 */

/** 单条缓存条目元数据(后端 cache.EntryStats,store.go:26)。 */
export interface L2EntryStat {
  key: string
  tenant_id: number
  vendor: string
  model: string
  status: number
  size_bytes: number
  /** RFC3339 时间串(Go time.Time JSON 形态)。 */
  stored_at: string
  expires_at: string
}

/** 按 vendor/model label 聚合的命中/容量指标行(后端 cachemetrics.L2SnapshotRow,l2.go:15)。
 *  仅 platform_admin 角色返回非空 metrics(handler.go:53),租户操作员拿到空对象。 */
export interface L2MetricsRow {
  hit_total: number
  miss_total: number
  size_bytes: number
}

/** GET /admin/v1/cache/l2/stats 的响应(后端 adminL2StatsResponse,handler.go:24)。 */
export interface L2StatsResponse {
  enabled: boolean
  size_bytes: number
  max_size_bytes: number
  ttl_seconds: number
  entries: L2EntryStat[]
  /** label → 指标行;键形如 "vendor/model"。租户操作员为空对象。 */
  metrics: Record<string, L2MetricsRow>
}

/** DELETE /admin/v1/cache/l2/{key} 的响应(后端 handler.go:90 写回 {key, deleted})。 */
export interface L2EvictResponse {
  key: string
  deleted: boolean
}
