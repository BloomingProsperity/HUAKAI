/*
 * 系统健康(运维台只读)前端类型 —— 镜像 systemhealthhttp.HealthResponse。
 * 端点:GET /v1/admin/system/health(admin token 鉴权,只读聚合,无计费副作用)。
 */
export type HealthStatus = 'healthy' | 'degraded' | 'unhealthy'

/** 单个子系统组件:database / channel_health / dlq / alerting。 */
export interface HealthComponent {
  name: string
  status: HealthStatus
  detail?: string
}

/** 网关进程运行时快照(请求时直接读 Go runtime,纯诊断,非密钥)。 */
export interface RuntimeInfo {
  go_version: string
  num_goroutine: number
  heap_alloc_bytes: number
  heap_sys_bytes: number
  num_gc: number
  uptime_seconds: number
  binary_size_bytes?: number
}

export interface HealthResponse {
  status: HealthStatus
  components: HealthComponent[]
  runtime: RuntimeInfo
}
