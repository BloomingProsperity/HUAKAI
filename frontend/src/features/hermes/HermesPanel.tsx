import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useAuth } from '../../auth/store'
import {
  actorReady,
  buildPageContextPrefix,
  extractEntityId,
  getCurrentPageLabel,
  loadActor,
  saveActor,
  toPositiveInt,
  type HermesActor,
} from './hermesContext'
import { useHermesChat } from './useHermesChat'
import { listTools, type HermesTool } from './hermesClient'
import { HermesHistoryTab } from './HermesHistoryTab'
import { HermesContextTab } from './HermesContextTab'
import * as S from './hermesPanelStyles'

/*
 * 运营台嵌入式 Hermes 对话面板(纯只读)。右侧停靠、可拖宽、可全屏、可关闭。
 * 顶部上下文 chip 标明当前页;无 admin token 走"需运维者 token"空状态(不发注定 401 的请求);
 * 未设操作身份(as_user_id)时输入禁用并提示先设置。绝不出现改动型/提议/确认 UI。
 *
 * 渲染前置:面板只在 operator 壳渲染——该判定在 AppShell 侧用 getCurrentShell 完成,这里默认已是 operator。
 */

const MIN_WIDTH = 360
const MAX_WIDTH = 720
const DEFAULT_WIDTH = 420
const LS_WIDTH = 'hk_hermes_panel_width'

interface HermesPanelProps {
  /** 关闭面板(交还焦点给运营台)。 */
  onClose: () => void
}

