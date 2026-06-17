// 基础 fetch 封装：统一处理 JSON 解析、错误抛出、Bearer Token 注入
import type { APIError } from './types';

// 从 localStorage 读取 admin token（开发控制台用，不做生产鉴权）
function getAdminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

// 构造公共 headers
function buildHeaders(extra?: Record<string, string>): Record<string, string> {
  const token = getAdminToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...extra,
  };
  return headers;
}

// API 调用异常（包含后端 error.code）
export class ApiError extends Error {
  status: number;
  code: string;

  constructor(status: number, payload: APIError) {
    super(payload.error.message);
    this.status = status;
    this.code = payload.error.code;
  }

  // 后端尚未实现的端点返回 501
  isNotImplemented(): boolean {
    return this.status === 501;
  }
}

// 通用 JSON GET
export async function apiGet<T>(path: string, params?: Record<string, string | number | boolean | undefined>): Promise<T> {
  let url = path;
  if (params) {
    // 过滤掉 undefined 值
    const filtered = Object.entries(params).filter(([, v]) => v !== undefined) as [string, string | number | boolean][];
    if (filtered.length > 0) {
      const qs = new URLSearchParams(filtered.map(([k, v]) => [k, String(v)]));
      url = `${path}?${qs.toString()}`;
    }
  }
  const resp = await fetch(url, {
    method: 'GET',
    headers: buildHeaders(),
    cache: 'no-store',
  });
  return parseResponse<T>(resp);
}

// 通用 JSON POST
export async function apiPost<T>(path: string, body?: unknown): Promise<T> {
  const resp = await fetch(path, {
    method: 'POST',
    headers: buildHeaders(),
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  return parseResponse<T>(resp);
}

// 通用 JSON PATCH
export async function apiPatch<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(path, {
    method: 'PATCH',
    headers: buildHeaders(),
    body: JSON.stringify(body),
  });
  return parseResponse<T>(resp);
}

// 204 No Content POST（如 clear-rate-limit）
export async function apiPostNoContent(path: string, body?: unknown): Promise<void> {
  const resp = await fetch(path, {
    method: 'POST',
    headers: buildHeaders(),
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (resp.status === 204) return;
  // 非 204 的其它 2xx 也视为成功
  if (resp.ok) return;
  // 尝试解析错误体
  let payload: APIError;
  try {
    payload = await resp.json() as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

// 鉴权 CSV 下载：带 admin token GET，2xx 取 blob 触发浏览器下载；非 2xx 抛 ApiError。
// CSV 端点成功响应是 text/csv（非 JSON），故不走 parseResponse；错误体仍是 JSON。
export async function downloadCsv(path: string, filename: string): Promise<void> {
  const resp = await fetch(path, {
    method: 'GET',
    headers: buildHeaders({ Accept: 'text/csv' }),
    cache: 'no-store',
  });
  if (!resp.ok) {
    let payload: APIError;
    try {
      payload = await resp.json() as APIError;
    } catch {
      throw new Error(`HTTP ${resp.status}`);
    }
    throw new ApiError(resp.status, payload);
  }
  // 后端行数达上限时置 X-Truncated:true 并在 CSV 末尾追加 "# truncated" 行（与 usage.ts 一致）。
  const truncated = resp.headers.get('X-Truncated') === 'true';
  const blob = await resp.blob();
  const objectURL = URL.createObjectURL(blob);
  try {
    const a = document.createElement('a');
    a.href = objectURL;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
  } finally {
    URL.revokeObjectURL(objectURL);
  }
  if (truncated) {
    // 文件已下载但仅含部分记录（达后端行上限）——抛可识别错误让调用页提示缩小时间范围。
    throw new ApiError(200, {
      error: { code: 'export_truncated', message: '导出行数已达上限，文件仅包含部分记录。请缩小时间范围。' },
    });
  }
}

// 解析响应：OK → 返回 T；非 OK → 抛 ApiError
async function parseResponse<T>(resp: Response): Promise<T> {
  if (resp.ok) {
    // 204 No Content
    if (resp.status === 204) return undefined as T;
    return resp.json() as Promise<T>;
  }
  let payload: APIError;
  try {
    payload = await resp.json() as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}
