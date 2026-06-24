import type { ApiKeyView } from './types'

/*
 * Key 编辑纯逻辑(可单测)。PATCH /v1/api-keys/{id} 部分更新:改名 + 到期【三态】。
 * 到期三态(对齐后端约定):
 *   缺省(不下发 expires_at) = 保持原到期不变
 *   空串 ""                = 清除到期(永不过期)
 *   RFC3339 字符串          = 设定到期(解析失败 → 400)
 * 只下发改动字段;无改动 noop。
 */
export type ExpiryMode = 'keep' | 'never' | 'date'

export interface KeyEditForm {
  name: string
  expiryMode: ExpiryMode
  /** 仅 expiryMode==='date' 时有意义:datetime-local 值。 */
  expiryDate: string
}

export interface KeyUpdateBody {
  name?: string
  /** 三态:undefined=不下发(不变);''=清除;ISO=设定。 */
  expires_at?: string
}

export function formFromKey(k: ApiKeyView): KeyEditForm {
  return { name: k.name, expiryMode: 'keep', expiryDate: '' }
}

export type KeyBuildResult = KeyUpdateBody | { error: string } | { noop: true }

/**
 * 构造 PATCH 体。name 仅在变更时下发;到期按 expiryMode 映射三态。
 * date 模式日期非法 → 报错;全无改动 → noop(不发空 PATCH)。
 */
export function buildKeyUpdate(original: ApiKeyView, form: KeyEditForm): KeyBuildResult {
  const body: KeyUpdateBody = {}

  const name = form.name.trim()
  if (!name) return { error: 'Key 名称不能为空' }
  if (name !== original.name) body.name = name

  if (form.expiryMode === 'never') {
    // 清除到期:仅当原本有到期才算改动(原本就永不过期则无需下发)。
    if (original.expires_at) body.expires_at = ''
  } else if (form.expiryMode === 'date') {
    const iso = toIsoOrEmpty(form.expiryDate)
    if (!iso) return { error: '请选择有效的到期时间' }
    // 与原到期不同才下发(同一时刻不算改动)。
    if (!sameInstant(iso, original.expires_at)) body.expires_at = iso
  }
  // expiryMode==='keep':不动 expires_at。

  if (body.name === undefined && body.expires_at === undefined) return { noop: true }
  return body
}

/** datetime-local → ISO8601;空/非法 → 空串。 */
export function toIsoOrEmpty(local: string): string {
  const v = local.trim()
  if (!v) return ''
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

function sameInstant(iso: string, other?: string | null): boolean {
  if (!other) return false
  const a = new Date(iso).getTime()
  const b = new Date(other).getTime()
  return !Number.isNaN(a) && !Number.isNaN(b) && a === b
}
