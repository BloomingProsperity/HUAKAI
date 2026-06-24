/*
 * 按请求路径选用哪种 Bearer token —— 纯逻辑(可单测)。HUAKAI 后端是双 token 体系:
 *  - 运维端点 /admin/*               → admin token(admin_tokens 表,运维者配置)
 *  - 用户态端点 /v1/me/* 与 /v1/api-keys → 用户 session token(登录返回)
 *  - 公开认证端点 /v1/auth/*          → 不带 token(登录/注册本身用于换取 token)
 * 两种鉴权中间件都读 Authorization: Bearer,故前端按路径注入对应 token。
 */
export interface TokenSet {
  sessionToken: string | null
  adminToken: string | null
}

export function tokenForPath(path: string, tokens: TokenSet): string | null {
  // 公开认证端点:绝不带 token(避免把过期/错误 token 干扰登录)。
  if (path.startsWith('/v1/auth/')) return null
  // 运维端点用 admin token。
  if (path.startsWith('/admin/')) return tokens.adminToken
  // 其余用户态端点用 session token。
  return tokens.sessionToken
}

/** 该路径是否需要 token(用于路由守卫判定能否访问)。 */
export function pathNeedsAdmin(path: string): boolean {
  return path.startsWith('/admin/')
}
