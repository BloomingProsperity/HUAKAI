import React, { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import {
  deleteConversation,
  listConversationMessages,
  listConversations,
  type HermesConversation,
  type HermesMessage,
} from './hermesClient'
import {
  conversationTitle,
  messageText,
  roleLabel,
  sortMessagesByCreatedAt,
} from './hermesHistory'
import * as S from './hermesPanelStyles'

/*
 * Hermes 历史会话回看页(纯只读 + 唯一允许的破坏性动作=删除自己的会话)。
 *
 * 两态:列表态(列会话,可点开、可删除)/ 回看态(载入并只读展示某会话的消息)。
 * 删除走 window.confirm 二次确认;删除仅作用于用户自己的会话记录,绝不触达系统状态 / 计费 / 配置。
 * 所有请求显式带 admin Bearer + as_user_id/tenant_id(见 hermesClient)。
 */

interface Props {
  adminToken: string
  asUserId: number
  tenantId?: number
}

export function HermesHistoryTab({ adminToken, asUserId, tenantId }: Props) {
  const [conversations, setConversations] = useState<HermesConversation[] | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [openId, setOpenId] = useState<number | null>(null)
  const [deletingId, setDeletingId] = useState<number | null>(null)

  const auth = { asUserId, tenantId }

  const reload = useCallback(
    (signal?: AbortSignal) => {
      setErr(null)
      return listConversations(adminToken, { asUserId, tenantId }, signal)
        .then((c) => setConversations(c))
        .catch((e) => {
          if (e instanceof DOMException && e.name === 'AbortError') return
          setErr(toMessage(e))
        })
    },
    [adminToken, asUserId, tenantId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    void reload(ctrl.signal)
    return () => ctrl.abort()
  }, [reload])

  const onDelete = useCallback(
    async (c: HermesConversation) => {
      // 破坏性动作:删除会话前二次确认。
      const ok = window.confirm(`确定删除「${conversationTitle(c)}」?该会话记录将被移除,无法恢复。`)
      if (!ok) return
      setDeletingId(c.id)
      setErr(null)
      try {
        await deleteConversation(adminToken, c.id, auth)
        if (openId === c.id) setOpenId(null)
        await reload()
      } catch (e) {
        setErr(toMessage(e))
      } finally {
        setDeletingId(null)
      }
    },
    [adminToken, auth, openId, reload],
  )

  if (openId !== null) {
    const current = (conversations ?? []).find((c) => c.id === openId)
    return (
      <MessageViewer
        adminToken={adminToken}
        conversationId={openId}
        title={current ? conversationTitle(current) : `会话 #${openId}`}
        asUserId={asUserId}
        tenantId={tenantId}
        onBack={() => setOpenId(null)}
      />
    )
  }

  return (
    <div style={S.messageScroll}>
      {err && (
        <div style={S.errorBox}>
          <strong>操作失败</strong>
          <div style={{ marginTop: 4 }}>{err}</div>
        </div>
      )}
      <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--hk-ink-500)' }}>
        历史会话({conversations?.length ?? 0})
      </div>
      {conversations === null && !err ? (
        <p style={muted}>加载中…</p>
      ) : (conversations ?? []).length === 0 ? (
        <p style={muted}>暂无历史会话。</p>
      ) : (
        (conversations ?? []).map((c) => (
          <div key={c.id} style={S.convoRow}>
            <button type="button" style={S.convoOpenBtn} onClick={() => setOpenId(c.id)}>
              <strong style={{ fontSize: 12, color: 'var(--hk-ink-900)' }}>{conversationTitle(c)}</strong>
              {c.last_message_at && (
                <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{formatTime(c.last_message_at)}</span>
              )}
            </button>
            <button
              type="button"
              style={{ ...S.convoDeleteBtn, opacity: deletingId === c.id ? 0.5 : 1 }}
              onClick={() => void onDelete(c)}
              disabled={deletingId === c.id}
              aria-label="删除会话"
            >
              {deletingId === c.id ? '删除中…' : '删除'}
            </button>
          </div>
        ))
      )}
    </div>
  )
}

function MessageViewer({
  adminToken,
  conversationId,
  title,
  asUserId,
  tenantId,
  onBack,
}: {
  adminToken: string
  conversationId: number
  title: string
  asUserId: number
  tenantId?: number
  onBack: () => void
}) {
  const [messages, setMessages] = useState<HermesMessage[] | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setErr(null)
    setMessages(null)
    void listConversationMessages(adminToken, conversationId, { asUserId, tenantId }, ctrl.signal)
      .then((m) => setMessages(sortMessagesByCreatedAt(m)))
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return
        setErr(toMessage(e))
      })
    return () => ctrl.abort()
  }, [adminToken, conversationId, asUserId, tenantId])

  return (
    <>
      <div style={S.viewerHeader}>
        <button type="button" style={S.backBtn} onClick={onBack}>
          ← 返回
        </button>
        <strong style={{ fontSize: 13, color: 'var(--hk-ink-900)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {title}
        </strong>
        <span style={S.readonlyBadge}>只读回看</span>
      </div>
      <div style={S.messageScroll}>
        {err && (
          <div style={S.errorBox}>
            <strong>加载失败</strong>
            <div style={{ marginTop: 4 }}>{err}</div>
          </div>
        )}
        {messages === null && !err ? (
          <p style={muted}>加载中…</p>
        ) : (messages ?? []).length === 0 ? (
          <p style={muted}>该会话暂无消息。</p>
        ) : (
          (messages ?? []).map((m) => {
            const text = messageText(m.content)
            const isUser = m.role === 'user'
            return (
              <div key={m.id} style={isUser ? S.userRow : S.assistantRow}>
                <div style={isUser ? S.userBubble : S.assistantBubble}>
                  <div style={{ fontSize: 10, opacity: 0.7, marginBottom: 2 }}>{roleLabel(m.role)}</div>
                  {text || <em style={{ opacity: 0.6 }}>(无文本内容)</em>}
                </div>
              </div>
            )
          })
        )}
      </div>
    </>
  )
}

const muted: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }

/** formatTime 把 RFC3339 转本地可读串;解析失败回退原串。 */
function formatTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

/** toMessage 把异常归一成展示串(ApiError 用其 message,其它取 message 或兜底)。 */
function toMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message
  if (e instanceof Error) return e.message
  return '请求失败'
}
