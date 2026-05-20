export interface SseEvent {
  // SSE event type（来自 "event: xxx" 行，缺省为 "message"）
  type: string;
  // data 行内容（已去除 "data: " 前缀）
  data: string;
}

export type SseCallback = (event: SseEvent) => void;
export type SseDoneCallback = () => void;
export type SseErrorCallback = (err: Error) => void;

// 从 ReadableStream<Uint8Array> 解析 SSE 事件流，逐事件回调
// 每个完整 SSE 消息（data + 空行）触发一次 onEvent
// 流结束时触发 onDone；出错触发 onError
export async function parseSSEStream(
  response: Response,
  onEvent: SseCallback,
  signal?: AbortSignal,
  onDone?: SseDoneCallback,
  onError?: SseErrorCallback,
): Promise<void> {
  const reader = response.body?.getReader();
  if (!reader) {
    onError?.(new Error('响应体为空，无法读取 SSE 流'));
    return;
  }

  const decoder = new TextDecoder();
  let buffer = '';
  // 每个 SSE 消息可能跨多行：event / data / id / retry
  let pendingType = '';
  let pendingData = '';
  // 防止 onDone 被多次触发（[DONE] 帧 + 循环结束各一次）
  let doneFired = false;

  function fireDone() {
    if (doneFired) return;
    doneFired = true;
    onDone?.();
  }

  function flushMessage() {
    if (pendingData === '') return;
    if (pendingData === '[DONE]') {
      fireDone();
      pendingData = '';
      pendingType = '';
      return;
    }
    onEvent({ type: pendingType || 'message', data: pendingData });
    pendingData = '';
    pendingType = '';
  }

  try {
    while (true) {
      if (signal?.aborted) break;
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });

      // 切割成行（兼容 \r\n 和 \n）
      const lines = buffer.split(/\r?\n/);
      // 最后一段可能不完整，留在 buffer
      buffer = lines.pop() ?? '';

      for (const line of lines) {
        if (line === '') {
          // 空行 = SSE 消息分隔符
          flushMessage();
        } else if (line.startsWith('event:')) {
          pendingType = line.slice(6).trim();
        } else if (line.startsWith('data:')) {
          // 多行 data 按规范应 join with \n，但 AI SSE 实践上单行即为完整 JSON
          const chunk = line.slice(5).trimStart();
          pendingData = pendingData ? `${pendingData}\n${chunk}` : chunk;
        }
        // id: / retry: 暂不处理
      }
    }
    // S2: buffer 中如有残余（末尾无空行），先解析为最后一行再 flush
    // Anthropic terminal frame "data: {...message_stop}\n" 无尾部空行时命中此分支
    if (buffer.trim() !== '') {
      const line = buffer.trim();
      if (line.startsWith('event:')) {
        pendingType = line.slice(6).trim();
      } else if (line.startsWith('data:')) {
        const chunk = line.slice(5).trimStart();
        pendingData = pendingData ? `${pendingData}\n${chunk}` : chunk;
      }
    }
    // 处理最后一段可能未以空行结尾的消息
    if (pendingData) flushMessage();
    // 流正常结束时触发 onDone（[DONE] 帧已触发则此处幂等）
    fireDone();
  } catch (err: unknown) {
    if (err instanceof Error && err.name === 'AbortError') {
      fireDone();
      return;
    }
    onError?.(err instanceof Error ? err : new Error(String(err)));
  } finally {
    try { reader.cancel(); } catch { /* 忽略 cancel 异常 */ }
  }
}