export function HermesPanel({ onClose }: HermesPanelProps) {
  const auth = useAuth()
  const location = useLocation()
  const chat = useHermesChat()

  const [width, setWidth] = useState<number>(() => loadWidth())
  const [fullscreen, setFullscreen] = useState(false)
  const [actor, setActor] = useState<HermesActor>(() => loadActor())
  const [showActorForm, setShowActorForm] = useState(false)
  const [input, setInput] = useState('')
  // 视图分页:对话 / 历史会话回看 / 只读工具清单 / 模块上下文。纯只读(历史页含删除自己会话的破坏性动作,已二次确认)。
  const [tab, setTab] = useState<'chat' | 'history' | 'tools' | 'context'>('chat')

  const adminToken = auth.adminToken
  const hasAdmin = !!adminToken

  // 当前页上下文(用于 chip 与发送前缀)。详情页(/accounts/:id 等)带实体 id。
  const pageLabel = useMemo(() => getCurrentPageLabel(location.pathname), [location.pathname])
  const entityId = useMemo(() => extractEntityId(location.pathname), [location.pathname])
  const contextPrefix = useMemo(
    () => buildPageContextPrefix(pageLabel, entityId),
    [pageLabel, entityId],
  )

  const ready = hasAdmin && actorReady(actor)

  // 关闭/卸载时取消进行中的流式请求,避免悬挂。
  useEffect(() => () => chat.abort(), [chat])

  // 拖宽:在右栏左缘按下后,监听 mousemove 调整宽度(向左拖变宽)。
  const dragging = useRef(false)
  const onDragStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    dragging.current = true
  }, [])
  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!dragging.current) return
      // 向左拖(clientX 变小)使面板变宽;clamp 进 [MIN, MAX]。
      setWidth(clampWidth(window.innerWidth - e.clientX))
    }
    function onUp() {
      dragging.current = false
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
  }, [])
  // 宽度变化即持久化(下次打开面板沿用上次宽度)。
  useEffect(() => {
    saveWidth(width)
  }, [width])

  const onSend = useCallback(() => {
    if (!ready || !adminToken || actor.asUserId === null) return
    const text = input
    setInput('')
    void chat.send({
      adminToken,
      asUserId: actor.asUserId,
      tenantId: actor.tenantId ?? undefined,
      userInput: text,
      contextPrefix,
    })
  }, [ready, adminToken, actor.asUserId, actor.tenantId, input, chat, contextPrefix])

  const onInputKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      // Enter 发送;Shift+Enter 换行。
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        if (ready && !chat.sending && input.trim() !== '') onSend()
      }
    },
    [ready, chat.sending, input, onSend],
  )

  const panelStyle: React.CSSProperties = {
    ...S.basePanel,
    width: fullscreen ? '100vw' : width,
    left: fullscreen ? 0 : 'auto',
  }

  return (
    <aside style={panelStyle} role="complementary" aria-label="Hermes 运维助手">
      {/* 拖宽手柄(全屏时隐藏) */}
      {!fullscreen && <div style={S.dragHandle} onMouseDown={onDragStart} aria-hidden />}

      <header style={S.headerBar}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', minWidth: 0 }}>
          <span style={S.brandDot} aria-hidden />
          <strong style={{ fontSize: 14, color: 'var(--hk-ink-900)' }}>Hermes 运维助手</strong>
          <span style={S.readonlyBadge}>只读</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-1)' }}>
          <button type="button" style={S.iconBtn} onClick={() => setFullscreen((v) => !v)} aria-label={fullscreen ? '退出全屏' : '全屏'}>
            {fullscreen ? '🗗' : '🗖'}
          </button>
          <button type="button" style={S.iconBtn} onClick={onClose} aria-label="关闭面板">
            ✕
          </button>
        </div>
      </header>

      {/* 上下文 chip + 操作身份 */}
      <div style={S.contextRow}>
        <span style={S.chip} title="助手已知道你当前所在页">
          上下文：{pageLabel}
          {entityId ? ` · #${entityId}` : ''}
        </span>
        <button type="button" style={S.actorChip} onClick={() => setShowActorForm((v) => !v)}>
          {actorReady(actor) ? `身份 as_user_id #${actor.asUserId}${actor.tenantId ? ` · 租户 ${actor.tenantId}` : ''}` : '设置操作身份'}
        </button>
      </div>

      {/* 视图分页:对话 / 只读工具清单 */}
      {ready && (
        <div style={{ ...S.contextRow, borderBottom: '1px solid var(--hk-line)' }}>
          <div style={S.tabBar}>
            <TabButton active={tab === 'chat'} onClick={() => setTab('chat')}>
              对话
            </TabButton>
            <TabButton active={tab === 'history'} onClick={() => setTab('history')}>
              历史
            </TabButton>
            <TabButton active={tab === 'tools'} onClick={() => setTab('tools')}>
              只读工具
            </TabButton>
            <TabButton active={tab === 'context'} onClick={() => setTab('context')}>
              模块
            </TabButton>
          </div>
        </div>
      )}

      {showActorForm && (
        <ActorForm
          actor={actor}
          onSave={(next) => {
            setActor(next)
            saveActor(next)
            setShowActorForm(false)
          }}
          onCancel={() => setShowActorForm(false)}
        />
      )}

      {/* 主体:无 token 空状态 / 消息区 / 历史回看 / 只读工具清单 / 模块上下文 */}
      {!hasAdmin ? (
        <EmptyState
          title="需运维者 token"
          desc="Hermes 运维助手只在运营台可用,且必须使用运维者(admin)token。请先在系统设置里配置 admin token 后重试。"
        />
      ) : ready && actor.asUserId !== null && adminToken && tab === 'history' ? (
        <HermesHistoryTab adminToken={adminToken} asUserId={actor.asUserId} tenantId={actor.tenantId ?? undefined} />
      ) : ready && actor.asUserId !== null && adminToken && tab === 'tools' ? (
        <ToolsTab adminToken={adminToken} asUserId={actor.asUserId} tenantId={actor.tenantId ?? undefined} />
      ) : ready && actor.asUserId !== null && adminToken && tab === 'context' ? (
        <HermesContextTab adminToken={adminToken} asUserId={actor.asUserId} tenantId={actor.tenantId ?? undefined} />
      ) : (
        <MessageArea
          messages={chat.messages}
          error={chat.error}
          ready={ready}
          hasActor={actorReady(actor)}
        />
      )}

      {/* 输入区(仅对话页) */}
      {hasAdmin && tab === 'chat' && (
        <div style={S.inputBar}>
          {!actorReady(actor) && (
            <div style={S.hintLine}>请先设置操作身份(as_user_id)后才能对话。</div>
          )}
          <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'flex-end' }}>
            <textarea
              style={S.textArea}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={onInputKeyDown}
              placeholder={ready ? '问 Hermes 关于当前页的事…(Enter 发送,Shift+Enter 换行)' : '设置操作身份后可对话'}
              disabled={!ready || chat.sending}
              rows={2}
            />
            <button
              type="button"
              style={{ ...S.sendBtn, opacity: !ready || chat.sending || input.trim() === '' ? 0.5 : 1 }}
              onClick={onSend}
              disabled={!ready || chat.sending || input.trim() === ''}
            >
              {chat.sending ? '…' : '发送'}
            </button>
          </div>
          {chat.messages.length > 0 && (
            <button type="button" style={S.resetBtn} onClick={chat.reset} disabled={chat.sending}>
              新对话
            </button>
          )}
        </div>
      )}
    </aside>
  )
}

