import type React from 'react'

/*
 * HermesPanel 的样式常量集中存放(从组件文件拆出以守 §13 codebudget 单文件 ≤600 行)。
 * 全部用 --hk-* 设计 token,禁硬编码颜色;on-primary 文本用 #fff(与既有 TopBar/ModelsPage 等一致)。
 */

export const basePanel: React.CSSProperties = {
  position: 'fixed',
  top: 0,
  right: 0,
  bottom: 0,
  display: 'flex',
  flexDirection: 'column',
  background: 'var(--hk-surface)',
  borderLeft: '1px solid var(--hk-line)',
  boxShadow: 'var(--hk-shadow-3)',
  zIndex: 'var(--hk-z-overlay)' as unknown as number,
}
export const dragHandle: React.CSSProperties = {
  position: 'absolute',
  left: 0,
  top: 0,
  bottom: 0,
  width: 6,
  cursor: 'col-resize',
  background: 'transparent',
}
export const headerBar: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  padding: 'var(--hk-space-3) var(--hk-space-4)',
  borderBottom: '1px solid var(--hk-line)',
  background: 'var(--hk-surface-sunken)',
}
export const brandDot: React.CSSProperties = {
  width: 18,
  height: 18,
  borderRadius: 'var(--hk-radius-sm)',
  background: 'linear-gradient(135deg, var(--hk-primary-500), var(--hk-primary-700))',
  flexShrink: 0,
}
export const readonlyBadge: React.CSSProperties = {
  fontSize: 11,
  color: 'var(--hk-primary-700)',
  background: 'var(--hk-primary-50)',
  border: '1px solid var(--hk-primary-100)',
  borderRadius: 'var(--hk-radius-pill)',
  padding: '1px 8px',
}
export const iconBtn: React.CSSProperties = {
  border: 'none',
  background: 'transparent',
  color: 'var(--hk-ink-500)',
  fontSize: 14,
  cursor: 'pointer',
  padding: '2px 6px',
  borderRadius: 'var(--hk-radius-sm)',
}
export const contextRow: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--hk-space-2)',
  flexWrap: 'wrap',
  padding: 'var(--hk-space-2) var(--hk-space-4)',
  borderBottom: '1px solid var(--hk-line)',
}
export const chip: React.CSSProperties = {
  fontSize: 12,
  color: 'var(--hk-ink-700)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-pill)',
  padding: '2px 10px',
  maxWidth: '100%',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
}
export const actorChip: React.CSSProperties = {
  fontSize: 12,
  color: 'var(--hk-primary-700)',
  background: 'var(--hk-surface)',
  border: '1px dashed var(--hk-primary-300)',
  borderRadius: 'var(--hk-radius-pill)',
  padding: '2px 10px',
  cursor: 'pointer',
}
export const actorForm: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-2)',
  padding: 'var(--hk-space-3) var(--hk-space-4)',
  borderBottom: '1px solid var(--hk-line)',
  background: 'var(--hk-surface-sunken)',
}
export const fieldLabel: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-1)',
  fontSize: 12,
  color: 'var(--hk-ink-700)',
}
export const fieldInput: React.CSSProperties = {
  height: 30,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
}
export const messageScroll: React.CSSProperties = {
  flex: 1,
  minHeight: 0,
  overflowY: 'auto',
  padding: 'var(--hk-space-4)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
}
export const welcomeBox: React.CSSProperties = {
  padding: 'var(--hk-space-4)',
  borderRadius: 'var(--hk-radius-lg)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
}
export const userRow: React.CSSProperties = { display: 'flex', justifyContent: 'flex-end' }
export const assistantRow: React.CSSProperties = { display: 'flex', justifyContent: 'flex-start' }
export const userBubble: React.CSSProperties = {
  maxWidth: '85%',
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-lg)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 13,
  lineHeight: 1.6,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
}
export const assistantBubble: React.CSSProperties = {
  maxWidth: '85%',
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-lg)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
  color: 'var(--hk-ink-900)',
  fontSize: 13,
  lineHeight: 1.6,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-word',
}
export const caret: React.CSSProperties = {
  display: 'inline-block',
  width: 6,
  height: 14,
  marginLeft: 2,
  verticalAlign: 'text-bottom',
  background: 'var(--hk-ink-300)',
}
export const errorBox: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 12,
  color: 'var(--hk-danger)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-danger)',
}
export const inputBar: React.CSSProperties = {
  borderTop: '1px solid var(--hk-line)',
  padding: 'var(--hk-space-3) var(--hk-space-4)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-2)',
  background: 'var(--hk-surface)',
}
export const textArea: React.CSSProperties = {
  flex: 1,
  resize: 'none',
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  fontFamily: 'var(--hk-font-sans)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  lineHeight: 1.5,
}
export const sendBtn: React.CSSProperties = {
  height: 36,
  padding: '0 var(--hk-space-4)',
  border: 'none',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 13,
  cursor: 'pointer',
  flexShrink: 0,
}
export const resetBtn: React.CSSProperties = {
  alignSelf: 'flex-start',
  border: 'none',
  background: 'transparent',
  color: 'var(--hk-ink-500)',
  fontSize: 12,
  cursor: 'pointer',
  padding: 0,
}
export const hintLine: React.CSSProperties = { fontSize: 12, color: 'var(--hk-warn)' }
export const emptyState: React.CSSProperties = {
  flex: 1,
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'center',
  justifyContent: 'center',
  textAlign: 'center',
  padding: 'var(--hk-space-6)',
}
export const ghostBtn: React.CSSProperties = {
  height: 30,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
  fontSize: 12,
  cursor: 'pointer',
}
export const primaryBtn: React.CSSProperties = {
  height: 30,
  padding: '0 var(--hk-space-4)',
  border: 'none',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 12,
  cursor: 'pointer',
}
export const toolRow: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-1)',
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
}
export const tabBar: React.CSSProperties = {
  display: 'inline-flex',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  overflow: 'hidden',
}
// 可点击的历史会话行(回看入口):左侧标题/时间,右侧删除按钮。
export const convoRow: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: 'var(--hk-space-2)',
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
}
export const convoOpenBtn: React.CSSProperties = {
  flex: 1,
  minWidth: 0,
  display: 'flex',
  flexDirection: 'column',
  gap: 2,
  alignItems: 'flex-start',
  border: 'none',
  background: 'transparent',
  cursor: 'pointer',
  padding: 0,
  textAlign: 'left',
}
// 删除会话按钮(破坏性动作:点击会先 window.confirm 二次确认)。
export const convoDeleteBtn: React.CSSProperties = {
  flexShrink: 0,
  border: '1px solid var(--hk-danger)',
  background: 'transparent',
  color: 'var(--hk-danger)',
  fontSize: 11,
  cursor: 'pointer',
  padding: '2px 8px',
  borderRadius: 'var(--hk-radius-sm)',
}
// 回看头部:返回按钮 + 会话标题。
export const viewerHeader: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--hk-space-2)',
  padding: 'var(--hk-space-2) var(--hk-space-4)',
  borderBottom: '1px solid var(--hk-line)',
  background: 'var(--hk-surface-sunken)',
}
export const backBtn: React.CSSProperties = {
  border: '1px solid var(--hk-line)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
  fontSize: 12,
  cursor: 'pointer',
  padding: '2px 10px',
  borderRadius: 'var(--hk-radius-pill)',
}
// 模块上下文卡片:身份 + 探针状态。
export const moduleRow: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-1)',
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
}
// 探针状态 pill 的色调映射(probeTone 的四档)。
export const probePill: Record<'ok' | 'warn' | 'danger' | 'muted', React.CSSProperties> = {
  ok: {
    fontSize: 11,
    color: 'var(--hk-success)',
    border: '1px solid var(--hk-success)',
    borderRadius: 'var(--hk-radius-pill)',
    padding: '0 8px',
  },
  warn: {
    fontSize: 11,
    color: 'var(--hk-warn)',
    border: '1px solid var(--hk-warn)',
    borderRadius: 'var(--hk-radius-pill)',
    padding: '0 8px',
  },
  danger: {
    fontSize: 11,
    color: 'var(--hk-danger)',
    border: '1px solid var(--hk-danger)',
    borderRadius: 'var(--hk-radius-pill)',
    padding: '0 8px',
  },
  muted: {
    fontSize: 11,
    color: 'var(--hk-ink-500)',
    border: '1px solid var(--hk-line)',
    borderRadius: 'var(--hk-radius-pill)',
    padding: '0 8px',
  },
}
