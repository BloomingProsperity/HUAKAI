import type { HealthComponent, HealthStatus, RuntimeInfo } from './types'

/*
 * 系统健康纯逻辑(可单测):状态配色、运行时数值格式化、组件名中文化。
 */

export type Tone = 'ok' | 'warn' | 'danger'

export interface HealthStatView {
  label: string
  value: string
  hint?: string
  tone?: Tone
}

/** 健康状态 → 配色 tone。healthy=ok / degraded=warn / unhealthy=danger。 */
export function statusTone(status: HealthStatus): Tone {
  switch (status) {
    case 'healthy':
      return 'ok'
    case 'degraded':
      return 'warn'
    case 'unhealthy':
      return 'danger'
    default:
      return 'warn'
  }
}

/** 健康状态 → 中文短标。 */
export function statusLabel(status: HealthStatus): string {
  switch (status) {
    case 'healthy':
      return '健康'
    case 'degraded':
      return '降级'
    case 'unhealthy':
      return '故障'
    default:
      return status
  }
}

const COMPONENT_LABELS: Record<string, string> = {
  database: '数据库',
  channel_health: '渠道健康',
  dlq: '死信队列',
  alerting: '告警',
  system_health_source: '健康数据源',
}

/** 组件机器名 → 中文标;未知名原样返回。 */
export function componentLabel(name: string): string {
  return COMPONENT_LABELS[name] ?? name
}

/** 子系统响应到状态卡的纯映射；状态值与颜色必须使用同一份三态口径。 */
export function mapComponentStats(components: HealthComponent[]): HealthStatView[] {
  return components.map((component) => ({
    label: componentLabel(component.name),
    value: statusLabel(component.status),
    hint: component.detail || '—',
    tone: statusTone(component.status),
  }))
}

/**
 * uptime 秒 → 人类可读("Nd Nh Nm" / "Nh Nm" / "Nm Ns" / "Ns")。
 * 取最高的两个非零量级,保持简洁。
 */
export function fmtUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const s = Math.floor(seconds)
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${sec}s`
  return `${sec}s`
}

/** 字节 → 可读(B/KB/MB/GB,1024 进制,保留 1 位小数,整数不带小数)。 */
export function fmtBytes(bytes?: number): string {
  if (bytes === undefined || !Number.isFinite(bytes) || bytes < 0) return '—'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = bytes / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  const rounded = Math.round(v * 10) / 10
  const text = Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1)
  return `${text} ${units[i]}`
}

/** 整数千分位。 */
export function fmtInt(n: number): string {
  if (!Number.isFinite(n)) return '—'
  return Math.round(n).toLocaleString('en-US')
}

/** 运行时快照到统计卡的纯映射；后端未提供二进制大小时不生成空卡。 */
export function mapRuntimeStats(runtime: RuntimeInfo): HealthStatView[] {
  const stats: HealthStatView[] = [
    { label: '运行时长', value: fmtUptime(runtime.uptime_seconds) },
    { label: 'Go 版本', value: runtime.go_version },
    { label: '协程数', value: fmtInt(runtime.num_goroutine) },
    { label: 'GC 次数', value: fmtInt(runtime.num_gc) },
    { label: '堆已分配', value: fmtBytes(runtime.heap_alloc_bytes) },
    { label: '堆系统占用', value: fmtBytes(runtime.heap_sys_bytes) },
  ]
  if (runtime.binary_size_bytes !== undefined && runtime.binary_size_bytes > 0) {
    stats.push({ label: '二进制大小', value: fmtBytes(runtime.binary_size_bytes) })
  }
  return stats
}
