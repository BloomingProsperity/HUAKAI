/*
 * 危险(不可逆)操作的二次确认 —— role 制单登录采用 new-api 模型:后端不做 step-up,
 * session-admin 直接操作,由前端在不可撤销的动作前弹窗二次确认并明示「无法撤销」。
 *
 * 单一真相源:所有不可逆 money/删除/撤销 动作统一走本助手,措辞一致、日后要换成
 * 带样式的模态框也只需改这一处。消息构造(buildIrreversibleMessage)是纯函数,便于测试;
 * confirmIrreversible 只是它加 window.confirm 的薄包装(副作用隔离在包装层)。
 *
 * 注意:前端确认仅为防误操作的体验层,不是授权边界——真正的边界在后端每个端点的鉴权。
 */

/** 不可逆确认文案的固定前缀。集中此处,保证全站措辞一致。 */
export const IRREVERSIBLE_PREFIX = '⚠️ 此操作无法撤销。'

/**
 * buildIrreversibleMessage 组装不可逆操作的确认文案(纯函数)。
 * action 为动作主句(如「吊销券 #12」);detail 为可选补充说明(影响范围/后果),追加为独立段。
 * 结果恒以 IRREVERSIBLE_PREFIX 开头,并以「确认继续?」收尾,措辞全站统一。
 */
export function buildIrreversibleMessage(action: string, detail?: string): string {
  const head = `${IRREVERSIBLE_PREFIX}${action.trim()}`
  const tail = '确认继续?'
  const extra = detail && detail.trim() !== '' ? `\n\n${detail.trim()}` : ''
  return `${head}${extra}\n\n${tail}`
}

/**
 * confirmIrreversible 弹出不可逆操作的二次确认框,用户点「确定」返回 true、「取消」返回 false。
 * 是 buildIrreversibleMessage + window.confirm 的薄包装;无 window(SSR/测试)时保守返回 false(不放行)。
 */
export function confirmIrreversible(action: string, detail?: string): boolean {
  const msg = buildIrreversibleMessage(action, detail)
  if (typeof window === 'undefined' || typeof window.confirm !== 'function') return false
  return window.confirm(msg)
}
