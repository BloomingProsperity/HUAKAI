'use client';

// Hermes 运维助手（admin）—— 管理 token 轨（lib/api/hermes.ts，从 localStorage huakai_admin_token 取 Bearer）。
// HUAKAI 独有面，无需对照 sub2api/new-api/CLIProxyAPI（CLAUDE.md §11/§12 clean-room 仅约束借鉴三家）。
//
// 后端 /v1/hermes/*（routes.go:368 admin-only 挂载，hermeshttp.AdminAuthMiddleware）。鉴权关键：
//   所有请求带 ?tenant_id + ?as_user_id（admin_auth.go：as_user_id 必带，platform_admin 必带 tenant_id；
//   tenant_operator 的 tenant_id 须与自身 scope 一致）。页内可设两者；单租户部署默认 tenant=1。
//
// 三 tab：
//   - 对话：类聊天 UI，输入运维问题 → 助手回复。POST /v1/hermes/chat 流式（SSE：conversation/token/done，
//     bridge_sse.go），用 lib/sse.ts parseSSEStream 消费。settings 未启用后端返回 403 hermes_disabled → 友好提示。
//   - 历史：GET /conversations + /conversations/{id}/messages，可删除会话。
//   - 工具：GET /tools 列出可调用运维工具（只读/变更/需确认/所需角色）；GET /context 模块知识视图。
//
// dev 未装配该服务 → 路由不挂载（404）或 503，错误经 friendlyMessage 友好降级，不崩。

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertCircle,
  Bot,
  Boxes,
  Loader2,
  MessageSquare,
  RefreshCw,
  Send,
  ShieldCheck,
  Trash2,
  User,
  Wrench,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { parseSSEStream } from '@/lib/sse';
import { friendlyMessage } from '@/lib/api/errors';
import {
  deleteHermesConversation,
  formatDateTime,
  getHermesContext,
  listHermesConversations,
  listHermesMessages,
  listHermesTools,
  messageContentToText,
  parseConversationEvent,
  parseDoneTotalTokens,
  parseTokenDelta,
  startHermesChat,
  type HermesConversation,
  type HermesMessage,
  type HermesModuleView,
  type HermesScope,
  type HermesTool,
} from '@/lib/api/hermes';

const DEFAULT_TENANT_ID = 1; // 单租户部署默认
const DEFAULT_AS_USER_ID = 1; // 代入的租户用户上下文，默认 1（可改）

type TabKey = 'chat' | 'history' | 'tools';

const TABS: { key: TabKey; label: string; icon: React.ReactNode }[] = [
  { key: 'chat', label: '对话', icon: <MessageSquare className="size-4" /> },
  { key: 'history', label: '历史', icon: <RefreshCw className="size-4" /> },
  { key: 'tools', label: '工具 / 模块', icon: <Wrench className="size-4" /> },
];

// ---- 主页面 ----

