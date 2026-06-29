import React from "react";

/**
 * HUAKAI Input — dark-surface text field matching the console form style.
 * Pairs with a Label. Mono variant for tokens / keys / IDs.
 */

export function Input({ invalid = false, mono = false, style, ...props }) {
  const [focus, setFocus] = React.useState(false);
  return (
    <input
      onFocus={(e) => { setFocus(true); props.onFocus?.(e); }}
      onBlur={(e) => { setFocus(false); props.onBlur?.(e); }}
      style={{
        width: "100%",
        height: "var(--control-h)",
        padding: "0 0.6rem",
        background: "var(--bg-surface-2)",
        color: "var(--text-body)",
        border: `1px solid ${invalid ? "var(--danger)" : focus ? "var(--accent)" : "var(--border-strong)"}`,
        borderRadius: "var(--radius-md)",
        fontFamily: mono ? "var(--font-mono)" : "var(--font-sans)",
        fontSize: "var(--text-sm)",
        outline: "none",
        boxShadow: focus ? "0 0 0 3px var(--accent-soft-bg)" : "none",
        transition: "border-color var(--dur-fast), box-shadow var(--dur-fast)",
        ...style,
      }}
      {...props}
    />
  );
}

export function Label({ children, style, ...props }) {
  return (
    <label style={{ display: "block", fontSize: "var(--text-xs)", fontWeight: 500, color: "var(--text-muted)", marginBottom: "0.35rem", ...style }} {...props}>
      {children}
    </label>
  );
}
