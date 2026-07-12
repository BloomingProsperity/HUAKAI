import { describe, expect, it } from 'vitest'
import { topTablesByRows, totalEstimatedRows } from './backup'
import type { BackupManifest, BackupTable } from './types'

function manifest(tables: BackupTable[]): BackupManifest {
  return {
    object: 'backup_manifest',
    schema_version: 151,
    schema_dirty: false,
    estimate_basis: 'reltuples',
    table_count: tables.length,
    tables,
    redaction_policy: { note: '', redacted_columns: [] },
  }
}

describe('totalEstimatedRows', () => {
  it('汇总所有表行数(变异:漏加任一表则偏小)', () => {
    expect(totalEstimatedRows(manifest([
      { name: 'users', estimated_rows: 10 },
      { name: 'api_keys', estimated_rows: 25 },
      { name: 'tenants', estimated_rows: 3 },
    ]))).toBe(38)
  })
  it('空清单为 0', () => {
    expect(totalEstimatedRows(manifest([]))).toBe(0)
  })
})

describe('topTablesByRows', () => {
  it('按行数降序取前 n(变异:若升序/不排序则首个不是最大表)', () => {
    const top = topTablesByRows(
      [
        { name: 'small', estimated_rows: 5 },
        { name: 'big', estimated_rows: 900 },
        { name: 'mid', estimated_rows: 50 },
      ],
      2,
    )
    expect(top.map((t) => t.name)).toEqual(['big', 'mid'])
  })
  it('同值时保留原序(稳定),且不超过 n', () => {
    const top = topTablesByRows(
      [
        { name: 'a', estimated_rows: 10 },
        { name: 'b', estimated_rows: 10 },
        { name: 'c', estimated_rows: 10 },
      ],
      2,
    )
    expect(top.map((t) => t.name)).toEqual(['a', 'b'])
  })
  it('不改原数组(纯函数)', () => {
    const tables = [
      { name: 'x', estimated_rows: 1 },
      { name: 'y', estimated_rows: 2 },
    ]
    topTablesByRows(tables, 1)
    expect(tables.map((t) => t.name)).toEqual(['x', 'y'])
  })
})
