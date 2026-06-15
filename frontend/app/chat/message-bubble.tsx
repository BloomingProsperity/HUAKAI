'use client';

// 单条会话气泡：用户消息纯文本，助手消息走极简 markdown。
// 助手消息底部显示本轮 token / 耗时（取自 SSE 最终 usage 块），并提供复制 /
// 重答 / 删除操作。这些交互形态在 new-api playground 的 message-actions /
// message bubble 出现；sub2api 的 AccountTestModal 只有"复制输出"一种。

import { useState } from 'react';
import { Check, Copy, RefreshCw, Trash2, User, Bot, AlertTriangle } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Markdown } from './markdown';
import type { ChatTurn } from './types';

function fmtTokens(n: number | undefined): string {
  if (n === undefined) return '–';
  return n.toLocaleString('zh-CN');
}

function fmtDuration(ms: number | undefined): string {
  if (ms === undefined) return '–';
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

export function MessageBubble({
  turn,
  streaming,
  onRegenerate,
  onDelete,
}: {
  turn: ChatTurn;
  streaming: boolean;
  onRegenerate?: () => void;
  onDelete?: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const isUser = turn.role === 'user';

  const copy = () => {
    if (typeof navigator === 'undefined' || !navigator.clipboard) return;
    void navigator.clipboard.writeText(turn.content).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    });
  };

  const total =
    turn.usage && (turn.usage.input_tokens ?? 0) + (turn.usage.output_tokens ?? 0) > 0
      ? (turn.usage.input_tokens ?? 0) + (turn.usage.output_tokens ?? 0)
      : undefined;

  return (
    <div className={cn('group flex gap-3', isUser ? 'flex-row-reverse' : 'flex-row')}>
      {/* 头像 */}
      <div
        className={cn(
          'flex size-8 shrink-0 items-center justify-center rounded-lg',
          isUser
            ? 'bg-primary-600 text-white'
            : 'bg-accent-100 text-primary-600 dark:bg-accent-800 dark:text-primary-300',
        )}
      >
        {isUser ? <User className="size-4" /> : <Bot className="size-4" />}
      </div>

      {/* 内容 */}
      <div className={cn('min-w-0 max-w-[85%] space-y-1', isUser ? 'items-end' : 'items-start')}>
        <div
          className={cn(
            'rounded-2xl px-4 py-2.5 text-sm',
            isUser
              ? 'rounded-tr-sm bg-primary-600 text-white'
              : turn.error
                ? 'rounded-tl-sm border border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300'
                : 'rounded-tl-sm border border-accent-200 bg-white text-accent-800 dark:border-accent-800 dark:bg-accent-950/60 dark:text-accent-100',
          )}
        >
          {turn.error ? (
            <div className="flex items-start gap-2">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" />
              <span className="whitespace-pre-wrap break-words">{turn.content || turn.error}</span>
            </div>
          ) : isUser ? (
            <span className="whitespace-pre-wrap break-words">{turn.content}</span>
          ) : turn.content ? (
            <Markdown content={turn.content} />
          ) : streaming ? (
            <span className="animate-pulse text-primary-500">▌</span>
          ) : (
            <span className="text-accent-400">（空回复）</span>
          )}
          {!isUser && !turn.error && turn.content && streaming && (
            <span className="ml-0.5 animate-pulse text-primary-500">▌</span>
          )}
        </div>

        {/* 助手消息：metrics + 操作 */}
        {!isUser && (
          <div
            className={cn(
              'flex flex-wrap items-center gap-x-3 gap-y-1 px-1 text-[11px] text-accent-400 dark:text-accent-500',
            )}
          >
            {turn.usage && (
              <>
                <span title="输入 token">↓ {fmtTokens(turn.usage.input_tokens)}</span>
                <span title="输出 token">↑ {fmtTokens(turn.usage.output_tokens)}</span>
                {total !== undefined && <span title="合计 token">Σ {fmtTokens(total)}</span>}
                {turn.usage.cache_read_input_tokens !== undefined &&
                  turn.usage.cache_read_input_tokens > 0 && (
                    <span title="缓存读取 token">⚡ {fmtTokens(turn.usage.cache_read_input_tokens)}</span>
                  )}
              </>
            )}
            {turn.durationMs !== undefined && (
              <span title="耗时">⏱ {fmtDuration(turn.durationMs)}</span>
            )}

            <span className="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
              {turn.content && (
                <button
                  type="button"
                  onClick={copy}
                  title={copied ? '已复制' : '复制'}
                  className="rounded p-1 transition-colors hover:bg-accent-100 hover:text-accent-700 dark:hover:bg-accent-800 dark:hover:text-accent-200"
                >
                  {copied ? <Check className="size-3.5 text-green-600" /> : <Copy className="size-3.5" />}
                </button>
              )}
              {onRegenerate && (
                <button
                  type="button"
                  onClick={onRegenerate}
                  disabled={streaming}
                  title="重答"
                  className="rounded p-1 transition-colors hover:bg-accent-100 hover:text-accent-700 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-accent-800 dark:hover:text-accent-200"
                >
                  <RefreshCw className="size-3.5" />
                </button>
              )}
              {onDelete && (
                <button
                  type="button"
                  onClick={onDelete}
                  disabled={streaming}
                  title="删除"
                  className="rounded p-1 transition-colors hover:bg-red-100 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-40 dark:hover:bg-red-950/40 dark:hover:text-red-400"
                >
                  <Trash2 className="size-3.5" />
                </button>
              )}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
