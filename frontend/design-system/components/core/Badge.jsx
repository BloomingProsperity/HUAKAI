import React from "react";

/**
 * HUAKAI Badge — small status/label pill.
 * variant: default (teal) · secondary (slate) · destructive (red) · outline ·
 *          success (emerald) · warning (amber) · info (blue).
 * Used heavily for account health states (健康 / 降级 / 失败 / 冷却中) and schedule status.
 */

const base = {
  display: "inline-flex",
  alignItems: "center",
  gap: "0.3rem",
  borderRadius: "var(--radius-full)",
  border: "1px solid transparent",
  padding: "0.125rem 0.625rem",
  fontFamily: "var(--font-sans)",
  fontSize: "var(--text-xs)",
  fontWeight: 600,
  lineHeight: 1.4,
  whiteSpace: "nowrap",
};

const variants = {
  default: { background: "var(--accent)", color: "var(--text-on-primary)" },
  secondary: { background: "var(--bg-surface-2)", color: "var(--text-body)" },
  destructive: { background: "var(--danger)", color: "#ffffff" },
  outline: { background: "transparent", color: "var(--text-body)", borderColor: "var(--border-strong)" },
  success: { background: "var(--success-bg)", color: "var(--success-fg)", borderColor: "var(--success-border)" },
  warning: { background: "var(--warning-bg)", color: "var(--warning-fg)", borderColor: "var(--warning-border)" },
  info: { background: "rgba(59,130,246,0.12)", color: "#2563eb", borderColor: "rgba(59,130,246,0.35)" },
};

export function Badge({ variant = "default", children, style, ...props }) {
  return (
    <span style={{ ...base, ...variants[variant], ...style }} {...props}>
      {children}
    </span>
  );
}
