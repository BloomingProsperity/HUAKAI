'use client';

// 极简安全 Markdown 渲染器（无第三方依赖）。
// 设计目标：不引重库、不用 dangerouslySetInnerHTML —— 全程产出 React 节点，
// 文本走 React 自动转义，杜绝 XSS。覆盖 Playground 助手回复常见子集：
//   - 代码围栏 ```lang ... ```（带语言标签 + 复制）
//   - 行内 `code`、**粗体**、*斜体*、[文本](http/https 链接)
//   - # ~ ###### 标题、- / * / 1. 列表、> 引用、--- 分隔线、段落
// 不支持的语法按纯文本原样显示（安全降级），不抛错。

import { useState, type ReactNode } from 'react';
import { Check, Copy } from 'lucide-react';
import { cn } from '@/lib/utils';

// ---- 行内解析：把一行文本切成 [text | code | bold | italic | link] 片段 ----

type Inline =
  | { kind: 'text'; value: string }
  | { kind: 'code'; value: string }
  | { kind: 'bold'; value: string }
  | { kind: 'italic'; value: string }
  | { kind: 'link'; value: string; href: string };

// 仅允许 http/https 链接，其余（javascript:、data: 等）降级为纯文本，避免协议注入。
function safeHref(raw: string): string | null {
  const trimmed = raw.trim();
  if (/^https?:\/\//i.test(trimmed)) return trimmed;
  return null;
}

// 行内分词器：逐字符扫描，遇到标记符号切片。未闭合标记按纯文本处理。
function parseInline(text: string): Inline[] {
  const out: Inline[] = [];
  let i = 0;
  let plain = '';

  const flushPlain = () => {
    if (plain) {
      out.push({ kind: 'text', value: plain });
      plain = '';
    }
  };

  while (i < text.length) {
    const ch = text[i];

    // 行内代码 `...`
    if (ch === '`') {
      const end = text.indexOf('`', i + 1);
      if (end > i) {
        flushPlain();
        out.push({ kind: 'code', value: text.slice(i + 1, end) });
        i = end + 1;
        continue;
      }
    }

    // 链接 [label](href)
    if (ch === '[') {
      const closeBracket = text.indexOf(']', i + 1);
      if (closeBracket > i && text[closeBracket + 1] === '(') {
        const closeParen = text.indexOf(')', closeBracket + 2);
        if (closeParen > closeBracket) {
          const label = text.slice(i + 1, closeBracket);
          const href = safeHref(text.slice(closeBracket + 2, closeParen));
          if (href) {
            flushPlain();
            out.push({ kind: 'link', value: label, href });
            i = closeParen + 1;
            continue;
          }
        }
      }
    }

    // 粗体 **...**
    if (ch === '*' && text[i + 1] === '*') {
      const end = text.indexOf('**', i + 2);
      if (end > i + 1) {
        flushPlain();
        out.push({ kind: 'bold', value: text.slice(i + 2, end) });
        i = end + 2;
        continue;
      }
    }

    // 斜体 *...*（单星，且不是 **）
    if (ch === '*' && text[i + 1] !== '*') {
      const end = text.indexOf('*', i + 1);
      if (end > i) {
        flushPlain();
        out.push({ kind: 'italic', value: text.slice(i + 1, end) });
        i = end + 1;
        continue;
      }
    }

    plain += ch;
    i += 1;
  }
  flushPlain();
  return out;
}

function renderInline(text: string, keyPrefix: string): ReactNode[] {
  return parseInline(text).map((seg, idx) => {
    const key = `${keyPrefix}-${idx}`;
    switch (seg.kind) {
      case 'code':
        return (
          <code
            key={key}
            className="rounded bg-accent-100 px-1 py-0.5 font-mono text-[0.85em] text-primary-700 dark:bg-accent-800/80 dark:text-primary-300"
          >
            {seg.value}
          </code>
        );
      case 'bold':
        return (
          <strong key={key} className="font-semibold text-accent-950 dark:text-white">
            {seg.value}
          </strong>
        );
      case 'italic':
        return (
          <em key={key} className="italic">
            {seg.value}
          </em>
        );
      case 'link':
        return (
          <a
            key={key}
            href={seg.href}
            target="_blank"
            rel="noopener noreferrer"
            className="text-primary-600 underline underline-offset-2 hover:text-primary-700 dark:text-primary-300"
          >
            {seg.value}
          </a>
        );
      default:
        return <span key={key}>{seg.value}</span>;
    }
  });
}

// ---- 代码块：带语言标签 + 复制按钮 ----

function CodeBlock({ lang, code }: { lang: string; code: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    if (typeof navigator === 'undefined' || !navigator.clipboard) return;
    void navigator.clipboard.writeText(code).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    });
  };
  return (
    <div className="group relative my-2 overflow-hidden rounded-lg border border-accent-200 bg-accent-950 dark:border-accent-800">
      <div className="flex items-center justify-between border-b border-white/10 px-3 py-1.5">
        <span className="font-mono text-[11px] uppercase tracking-wide text-accent-400">
          {lang || 'text'}
        </span>
        <button
          type="button"
          onClick={copy}
          className="flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] text-accent-300 transition-colors hover:bg-white/10 hover:text-white"
        >
          {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <pre className="overflow-x-auto p-3 text-[13px] leading-6">
        <code className="font-mono text-accent-100">{code}</code>
      </pre>
    </div>
  );
}