// ── 子组件 ──

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        height: 28,
        padding: '0 var(--hk-space-3)',
        fontSize: 12,
        cursor: 'pointer',
        border: 'none',
        background: active ? 'var(--hk-primary-500)' : 'var(--hk-surface)',
        color: active ? '#fff' : 'var(--hk-ink-700)',
      }}
    >
      {children}
    </button>
  )
}

/**
 * ToolsTab 列出 Hermes 可用的只读工具(发现用)。纯只读:绝不提供任何执行 / 提议 / 确认入口,
 * 只展示清单。用显式 admin Bearer 拉取(见 hermesClient)。历史会话回看已拆到 HermesHistoryTab。
 */
function ToolsTab({ adminToken, asUserId, tenantId }: { adminToken: string; asUserId: number; tenantId?: number }) {
  const [tools, setTools] = useState<HermesTool[] | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setErr(null)
    void listTools(adminToken, { asUserId, tenantId }, ctrl.signal)
      .then((t) => setTools(t))
      .catch((e) => {
        if (!(e instanceof DOMException && e.name === 'AbortError')) {
          setErr(e instanceof Error ? e.message : '加载失败')
        }
      })
    return () => ctrl.abort()
  }, [adminToken, asUserId, tenantId])

  // 只展示只读工具(read_only 且非 mutating),贯彻面板纯只读约束。
  const readOnlyTools = (tools ?? []).filter((t) => t.read_only && !t.mutating)

  return (
    <div style={S.messageScroll}>
      {err && (
        <div style={S.errorBox}>
          <strong>加载失败</strong>
          <div style={{ marginTop: 4 }}>{err}</div>
        </div>
      )}
      <SectionLabel>只读工具({readOnlyTools.length})</SectionLabel>
      {tools === null && !err ? (
        <p style={mutedText}>加载中…</p>
      ) : readOnlyTools.length === 0 ? (
        <p style={mutedText}>暂无可用只读工具。</p>
      ) : (
        readOnlyTools.map((t) => (
          <div key={t.name} style={S.toolRow}>
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--hk-space-2)' }}>
              <strong style={{ fontSize: 12, color: 'var(--hk-ink-900)' }}>{t.name}</strong>
              <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{t.category}</span>
            </div>
            {t.description && <div style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{t.description}</div>}
          </div>
        ))
      )}
    </div>
  )
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--hk-ink-500)', marginTop: 'var(--hk-space-2)' }}>{children}</div>
  )
}

const mutedText: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }

