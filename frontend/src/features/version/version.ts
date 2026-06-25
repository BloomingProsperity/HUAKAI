import type { BuildInfo, SmtpTestRequest } from './types'

/*
 * 版本与维护纯逻辑(可单测):
 *  - 构建版本字段的友好化展示(后端默认占位 dev/unknown 显示为「本地/开发构建」语义,不误导)。
 *  - commit 短哈希截断。
 *  - SMTP 测试请求构造与校验(收件人必填且为合法邮箱;租户号留空→0)。
 * 纯函数不触网、不读 token、零 console。
 */

/** 后端 buildinfo 的占位值:Version 默认 "dev",Commit/BuildTime 默认 "unknown"。 */
const PLACEHOLDER = new Set(['', 'dev', 'unknown'])

/** 字段是否为占位(未通过 -ldflags 注入真实值)。 */
export function isPlaceholder(value: string): boolean {
  return PLACEHOLDER.has(value.trim().toLowerCase())
}

/**
 * 版本号展示:真实版本原样;占位(dev/空)→「开发构建」。
 * 判别核心:占位与真实版本必须区分,避免把 dev 当正式发布版误导运维。
 */
export function displayVersion(version: string): string {
  return isPlaceholder(version) ? '开发构建' : version.trim()
}

/** commit 展示:真实哈希截前 12 位;占位 → 「未知」。 */
export function displayCommit(commit: string): string {
  const v = commit.trim()
  if (isPlaceholder(v)) return '未知'
  return v.length > 12 ? v.slice(0, 12) : v
}

/** 构建时间展示:能解析的 ISO/RFC3339 转本地时间;占位或不可解析 → 原样/「未知」。 */
export function displayBuildTime(buildTime: string): string {
  const v = buildTime.trim()
  if (isPlaceholder(v)) return '未知'
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? v : d.toLocaleString('zh-CN', { hour12: false })
}

/** 整卡是否「全为占位」——用于提示运维「这是未打标的本地构建」。 */
export function isDevBuild(info: BuildInfo): boolean {
  return isPlaceholder(info.version) && isPlaceholder(info.commit) && isPlaceholder(info.build_time)
}

export interface SmtpTestForm {
  to: string
  tenantId: string
}

export const EMPTY_SMTP_TEST: SmtpTestForm = { to: '', tenantId: '' }

/** 极简邮箱形态校验(有 @ 且两侧非空、含点的域名);仅做客户端预检,真正可达性由后端 SMTP 决定。 */
function looksLikeEmail(s: string): boolean {
  const m = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s)
  return m
}

/**
 * 构造 SMTP 测试请求或返回错误。
 * 判别核心:收件人必填且须像邮箱(挡空/缺@),否则后端必返 400 email_test_recipient_required;
 * 租户号留空视为 0(单租户运营),非法数字报错(避免把 NaN 发给后端)。
 */
export function buildSmtpTest(form: SmtpTestForm): SmtpTestRequest | { error: string } {
  const to = form.to.trim()
  if (!to) return { error: '请填写测试收件邮箱' }
  if (!looksLikeEmail(to)) return { error: '收件邮箱格式不正确' }
  const raw = form.tenantId.trim()
  let tenantId = 0
  if (raw) {
    const n = Number(raw)
    if (!Number.isInteger(n) || n < 0) return { error: '租户号须为非负整数' }
    tenantId = n
  }
  return { tenant_id: tenantId, to }
}
