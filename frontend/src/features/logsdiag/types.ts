/*
 * 日志与诊断(运维台/admin)前端类型 —— 镜像 zap AtomicLevel HTTP 处理器的 JSON。
 * 端点:GET/PUT /v1/admin/loglevel(亦挂 /admin/v1/loglevel,platform_admin 鉴权)。
 *
 * 后端 adminhttp/loglevel_handler.go 在鉴权通过后,直接委派 zap 的
 * AtomicLevel.ServeHTTP:
 *   - GET  → 200 {"level":"info"}            (当前进程级日志级别)
 *   - PUT  {"level":"debug"} → 200 {"level":"debug"}  (运行时热调,无需重启)
 * 级别字符串由 zapcore.Level 序列化,小写:debug/info/warn/error(及更高的 dpanic/panic/fatal)。
 */

/** zap AtomicLevel GET/PUT 成功返回体。 */
export interface LogLevelResponse {
  level: string
}

/** PUT 请求体。 */
export interface LogLevelUpdate {
  level: string
}

/**
 * 运维可热调的日志级别(由低到高的详尽度递减)。
 * 只暴露这四档:debug/info/warn/error —— 它们是事故排查时唯一有运维意义的旋钮;
 * dpanic/panic/fatal 属程序自身控制流,不开放给运维下拉以免误把网关静音到 fatal。
 */
export const SELECTABLE_LEVELS = ['debug', 'info', 'warn', 'error'] as const

export type SelectableLevel = (typeof SELECTABLE_LEVELS)[number]
