import { describe, expect, it } from 'vitest'
import {
  dispatchEvent,
  parseErrorEnvelope,
  parseSSEBlocks,
  streamChat,
  type SSEEvent,
  type SSEHandlers,
} from './hermesStream'

/*
 * Hermes SSE 解析纯逻辑单测。重点覆盖三类真实风险:
 *  ① 跨 fetch chunk 边界缓冲(一个事件跨多次 read 到达,不能丢/不能错切);
 *  ② 4 种已知事件(conversation/token/done/error)解析正确且 done/error 判为终止;
 *  ③ 未知 event 与坏 JSON 优雅忽略(不抛、不崩),错误事件坏 JSON 退化为原始 data。
 * 每条用例都设计成:若把被测的判定改坏(切错边界/取错字段/漏判终止/不忽略未知),断言必转红。
 */

describe('parseSSEBlocks 跨 chunk 缓冲与边界切分', () => {
  it('切出完整事件块,把不完整尾部留在 rest(变异:若不缓冲 rest 则 token 内容残缺)', () => {
    // 第一段:一个完整 token 事件 + 一个不完整的 conversation 事件(尾部无空行)。
    const chunk1 = 'event: token\ndata: {"delta":"你好"}\n\nevent: conversation\ndata: {"id":7'
    const r1 = parseSSEBlocks(chunk1)
    expect(r1.events).toEqual([{ event: 'token', data: '{"delta":"你好"}' }])
    // 不完整部分必须原样保留在 rest,否则后续拼接会丢字符。
    expect(r1.rest).toBe('event: conversation\ndata: {"id":7')

    // 第二段:补齐上一个事件的尾部 + 空行。
    const chunk2 = '}\n\n'
    const r2 = parseSSEBlocks(r1.rest + chunk2)
    expect(r2.events).toEqual([{ event: 'conversation', data: '{"id":7}' }])
    expect(r2.rest).toBe('')
  })

  it('单个事件拆成两次到达也能在补齐后解析(变异:若按行而非按 \\n\\n 切则提前误判完成)', () => {
    const part1 = 'event: token\ndata: {"delta":"半' // 注意:无空行,事件未结束
    const r1 = parseSSEBlocks(part1)
    expect(r1.events).toEqual([]) // 没有空行,不应产出任何事件
    expect(r1.rest).toBe(part1)
    const r2 = parseSSEBlocks(r1.rest + '句"}\n\n')
    expect(r2.events).toEqual([{ event: 'token', data: '{"delta":"半句"}' }])
  })

  it('多 data 行以 \\n 连接(变异:若只取末行/首行则拼接结果不同)', () => {
    const buf = 'event: token\ndata: 第一行\ndata: 第二行\n\n'
    const { events } = parseSSEBlocks(buf)
    expect(events).toEqual([{ event: 'token', data: '第一行\n第二行' }])
  })

  it('忽略注释/心跳块(无 data 行的块不产出事件)', () => {
    const buf = ': keep-alive\n\nevent: token\ndata: {"delta":"x"}\n\n'
    const { events } = parseSSEBlocks(buf)
    expect(events).toEqual([{ event: 'token', data: '{"delta":"x"}' }])
  })

  it('容忍 CRLF 换行(变异:若不归一化 \\r\\n 则 data 含残留 \\r 比对失败)', () => {
    const buf = 'event: token\r\ndata: {"delta":"x"}\r\n\r\n'
    const { events } = parseSSEBlocks(buf)
    expect(events).toEqual([{ event: 'token', data: '{"delta":"x"}' }])
  })
})

// 收集回调侧效果的辅助:返回 handlers + 记录数组。
function recorder() {
  const calls = {
    conversation: [] as number[],
    token: [] as string[],
    done: [] as Array<number | undefined>,
    error: [] as Array<{ code: string; message: string }>,
  }
  const handlers: SSEHandlers = {
    onConversation: (id) => calls.conversation.push(id),
    onToken: (d) => calls.token.push(d),
    onDone: (t) => calls.done.push(t),
    onError: (code, message) => calls.error.push({ code, message }),
  }
  return { calls, handlers }
}

