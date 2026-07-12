import type { DeleteAccountResult } from './types'

/*
 * 账号「危险操作 - 硬删」的纯逻辑(与 React 解耦,便于 vitest 变异测试)。
 *
 * 对应后端 backend/internal/gatewayhttp/admin_pool_accounts_handler.go:
 *   DELETE /admin/v1/provider-accounts/{id}(newDeleteProviderAccountHandler:665),
 *   响应 {id, deleted:true}(:695)。这是不可逆删除,与「停用账号」(可恢复软停)区分开。
 *
 * 这里把两道护栏的判定抽成纯函数:
 *   ① confirmPromptText —— window.confirm 弹窗文案,必须带账号名 + #id,让运营者确认删的是哪个;
 *   ② nameMatchesConfirmation —— 运营者还须在输入框里手抄账号名,严格相等才放行(防误点 window.confirm)。
 * 双确认 = window.confirm 弹窗(操作系统级阻断)+ 手抄账号名(防肌肉记忆狂点)。
 */

/**
 * confirmPromptText 生成 window.confirm 的提示文案。
 * 必须同时含账号名与 #id —— 这样运营者在弹窗里就能核对删的是不是目标账号,而不是只看到"确认删除?"。
 * 变异:若漏掉 name 或 #id,则下方断言("含名"/"含#id")转红。
 */
export function confirmPromptText(name: string, id: number): string {
  return `不可逆操作:确认硬删账号「${name}」(#${id})?删除后该账号将从可调度池永久移除,无法恢复。`
}

/**
 * nameMatchesConfirmation 判定运营者手抄的确认串是否与账号名严格匹配。
 * 仅两端空白容差(trim);大小写、内部空格、任何字符差异都视为不匹配 —— 这是删除前的"打字确认"护栏,
 * 必须高摩擦,不能模糊匹配。空串(运营者没抄)永远不匹配。
 * 变异:
 *   - 若改成 includes/大小写不敏感,则"大小写不同""部分匹配""空串"等用例转红;
 *   - 若忽略 trim,则"带首尾空格的正确名"用例转红。
 */
export function nameMatchesConfirmation(typed: string, accountName: string): boolean {
  const t = typed.trim()
  if (t.length === 0) return false
  return t === accountName
}

/**
 * deleteResultMessage 把 DELETE 响应转成给运营者看的文案。
 * deleted=true 才算删成功;后端恒返回 true(:695),但这里仍按字段判定,
 * 不假定"调用没抛错=删成功"(防后端契约漂移时谎报)。
 * 变异:若忽略 deleted 字段恒报成功,则 deleted=false 用例的断言转红。
 */
export function deleteResultMessage(res: DeleteAccountResult, name: string): string {
  if (!res.deleted) {
    return `账号「${name}」(#${res.id})删除未确认,请刷新核对。`
  }
  return `已硬删账号「${name}」(#${res.id}),正在返回列表…`
}
