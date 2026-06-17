// 渠道测试模板表单的纯逻辑层（零依赖 → 可直接 strip-types 单测）。
// 常量 + 校验 + 请求体构造，逐条镜像后端 channel_test_template_handler.go 不变式（禁止凭记忆）：
//   - name：trim 非空且 ≤128 字符。
//   - method：upper(trim)，必须 ∈ {GET,POST,PUT,PATCH,DELETE}。
//   - path：trim，必须以 '/' 开头且 ≤2048 字符。
//   - headers：可选 JSON 对象；非对象拒绝；**含凭证头拒绝**（防把密钥写入存储的测试配置）。空 → {}。
//   - body_template：自由字符串（无校验）。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码；融合 = 升级）：
//   - sub2api(LGPL) channel_monitor_template_service.go：有可复用渠道监控模板 CRUD，但头部黑名单挡 HTTP 层头
//     （host/content-length 等），**非凭证头**；模板按 monitor 快照 + 定时探测。
//   - new-api(AGPL) controller/channel-test.go：无模板存储，test payload 按 endpoint/model 硬编码 + 手动/定时测。
//   - CLIProxyAPI@21fad9db：无渠道测试模板/端点（凭证代理，无等价物）。
//   HUAKAI delta：通用 HTTP 请求形模板（method 白名单 + path 前缀 + body_template + headers）+ **凭证头拒绝
//   （防密钥写入测试配置，生态-安全）**，sub2api/new-api 皆无此凭证头守门。

// ── 常量 ────────────────────────────────────────────────────────────────

export const TEMPLATE_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] as const;

// 凭证/鉴权头白名单（小写）—— 这些不得写入存储的测试模板（镜像后端 isCredentialHeaderName）。
export const CREDENTIAL_HEADER_NAMES = [
  'authorization',
  'proxy-authorization',
  'cookie',
  'x-api-key',
  'api-key',
  'x-auth-token',
] as const;

export const MAX_TEMPLATE_NAME_LEN = 128;
export const MAX_TEMPLATE_PATH_LEN = 2048;

// isCredentialHeaderName：大小写/空白不敏感判定凭证头。
export function isCredentialHeaderName(name: string): boolean {
  const n = name.trim().toLowerCase();
  return (CREDENTIAL_HEADER_NAMES as readonly string[]).includes(n);
}

export type HeadersParse =
  | { ok: true; value: Record<string, unknown>; error: null }
  | { ok: false; value: null; error: string };

// parseHeadersField：解析 headers JSON 文本。空 → {}；非 JSON 对象拒绝；含凭证头拒绝。
export function parseHeadersField(raw: string): HeadersParse {
  const s = raw.trim();
  if (s === '') return { ok: true, value: {}, error: null };
  let parsed: unknown;
  try {
    parsed = JSON.parse(s);
  } catch {
    return { ok: false, value: null, error: 'headers 必须是合法 JSON。' };
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false, value: null, error: 'headers 必须是 JSON 对象。' };
  }
  const obj = parsed as Record<string, unknown>;
  for (const key of Object.keys(obj)) {
    if (isCredentialHeaderName(key)) {
      return { ok: false, value: null, error: `凭证头「${key}」不得写入测试模板（防密钥泄漏）。` };
    }
  }
  return { ok: true, value: obj, error: null };
}

export interface ChannelTestTemplateFormInput {
  name: string;
  method: string;
  path: string;
  body_template?: string;
  headers_raw?: string; // headers 的 JSON 文本
}

// validateChannelTestTemplateForm：逐条短路校验（与后端同序），返回首个错误文案；合法返回 null。
export function validateChannelTestTemplateForm(input: ChannelTestTemplateFormInput): string | null {
  const name = input.name.trim();
  if (name === '' || name.length > MAX_TEMPLATE_NAME_LEN) {
    return `name 必填且不超过 ${MAX_TEMPLATE_NAME_LEN} 字符。`;
  }
  const method = input.method.trim().toUpperCase();
  if (!(TEMPLATE_METHODS as readonly string[]).includes(method)) {
    return 'method 必须是 GET / POST / PUT / PATCH / DELETE。';
  }
  const path = input.path.trim();
  if (path === '' || !path.startsWith('/') || path.length > MAX_TEMPLATE_PATH_LEN) {
    return `path 必须以 / 开头且不超过 ${MAX_TEMPLATE_PATH_LEN} 字符。`;
  }
  const headers = parseHeadersField(input.headers_raw ?? '');
  if (!headers.ok) return headers.error;
  return null;
}

// buildChannelTestTemplateBody：构造 channelTestTemplateRequest。method 大写、name/path trim、headers 解析为对象。
// 假定已过 validate；headers 解析失败时回退 {}（不抛，保持纯函数）。
export function buildChannelTestTemplateBody(input: ChannelTestTemplateFormInput): Record<string, unknown> {
  const headers = parseHeadersField(input.headers_raw ?? '');
  return {
    name: input.name.trim(),
    method: input.method.trim().toUpperCase(),
    path: input.path.trim(),
    body_template: input.body_template ?? '',
    headers: headers.ok ? headers.value : {},
  };
}