describe('dispatchEvent 四种已知事件', () => {
  it('conversation 解出数值 id 并回调(变异:取错字段则 id 不入账)', () => {
    const { calls, handlers } = recorder()
    const r = dispatchEvent({ event: 'conversation', data: '{"id":42}' }, handlers)
    expect(calls.conversation).toEqual([42])
    expect(r.terminal).toBe(false)
  })

  it('token 累加 delta;非字符串 delta 不回调(变异:若不做类型判定会把非串当文本)', () => {
    const { calls, handlers } = recorder()
    dispatchEvent({ event: 'token', data: '{"delta":"甲"}' }, handlers)
    dispatchEvent({ event: 'token', data: '{"delta":"乙"}' }, handlers)
    dispatchEvent({ event: 'token', data: '{"delta":123}' }, handlers) // 非字符串,忽略
    expect(calls.token).toEqual(['甲', '乙'])
  })

  it('done 判为终止并带出 total_tokens(变异:若不返回 terminal=true 则流不会停)', () => {
    const { calls, handlers } = recorder()
    const r = dispatchEvent({ event: 'done', data: '{"total_tokens":1500}' }, handlers)
    expect(r.terminal).toBe(true)
    expect(calls.done).toEqual([1500])
  })

  it('error 判为终止并带出 code/message(变异:若不返回 terminal=true 则错误后仍在读)', () => {
    const { calls, handlers } = recorder()
    const r = dispatchEvent(
      { event: 'error', data: '{"code":"rate_limited","message":"太频繁"}' },
      handlers,
    )
    expect(r.terminal).toBe(true)
    expect(calls.error).toEqual([{ code: 'rate_limited', message: '太频繁' }])
  })
})

describe('dispatchEvent 健壮性', () => {
  it('未知 event 优雅忽略,不触发任何 handler、不抛(变异:若不走 default 分支会崩或误派)', () => {
    const { calls, handlers } = recorder()
    const r = dispatchEvent({ event: 'tool_call', data: '{"name":"x"}' }, handlers)
    expect(r.terminal).toBe(false)
    expect(calls.conversation).toEqual([])
    expect(calls.token).toEqual([])
    expect(calls.done).toEqual([])
    expect(calls.error).toEqual([])
  })

  it('token 坏 JSON 不抛、不回调(变异:若不 try/catch 解析会抛异常中断流)', () => {
    const { calls, handlers } = recorder()
    expect(() => dispatchEvent({ event: 'token', data: '{不是合法json' }, handlers)).not.toThrow()
    expect(calls.token).toEqual([])
  })

  it('error 坏 JSON 退化为以原始 data 作 message(变异:若直接渲染会丢错误文本)', () => {
    const { calls, handlers } = recorder()
    dispatchEvent({ event: 'error', data: '上游连接中断' }, handlers)
    expect(calls.error).toEqual([{ code: 'hermes_error', message: '上游连接中断' }])
  })
})

describe('parseErrorEnvelope —— 非流式失败错误体', () => {
  it('扁平形态 {"error":"hermes_disabled"} 取出真实 code/message(变异:若 if(error) 先判对象会丢成 http_403)', () => {
    const r = parseErrorEnvelope('{"error":"hermes_disabled"}', 403, 'Forbidden')
    expect(r).toEqual({ code: 'hermes_disabled', message: 'hermes_disabled' })
  })
  it('嵌套形态 {"error":{code,message}} 取出 code/message', () => {
    const r = parseErrorEnvelope('{"error":{"code":"hermes_admin_user_required","message":"需要 as_user_id"}}', 400, 'Bad Request')
    expect(r).toEqual({ code: 'hermes_admin_user_required', message: '需要 as_user_id' })
  })
  it('非 JSON 错误体退回 HTTP 状态文案(变异:若不兜底会渲染 undefined)', () => {
    const r = parseErrorEnvelope('<html>502</html>', 502, 'Bad Gateway')
    expect(r).toEqual({ code: 'http_502', message: 'Bad Gateway' })
  })
  it('空体退回状态文案', () => {
    expect(parseErrorEnvelope('', 500, 'Internal Server Error')).toEqual({
      code: 'http_500',
      message: 'Internal Server Error',
    })
  })
})

