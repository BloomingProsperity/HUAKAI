// 统一把后端错误翻译成用户能看懂的中文。页面 catch 后用 friendlyMessage(err) 显示。
import { ApiError } from './client';

const CODE_MAP: Record<string, string> = {
  invalid_credentials: '邮箱或密码不正确',
  session_expired: '会话已过期，请重新登录',
  unauthorized: '未登录或登录已失效',
  forbidden: '没有权限执行该操作',
  insufficient_balance: '余额不足，请先充值',
  quota_exceeded: '已超出配额限制',
  rate_limited: '请求过于频繁，请稍后再试',
  no_capacity: '当前无可用渠道，请稍后再试',
  email_not_verified: '邮箱尚未验证，请先完成验证',
  not_found: '资源不存在',
};

const STATUS_MAP: Record<number, string> = {
  401: '未登录或登录已失效，请重新登录',
  402: '余额不足，请先充值',
  403: '没有权限执行该操作',
  404: '资源不存在',
  409: '操作冲突，请刷新后重试',
  429: '请求过于频繁，请稍后再试',
  500: '服务器开小差了，请稍后再试',
  502: '上游网关异常，请稍后再试',
  503: '服务暂不可用，请稍后再试',
};

export function friendlyMessage(err: unknown): string {
  if (err instanceof ApiError) {
    return CODE_MAP[err.code] ?? STATUS_MAP[err.status] ?? err.message ?? '操作失败';
  }
  const e = err as { code?: string; status?: number; message?: string };
  if (e?.code && CODE_MAP[e.code]) return CODE_MAP[e.code];
  if (e?.status && STATUS_MAP[e.status]) return STATUS_MAP[e.status];
  if (e?.message) return e.message;
  return '操作失败，请稍后再试';
}
