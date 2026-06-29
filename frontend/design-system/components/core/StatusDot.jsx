import React from "react";

/**
 * HUAKAI StatusDot — a small glowing dot for live/heartbeat indicators.
 * tone: online (emerald) · offline (red) · pending (amber) · idle (slate) · live (teal).
 * Mirrors the header "后端心跳" indicator and account live dots.
 */

const tones = {
  online: { color: "#10b981", glow: "rgba(16,185,129,0.18)" },
  offline: { color: "#ef4444", glow: "rgba(239,68,68,0.18)" },
  pending: { color: "#f59e0b", glow: "rgba(245,158,11,0.18)" },
  idle: { color: "#94a3b8", glow: "rgba(148,163,184,0.18)" },
  live: { color: "#14b8a6", glow: "rgba(20,184,166,0.22)" },
};

export function StatusDot({ tone = "online", size = 8, pulse = false, style, ...props }) {
  const t = tones[tone] || tones.online;
  return (
    <span
      style={{
        display: "inline-block",
        width: size,
        height: size,
        borderRadius: "var(--radius-full)",
        background: t.color,
        boxShadow: `0 0 0 3px ${t.glow}`,
        animation: pulse ? "hk-dot-pulse 1.6s var(--ease-standard) infinite" : "none",
        ...style,
      }}
      {...props}
    >
      <style>{`@keyframes hk-dot-pulse{0%,100%{opacity:1}50%{opacity:.45}}`}</style>
    </span>
  );
}