describe('端到端:解析 + 分派组合', () => {
  it('一段完整流按序产出 conversation→token×2→done(变异:任一环节切错/派错则序列不符)', () => {
    const stream =
      'event: conversation\ndata: {"id":9}\n\n' +
      'event: token\ndata: {"delta":"你"}\n\n' +
      'event: token\ndata: {"delta":"好"}\n\n' +
      'event: done\ndata: {"total_tokens":3}\n\n'
    const { events } = parseSSEBlocks(stream)
    const { calls, handlers } = recorder()
    const seq: SSEEvent['event'][] = []
    for (const ev of events) {
      seq.push(ev.event)
      dispatchEvent(ev, handlers)
    }
    expect(seq).toEqual(['conversation', 'token', 'token', 'done'])
    expect(calls.conversation).toEqual([9])
    expect(calls.token.join('')).toBe('你好')
    expect(calls.done).toEqual([3])
  })
})

describe('streamChat SSE 缓冲上限保护(防畸形流 OOM)', () => {
  // 构造一个 fake fetch:body 先吐一个不含事件边界(\n\n)的超长块,再 done。
  // 这种流永远凑不成完整事件,残留缓冲会一路增长——上限守卫必须中止并报 overflow。
  function fakeFetchWithChunk(chunkChars: number): typeof fetch {
    const chunk = new TextEncoder().encode('x'.repeat(chunkChars))
    return (async () => {
      let read = 0
      return {
        ok: true,
        status: 200,
        statusText: 'OK',
        body: {
          getReader() {
            return {
              read: async () =>
                read++ === 0
                  ? { value: chunk, done: false }
                  : { value: undefined, done: true },
              cancel: async () => {},
            }
          },
        },
      } as unknown as Response
    }) as unknown as typeof fetch
  }

  it('残留缓冲超过 maxBufferChars 时中止并报 hermes_stream_overflow(变异:删守卫则不报错)', async () => {
    const orig = globalThis.fetch
    globalThis.fetch = fakeFetchWithChunk(200) // 200 字符、无 \n\n
    let errCode = ''
    let errMsg = ''
    try {
      await streamChat({
        adminToken: 't',
        asUserId: 1,
        messages: [{ role: 'user', content: 'hi' }],
        conversationId: null,
        maxBufferChars: 100, // 上限 100 < 200,必触发
        handlers: { onError: (c, m) => { errCode = c; errMsg = m } },
      })
    } finally {
      globalThis.fetch = orig
    }
    // 若删掉 streamChat 里的上限守卫,这个块会被当未知/不完整流静默吞掉,onError 不触发 → 断言转红。
    expect(errCode).toBe('hermes_stream_overflow')
    expect(errMsg).not.toBe('')
  })

  it('缓冲未超上限时不误报 overflow(歧视性:证明守卫不是恒触发)', async () => {
    const orig = globalThis.fetch
    // 一个完整的 done 事件流(带 \n\n),正常结束,不应触发 overflow。
    const ok = new TextEncoder().encode('event: done\ndata: {"total_tokens":1}\n\n')
    globalThis.fetch = (async () => {
      let read = 0
      return {
        ok: true,
        status: 200,
        statusText: 'OK',
        body: {
          getReader() {
            return {
              read: async () =>
                read++ === 0 ? { value: ok, done: false } : { value: undefined, done: true },
              cancel: async () => {},
            }
          },
        },
      } as unknown as Response
    }) as unknown as typeof fetch
    let errCode = ''
    let doneTotal = -1
    try {
      await streamChat({
        adminToken: 't',
        asUserId: 1,
        messages: [{ role: 'user', content: 'hi' }],
        conversationId: null,
        maxBufferChars: 100,
        handlers: { onError: (c) => { errCode = c }, onDone: (t) => { doneTotal = t ?? -1 } },
      })
    } finally {
      globalThis.fetch = orig
    }
    expect(errCode).toBe('') // 没有误报 overflow
    expect(doneTotal).toBe(1) // done 正常分派
  })
})
