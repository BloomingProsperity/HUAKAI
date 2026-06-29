import type { FingerprintBindResult, FingerprintProfileOption } from './types'

/*
 * 账号 TLS 指纹 profile 绑定的纯逻辑(与 React 解耦,便于 vitest 变异测试)。
 * 对应后端 backend/internal/accountfphttp/fingerprint_handler.go:
 *   PATCH /{id}/fingerprint-profile,body {profile_id:int64|null}。
 *   - profile_id 正整数 = 绑定该 profile;
 *   - profile_id = null   = 解绑(回内置默认拟真 profile)。
 *   - 后端校验 profile_id 必须 > 0(:81),否则 400 invalid_profile_id。
 *
 * 下拉的取值约定:用字符串值承载三态——
 *   ''        = 解绑(回内置默认),提交 profile_id=null
 *   '<id>'    = 绑定该 id,提交 profile_id=<id>
 * 不引入"未选择"哨兵:进入时默认选中 UNBIND_VALUE 还是某个 id 由调用方决定。
 */

/** 下拉里"解绑(回内置默认)"项的 value。空串便于和真实 id 区分。 */
export const UNBIND_VALUE = ''

/** profile 状态 → 中文标注(下拉项与提示用)。未知状态兜底原值。 */
const STATUS_LABELS: Record<string, string> = {
  active: '启用',
  disabled: '已停用',
  drift_detected: '指纹漂移',
}

/**
 * statusLabel 把 profile.status 映射成中文短标。
 * 变异:若返回恒空串,则"已停用/漂移"项失去提示 → 相关断言转红。
 */
export function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status
}

/**
 * optionText 渲染一个下拉项的展示文本:"名称(#id)[· 状态]"。
 * status 为 active 时不赘述状态(默认即可用);disabled / drift_detected 显式标注以警示。
 * 变异:若总不标注状态,则停用/漂移用例的"含状态"断言转红。
 */
export function optionText(o: FingerprintProfileOption): string {
  const base = `${o.name}(#${o.id})`
  if (o.status === 'active' || !o.status) return base
  return `${base} · ${statusLabel(o.status)}`
}

/**
 * selectionToProfileId 把下拉的字符串 value 转成请求体的 profile_id。
 * '' → null(解绑);'<正整数>' → 该数字;其它(非正整数 / NaN)→ 抛出错误,
 * 因为后端会以 400 拒绝非正整数(fingerprint_handler.go:81),前端不应放过。
 * 变异:
 *   - 若把 '' 也当作 0/NaN 下发,则解绑用例(期望 null)转红;
 *   - 若不校验正整数,则 'abc' 用例(期望抛错)转红。
 */
export function selectionToProfileId(value: string): number | null {
  if (value === UNBIND_VALUE) return null
  const n = Number(value)
  if (!Number.isInteger(n) || n <= 0) {
    throw new Error(`非法的 profile 选择:${value}`)
  }
  return n
}

/**
 * bindResultMessage 把 PATCH 响应转成给运营者看的成功文案。
 * tls_fingerprint_profile_id 为 null → 解绑成功;否则 → 绑定到 #id 成功。
 * 变异:若忽略 null 分支恒报"绑定成功",则解绑用例的文案断言转红。
 */
export function bindResultMessage(res: FingerprintBindResult): string {
  if (res.tls_fingerprint_profile_id == null) {
    return '已解绑指纹 profile(回内置默认)'
  }
  return `已绑定指纹 profile #${res.tls_fingerprint_profile_id}`
}

/**
 * currentSelectionValue 由"已知的当前绑定 id"算出下拉应选中的 value。
 * 当前绑定未知(null/undefined,账号 DTO 不暴露该字段)→ 返回 UNBIND_VALUE 作为默认显示,
 * 但调用方应据 known=false 提示"当前绑定未知,保存才会改"。
 * 变异:若把 undefined 当成已知 0,则会错误选中某 id。
 */
export function currentSelectionValue(boundProfileId: number | null | undefined): string {
  if (boundProfileId == null || boundProfileId <= 0) return UNBIND_VALUE
  return String(boundProfileId)
}
