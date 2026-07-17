import { describe, expect, it } from 'vitest'
import {
  buildCapabilitiesMap,
  importResultTone,
  isKnownCapability,
  KNOWN_CAPABILITIES,
  mapAliasResultRows,
  mapAliasValidationRows,
  mapAdminModelRows,
  mapCapabilityBindingRows,
  parseAliasLines,
  splitParsedAliases,
  summarizeImportResults,
} from './modelregistry'
import type { AdminModel, AliasImportResult } from './types'

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

describe('模型注册列表映射', () => {
  it('完整映射能力绑定状态与空值', () => {
    const rows = mapCapabilityBindingRows([{ model_id: 4, scope: 'tenant', tenant_id: null, capability: 'vision', capability_value: null, enabled: false, source: 'operator' }])
    expect(rows[0]).toEqual({ key: 'tenant-vision-0', capability: 'vision', scope: 'tenant', tenant: '—', value: '—', enabled: '停用', enabledTone: 'muted', source: 'operator' })
  })

  it('保留本地校验行并映射导入结果语气', () => {
    expect(mapAliasValidationRows([{ line: 3, raw: 'bad', error: '错误' }])).toEqual([{ line: 3, raw: 'bad', error: '错误' }])
    expect(mapAliasResultRows([
      { index: 0, alias: 'ok', model_id: 9, status: 'upserted' },
      { index: 1, alias: 'bad', status: 'failed', error: '冲突' },
    ])).toEqual([
      { index: 0, alias: 'ok', modelId: 9, status: 'upserted', statusTone: 'ok', error: '—', hasError: false },
      { index: 1, alias: 'bad', modelId: '—', status: 'failed', statusTone: 'danger', error: '冲突', hasError: true },
    ])
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

describe('mapAdminModelRows', () => {
  it('保留数字 id 并区分 tenant/global 与 active/disabled', () => {
    // 判别核心:数字 id 必须原样进入列表选择；若改用 canonical_id 或丢 id，首行断言转红。
    const base: AdminModel = {
      id: 17,
      tenant_id: 42,
      scope: 'tenant',
      canonical_id: 'tenant/model-a',
      protocol_family: 'openai_chat',
      default_provider_model_id: 'provider-a',
      default_context_window: 8192,
      default_request_timeout_ms: 60000,
      pricing_class: 'standard',
      model_owner: 'owner',
      model_created_at: null,
      capabilities: {},
      max_output_tokens: null,
      model_mode: null,
      status: 'active',
      created_at: '2026-07-15T00:00:00Z',
      updated_at: '2026-07-15T00:00:00Z',
    }
    const rows = mapAdminModelRows([
      base,
      { ...base, id: 18, tenant_id: null, scope: 'global', canonical_id: 'global/model-b', status: 'disabled' },
    ])
    expect(rows[0]).toMatchObject({ id: 17, tenant: 42, scope: 'tenant', status: '启用', statusTone: 'ok' })
    expect(rows[1]).toMatchObject({ id: 18, tenant: '全局', scope: 'global', status: '停用', statusTone: 'muted' })
  })
})
