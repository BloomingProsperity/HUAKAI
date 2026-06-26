import type { BackupManifest, BackupTable } from './types'

/*
 * 备份 manifest 纯展示逻辑(与 React 解耦,便于 vitest 变异测试)。
 */

/** totalEstimatedRows 汇总所有表的行数估算(页头一眼看全库规模)。 */
export function totalEstimatedRows(m: BackupManifest): number {
  return m.tables.reduce((sum, t) => sum + (t.estimated_rows || 0), 0)
}

/** topTablesByRows 按行数降序取前 n 大表(空表名兜底,稳定排序保留同值原序)。 */
export function topTablesByRows(tables: BackupTable[], n: number): BackupTable[] {
  return [...tables]
    .map((t, i) => ({ t, i }))
    .sort((a, b) => b.t.estimated_rows - a.t.estimated_rows || a.i - b.i)
    .slice(0, n)
    .map((x) => x.t)
}
