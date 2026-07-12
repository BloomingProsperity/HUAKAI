import { ALLOWED_METHODS, EMPTY_FORM } from './types'
import type {
  AllowedMethod,
  ChannelTestTemplate,
  ChannelTestTemplateRequest,
  TemplateForm,
} from './types'

/*
 * 渠道测试模板页的纯逻辑(可单测,无 DOM / 网络副作用):
 *   - 列表分页 query 构造(tenant_id 必带,limit/offset 透传)
 *   - 表单校验 + 归一化(镜像后端 validateChannelTestTemplateRequest /
 *     normalizeChannelTestTemplateHeaders,见 channel_test_template_handler.go:300-352)
 *   - 凭证 header 名拦截(镜像 isCredentialHeaderName,handler.go:389)
 *   - 后端 DTO ↔ 表单互转
 * 全部同步纯函数,便于 §14 变异测试打红。
 */

export type QueryValue = string | number | undefined

/**
 * 列表 query:tenant_id 必带(platform_admin 角色后端必填,parseAdminCatalogTenant);
 * limit/offset 直接透传(调用方保证范围 1~500 / ≥0)。
 */
export function buildListQuery(
  tenantId: number,
  limit: number,
  offset: number,
): Record<string, QueryValue> {
  return { tenant_id: tenantId, limit, offset }
}

/** 删除 / 取详情的 query:只需 tenant_id(后端 parseAdminCatalogTenant 必读)。 */
export function buildTenantQuery(tenantId: number): Record<string, QueryValue> {
  return { tenant_id: tenantId }
}

/** 凭证类 header 名(镜像后端 isCredentialHeaderName,大小写不敏感)。 */
const CREDENTIAL_HEADER_NAMES = new Set([
  'authorization',
  'proxy-authorization',
  'cookie',
  'x-api-key',
  'api-key',
  'x-auth-token',
])

/** 判某 header 名是否为禁止存储的凭证类(后端会 403/400 拒)。 */
export function isCredentialHeaderName(name: string): boolean {
  return CREDENTIAL_HEADER_NAMES.has(name.trim().toLowerCase())
}

/** 后端方法白名单判定(大写后比对)。 */
export function isAllowedMethod(method: string): method is AllowedMethod {
  return (ALLOWED_METHODS as readonly string[]).includes(method.trim().toUpperCase())
}

/** 校验结果:ok 携带可提交请求体,否则带中文错误说明。 */
export type FormValidation =
  | { ok: true; value: ChannelTestTemplateRequest }
  | { ok: false; error: string }

/**
 * 校验并归一化表单,镜像后端约束(channel_test_template_handler.go:300-352):
 *   - name:trim 后非空且 ≤128 字符
 *   - method:大写后必须 ∈ {GET,POST,PUT,PATCH,DELETE}
 *   - path:trim 后非空、必须以 / 开头、≤2048 字符
 *   - headers:空 → {};否则必须是 JSON 对象,且不得含任何凭证类 header 名
 * 前端先拦避免无谓 4xx;后端仍是权威。判别核心逐项可被变异打红。
 */
export function validateForm(form: TemplateForm): FormValidation {
  const name = form.name.trim()
  if (name === '') {
    return { ok: false, error: '模板名称不能为空' }
  }
  // 判别核心:name 超过 128 必须拒(后端硬约束)。
  if (name.length > 128) {
    return { ok: false, error: '模板名称最多 128 个字符' }
  }

  const method = form.method.trim().toUpperCase()
  if (!isAllowedMethod(method)) {
    return { ok: false, error: '方法必须是 GET、POST、PUT、PATCH 或 DELETE' }
  }

  const path = form.path.trim()
  // 判别核心:空 / 不以 / 开头 / 超 2048 任一即拒。
  if (path === '' || !path.startsWith('/') || path.length > 2048) {
    return { ok: false, error: '路径必须以 / 开头,且不超过 2048 个字符' }
  }

  const headers = parseHeaders(form.headersText)
  if (!headers.ok) {
    return { ok: false, error: headers.error }
  }

  return {
    ok: true,
    value: {
      name,
      method,
      path,
      // body_template 原样透传(后端不 trim,允许任意内容含空)。
      body_template: form.bodyTemplate,
      headers: headers.value,
    },
  }
}

export type HeadersParse =
  | { ok: true; value: Record<string, unknown> }
  | { ok: false; error: string }

/**
 * 解析 headers 文本(镜像后端 normalizeChannelTestTemplateHeaders):
 *   - 空白 → 空对象 {}
 *   - 必须是 JSON 对象(非数组 / 非标量 / 非 null)
 *   - 任一 key 命中凭证类 header 名即拒(后端 credential_header_not_allowed)
 */
export function parseHeaders(text: string): HeadersParse {
  const trimmed = text.trim()
  if (trimmed === '') {
    return { ok: true, value: {} }
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return { ok: false, error: '请求头必须是合法 JSON' }
  }
  // 判别核心:必须是普通对象,数组 / null / 标量都不算(后端 unmarshal 到 map 会失败)。
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false, error: '请求头必须是 JSON 对象(形如 {"X-Foo":"bar"})' }
  }
  const obj = parsed as Record<string, unknown>
  for (const key of Object.keys(obj)) {
    // 判别核心:凭证类 header 名禁止存储,前端先拦(后端也会拒)。
    if (isCredentialHeaderName(key)) {
      return { ok: false, error: `请求头 "${key}" 是凭证类,禁止存入测试模板` }
    }
  }
  return { ok: true, value: obj }
}

/** 把 headers 对象渲染成可编辑的多行 JSON 文本(空对象 → 空串,便于占位提示)。 */
export function headersToText(headers: Record<string, unknown> | null | undefined): string {
  if (!headers || Object.keys(headers).length === 0) return ''
  return JSON.stringify(headers, null, 2)
}

/** 把后端 DTO 拍平成表单初值(供编辑时填充)。 */
export function templateToForm(t: ChannelTestTemplate): TemplateForm {
  return {
    name: t.name,
    method: t.method,
    path: t.path,
    bodyTemplate: t.body_template ?? '',
    headersText: headersToText(t.headers),
  }
}

/** 新建用的空表单初值。 */
export function emptyForm(): TemplateForm {
  return { ...EMPTY_FORM }
}

/** headers 对象的键数(用于列表摘要展示)。 */
export function headerCount(headers: Record<string, unknown> | null | undefined): number {
  return headers ? Object.keys(headers).length : 0
}
