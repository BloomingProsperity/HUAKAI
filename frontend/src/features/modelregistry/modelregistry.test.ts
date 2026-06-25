import { describe, expect, it } from 'vitest'
import {
  buildCapabilitiesMap,
  importResultTone,
  isKnownCapability,
  KNOWN_CAPABILITIES,
  parseAliasLines,
  splitParsedAliases,
  summarizeImportResults,
} from './modelregistry'
import type { AliasImportResult } from './types'

describe('parseAliasLines', () => {
  it('跳过空行/纯空白行/# 注释行', () => {
    // 判别核心:仅非空非注释行产出。变异(去掉跳过逻辑)→ 空行会变成失败行使长度≠1 → RED。
    const parsed = parseAliasLines('\n  \n# 这是注释\n7,gpt-x,global\n')
    expect(parsed).toHaveLength(1)
    expect(parsed[0].ok).toBe(true)
  })

  it('合法 tenant 行解析出全部字段', () => {
    const [p] = parseAliasLines('42, claude-fast , tenant, 9, 极速版')
    expect(p.ok).toBe(true)
    if (p.ok) {
      expect(p.row).toEqual({ model_id: 42, alias: 'claude-fast', scope: 'tenant', tenant_id: 9, display: '极速版' })
    }
  })

  it('scope 缺省为 tenant 且要求 tenant_id', () => {
    // 判别核心:scope 省略→tenant,缺 tenant_id 必须失败。变异(默认 global 或不校验 tenant_id)→ RED。
    const [p] = parseAliasLines('5,my-alias')
    expect(p.ok).toBe(false)
    if (!p.ok) expect(p.error).toContain('tenant_id')
  })

  it('global 行不要求 tenant_id', () => {
    const [p] = parseAliasLines('5,my-alias,global')
    expect(p.ok).toBe(true)
    if (p.ok) {
      expect(p.row.scope).toBe('global')
      expect(p.row.tenant_id).toBeUndefined()
    }
  })

  it('model_id 非正整数判失败', () => {
    expect(parseAliasLines('0,a,global')[0].ok).toBe(false)
    expect(parseAliasLines('-3,a,global')[0].ok).toBe(false)
    expect(parseAliasLines('abc,a,global')[0].ok).toBe(false)
    expect(parseAliasLines('1.5,a,global')[0].ok).toBe(false)
  })

  it('alias 为空判失败', () => {
    const [p] = parseAliasLines('7,,global')
    expect(p.ok).toBe(false)
    if (!p.ok) expect(p.error).toContain('alias')
  })

  it('非法 scope 判失败', () => {
    const [p] = parseAliasLines('7,a,weird')
    expect(p.ok).toBe(false)
    if (!p.ok) expect(p.error).toContain('scope')
  })
})

describe('splitParsedAliases', () => {
  it('拆出可提交行与本地失败行', () => {
    const parsed = parseAliasLines('1,ok-a,global\nbad,row,global\n2,ok-b,global')
    const { rows, invalid } = splitParsedAliases(parsed)
    expect(rows).toHaveLength(2)
    expect(invalid).toHaveLength(1)
    expect(invalid[0].line).toBe(2)
  })
})

describe('summarizeImportResults', () => {
  it('upserted 计成功,其余计失败', () => {
    // 判别核心:只有 status==='upserted' 计成功。变异(把 failed 也当成功)→ failed 计数 RED。
    const results: AliasImportResult[] = [
      { index: 0, alias: 'a', status: 'upserted' },
      { index: 1, alias: 'b', status: 'failed', error: 'model_not_found' },
      { index: 2, alias: 'c', status: 'skipped' },
    ]
    expect(summarizeImportResults(results)).toEqual({ upserted: 1, failed: 2 })
  })
})

describe('buildCapabilitiesMap', () => {
  it('保留 true/false,去掉空白 key', () => {
    // 判别核心:false 值必须保留(是有效断言),空白 key 必须剔除。变异(只留 true)→ vision:false 丢失 RED。
    const map = buildCapabilitiesMap({ vision: false, tools: true, '  ': true, ' chat ': true })
    expect(map).toEqual({ vision: false, tools: true, chat: true })
    expect('  ' in map).toBe(false)
  })
})

describe('isKnownCapability / 白名单', () => {
  it('白名单内能力放行,外部能力拒绝', () => {
    expect(isKnownCapability('vision')).toBe(true)
    expect(isKnownCapability(' tool_use ')).toBe(true)
    expect(isKnownCapability('not_a_real_cap')).toBe(false)
  })

  it('白名单含后端关键能力名(防漂移)', () => {
    // 镜像 registry.knownModelCapabilityBindings 的代表项;若白名单被误删核心项 → RED。
    for (const c of ['vision', 'function_calling', 'thinking', 'cache_breakpoints', 'batchEmbedContents']) {
      expect(KNOWN_CAPABILITIES.has(c)).toBe(true)
    }
  })
})

describe('importResultTone', () => {
  it('upserted→ok,failed→danger,其余→muted', () => {
    expect(importResultTone('upserted')).toBe('ok')
    expect(importResultTone('failed')).toBe('danger')
    expect(importResultTone('skipped')).toBe('muted')
  })
})