// ---- 块级解析：把整段文本按行扫描，分出代码块/标题/列表/引用/分隔/段落 ----

type Block =
  | { kind: 'code'; lang: string; code: string }
  | { kind: 'heading'; level: number; text: string }
  | { kind: 'ul'; items: string[] }
  | { kind: 'ol'; items: string[] }
  | { kind: 'quote'; text: string }
  | { kind: 'hr' }
  | { kind: 'para'; text: string };

function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n/g, '\n').split('\n');
  const blocks: Block[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // 代码围栏
    const fence = /^```(.*)$/.exec(line.trim());
    if (fence) {
      const lang = fence[1].trim();
      const body: string[] = [];
      i += 1;
      while (i < lines.length && !/^```/.test(lines[i].trim())) {
        body.push(lines[i]);
        i += 1;
      }
      i += 1; // 跳过收尾 ```
      blocks.push({ kind: 'code', lang, code: body.join('\n') });
      continue;
    }

    // 分隔线
    if (/^(---|\*\*\*|___)\s*$/.test(line.trim())) {
      blocks.push({ kind: 'hr' });
      i += 1;
      continue;
    }

    // 标题
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      blocks.push({ kind: 'heading', level: heading[1].length, text: heading[2] });
      i += 1;
      continue;
    }

    // 无序列表（连续行聚合）
    if (/^\s*[-*]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*]\s+/, ''));
        i += 1;
      }
      blocks.push({ kind: 'ul', items });
      continue;
    }

    // 有序列表
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+\.\s+/, ''));
        i += 1;
      }
      blocks.push({ kind: 'ol', items });
      continue;
    }

    // 引用
    if (/^\s*>\s?/.test(line)) {
      const buf: string[] = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^\s*>\s?/, ''));
        i += 1;
      }
      blocks.push({ kind: 'quote', text: buf.join('\n') });
      continue;
    }

    // 空行：跳过
    if (line.trim() === '') {
      i += 1;
      continue;
    }

    // 段落（聚合到下一个空行或块级标记）
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !/^```/.test(lines[i].trim()) &&
      !/^(#{1,6})\s+/.test(lines[i]) &&
      !/^\s*[-*]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i]) &&
      !/^\s*>\s?/.test(lines[i]) &&
      !/^(---|\*\*\*|___)\s*$/.test(lines[i].trim())
    ) {
      para.push(lines[i]);
      i += 1;
    }
    blocks.push({ kind: 'para', text: para.join('\n') });
  }

  return blocks;
}

const HEADING_SIZE: Record<number, string> = {
  1: 'text-lg font-bold',
  2: 'text-base font-bold',
  3: 'text-sm font-semibold',
  4: 'text-sm font-semibold',
  5: 'text-xs font-semibold',
  6: 'text-xs font-semibold',
};

export function Markdown({ content, className }: { content: string; className?: string }) {
  const blocks = parseBlocks(content);
  return (
    <div className={cn('space-y-2 text-sm leading-6', className)}>
      {blocks.map((block, idx) => {
        const key = `b-${idx}`;
        switch (block.kind) {
          case 'code':
            return <CodeBlock key={key} lang={block.lang} code={block.code} />;
          case 'hr':
            return <hr key={key} className="border-accent-200 dark:border-accent-800" />;
          case 'heading':
            return (
              <p
                key={key}
                className={cn(
                  HEADING_SIZE[block.level] ?? 'font-semibold',
                  'text-accent-950 dark:text-white',
                )}
              >
                {renderInline(block.text, key)}
              </p>
            );
          case 'ul':
            return (
              <ul key={key} className="list-disc space-y-1 pl-5">
                {block.items.map((it, j) => (
                  <li key={`${key}-${j}`}>{renderInline(it, `${key}-${j}`)}</li>
                ))}
              </ul>
            );
          case 'ol':
            return (
              <ol key={key} className="list-decimal space-y-1 pl-5">
                {block.items.map((it, j) => (
                  <li key={`${key}-${j}`}>{renderInline(it, `${key}-${j}`)}</li>
                ))}
              </ol>
            );
          case 'quote':
            return (
              <blockquote
                key={key}
                className="border-l-2 border-primary-400 pl-3 text-accent-600 dark:border-primary-600 dark:text-accent-300"
              >
                {block.text.split('\n').map((ln, j) => (
                  <p key={`${key}-${j}`}>{renderInline(ln, `${key}-${j}`)}</p>
                ))}
              </blockquote>
            );
          default:
            return (
              <p key={key} className="whitespace-pre-wrap break-words">
                {block.text.split('\n').map((ln, j) => (
                  <span key={`${key}-${j}`}>
                    {j > 0 && <br />}
                    {renderInline(ln, `${key}-${j}`)}
                  </span>
                ))}
              </p>
            );
        }
      })}
    </div>
  );
}
