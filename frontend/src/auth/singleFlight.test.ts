import { describe, expect, it } from 'vitest'
import { createSingleFlight } from './singleFlight'

/** 可手动 resolve 的 deferred,便于精确控制在途窗口。 */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('createSingleFlight', () => {
  it('在途期间并发调用复用同一 promise,只执行一次 fn', async () => {
    // 判别核心:第二次在途调用必须复用,不得重新执行 fn。变异(每次都新建)→ calls=2 → RED。
    let calls = 0
    const d = deferred<string>()
    const flight = createSingleFlight(() => {
      calls++
      return d.promise
    })
    const p1 = flight()
    const p2 = flight()
    expect(calls).toBe(1)
    expect(p1).toBe(p2)
    d.resolve('ok')
    expect(await p1).toBe('ok')
    expect(await p2).toBe('ok')
  })

  it('完成后释放:下一次调用重新执行 fn', async () => {
    let calls = 0
    const flight = createSingleFlight(async () => {
      calls++
      return calls
    })
    expect(await flight()).toBe(1)
    expect(await flight()).toBe(2)
  })

  it('失败也释放在途位:下次可重试', async () => {
    let calls = 0
    const flight = createSingleFlight(async () => {
      calls++
      if (calls === 1) throw new Error('boom')
      return 'recovered'
    })
    await expect(flight()).rejects.toThrow('boom')
    expect(await flight()).toBe('recovered')
  })
})
