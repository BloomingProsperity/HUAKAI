import React from "react";

/**
 * HUAKAI StatCard — KPI tile used across the operations dashboard.
 * A title + big value, with a tone-colored icon chip (ring-1) top-right, and an
 * optional description/detail line. Lifts on hover. Pass a lucide icon element.
 */

const tones = {
  primary: { bg: "var(--accent-soft-bg)", fg: "var(--accent-soft-text)", ring: "var(--accent-soft-border)" },
  blue: { bg: "rgba(59,130,246,0.10)", fg: "#2563eb", ring: "rgba(59,130,246,0.25)" },
  emerald: { bg: "var(--success-bg)", fg: "var(--success-fg)", ring: "var(--success-border)" },
  amber: { bg: "var(--warning-bg)", fg: "var(--warning-fg)", ring: "var(--warning-border)" },
  red: { bg: "var(--danger-bg)", fg: "var(--danger-fg)", ring: "var(--danger-border)" },
  slate: { bg: "var(--bg-surface-2)", fg: "var(--text-muted)", ring: "var(--border-strong)" },
};

export function StatCard({ title, value, icon, description, detail, tone = "primary", style, ...props }) {
  const [h, setH] = React.useState(false);
  const t = tones[tone] || tones.primary;
  return (
    <div
      onMouseEnter={() => setH(true)}
      onMouseLeave={() => setH(false)}
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-lg)",
        boxShadow: h ? "var(--shadow-card-hover)" : "var(--shadow-card)",
        transform: h ? "translateY(var(--lift-hover))" : "none",
        transition: "transform var(--dur-base) var(--ease-out), box-shadow var(--dur-base)",
        padding: "1rem",
        ...style,
      }}
      {...props}
    >
      <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: "0.75rem" }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: "var(--text-sm)", fontWeight: 500, color: "var(--text-muted)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{title}</div>
          {description && (
            <div style={{ marginTop: "0.25rem", fontSize: "var(--text-xs)", color: "var(--text-subtle)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{description}</div>
          )}
        </div>
        {icon && (
          <div style={{ flexShrink: 0, display: "flex", alignItems: "center", justifyContent: "center", width: "2.25rem", height: "2.25rem", borderRadius: "var(--radius-lg)", background: t.bg, color: t.fg, boxShadow: `inset 0 0 0 1px ${t.ring}` }}>
            {icon}
          </div>
        )}
      </div>
      <div style={{ marginTop: "0.75rem", fontSize: "var(--text-2xl)", fontWeight: 700, color: "var(--text-strong)", fontVariantNumeric: "tabular-nums" }}>{value}</div>
      {detail && (
        <div style={{ marginTop: "0.5rem", fontSize: "var(--text-xs)", lineHeight: 1.45, color: "var(--text-muted)" }}>{detail}</div>
      )}
    </div>
  );
}
