import type { ProviderAccount } from './types'

/*
 * 账号编辑(池调优旋钮)纯逻辑(可单测)。PATCH /{id} 是部分更新:只下发【实际改动】的字段,
 * 未改字段省略(避免无谓覆盖)。对齐 routing 的 buildBindingUpdate 模式。
 */
export interface AccountEditForm {
  priority: string
  staticWeight: string
  capConcurrency: string
  /** 逗号分隔的标签串。 */
  tags: string
  reason: string
}

export interface AccountUpdateBody {
  priority?: number
  static_weight?: number
  cap_concurrency?: number
  tags?: string[]
  reason?: string
}

/** 把账号现状填充成编辑表单初值。 */
export function formFromAccount(a: ProviderAccount): AccountEditForm {
  return {
    priority: String(a.priority),
    staticWeight: String(a.static_weight),
    capConcurrency: String(a.cap_concurrency),
    tags: a.tags.join(', '),
    reason: '',
  }
}

/** 逗号分隔串 → 去空去首尾空白的标签数组。 */
export function parseTags(raw: string): string[] {
  return raw
    .split(',')
    .map((t) => t.trim())
    .filter((t) => t.length > 0)
}

function tagsEqual(a: string[], b: string[]): boolean {
  if (a.length !== b.length) return false
  return a.every((v, i) => v === b[i])
}

export type BuildResult = AccountUpdateBody | { error: string } | { noop: true }

/**
 * 构造 PATCH 体:逐字段与原值比较,只收改动项。数字字段非法(NaN/负)报错;
 * 全无改动返回 noop(不发空 PATCH)。reason 仅在有实际改动时附带。
 */
export function buildAccountUpdate(original: ProviderAccount, form: AccountEditForm): BuildResult {
  const body: AccountUpdateBody = {}

  const numField = (raw: string, orig: number, key: 'priority' | 'static_weight' | 'cap_concurrency', label: string): string | null => {
    const n = Number(raw.trim())
    if (!Number.isInteger(n) || n < 0) return `${label}必须是非负整数`
    if (n !== orig) body[key] = n
    return null
  }

  const e1 = numField(form.priority, original.priority, 'priority', '优先级')
  if (e1) return { error: e1 }
  const e2 = numField(form.staticWeight, original.static_weight, 'static_weight', '静态权重')
  if (e2) return { error: e2 }
  const e3 = numField(form.capConcurrency, original.cap_concurrency, 'cap_concurrency', '并发上限')
  if (e3) return { error: e3 }

  const nextTags = parseTags(form.tags)
  if (!tagsEqual(nextTags, original.tags)) body.tags = nextTags

  if (Object.keys(body).length === 0) return { noop: true }
  const r = form.reason.trim()
  if (r) body.reason = r
  return body
}
