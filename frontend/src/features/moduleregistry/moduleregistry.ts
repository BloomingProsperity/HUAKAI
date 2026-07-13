import type { BadgeTone } from '../../ui/StatusBadge'
import type { ModuleView, ProbeStatus } from './types'

/*
 * 模块知识脊柱总览页的纯逻辑(可单测,无 DOM/网络副作用):
 *   - 探针状态 → 徽章语气 + 中文标签(镜像后端 ProbeStatus 封闭枚举,descriptor.go:23-34)
 *   - 模块按 category 归组(保留后端 Snapshot 的按 ID 排序顺序)
 *   - 从模块列表抽取去重排序后的 category 选项(供过滤下拉)
 *   - 健康统计计数(ok/degraded/error/unknown 各几个)
 * 全部为同步纯函数,便于变异测试打红。
 */

/**
 * 探针状态 → 徽章语气。ok=正常(ok);degraded=降级(warn);error=失败(danger);
 * unknown=未知/无探针(muted,后端明确「unknown 不是错误」,descriptor.go:28)。
 */
export function probeTone(status: ProbeStatus): BadgeTone {
  switch (status) {
    case 'ok':
      return 'ok'
    case 'degraded':
      return 'warn'
    case 'error':
      return 'danger'
    case 'unknown':
      return 'muted'
    default:
      // 后端枚举封闭;遇到未知字符串按中性处理,不误报为错误。
      return 'muted'
  }
}

/** 探针状态 → 中文标签。 */
export function probeLabel(status: ProbeStatus): string {
  switch (status) {
    case 'ok':
      return '正常'
    case 'degraded':
      return '降级'
    case 'error':
      return '失败'
    case 'unknown':
      return '未知'
    default:
      return status || '—'
  }
}

/**
 * 抽取去重后的 category 列表,按字典序升序(供过滤下拉的选项)。
 * 判别核心:同名 category 只出现一次,且结果有序;空 category 串被丢弃。
 */
export function extractCategories(modules: ModuleView[]): string[] {
  const seen = new Set<string>()
  for (const m of modules) {
    const c = m.category.trim()
    if (c !== '') seen.add(c)
  }
  return Array.from(seen).sort((a, b) => a.localeCompare(b))
}

/** 按 category 归组的结果项:类别名 + 该类别下的模块(保留入参顺序)。 */
export interface CategoryGroup {
  category: string
  modules: ModuleView[]
}

/**
 * 把模块按 category 归组,组的出现顺序 = 各 category 首次出现的顺序(稳定),
 * 组内模块顺序 = 入参顺序(即后端的按 ID 排序,view.go:77 注释)。
 * 判别核心:同 category 的模块聚到同一组、不打乱组内相对顺序,且不丢任何模块。
 */
export function groupByCategory(modules: ModuleView[]): CategoryGroup[] {
  const order: string[] = []
  const buckets = new Map<string, ModuleView[]>()
  for (const m of modules) {
    const c = m.category.trim() === '' ? '(未分类)' : m.category
    let bucket = buckets.get(c)
    if (!bucket) {
      bucket = []
      buckets.set(c, bucket)
      order.push(c)
    }
    bucket.push(m)
  }
  return order.map((c) => ({ category: c, modules: buckets.get(c) ?? [] }))
}

/** 健康统计:四种探针状态各计数。 */
export interface ProbeCounts {
  ok: number
  degraded: number
  error: number
  unknown: number
  total: number
}

/**
 * 统计各探针状态的模块数。
 * 判别核心:每个模块按其 live_probe.status 精确计入对应桶,total = 模块总数;
 * 未知/缺失状态计入 unknown(不漏计、不重复计)。
 */
export function countByProbe(modules: ModuleView[]): ProbeCounts {
  const counts: ProbeCounts = { ok: 0, degraded: 0, error: 0, unknown: 0, total: modules.length }
  for (const m of modules) {
    switch (m.live_probe?.status) {
      case 'ok':
        counts.ok += 1
        break
      case 'degraded':
        counts.degraded += 1
        break
      case 'error':
        counts.error += 1
        break
      default:
        // unknown 或任何缺失/未识别状态都归入 unknown 桶。
        counts.unknown += 1
        break
    }
  }
  return counts
}

export interface ModuleTableRow {
  id: string
  title: string
  capabilities: string[]
  probe: string
  probeTone: BadgeTone
  probeDetail: string
  catalogSummary: string
  featureId: string
  packages: string
  section: string
}

/** 模块视图 DTO 到列表展示行的纯映射。 */
export function mapModuleRows(modules: ModuleView[]): ModuleTableRow[] {
  return modules.map((module) => ({
    id: module.id,
    title: module.title || module.id,
    capabilities: module.capabilities ?? [],
    probe: probeLabel(module.live_probe.status),
    probeTone: probeTone(module.live_probe.status),
    probeDetail: module.live_probe.detail ?? '',
    catalogSummary: module.catalog
      ? `${module.catalog.status || '—'}${module.catalog.parity ? ` · ${module.catalog.parity}` : ''}`
      : '纯实时',
    featureId: module.catalog?.feature_id ?? '',
    packages: module.catalog?.pkgs?.join(', ') ?? '',
    section: module.catalog?.section || '—',
  }))
}