function MessageArea({
  messages,
  error,
  ready,
  hasActor,
}: {
  messages: ReturnType<typeof useHermesChat>['messages']
  error: { code: string; message: string } | null
  ready: boolean
  hasActor: boolean
}) {
  const scrollRef = useRef<HTMLDivElement>(null)
  // 新消息/增量到达时滚到底。
  useEffect(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages, error])

  if (messages.length === 0) {
    return (
      <div style={S.messageScroll} ref={scrollRef}>
        <div style={S.welcomeBox}>
          <p style={{ margin: 0, fontWeight: 600, color: 'var(--hk-ink-900)' }}>你好,我是 Hermes</p>
          <p style={{ margin: '8px 0 0', color: 'var(--hk-ink-500)', fontSize: 13, lineHeight: 1.6 }}>
            我能帮你查看运营台只读信息(账号、路由、配额、健康、告警等),并结合你当前所在页回答。
            我只读取与说明,<strong>不会做任何改动</strong>。
          </p>
          {!hasActor && !ready && (
            <p style={{ margin: '12px 0 0', color: 'var(--hk-warn)', fontSize: 12 }}>
              提示:先设置操作身份(as_user_id)即可开始。
            </p>
          )}
        </div>
      </div>
    )
  }

  return (
    <div style={S.messageScroll} ref={scrollRef}>
      {messages.map((m, i) => (
        <div key={i} style={m.role === 'user' ? S.userRow : S.assistantRow}>
          <div style={m.role === 'user' ? S.userBubble : S.assistantBubble}>
            {m.text || (m.streaming ? '思考中…' : '')}
            {m.streaming && m.text !== '' && <span style={S.caret} aria-hidden />}
          </div>
        </div>
      ))}
      {error && (
        <div style={S.errorBox}>
          <strong>出错了({error.code})</strong>
          <div style={{ marginTop: 4 }}>{error.message}</div>
        </div>
      )}
    </div>
  )
}

function ActorForm({
  actor,
  onSave,
  onCancel,
}: {
  actor: HermesActor
  onSave: (a: HermesActor) => void
  onCancel: () => void
}) {
  const [asUser, setAsUser] = useState(actor.asUserId !== null ? String(actor.asUserId) : '')
  const [tenant, setTenant] = useState(actor.tenantId !== null ? String(actor.tenantId) : '')
  const parsedAsUser = toPositiveInt(asUser)
  const parsedTenant = tenant.trim() === '' ? null : toPositiveInt(tenant)
  const valid = parsedAsUser !== null && (tenant.trim() === '' || parsedTenant !== null)

  return (
    <div style={S.actorForm}>
      <label style={S.fieldLabel}>
        as_user_id(必填,正整数)
        <input
          style={S.fieldInput}
          value={asUser}
          onChange={(e) => setAsUser(e.target.value)}
          placeholder="例如 1"
          inputMode="numeric"
        />
      </label>
      <label style={S.fieldLabel}>
        tenant_id(可选;平台管理员需填,租户运营可省)
        <input
          style={S.fieldInput}
          value={tenant}
          onChange={(e) => setTenant(e.target.value)}
          placeholder="留空走 token scope"
          inputMode="numeric"
        />
      </label>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
        <button type="button" style={S.ghostBtn} onClick={onCancel}>
          取消
        </button>
        <button
          type="button"
          style={{ ...S.primaryBtn, opacity: valid ? 1 : 0.5 }}
          disabled={!valid}
          onClick={() => onSave({ asUserId: parsedAsUser, tenantId: parsedTenant })}
        >
          保存
        </button>
      </div>
      {asUser.trim() !== '' && parsedAsUser === null && (
        <div style={S.hintLine}>as_user_id 必须是正整数。</div>
      )}
    </div>
  )
}

function EmptyState({ title, desc }: { title: string; desc: string }) {
  return (
    <div style={S.emptyState}>
      <div style={{ fontSize: 28 }} aria-hidden>
        🔒
      </div>
      <p style={{ margin: '12px 0 4px', fontWeight: 600, color: 'var(--hk-ink-900)' }}>{title}</p>
      <p style={{ margin: 0, color: 'var(--hk-ink-500)', fontSize: 13, lineHeight: 1.6 }}>{desc}</p>
    </div>
  )
}

// ── 宽度持久化 ──

function clampWidth(n: number): number {
  if (Number.isNaN(n) || n <= 0) return DEFAULT_WIDTH
  return Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, Math.round(n)))
}

function loadWidth(): number {
  try {
    const raw = localStorage.getItem(LS_WIDTH)
    if (!raw) return DEFAULT_WIDTH
    return clampWidth(Number(raw))
  } catch {
    return DEFAULT_WIDTH
  }
}

function saveWidth(w: number): void {
  try {
    localStorage.setItem(LS_WIDTH, String(w))
  } catch {
    /* localStorage 不可用,忽略 */
  }
}

