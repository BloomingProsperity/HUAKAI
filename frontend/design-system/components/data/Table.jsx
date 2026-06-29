import React from "react";

/**
 * HUAKAI Table primitives — thin styled wrappers over <table>. Hairline row
 * borders, uppercase muted header, hover-highlight rows. Use mono cells for
 * IDs / concurrency / timestamps.
 */

export function Table({ children, style, ...props }) {
  return (
    <div style={{ width: "100%", overflowX: "auto" }}>
      <table style={{ width: "100%", borderCollapse: "collapse", fontFamily: "var(--font-sans)", fontSize: "var(--text-sm)", ...style }} {...props}>
        {children}
      </table>
    </div>
  );
}

export function THead({ children, ...props }) {
  return <thead {...props}>{children}</thead>;
}

export function TBody({ children, ...props }) {
  return <tbody {...props}>{children}</tbody>;
}

export function TR({ children, hover = true, style, ...props }) {
  const [h, setH] = React.useState(false);
  return (
    <tr
      onMouseEnter={() => setH(true)}
      onMouseLeave={() => setH(false)}
      style={{
        borderBottom: "1px solid var(--border)",
        background: h && hover ? "var(--bg-surface-hover)" : "transparent",
        transition: "background var(--dur-fast)",
        ...style,
      }}
      {...props}
    >
      {children}
    </tr>
  );
}

export function TH({ children, style, ...props }) {
  return (
    <th
      style={{
        textAlign: "left",
        padding: "0.6rem 0.9rem",
        fontSize: "var(--text-2xs)",
        fontWeight: 600,
        letterSpacing: "0.04em",
        textTransform: "uppercase",
        color: "var(--text-muted)",
        verticalAlign: "middle",
        whiteSpace: "nowrap",
        ...style,
      }}
      {...props}
    >
      {children}
    </th>
  );
}

export function TD({ children, mono = false, style, ...props }) {
  return (
    <td
      style={{
        padding: "0.7rem 0.9rem",
        color: "var(--text-body)",
        verticalAlign: "middle",
        fontFamily: mono ? "var(--font-mono)" : "inherit",
        fontVariantNumeric: mono ? "tabular-nums" : "normal",
        ...style,
      }}
      {...props}
    >
      {children}
    </td>
  );
}
