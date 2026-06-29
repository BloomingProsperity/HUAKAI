import React from "react";

/**
 * HUAKAI Button — shadcn-derived control with the brand teal as the default fill.
 * Variants: default (primary teal) · destructive · outline · secondary · ghost · link.
 * Sizes: sm · md · lg · icon.
 */

const base = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  gap: "0.5rem",
  whiteSpace: "nowrap",
  fontFamily: "var(--font-sans)",
  fontWeight: 500,
  borderRadius: "var(--radius-md)",
  border: "1px solid transparent",
  cursor: "pointer",
  textDecoration: "none",
  transition: "background-color var(--dur-fast) var(--ease-standard), color var(--dur-fast) var(--ease-standard), border-color var(--dur-fast) var(--ease-standard), opacity var(--dur-fast)",
  userSelect: "none",
};

const sizes = {
  sm: { height: "var(--control-h-sm)", padding: "0 0.75rem", fontSize: "var(--text-sm)" },
  md: { height: "var(--control-h)", padding: "0 1rem", fontSize: "var(--text-sm)" },
  lg: { height: "2.75rem", padding: "0 2rem", fontSize: "var(--text-base)" },
  icon: { height: "var(--control-h)", width: "var(--control-h)", padding: 0 },
};

const variants = {
  default: {
    background: "var(--accent)",
    color: "var(--text-on-primary)",
    borderColor: "var(--accent)",
  },
  destructive: {
    background: "var(--danger)",
    color: "#ffffff",
    borderColor: "var(--danger)",
  },
  outline: {
    background: "var(--bg-surface)",
    color: "var(--text-body)",
    borderColor: "var(--border-strong)",
  },
  secondary: {
    background: "var(--bg-surface-2)",
    color: "var(--text-body)",
    borderColor: "var(--bg-surface-2)",
  },
  ghost: {
    background: "transparent",
    color: "var(--text-body)",
    borderColor: "transparent",
  },
  link: {
    background: "transparent",
    color: "var(--accent)",
    borderColor: "transparent",
    textDecoration: "underline",
    textUnderlineOffset: "4px",
    height: "auto",
    padding: 0,
  },
};

const hover = {
  default: { background: "var(--accent-hover)", borderColor: "var(--accent-hover)" },
  destructive: { background: "#dc2626", borderColor: "#dc2626" },
  outline: { background: "var(--accent-soft-bg)", color: "var(--accent-soft-text)", borderColor: "var(--accent-soft-border)" },
  secondary: { background: "var(--bg-surface-hover)" },
  ghost: { background: "var(--accent-soft-bg)", color: "var(--accent-soft-text)" },
  link: {},
};

export function Button({
  variant = "default",
  size = "md",
  disabled = false,
  asChild = false,
  children,
  style,
  ...props
}) {
  const [h, setH] = React.useState(false);
  const merged = {
    ...base,
    ...sizes[size],
    ...variants[variant],
    ...(h && !disabled ? hover[variant] : null),
    ...(disabled ? { opacity: 0.5, cursor: "not-allowed", pointerEvents: "none" } : null),
    ...style,
  };
  return (
    <button
      style={merged}
      disabled={disabled}
      onMouseEnter={() => setH(true)}
      onMouseLeave={() => setH(false)}
      {...props}
    >
      {children}
    </button>
  );
}