export default function AdminHermesPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [asUserId, setAsUserId] = useState<number>(DEFAULT_AS_USER_ID);
  const [tab, setTab] = useState<TabKey>('chat');

  const scope = useMemo<HermesScope>(() => ({ tenantId, asUserId }), [tenantId, asUserId]);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="flex items-center gap-2 text-xl font-bold text-accent-950 dark:text-white">
          <Bot className="size-5 text-teal-500" />
          Hermes 运维助手
        </h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          面向 operator / admin 的运维对话助手。走管理 token；须指定目标租户 ID 与代入用户 ID
          （platform_admin 必带租户，tenant_operator 用自身 scope）。
        </p>
      </div>

      {/* scope + tab 控制条 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-wrap items-center gap-4 p-4">
          <div className="flex items-center gap-2">
            <label className="text-xs text-accent-500 dark:text-accent-400">租户 ID</label>
            <input
              type="number"
              min={1}
              value={tenantId}
              onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
              className="h-9 w-20 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
            />
          </div>
          <div className="flex items-center gap-2">
            <label className="text-xs text-accent-500 dark:text-accent-400">代入用户 ID</label>
            <input
              type="number"
              min={1}
              value={asUserId}
              onChange={(e) => setAsUserId(Math.max(1, Number(e.target.value) || 1))}
              className="h-9 w-20 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
            />
          </div>
          <div className="ml-auto flex flex-wrap gap-1.5">
            {TABS.map((t) => (
              <Button
                key={t.key}
                size="sm"
                variant={tab === t.key ? 'default' : 'outline'}
                onClick={() => setTab(t.key)}
              >
                {t.icon}
                {t.label}
              </Button>
            ))}
          </div>
        </CardContent>
      </Card>

      {tab === 'chat' && <ChatTab scope={scope} />}
      {tab === 'history' && <HistoryTab scope={scope} />}
      {tab === 'tools' && <ToolsTab scope={scope} />}
    </div>
  );
}

// ---- 共享 UI ----

function Banner({ kind, text }: { kind: 'error' | 'info'; text: string }) {
  if (kind === 'error') {
    return (
      <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
        <AlertCircle className="mt-0.5 size-4 shrink-0" />
        <span>{text}</span>
      </div>
    );
  }
  return (
    <div className="flex items-start gap-2 rounded-lg border border-teal-200 bg-teal-50 px-4 py-3 text-sm text-teal-700 dark:border-teal-900/60 dark:bg-teal-950/40 dark:text-teal-300">
      <ShieldCheck className="mt-0.5 size-4 shrink-0" />
      <span>{text}</span>
    </div>
  );
}

function SectionCard({
  title,
  icon,
  action,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          {icon}
          {title}
        </CardTitle>
        {action}
      </CardHeader>
      <CardContent className="p-5 pt-0">{children}</CardContent>
    </Card>
  );
}

function EmptyRow({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      {text}
    </div>
  );
}

function LoadingRow({ text }: { text: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-12 text-sm text-accent-400">
      <Loader2 className="size-5 animate-spin" /> {text}
    </div>
  );
}

// ---- Tab 1：对话（流式聊天）----

interface ChatTurn {
  role: 'user' | 'assistant';
  content: string;
  pending?: boolean;
}

function ChatTab({ scope }: { scope: HermesScope }) {
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [input, setInput] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [conversationId, setConversationId] = useState<number | undefined>(undefined);
  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });
  }, [turns]);

  // 切换 scope 时清空当前会话上下文，避免跨租户/用户串话
  useEffect(() => {
    setTurns([]);
    setConversationId(undefined);
    setError(null);
  }, [scope.tenantId, scope.asUserId]);

  useEffect(() => () => abortRef.current?.abort(), []);

  const send = useCallback(async () => {
    const text = input.trim();
    if (!text || streaming) return;
    setError(null);
    setInput('');

    const history = turns
      .filter((t) => !t.pending)
      .map((t) => ({ role: t.role, content: t.content }));
    const outgoing = [...history, { role: 'user' as const, content: text }];

    setTurns((prev) => [
      ...prev,
      { role: 'user', content: text },
      { role: 'assistant', content: '', pending: true },
    ]);
    setStreaming(true);

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const resp = await startHermesChat(
        scope,
        { messages: outgoing, conversation_id: conversationId },
        controller.signal,
      );

      let assistantText = '';
      await parseSSEStream(
        resp,
        (evt) => {
          if (evt.type === 'conversation') {
            const id = parseConversationEvent(evt.data);
            if (id !== null) setConversationId(id);
          } else if (evt.type === 'token') {
            assistantText += parseTokenDelta(evt.data);
            setTurns((prev) => updateLastAssistant(prev, assistantText, true));
          } else if (evt.type === 'done') {
            parseDoneTotalTokens(evt.data); // 总 token 数当前不展示，预留
            setTurns((prev) => updateLastAssistant(prev, assistantText, false));
          } else if (evt.type === 'error') {
            setError(evt.data || '助手响应中断');
            setTurns((prev) => updateLastAssistant(prev, assistantText, false));
          }
        },
        controller.signal,
        () => {
          setTurns((prev) => updateLastAssistant(prev, assistantText, false));
        },
        (err) => {
          setError(friendlyMessage(err));
          setTurns((prev) => updateLastAssistant(prev, assistantText, false));
        },
      );
    } catch (err) {
      setError(friendlyMessage(err));
      setTurns((prev) => prev.filter((t) => !t.pending));
    } finally {
      setStreaming(false);
      abortRef.current = null;
    }
  }, [input, streaming, turns, scope, conversationId]);

  const stop = useCallback(() => {
    abortRef.current?.abort();
    setStreaming(false);
    setTurns((prev) => prev.map((t) => (t.pending ? { ...t, pending: false } : t)));
  }, []);

  const reset = useCallback(() => {
    abortRef.current?.abort();
    setTurns([]);
    setConversationId(undefined);
    setError(null);
  }, []);

  return (
    <SectionCard
      title="运维对话"
      icon={<MessageSquare className="size-4 text-teal-500" />}
      action={
        <Button size="sm" variant="outline" onClick={reset} disabled={streaming && turns.length === 0}>
          <RefreshCw className="size-4" /> 新会话
        </Button>
      }
    >
      <div className="flex flex-col gap-3">
        {conversationId !== undefined && (
          <Banner kind="info" text={`当前会话 #${conversationId}（绑定租户 ${scope.tenantId} / 用户 ${scope.asUserId}）`} />
        )}
        {error && <Banner kind="error" text={error} />}

        <div
          ref={scrollRef}
          className="flex max-h-[52vh] min-h-[280px] flex-col gap-3 overflow-y-auto rounded-lg border border-accent-200 bg-accent-50/40 p-4 dark:border-accent-800 dark:bg-accent-950/30"
        >
          {turns.length === 0 ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-2 text-center text-sm text-accent-400">
              <Bot className="size-8 text-teal-400" />
              <p>向 Hermes 提运维问题，例如&ldquo;查一下当前渠道健康状况&rdquo;。</p>
              <p className="text-xs">若该助手在当前环境未启用，发送后会提示已禁用。</p>
            </div>
          ) : (
            turns.map((t, i) => <ChatBubble key={i} turn={t} />)
          )}
        </div>

        <div className="flex items-end gap-2">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                void send();
              }
            }}
            rows={2}
            placeholder="输入运维问题，Enter 发送，Shift+Enter 换行"
            className="min-h-[44px] flex-1 resize-y rounded-md border border-input bg-background px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-teal-400/40"
            disabled={streaming}
          />
          {streaming ? (
            <Button variant="outline" onClick={stop}>
              <Loader2 className="size-4 animate-spin" /> 停止
            </Button>
          ) : (
            <Button onClick={() => void send()} disabled={!input.trim()}>
              <Send className="size-4" /> 发送
            </Button>
          )}
        </div>
      </div>
    </SectionCard>
  );
}

function updateLastAssistant(prev: ChatTurn[], content: string, pending: boolean): ChatTurn[] {
  const next = [...prev];
  for (let i = next.length - 1; i >= 0; i--) {
    if (next[i].role === 'assistant') {
      next[i] = { ...next[i], content, pending };
      break;
    }
  }
  return next;
}

function ChatBubble({ turn }: { turn: ChatTurn }) {
  const isUser = turn.role === 'user';
  return (
    <div className={cn('flex gap-2.5', isUser ? 'flex-row-reverse' : 'flex-row')}>
      <div
        className={cn(
          'flex size-7 shrink-0 items-center justify-center rounded-full',
          isUser
            ? 'bg-accent-200 text-accent-700 dark:bg-accent-700 dark:text-accent-100'
            : 'bg-teal-100 text-teal-600 dark:bg-teal-900/60 dark:text-teal-300',
        )}
      >
        {isUser ? <User className="size-4" /> : <Bot className="size-4" />}
      </div>
      <div
        className={cn(
          'max-w-[80%] whitespace-pre-wrap rounded-lg px-3.5 py-2 text-sm',
          isUser
            ? 'bg-teal-500 text-white'
            : 'border border-accent-200 bg-white text-accent-800 dark:border-accent-800 dark:bg-accent-900 dark:text-accent-100',
        )}
      >
        {turn.content || (turn.pending ? <span className="text-accent-400">思考中…</span> : '')}
        {turn.pending && turn.content && (
          <Loader2 className="ml-1 inline size-3 animate-spin align-middle text-accent-400" />
        )}
      </div>
    </div>
  );
}

// ---- Tab 2：历史会话 ----

function HistoryTab({ scope }: { scope: HermesScope }) {
  const [conversations, setConversations] = useState<HermesConversation[]>([]);
  const [selected, setSelected] = useState<number | null>(null);
  const [messages, setMessages] = useState<HermesMessage[]>([]);
  const [loadingList, setLoadingList] = useState(false);
  const [loadingMsgs, setLoadingMsgs] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadList = useCallback(async () => {
    setLoadingList(true);
    setError(null);
    try {
      const res = await listHermesConversations(scope, { limit: 100 });
      setConversations(res.conversations ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setConversations([]);
    } finally {
      setLoadingList(false);
    }
  }, [scope]);

  useEffect(() => {
    void loadList();
    setSelected(null);
    setMessages([]);
  }, [loadList]);

  const openConversation = useCallback(
    async (id: number) => {
      setSelected(id);
      setLoadingMsgs(true);
      setError(null);
      try {
        const res = await listHermesMessages(scope, id, { limit: 200 });
        setMessages(res.messages ?? []);
      } catch (err) {
        setError(friendlyMessage(err));
        setMessages([]);
      } finally {
        setLoadingMsgs(false);
      }
    },
    [scope],
  );

  const remove = useCallback(
    async (id: number) => {
      try {
        await deleteHermesConversation(scope, id);
        setConversations((prev) => prev.filter((c) => c.id !== id));
        if (selected === id) {
          setSelected(null);
          setMessages([]);
        }
      } catch (err) {
        setError(friendlyMessage(err));
      }
    },
    [scope, selected],
  );

  return (
    <div className="grid gap-4 lg:grid-cols-[minmax(0,360px)_minmax(0,1fr)]">
      <SectionCard
        title="会话列表"
        icon={<MessageSquare className="size-4 text-teal-500" />}
        action={
          <Button size="sm" variant="outline" onClick={() => void loadList()} disabled={loadingList}>
            <RefreshCw className={cn('size-4', loadingList && 'animate-spin')} /> 刷新
          </Button>
        }
      >
        {error && <div className="mb-3"><Banner kind="error" text={error} /></div>}
        {loadingList ? (
          <LoadingRow text="加载会话…" />
        ) : conversations.length === 0 ? (
          <EmptyRow text="暂无会话记录" />
        ) : (
          <div className="flex flex-col gap-1.5">
            {conversations.map((c) => (
              <button
                key={c.id}
                type="button"
                onClick={() => void openConversation(c.id)}
                className={cn(
                  'flex items-center justify-between gap-2 rounded-lg border px-3 py-2 text-left text-sm transition-colors',
                  selected === c.id
                    ? 'border-teal-300 bg-teal-50 dark:border-teal-800 dark:bg-teal-950/40'
                    : 'border-accent-200 bg-white hover:bg-accent-50 dark:border-accent-800 dark:bg-accent-900 dark:hover:bg-accent-800/60',
                )}
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium text-accent-800 dark:text-accent-100">
                    {c.title?.trim() || `会话 #${c.id}`}
                  </div>
                  <div className="text-xs text-accent-400">
                    {formatDateTime(c.last_message_at ?? c.updated_at)}
                  </div>
                </div>
                <Trash2
                  className="size-4 shrink-0 text-accent-400 hover:text-red-500"
                  onClick={(e) => {
                    e.stopPropagation();
                    void remove(c.id);
                  }}
                  role="button"
                  aria-label="删除会话"
                />
              </button>
            ))}
          </div>
        )}
      </SectionCard>

      <SectionCard title="会话消息" icon={<Bot className="size-4 text-teal-500" />}>
        {selected === null ? (
          <EmptyRow text="从左侧选择一个会话查看消息" />
        ) : loadingMsgs ? (
          <LoadingRow text="加载消息…" />
        ) : messages.length === 0 ? (
          <EmptyRow text="该会话暂无消息" />
        ) : (
          <div className="flex max-h-[60vh] flex-col gap-3 overflow-y-auto">
            {messages.map((m) => (
              <ChatBubble
                key={m.id}
                turn={{
                  role: m.role === 'user' ? 'user' : 'assistant',
                  content: messageContentToText(m.content),
                }}
              />
            ))}
          </div>
        )}
      </SectionCard>
    </div>
  );
}

// ---- Tab 3：工具 + 模块上下文 ----

function ToolsTab({ scope }: { scope: HermesScope }) {
  const [tools, setTools] = useState<HermesTool[]>([]);
  const [modules, setModules] = useState<HermesModuleView[]>([]);
  const [loading, setLoading] = useState(false);
  const [toolsError, setToolsError] = useState<string | null>(null);
  const [ctxError, setCtxError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setToolsError(null);
    setCtxError(null);
    const [toolsRes, ctxRes] = await Promise.allSettled([
      listHermesTools(scope),
      getHermesContext(scope),
    ]);
    if (toolsRes.status === 'fulfilled') {
      setTools(toolsRes.value.tools ?? []);
    } else {
      setTools([]);
      setToolsError(friendlyMessage(toolsRes.reason));
    }
    if (ctxRes.status === 'fulfilled') {
      setModules(ctxRes.value.modules ?? []);
    } else {
      setModules([]);
      setCtxError(friendlyMessage(ctxRes.reason));
    }
    setLoading(false);
  }, [scope]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="flex flex-col gap-4">
      <SectionCard
        title="可用运维工具"
        icon={<Wrench className="size-4 text-teal-500" />}
        action={
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCw className={cn('size-4', loading && 'animate-spin')} /> 刷新
          </Button>
        }
      >
        {toolsError && <div className="mb-3"><Banner kind="error" text={toolsError} /></div>}
        {loading ? (
          <LoadingRow text="加载工具清单…" />
        ) : tools.length === 0 ? (
          <EmptyRow text={toolsError ? '工具清单不可用' : '当前环境未注册运维工具'} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>工具</TableHead>
                <TableHead>分类</TableHead>
                <TableHead>类型</TableHead>
                <TableHead>所需角色</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tools.map((t) => (
                <TableRow key={t.name}>
                  <TableCell>
                    <div className="font-medium text-accent-800 dark:text-accent-100">{t.name}</div>
                    <div className="max-w-md text-xs text-accent-400">{t.description}</div>
                  </TableCell>
                  <TableCell className="text-sm text-accent-500">{t.category || '—'}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {t.read_only && <Badge variant="secondary">只读</Badge>}
                      {t.mutating && <Badge variant="destructive">变更</Badge>}
                      {t.requires_confirmation && <Badge variant="outline">需确认</Badge>}
                    </div>
                  </TableCell>
                  <TableCell className="text-sm tabular-nums text-accent-500">
                    {t.required_role || '—'}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </SectionCard>

      <SectionCard title="模块知识上下文" icon={<Boxes className="size-4 text-teal-500" />}>
        {ctxError && <div className="mb-3"><Banner kind="error" text={ctxError} /></div>}
        {loading ? (
          <LoadingRow text="加载模块上下文…" />
        ) : modules.length === 0 ? (
          <EmptyRow text={ctxError ? '模块上下文不可用' : '暂无模块上下文'} />
        ) : (
          <div className="grid gap-2 sm:grid-cols-2">
            {modules.map((m, i) => (
              <ModuleCard key={i} view={m} />
            ))}
          </div>
        )}
      </SectionCard>
    </div>
  );
}

function ModuleCard({ view }: { view: HermesModuleView }) {
  // modulehttp.ContextSummary 字段宽松，挑常见键展示，其余折叠为 JSON。
  const name = pickString(view, ['name', 'module', 'id', 'key']) ?? '模块';
  const status = pickString(view, ['status', 'state', 'health']);
  const detail = pickString(view, ['detail', 'description', 'summary', 'message']);
  return (
    <div className="rounded-lg border border-accent-200 bg-white p-3 text-sm dark:border-accent-800 dark:bg-accent-900">
      <div className="flex items-center justify-between gap-2">
        <span className="font-medium text-accent-800 dark:text-accent-100">{name}</span>
        {status && <Badge variant="outline">{status}</Badge>}
      </div>
      {detail && <p className="mt-1 text-xs text-accent-400">{detail}</p>}
      {!detail && !status && (
        <pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap break-all text-xs text-accent-400">
          {JSON.stringify(view, null, 2)}
        </pre>
      )}
    </div>
  );
}

function pickString(obj: Record<string, unknown>, keys: string[]): string | undefined {
  for (const k of keys) {
    const v = obj[k];
    if (typeof v === 'string' && v.trim() !== '') return v;
  }
  return undefined;
}
