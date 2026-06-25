/*
 * single-flight 合并器(纯逻辑,可单测)。
 *
 * 把对同一异步操作的并发调用合并为一次在途执行:在途期间所有调用复用同一 promise;
 * 完成(无论成功或失败)后释放,下次调用重新执行。
 *
 * 用途:多个并发请求同时发现 session 将到期 / 收到 401 时,只触发一次刷新,避免「刷新风暴」
 * 把同一个 refresh token 重复消费(后端 family 重放检测会因此撤销整条会话族)。
 */
export function createSingleFlight<T>(fn: () => Promise<T>): () => Promise<T> {
  let inflight: Promise<T> | null = null
  return () => {
    if (inflight) return inflight
    inflight = fn().finally(() => {
      inflight = null
    })
    return inflight
  }
}
