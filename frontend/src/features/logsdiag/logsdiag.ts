import { SELECTABLE_LEVELS, type SelectableLevel } from './types'

/*
 * 日志与诊断纯逻辑(可单测):级别归一化、合法性判定、详尽度配色、提交守卫。
 * 不触网、不打印 —— 仅做字符串 → 视图语义的纯映射。
 */

export type LevelTone = 'muted' | 'info' | 'warn' | 'danger'

/**
 * 把后端返回的级别字符串归一化:去空白、转小写。
 * zapcore 永远输出小写,但 GET 响应可能因代理/缓存大小写漂移,统一兜底。
 */
export function normalizeLevel(raw: string | undefined | null): string {
  return (raw ?? '').trim().toLowerCase()
}

/**
 * 该级别是否属于运维下拉可选档(debug/info/warn/error)。
 * 用于判断「当前后端级别」是否落在我们暴露的可选集合内 —— 若否(如 dpanic),
 * 下拉应保持只读展示而非伪装成某个可选值。
 */
export function isSelectableLevel(level: string): level is SelectableLevel {
  return (SELECTABLE_LEVELS as readonly string[]).includes(normalizeLevel(level))
}

/**
 * 级别 → 配色档:debug 中性、info 信息蓝、warn 警告黄、error 危险红。
 * 更高级别(dpanic/panic/fatal)亦归 danger —— 网关被静音到这种程度本身是异常态。
 */
export function levelTone(level: string): LevelTone {
  switch (normalizeLevel(level)) {
    case 'debug':
      return 'muted'
    case 'info':
      return 'info'
    case 'warn':
    case 'warning':
      return 'warn'
    case 'error':
    case 'dpanic':
    case 'panic':
    case 'fatal':
      return 'danger'
    default:
      return 'muted'
  }
}

/** 级别 → 中文释义(给下拉与展示加一句人话)。 */
export function levelLabel(level: string): string {
  switch (normalizeLevel(level)) {
    case 'debug':
      return 'debug · 最详尽(含调试细节,仅排障时短开)'
    case 'info':
      return 'info · 默认(常规运行日志)'
    case 'warn':
      return 'warn · 仅警告及以上'
    case 'error':
      return 'error · 仅错误(最安静)'
    default:
      return level || '未知'
  }
}

/**
 * 提交守卫:仅当目标级别合法、且与当前级别不同(归一化后比较)时才允许下发 PUT。
 * 判别核心:相同级别不发请求(避免无谓写),非法级别一律拒发(防御越权/拼写漂移)。
 */
export function canSubmit(current: string, target: string): boolean {
  const t = normalizeLevel(target)
  if (!isSelectableLevel(t)) return false
  return t !== normalizeLevel(current)
}
