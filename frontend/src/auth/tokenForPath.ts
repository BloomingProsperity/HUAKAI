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

/** 该路径是否走运维(admin token)鉴权。后端两种前缀同属一套 adminGate(platform_admin
 *  RBAC,校验 admin token):规范前缀 /admin/v1/*,以及若干 /v1/admin/*(platform-settings、
 *  usage、system/health 等)。两者都必须带 admin token,否则恒 401 admin_unauthorized。 */
export function pathNeedsAdmin(path: string): boolean {
  return path.startsWith('/admin/') || path.startsWith('/v1/admin/')
}

/*
 * /v1/auth/* 前缀里只有【换取 token 前】的公开端点不带 token;其余 /v1/auth/* 子端点
 * (/me、/me/profile、/me/password、/2fa/*、/logout、/account-bindings/* 等)是 session 鉴权
 * (后端 routes.go:254/262 对它们挂了 SessionMiddleware),必须带 session token,否则恒 401。
 * 历史 bug:曾把整个 /v1/auth/* 一律判为 null,导致个人资料/2FA 等页拿不到 token。
 */
const PUBLIC_AUTH_PREFIXES = [
  '/v1/auth/login',
  '/v1/auth/register',
  '/v1/auth/reset-password',
  '/v1/auth/verify-email',
  '/v1/auth/oauth',
  '/v1/auth/telegram',
  '/v1/auth/passkey', // 通行密钥登录(begin/finish)在登录前,公开
]

function isPublicAuthPath(path: string): boolean {
  // 用纯前缀匹配:后端公开端点有连字符形态(oauth-init/oauth-callback/telegram-login),
  // 不能要求 `/` 边界;这些前缀都很独特,不会误伤 /v1/auth/me、/v1/auth/2fa 等 session 端点。
  return PUBLIC_AUTH_PREFIXES.some((p) => path.startsWith(p))
}

export function tokenForPath(path: string, tokens: TokenSet): string | null {
  if (path.startsWith('/v1/auth/')) {
    // 公开认证端点(登录/注册/找回/邮箱验证/OAuth/Passkey登录)不带 token;
    // 其余 /v1/auth/* 是 session 鉴权(个人资料/2FA/登出/解绑),带 session token。
    return isPublicAuthPath(path) ? null : tokens.sessionToken
  }
  // 运维端点用 admin token(两种 admin 前缀都算)。
  if (pathNeedsAdmin(path)) return tokens.adminToken
  // 其余用户态端点用 session token。
  return tokens.sessionToken
}
