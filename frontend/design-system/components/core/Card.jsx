import React from "react";

/**
 * HUAKAI Card — the console's primary surface. Soft "card" shadow, hairline border,
 * rounded-lg. Set `interactive` to lift + warm the border on hover (used for nav cards).
 * Compose with CardHeader / CardTitle / CardContent / CardFooter.
 */

export function Card({ interactive = false, children, style, ...props }) {
  const [h, setH] = React.useState(false);
  return (
    <div
      onMouseEnter={() => setH(true)}
      onMouseLeave={() => setH(false)}
      style={{
        background: "var(--bg-surface)",
        border: "1px solid var(--border)",
        borderRadius: "var(--radius-lg)",
        boxShadow: h && interactive ? "var(--shadow-card-hover)" : "var(--shadow-card)",
        color: "var(--text-body)",
        transition: "transform var(--dur-base) var(--ease-out), box-shadow var(--dur-base), border-color var(--dur-fast)",
        transform: h && interactive ? "translateY(var(--lift-hover))" : "none",
        borderColor: h && interactive ? "var(--accent)" : "var(--border)",
        ...style,
      }}
      {...props}
    >
      {children}
    </div>
  );
}

export function CardHeader({ children, style, ...props }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "0.375rem", padding: "1.25rem 1.25rem 0.75rem", ...style }} {...props}>
      {children}
    </div>
  );
}

export function CardTitle({ children, style, ...props }) {
  return (
    <div style={{ fontSize: "var(--text-lg)", fontWeight: 600, lineHeight: 1.2, color: "var(--text-strong)", display: "flex", alignItems: "center", gap: "0.5rem", ...style }} {...props}>
      {children}
    </div>
  );
}

export function CardDescription({ children, style, ...props }) {
  return (
    <div style={{ fontSize: "var(--text-sm)", color: "var(--text-muted)", ...style }} {...props}>
      {children}
    </div>
  );
}

export function CardContent({ children, style, ...props }) {
  return (
    <div style={{ padding: "0 1.25rem 1.25rem", ...style }} {...props}>
      {children}
    </div>
  );
}

export function CardFooter({ children, style, ...props }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: "0.5rem", padding: "0 1.25rem 1.25rem", ...style }} {...props}>
      {children}
    </div>
  );
}
