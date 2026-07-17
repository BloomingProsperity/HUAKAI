import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const openAPI = readFileSync(new URL('../../../../docs/openapi/openapi.yaml', import.meta.url), 'utf8')
const bindingSchemas = ['ModelPoolBinding', 'ModelPoolBindingCreateRequest', 'ModelPoolBindingUpdateRequest'] as const
const fallbackClasses = ['normal', 'context_window', 'safety', 'quota', 'manual']

function componentSchema(source: string, name: string): string {
  const lines = source.split('\n')
  const start = lines.findIndex((line) => line === `    ${name}:`)
  if (start < 0) throw new Error(`OpenAPI 缺少 schema ${name}`)
  let end = lines.length
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^    [A-Za-z0-9_]+:$/.test(lines[index])) {
      end = index
      break
    }
  }
  return lines.slice(start, end).join('\n')
}

function schemaProperty(schema: string, name: string): string {
  const lines = schema.split('\n')
  const start = lines.findIndex((line) => line === `        ${name}:`)
  if (start < 0) throw new Error(`OpenAPI schema 缺少字段 ${name}`)
  let end = lines.length
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^        [A-Za-z0-9_]+:/.test(lines[index])) {
      end = index
      break
    }
  }
  return lines.slice(start, end).join('\n')
}

function enumValues(property: string): string[] {
  const match = property.match(/enum:\s*\[([^\]]+)]/)
  if (!match) throw new Error('OpenAPI 字段缺少内联 enum')
  return match[1].split(',').map((value) => value.trim())
}

describe('AT-BFC-012 OpenAPI fallback_class 契约', () => {
  it('response/create/PATCH 都只声明五枚举且默认 normal', () => {
    for (const schemaName of bindingSchemas) {
      const property = schemaProperty(componentSchema(openAPI, schemaName), 'fallback_class')
      // 变异：删除、改名或增加第六枚举都会破坏精确数组断言。
      expect(enumValues(property), schemaName).toEqual(fallbackClasses)
      expect(property, schemaName).toContain('default: normal')
      expect(property, schemaName).not.toContain('emergency')
    }
  })

  it('三处都锁定触发族、单次转移、目标子预算和终态边界', () => {
    const requiredSemantics = [
      'normal 是唯一主类',
      'context_window 只承接明确的上下文超限，管理员须确认目标池/模型确有更大窗口，系统不代验',
      'safety 只承接上游内容策略拒绝，配置后即生效且没有额外环境开关',
      'quota 承接绑定、账号或上游容量/限流耗尽',
      'manual 承接上游 5xx、连接或首字节超时、空响应等通用瞬态故障',
      '每个请求的每个模型总是从 normal 开始，最多转移一次',
      '目标类只有一次额外 attempt',
      '已向客户端交付首字节后的失败均为终态，绝不跨类',
    ]
    for (const schemaName of bindingSchemas) {
      const property = schemaProperty(componentSchema(openAPI, schemaName), 'fallback_class')
      for (const text of requiredSemantics) expect(property, `${schemaName}: ${text}`).toContain(text)
    }
  })

  it('PATCH 明示整行覆盖，selection_mode 不再声称只存不执行', () => {
    const updateSchema = componentSchema(openAPI, 'ModelPoolBindingUpdateRequest')
    expect(updateSchema).toContain('PATCH，当前写口采用整行默认覆盖')
    expect(updateSchema).toContain('省略 fallback_class 会重置为 normal')

    for (const schemaName of bindingSchemas) {
      const selectionMode = schemaProperty(componentSchema(openAPI, schemaName), 'selection_mode')
      expect(selectionMode).toContain('运行时池内账号选号策略')
      expect(selectionMode).not.toContain('stored but not yet executed')
    }
  })
})
