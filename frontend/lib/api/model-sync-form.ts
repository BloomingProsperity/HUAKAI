// model-sync 触发表单的纯逻辑层（零依赖 → 可直接 strip-types 单测）。
// 逐条镜像后端 model_sync_handler.go 不变式（禁止凭记忆）：
//   - reason：可选；trim 后按【码点数】计长，> 200 拒（后端 utf8.RuneCountInString > 200 → 400 invalid_reason）。
//   - reason 为空：请求体省略该键，后端自填默认 "admin_manual"（不在前端硬塞默认，保持与后端单一事实源一致）。
//   - 端点 platform_admin only、无 tenant_id（全局模型目录同步，影响所有继承 global catalog 的租户）。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码；融合 = 升级。源经 reviewer-lane 核实，
// 详细 <repo>@<sha>:<file>:<line> 对照见 docs/process/plans/2026-06-17-wave2-model-sync-trigger-frontend.md）：
//   - new-api@1ac0f58(AGPL，one-api 系)：实有【跨渠道聚合】apply-all 管理端点（遍历全部启用渠道，返回聚合
//     added/removed + 逐渠道明细）+ 默认开启的【定时】自动应用 ticker；但以逐渠道 Models CSV 仅做 add/remove，
//     无 reactivate/disable/snapshot 目录级动词，亦非单一全局目录。另有静态 llm-metadata JSON 同步（异源机制）。
//   - sub2api@e34ad2b(LGPL)：实有【按账号】实时拉取上游可用模型端点（sync-upstream / sync-upstream-preview /
//     pricing/sync-models），返回扁平 {models:[]} 列表，【无】add/update/disable/reactivate 差量结算；另以模型映射/分组维护可用模型。
//   - CLIProxyAPI@2a050dc：【无】管理员触发的上游账号目录拉取端点（仅 GET 读模型）；有内部 config/CDN 驱动的
//     模型刷新 + hash-diff 回环（拉 maintainer 静态 models.json + 本地 config 热载差异），但不发现账号在上游真实可用的模型。
//   HUAKAI delta（生态升级）：单 platform_admin【全局目录】一次触发，返回逐厂商【新增/更新/复活/停用/未变/快照
//     递增】差量结算 + reason 审计 actor —— 较 new-api 逐渠道 add/remove 动词更全、且收敛为单一全局目录形态。

// ── 常量 ────────────────────────────────────────────────────────────────

// reason 最大码点数（与后端 utf8.RuneCountInString 上限一致；用码点而非 UTF-16 长度，避免 emoji/CJK 误判）。
export const MAX_SYNC_REASON_LEN = 200;

// ── 结果结构（结构化形状，供页面汇总；与 adminModelSync.ts 的 ModelSyncResult 同形）──

export interface ModelSyncResultItemShape {
  vendor: string;
  added: number;
  updated: number;
  reactivated: number;
  disabled: number;
  unchanged: number;
  snapshot_bumps: number;
}

export interface ModelSyncResultShape {
  total_added: number;
  total_updated: number;
  total_disabled: number;
  results: ModelSyncResultItemShape[];
}

// ── 校验 ────────────────────────────────────────────────────────────────

// validateModelSyncReason：trim 后按码点计长，超 200 返回错误文案；空与普通长度均合法返回 null。
export function validateModelSyncReason(reason: string): string | null {
  // [...s] 按 Unicode 码点拆分，与后端 RuneCount 同口径（'😀'.length===2 但码点为 1）。
  const runes = [...reason.trim()].length;
  if (runes > MAX_SYNC_REASON_LEN) {
    return `原因不超过 ${MAX_SYNC_REASON_LEN} 个字符。`;
  }
  return null;
}

// ── 请求体构造 ──────────────────────────────────────────────────────────

// buildModelSyncBody：trim；空 reason → 省略键（{}），让后端自填默认；非空 → { reason }。
export function buildModelSyncBody(reason: string): Record<string, unknown> {
  const r = reason.trim();
  return r === '' ? {} : { reason: r };
}

// ── 结果汇总（页面展示用，可单测）──────────────────────────────────────

// vendorChangeCount：单厂商【有效变更】数 = 新增+更新+复活+停用（不含 unchanged / snapshot_bumps）。
// 用于高亮真正改动了目录的厂商，区别于「探测了但无变化」。
export function vendorChangeCount(item: ModelSyncResultItemShape): number {
  return item.added + item.updated + item.reactivated + item.disabled;
}

// syncHadChanges：本次同步是否产生任何目录变更（决定空态/「无变更」文案）。
export function syncHadChanges(result: ModelSyncResultShape): boolean {
  return result.total_added + result.total_updated + result.total_disabled > 0;
}
