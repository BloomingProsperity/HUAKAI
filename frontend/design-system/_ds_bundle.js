/* @ds-bundle: {"format":3,"namespace":"HUAKAIDesignSystem_36f9be","components":[{"name":"Badge","sourcePath":"components/core/Badge.jsx"},{"name":"Button","sourcePath":"components/core/Button.jsx"},{"name":"Card","sourcePath":"components/core/Card.jsx"},{"name":"CardHeader","sourcePath":"components/core/Card.jsx"},{"name":"CardTitle","sourcePath":"components/core/Card.jsx"},{"name":"CardDescription","sourcePath":"components/core/Card.jsx"},{"name":"CardContent","sourcePath":"components/core/Card.jsx"},{"name":"CardFooter","sourcePath":"components/core/Card.jsx"},{"name":"Input","sourcePath":"components/core/Input.jsx"},{"name":"Label","sourcePath":"components/core/Input.jsx"},{"name":"StatusDot","sourcePath":"components/core/StatusDot.jsx"},{"name":"StatCard","sourcePath":"components/data/StatCard.jsx"},{"name":"Table","sourcePath":"components/data/Table.jsx"},{"name":"THead","sourcePath":"components/data/Table.jsx"},{"name":"TBody","sourcePath":"components/data/Table.jsx"},{"name":"TR","sourcePath":"components/data/Table.jsx"},{"name":"TH","sourcePath":"components/data/Table.jsx"},{"name":"TD","sourcePath":"components/data/Table.jsx"}],"sourceHashes":{"components/core/Badge.jsx":"d59434023628","components/core/Button.jsx":"ae322a7f4bb5","components/core/Card.jsx":"1ee4c78cade7","components/core/Input.jsx":"69e844cdc04d","components/core/StatusDot.jsx":"e547b48d09e7","components/data/StatCard.jsx":"25a23b8ad5bd","components/data/Table.jsx":"081e1ba39584","ui_kits/console/Accounts.jsx":"5b84ba386300","ui_kits/console/Chat.jsx":"f67c9c1dd654","ui_kits/console/Dashboard.jsx":"27e0040b083a","ui_kits/console/Shell.jsx":"ccfe08cd3cb6","ui_kits/console/data.js":"184cdfd4078d","ui_kits/console/icons.jsx":"03c194df0d0c","ui_kits/console/nav.js":"a184a7b56a71","ui_kits/website/CTA.jsx":"ceb6aec0a617","ui_kits/website/Features.jsx":"92bc293eb18b","ui_kits/website/Footer.jsx":"fbbcc4199e4c","ui_kits/website/Hero.jsx":"4b9c5e9dad8d","ui_kits/website/Nav.jsx":"235ad6d79e8c","ui_kits/website/Providers.jsx":"62e27fa2e055","ui_kits/website/icons.jsx":"e6078633de9e"},"inlinedExternals":[],"unexposedExports":[]} */

(() => {

const __ds_ns = (window.HUAKAIDesignSystem_36f9be = window.HUAKAIDesignSystem_36f9be || {});

const __ds_scope = {};

(__ds_ns.__errors = __ds_ns.__errors || []);

// components/core/Badge.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
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
  whiteSpace: "nowrap"
};
const variants = {
  default: {
    background: "var(--accent)",
    color: "var(--text-on-primary)"
  },
  secondary: {
    background: "var(--bg-surface-2)",
    color: "var(--text-body)"
  },
  destructive: {
    background: "var(--danger)",
    color: "#ffffff"
  },
  outline: {
    background: "transparent",
    color: "var(--text-body)",
    borderColor: "var(--border-strong)"
  },
  success: {
    background: "var(--success-bg)",
    color: "var(--success-fg)",
    borderColor: "var(--success-border)"
  },
  warning: {
    background: "var(--warning-bg)",
    color: "var(--warning-fg)",
    borderColor: "var(--warning-border)"
  },
  info: {
    background: "rgba(59,130,246,0.12)",
    color: "#2563eb",
    borderColor: "rgba(59,130,246,0.35)"
  }
};
function Badge({
  variant = "default",
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("span", _extends({
    style: {
      ...base,
      ...variants[variant],
      ...style
    }
  }, props), children);
}
Object.assign(__ds_scope, { Badge });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/Badge.jsx", error: String((e && e.message) || e) }); }

// components/core/Button.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
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
  userSelect: "none"
};
const sizes = {
  sm: {
    height: "var(--control-h-sm)",
    padding: "0 0.75rem",
    fontSize: "var(--text-sm)"
  },
  md: {
    height: "var(--control-h)",
    padding: "0 1rem",
    fontSize: "var(--text-sm)"
  },
  lg: {
    height: "2.75rem",
    padding: "0 2rem",
    fontSize: "var(--text-base)"
  },
  icon: {
    height: "var(--control-h)",
    width: "var(--control-h)",
    padding: 0
  }
};
const variants = {
  default: {
    background: "var(--accent)",
    color: "var(--text-on-primary)",
    borderColor: "var(--accent)"
  },
  destructive: {
    background: "var(--danger)",
    color: "#ffffff",
    borderColor: "var(--danger)"
  },
  outline: {
    background: "var(--bg-surface)",
    color: "var(--text-body)",
    borderColor: "var(--border-strong)"
  },
  secondary: {
    background: "var(--bg-surface-2)",
    color: "var(--text-body)",
    borderColor: "var(--bg-surface-2)"
  },
  ghost: {
    background: "transparent",
    color: "var(--text-body)",
    borderColor: "transparent"
  },
  link: {
    background: "transparent",
    color: "var(--accent)",
    borderColor: "transparent",
    textDecoration: "underline",
    textUnderlineOffset: "4px",
    height: "auto",
    padding: 0
  }
};
const hover = {
  default: {
    background: "var(--accent-hover)",
    borderColor: "var(--accent-hover)"
  },
  destructive: {
    background: "#dc2626",
    borderColor: "#dc2626"
  },
  outline: {
    background: "var(--accent-soft-bg)",
    color: "var(--accent-soft-text)",
    borderColor: "var(--accent-soft-border)"
  },
  secondary: {
    background: "var(--bg-surface-hover)"
  },
  ghost: {
    background: "var(--accent-soft-bg)",
    color: "var(--accent-soft-text)"
  },
  link: {}
};
function Button({
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
    ...(disabled ? {
      opacity: 0.5,
      cursor: "not-allowed",
      pointerEvents: "none"
    } : null),
    ...style
  };
  return /*#__PURE__*/React.createElement("button", _extends({
    style: merged,
    disabled: disabled,
    onMouseEnter: () => setH(true),
    onMouseLeave: () => setH(false)
  }, props), children);
}
Object.assign(__ds_scope, { Button });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/Button.jsx", error: String((e && e.message) || e) }); }

// components/core/Card.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
/**
 * HUAKAI Card — the console's primary surface. Soft "card" shadow, hairline border,
 * rounded-lg. Set `interactive` to lift + warm the border on hover (used for nav cards).
 * Compose with CardHeader / CardTitle / CardContent / CardFooter.
 */

function Card({
  interactive = false,
  children,
  style,
  ...props
}) {
  const [h, setH] = React.useState(false);
  return /*#__PURE__*/React.createElement("div", _extends({
    onMouseEnter: () => setH(true),
    onMouseLeave: () => setH(false),
    style: {
      background: "var(--bg-surface)",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius-lg)",
      boxShadow: h && interactive ? "var(--shadow-card-hover)" : "var(--shadow-card)",
      color: "var(--text-body)",
      transition: "transform var(--dur-base) var(--ease-out), box-shadow var(--dur-base), border-color var(--dur-fast)",
      transform: h && interactive ? "translateY(var(--lift-hover))" : "none",
      borderColor: h && interactive ? "var(--accent)" : "var(--border)",
      ...style
    }
  }, props), children);
}
function CardHeader({
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("div", _extends({
    style: {
      display: "flex",
      flexDirection: "column",
      gap: "0.375rem",
      padding: "1.25rem 1.25rem 0.75rem",
      ...style
    }
  }, props), children);
}
function CardTitle({
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("div", _extends({
    style: {
      fontSize: "var(--text-lg)",
      fontWeight: 600,
      lineHeight: 1.2,
      color: "var(--text-strong)",
      display: "flex",
      alignItems: "center",
      gap: "0.5rem",
      ...style
    }
  }, props), children);
}
function CardDescription({
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("div", _extends({
    style: {
      fontSize: "var(--text-sm)",
      color: "var(--text-muted)",
      ...style
    }
  }, props), children);
}
function CardContent({
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("div", _extends({
    style: {
      padding: "0 1.25rem 1.25rem",
      ...style
    }
  }, props), children);
}
function CardFooter({
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("div", _extends({
    style: {
      display: "flex",
      alignItems: "center",
      gap: "0.5rem",
      padding: "0 1.25rem 1.25rem",
      ...style
    }
  }, props), children);
}
Object.assign(__ds_scope, { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/Card.jsx", error: String((e && e.message) || e) }); }

// components/core/Input.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
/**
 * HUAKAI Input — dark-surface text field matching the console form style.
 * Pairs with a Label. Mono variant for tokens / keys / IDs.
 */

function Input({
  invalid = false,
  mono = false,
  style,
  ...props
}) {
  const [focus, setFocus] = React.useState(false);
  return /*#__PURE__*/React.createElement("input", _extends({
    onFocus: e => {
      setFocus(true);
      props.onFocus?.(e);
    },
    onBlur: e => {
      setFocus(false);
      props.onBlur?.(e);
    },
    style: {
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
      ...style
    }
  }, props));
}
function Label({
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("label", _extends({
    style: {
      display: "block",
      fontSize: "var(--text-xs)",
      fontWeight: 500,
      color: "var(--text-muted)",
      marginBottom: "0.35rem",
      ...style
    }
  }, props), children);
}
Object.assign(__ds_scope, { Input, Label });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/Input.jsx", error: String((e && e.message) || e) }); }

// components/core/StatusDot.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
/**
 * HUAKAI StatusDot — a small glowing dot for live/heartbeat indicators.
 * tone: online (emerald) · offline (red) · pending (amber) · idle (slate) · live (teal).
 * Mirrors the header "后端心跳" indicator and account live dots.
 */

const tones = {
  online: {
    color: "#10b981",
    glow: "rgba(16,185,129,0.18)"
  },
  offline: {
    color: "#ef4444",
    glow: "rgba(239,68,68,0.18)"
  },
  pending: {
    color: "#f59e0b",
    glow: "rgba(245,158,11,0.18)"
  },
  idle: {
    color: "#94a3b8",
    glow: "rgba(148,163,184,0.18)"
  },
  live: {
    color: "#14b8a6",
    glow: "rgba(20,184,166,0.22)"
  }
};
function StatusDot({
  tone = "online",
  size = 8,
  pulse = false,
  style,
  ...props
}) {
  const t = tones[tone] || tones.online;
  return /*#__PURE__*/React.createElement("span", _extends({
    style: {
      display: "inline-block",
      width: size,
      height: size,
      borderRadius: "var(--radius-full)",
      background: t.color,
      boxShadow: `0 0 0 3px ${t.glow}`,
      animation: pulse ? "hk-dot-pulse 1.6s var(--ease-standard) infinite" : "none",
      ...style
    }
  }, props), /*#__PURE__*/React.createElement("style", null, `@keyframes hk-dot-pulse{0%,100%{opacity:1}50%{opacity:.45}}`));
}
Object.assign(__ds_scope, { StatusDot });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/core/StatusDot.jsx", error: String((e && e.message) || e) }); }

// components/data/StatCard.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
/**
 * HUAKAI StatCard — KPI tile used across the operations dashboard.
 * A title + big value, with a tone-colored icon chip (ring-1) top-right, and an
 * optional description/detail line. Lifts on hover. Pass a lucide icon element.
 */

const tones = {
  primary: {
    bg: "var(--accent-soft-bg)",
    fg: "var(--accent-soft-text)",
    ring: "var(--accent-soft-border)"
  },
  blue: {
    bg: "rgba(59,130,246,0.10)",
    fg: "#2563eb",
    ring: "rgba(59,130,246,0.25)"
  },
  emerald: {
    bg: "var(--success-bg)",
    fg: "var(--success-fg)",
    ring: "var(--success-border)"
  },
  amber: {
    bg: "var(--warning-bg)",
    fg: "var(--warning-fg)",
    ring: "var(--warning-border)"
  },
  red: {
    bg: "var(--danger-bg)",
    fg: "var(--danger-fg)",
    ring: "var(--danger-border)"
  },
  slate: {
    bg: "var(--bg-surface-2)",
    fg: "var(--text-muted)",
    ring: "var(--border-strong)"
  }
};
function StatCard({
  title,
  value,
  icon,
  description,
  detail,
  tone = "primary",
  style,
  ...props
}) {
  const [h, setH] = React.useState(false);
  const t = tones[tone] || tones.primary;
  return /*#__PURE__*/React.createElement("div", _extends({
    onMouseEnter: () => setH(true),
    onMouseLeave: () => setH(false),
    style: {
      background: "var(--bg-surface)",
      border: "1px solid var(--border)",
      borderRadius: "var(--radius-lg)",
      boxShadow: h ? "var(--shadow-card-hover)" : "var(--shadow-card)",
      transform: h ? "translateY(var(--lift-hover))" : "none",
      transition: "transform var(--dur-base) var(--ease-out), box-shadow var(--dur-base)",
      padding: "1rem",
      ...style
    }
  }, props), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "flex-start",
      justifyContent: "space-between",
      gap: "0.75rem"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      minWidth: 0
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: "var(--text-sm)",
      fontWeight: 500,
      color: "var(--text-muted)",
      whiteSpace: "nowrap",
      overflow: "hidden",
      textOverflow: "ellipsis"
    }
  }, title), description && /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: "0.25rem",
      fontSize: "var(--text-xs)",
      color: "var(--text-subtle)",
      whiteSpace: "nowrap",
      overflow: "hidden",
      textOverflow: "ellipsis"
    }
  }, description)), icon && /*#__PURE__*/React.createElement("div", {
    style: {
      flexShrink: 0,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      width: "2.25rem",
      height: "2.25rem",
      borderRadius: "var(--radius-lg)",
      background: t.bg,
      color: t.fg,
      boxShadow: `inset 0 0 0 1px ${t.ring}`
    }
  }, icon)), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: "0.75rem",
      fontSize: "var(--text-2xl)",
      fontWeight: 700,
      color: "var(--text-strong)",
      fontVariantNumeric: "tabular-nums"
    }
  }, value), detail && /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: "0.5rem",
      fontSize: "var(--text-xs)",
      lineHeight: 1.45,
      color: "var(--text-muted)"
    }
  }, detail));
}
Object.assign(__ds_scope, { StatCard });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/data/StatCard.jsx", error: String((e && e.message) || e) }); }

// components/data/Table.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
/**
 * HUAKAI Table primitives — thin styled wrappers over <table>. Hairline row
 * borders, uppercase muted header, hover-highlight rows. Use mono cells for
 * IDs / concurrency / timestamps.
 */

function Table({
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      width: "100%",
      overflowX: "auto"
    }
  }, /*#__PURE__*/React.createElement("table", _extends({
    style: {
      width: "100%",
      borderCollapse: "collapse",
      fontFamily: "var(--font-sans)",
      fontSize: "var(--text-sm)",
      ...style
    }
  }, props), children));
}
function THead({
  children,
  ...props
}) {
  return /*#__PURE__*/React.createElement("thead", props, children);
}
function TBody({
  children,
  ...props
}) {
  return /*#__PURE__*/React.createElement("tbody", props, children);
}
function TR({
  children,
  hover = true,
  style,
  ...props
}) {
  const [h, setH] = React.useState(false);
  return /*#__PURE__*/React.createElement("tr", _extends({
    onMouseEnter: () => setH(true),
    onMouseLeave: () => setH(false),
    style: {
      borderBottom: "1px solid var(--border)",
      background: h && hover ? "var(--bg-surface-hover)" : "transparent",
      transition: "background var(--dur-fast)",
      ...style
    }
  }, props), children);
}
function TH({
  children,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("th", _extends({
    style: {
      textAlign: "left",
      padding: "0.6rem 0.9rem",
      fontSize: "var(--text-2xs)",
      fontWeight: 600,
      letterSpacing: "0.04em",
      textTransform: "uppercase",
      color: "var(--text-muted)",
      verticalAlign: "middle",
      whiteSpace: "nowrap",
      ...style
    }
  }, props), children);
}
function TD({
  children,
  mono = false,
  style,
  ...props
}) {
  return /*#__PURE__*/React.createElement("td", _extends({
    style: {
      padding: "0.7rem 0.9rem",
      color: "var(--text-body)",
      verticalAlign: "middle",
      fontFamily: mono ? "var(--font-mono)" : "inherit",
      fontVariantNumeric: mono ? "tabular-nums" : "normal",
      ...style
    }
  }, props), children);
}
Object.assign(__ds_scope, { Table, THead, TBody, TR, TH, TD });
})(); } catch (e) { __ds_ns.__errors.push({ path: "components/data/Table.jsx", error: String((e && e.message) || e) }); }

// ui_kits/console/Accounts.jsx
try { (() => {
// 账号池 — provider account list with filter chips and a "新增账号" action.
function Accounts() {
  const {
    Card,
    CardHeader,
    CardTitle,
    CardContent,
    Badge,
    Button,
    Table,
    THead,
    TBody,
    TR,
    TH,
    TD,
    Input
  } = window.HUAKAIDesignSystem_36f9be;
  const [filter, setFilter] = React.useState("all");
  const chips = [{
    key: "all",
    label: "全部"
  }, {
    key: "operational",
    label: "健康"
  }, {
    key: "degraded",
    label: "降级"
  }, {
    key: "cooling_down",
    label: "冷却中"
  }, {
    key: "failed",
    label: "失败"
  }];
  const rows = window.HK_ACCOUNTS.filter(a => filter === "all" || a.health === filter);
  return /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      justifyContent: "space-between",
      alignItems: "center",
      gap: 16,
      flexWrap: "wrap",
      marginBottom: 20
    }
  }, /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement("h1", {
    style: {
      margin: 0,
      fontSize: 24,
      fontWeight: 700,
      color: "var(--text-strong)"
    }
  }, "\u8D26\u53F7\u6C60"), /*#__PURE__*/React.createElement("p", {
    style: {
      margin: "6px 0 0",
      fontSize: 14,
      color: "var(--text-muted)"
    }
  }, "Provider Account \u2014 list / create / clear-rate-limit")), /*#__PURE__*/React.createElement(Button, null, /*#__PURE__*/React.createElement(Icon, {
    name: "plus"
  }), " \u65B0\u589E\u8D26\u53F7")), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 10,
      marginBottom: 16,
      flexWrap: "wrap"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      width: 240
    }
  }, /*#__PURE__*/React.createElement(Input, {
    placeholder: "\u641C\u7D22\u8D26\u53F7 ID / \u4F9B\u5E94\u5546\u2026"
  })), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 8,
      flexWrap: "wrap"
    }
  }, chips.map(c => /*#__PURE__*/React.createElement("button", {
    key: c.key,
    onClick: () => setFilter(c.key),
    style: {
      padding: "6px 12px",
      borderRadius: 999,
      fontSize: 12,
      fontWeight: 600,
      cursor: "pointer",
      fontFamily: "var(--font-sans)",
      border: `1px solid ${filter === c.key ? "var(--accent-soft-border)" : "var(--border)"}`,
      background: filter === c.key ? "var(--accent-soft-bg)" : "transparent",
      color: filter === c.key ? "var(--primary-300)" : "var(--text-muted)"
    }
  }, c.label)))), /*#__PURE__*/React.createElement(Card, null, /*#__PURE__*/React.createElement(CardContent, {
    style: {
      padding: "6px 0"
    }
  }, /*#__PURE__*/React.createElement(Table, null, /*#__PURE__*/React.createElement(THead, null, /*#__PURE__*/React.createElement(TR, {
    hover: false
  }, /*#__PURE__*/React.createElement(TH, null, "\u8D26\u53F7"), /*#__PURE__*/React.createElement(TH, null, "\u4F9B\u5E94\u5546"), /*#__PURE__*/React.createElement(TH, null, "\u6A21\u578B"), /*#__PURE__*/React.createElement(TH, null, "\u5065\u5EB7"), /*#__PURE__*/React.createElement(TH, null, "\u8C03\u5EA6"), /*#__PURE__*/React.createElement(TH, null, "\u5E76\u53D1"), /*#__PURE__*/React.createElement(TH, null, "\u5931\u8D25"), /*#__PURE__*/React.createElement(TH, null))), /*#__PURE__*/React.createElement(TBody, null, rows.map(a => {
    const hl = window.HK_HEALTH[a.health],
      sc = window.HK_SCHEDULE[a.schedule];
    return /*#__PURE__*/React.createElement(TR, {
      key: a.id
    }, /*#__PURE__*/React.createElement(TD, null, /*#__PURE__*/React.createElement("div", {
      style: {
        fontWeight: 500,
        color: "var(--text-strong)"
      }
    }, a.id), /*#__PURE__*/React.createElement("div", {
      style: {
        fontSize: 11,
        color: "var(--text-subtle)",
        marginTop: 2,
        fontFamily: "var(--font-mono)"
      }
    }, a.channel)), /*#__PURE__*/React.createElement(TD, {
      style: {
        color: "var(--text-muted)"
      }
    }, a.provider), /*#__PURE__*/React.createElement(TD, {
      style: {
        color: "var(--text-muted)"
      }
    }, a.models.join("、")), /*#__PURE__*/React.createElement(TD, null, /*#__PURE__*/React.createElement(Badge, {
      variant: hl.variant
    }, hl.label)), /*#__PURE__*/React.createElement(TD, null, /*#__PURE__*/React.createElement(Badge, {
      variant: sc.variant
    }, sc.label)), /*#__PURE__*/React.createElement(TD, {
      mono: true
    }, a.inFlight, "/", a.cap), /*#__PURE__*/React.createElement(TD, {
      mono: true,
      style: {
        color: a.fail > 0 ? "var(--danger-fg)" : "var(--text-muted)"
      }
    }, a.fail), /*#__PURE__*/React.createElement(TD, null, /*#__PURE__*/React.createElement(Button, {
      variant: "ghost",
      size: "sm"
    }, /*#__PURE__*/React.createElement(Icon, {
      name: "ellipsis"
    }))));
  }))))));
}
window.Accounts = Accounts;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/console/Accounts.jsx", error: String((e && e.message) || e) }); }

// ui_kits/console/Chat.jsx
try { (() => {
// Chat 调试器 — send a prompt through the gateway; fake streamed SSE response.
function Chat() {
  const {
    Card,
    CardContent,
    Button,
    Input,
    Label,
    Badge
  } = window.HUAKAIDesignSystem_36f9be;
  const [model, setModel] = React.useState("claude-sonnet-4.5");
  const [prompt, setPrompt] = React.useState("用一句话解释反向代理网关的作用。");
  const [output, setOutput] = React.useState("");
  const [streaming, setStreaming] = React.useState(false);
  const timer = React.useRef(null);
  const send = () => {
    if (streaming) return;
    const full = "反向代理网关（如 HUAKAI）位于客户端与多个上游 LLM 账号之间，统一协议入口、做健康感知的账号调度与限流重试，并在转发流式响应的同时完成用量与计费结算。";
    setOutput("");
    setStreaming(true);
    let i = 0;
    timer.current = setInterval(() => {
      i += 2;
      setOutput(full.slice(0, i));
      if (i >= full.length) {
        clearInterval(timer.current);
        setStreaming(false);
      }
    }, 24);
  };
  React.useEffect(() => () => clearInterval(timer.current), []);
  const models = ["claude-sonnet-4.5", "claude-opus-4.1", "gpt-5", "gemini-2.5-pro"];
  return /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement("div", {
    style: {
      marginBottom: 20
    }
  }, /*#__PURE__*/React.createElement("h1", {
    style: {
      margin: 0,
      fontSize: 24,
      fontWeight: 700,
      color: "var(--text-strong)"
    }
  }, "Chat \u8C03\u8BD5\u5668"), /*#__PURE__*/React.createElement("p", {
    style: {
      margin: "6px 0 0",
      fontSize: 14,
      color: "var(--text-muted)"
    }
  }, "POST /v1/messages \xB7 \u652F\u6301 SSE \u6D41\u5F0F")), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "grid",
      gridTemplateColumns: "minmax(0,1fr) minmax(0,1fr)",
      gap: 24
    }
  }, /*#__PURE__*/React.createElement(Card, null, /*#__PURE__*/React.createElement(CardContent, {
    style: {
      padding: 20,
      display: "flex",
      flexDirection: "column",
      gap: 16
    }
  }, /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement(Label, null, "\u6A21\u578B"), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 8,
      flexWrap: "wrap"
    }
  }, models.map(m => /*#__PURE__*/React.createElement("button", {
    key: m,
    onClick: () => setModel(m),
    style: {
      padding: "6px 12px",
      borderRadius: 6,
      fontSize: 12,
      fontFamily: "var(--font-mono)",
      cursor: "pointer",
      border: `1px solid ${model === m ? "var(--accent-soft-border)" : "var(--border)"}`,
      background: model === m ? "var(--accent-soft-bg)" : "var(--bg-surface-2)",
      color: model === m ? "var(--primary-300)" : "var(--text-muted)"
    }
  }, m)))), /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement(Label, null, "Prompt"), /*#__PURE__*/React.createElement("textarea", {
    value: prompt,
    onChange: e => setPrompt(e.target.value),
    rows: 5,
    style: {
      width: "100%",
      padding: "10px 12px",
      background: "var(--bg-surface-2)",
      color: "var(--text-body)",
      border: "1px solid var(--border-strong)",
      borderRadius: 6,
      fontFamily: "var(--font-sans)",
      fontSize: 14,
      resize: "vertical",
      outline: "none"
    }
  })), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 12
    }
  }, /*#__PURE__*/React.createElement(Button, {
    onClick: send,
    disabled: streaming
  }, /*#__PURE__*/React.createElement(Icon, {
    name: streaming ? "loader" : "send",
    style: {
      animation: streaming ? "hk-spin 0.8s linear infinite" : "none"
    }
  }), " ", streaming ? "流式中…" : "发送"), /*#__PURE__*/React.createElement("span", {
    style: {
      fontSize: 12,
      fontFamily: "var(--font-mono)",
      color: "var(--text-subtle)"
    }
  }, "hk_live_9fA2\u20267c")))), /*#__PURE__*/React.createElement(Card, null, /*#__PURE__*/React.createElement(CardContent, {
    style: {
      padding: 20
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      justifyContent: "space-between",
      marginBottom: 12
    }
  }, /*#__PURE__*/React.createElement(Label, {
    style: {
      margin: 0
    }
  }, "\u54CD\u5E94"), /*#__PURE__*/React.createElement(Badge, {
    variant: streaming ? "warning" : output ? "success" : "secondary"
  }, streaming ? "streaming" : output ? "200 OK" : "idle")), /*#__PURE__*/React.createElement("div", {
    style: {
      minHeight: 200,
      borderRadius: 8,
      border: "1px solid var(--border)",
      background: "var(--bg-surface-2)",
      padding: 14,
      fontSize: 14,
      lineHeight: 1.6,
      color: "var(--text-body)",
      whiteSpace: "pre-wrap"
    }
  }, output || /*#__PURE__*/React.createElement("span", {
    style: {
      color: "var(--text-subtle)"
    }
  }, "\u54CD\u5E94\u5C06\u5728\u6B64\u5904\u6D41\u5F0F\u663E\u793A\u2026"), streaming && /*#__PURE__*/React.createElement("span", {
    style: {
      color: "var(--primary-400)"
    }
  }, "\u258C"))))));
}
window.Chat = Chat;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/console/Chat.jsx", error: String((e && e.message) || e) }); }

// ui_kits/console/Dashboard.jsx
try { (() => {
function _extends() { return _extends = Object.assign ? Object.assign.bind() : function (n) { for (var e = 1; e < arguments.length; e++) { var t = arguments[e]; for (var r in t) ({}).hasOwnProperty.call(t, r) && (n[r] = t[r]); } return n; }, _extends.apply(null, arguments); }
// HUAKAI 运营总览 dashboard view — KPI grid, cache-hit trend, account table, alert panel.
function PageHeader({
  onRefresh,
  spinning
}) {
  const {
    Button
  } = window.HUAKAIDesignSystem_36f9be;
  return /*#__PURE__*/React.createElement("section", {
    style: {
      display: "flex",
      justifyContent: "space-between",
      alignItems: "center",
      gap: 16,
      flexWrap: "wrap",
      borderRadius: 8,
      border: "1px solid var(--border)",
      background: "var(--bg-surface)",
      padding: "16px 20px",
      boxShadow: "var(--shadow-card)",
      marginBottom: 24
    }
  }, /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 12,
      fontWeight: 500,
      color: "var(--primary-300)"
    }
  }, "P1 \u603B\u89C8"), /*#__PURE__*/React.createElement("h1", {
    style: {
      margin: "4px 0 0",
      fontSize: 24,
      fontWeight: 700,
      color: "var(--text-strong)"
    }
  }, "\u8FD0\u8425\u603B\u89C8"), /*#__PURE__*/React.createElement("p", {
    style: {
      margin: "8px 0 0",
      fontSize: 14,
      color: "var(--text-muted)"
    }
  }, "\u771F\u5B9E\u540E\u7AEF\u8D26\u53F7\u6C60\u5065\u5EB7\u3001\u6210\u672C\u3001\u7528\u91CF\u4E0E\u7F13\u5B58\u6548\u7387\u96C6\u4E2D\u89C6\u56FE")), /*#__PURE__*/React.createElement(Button, {
    variant: "outline",
    size: "sm",
    onClick: onRefresh
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "refresh-cw",
    style: {
      animation: spinning ? "hk-spin 0.8s linear infinite" : "none"
    }
  }), " \u5237\u65B0"));
}
function TrendPanel() {
  const {
    Card,
    CardHeader,
    CardTitle,
    CardContent
  } = window.HUAKAIDesignSystem_36f9be;
  const pts = [62, 58, 64, 71, 69, 74, 78, 82, 80, 85, 87, 84, 88, 86, 90];
  const w = 760,
    h = 180,
    max = 100;
  const step = w / (pts.length - 1);
  const line = pts.map((p, i) => `${i * step},${h - p / max * h}`).join(" ");
  const area = `0,${h} ${line} ${w},${h}`;
  return /*#__PURE__*/React.createElement(Card, {
    style: {
      marginBottom: 24
    }
  }, /*#__PURE__*/React.createElement(CardHeader, null, /*#__PURE__*/React.createElement(CardTitle, null, "24h \u7F13\u5B58\u547D\u4E2D\u7387\u8D8B\u52BF")), /*#__PURE__*/React.createElement(CardContent, null, /*#__PURE__*/React.createElement("svg", {
    viewBox: `0 0 ${w} ${h}`,
    preserveAspectRatio: "none",
    style: {
      width: "100%",
      height: 180,
      display: "block"
    }
  }, /*#__PURE__*/React.createElement("defs", null, /*#__PURE__*/React.createElement("linearGradient", {
    id: "hk-fill",
    x1: "0",
    y1: "0",
    x2: "0",
    y2: "1"
  }, /*#__PURE__*/React.createElement("stop", {
    offset: "0%",
    stopColor: "rgba(20,184,166,0.28)"
  }), /*#__PURE__*/React.createElement("stop", {
    offset: "100%",
    stopColor: "rgba(20,184,166,0)"
  }))), [0.25, 0.5, 0.75].map(g => /*#__PURE__*/React.createElement("line", {
    key: g,
    x1: "0",
    y1: h * g,
    x2: w,
    y2: h * g,
    stroke: "rgba(148,163,184,0.18)",
    strokeDasharray: "3 3"
  })), /*#__PURE__*/React.createElement("polygon", {
    points: area,
    fill: "url(#hk-fill)"
  }), /*#__PURE__*/React.createElement("polyline", {
    points: line,
    fill: "none",
    stroke: "#14b8a6",
    strokeWidth: "2.5"
  })), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      justifyContent: "space-between",
      marginTop: 8,
      fontSize: 11,
      fontFamily: "var(--font-mono)",
      color: "var(--text-subtle)"
    }
  }, /*#__PURE__*/React.createElement("span", null, "00:00"), /*#__PURE__*/React.createElement("span", null, "06:00"), /*#__PURE__*/React.createElement("span", null, "12:00"), /*#__PURE__*/React.createElement("span", null, "18:00"), /*#__PURE__*/React.createElement("span", null, "\u73B0\u5728"))));
}
function AccountTable() {
  const {
    Card,
    CardHeader,
    CardTitle,
    CardContent,
    Badge,
    Table,
    THead,
    TBody,
    TR,
    TH,
    TD
  } = window.HUAKAIDesignSystem_36f9be;
  return /*#__PURE__*/React.createElement(Card, null, /*#__PURE__*/React.createElement(CardHeader, null, /*#__PURE__*/React.createElement(CardTitle, null, /*#__PURE__*/React.createElement(Icon, {
    name: "layers",
    color: "var(--primary-400)"
  }), " Top 5 \u4F9B\u5E94\u5546\u8D26\u53F7")), /*#__PURE__*/React.createElement(CardContent, {
    style: {
      padding: "0 0 8px"
    }
  }, /*#__PURE__*/React.createElement(Table, null, /*#__PURE__*/React.createElement(THead, null, /*#__PURE__*/React.createElement(TR, {
    hover: false
  }, /*#__PURE__*/React.createElement(TH, null, "\u8D26\u53F7"), /*#__PURE__*/React.createElement(TH, null, "\u4F9B\u5E94\u5546"), /*#__PURE__*/React.createElement(TH, null, "\u6A21\u578B"), /*#__PURE__*/React.createElement(TH, null, "\u5065\u5EB7\u72B6\u6001"), /*#__PURE__*/React.createElement(TH, null, "\u5E76\u53D1"), /*#__PURE__*/React.createElement(TH, null, "\u8C03\u5EA6"))), /*#__PURE__*/React.createElement(TBody, null, window.HK_ACCOUNTS.map(a => {
    const hl = window.HK_HEALTH[a.health],
      sc = window.HK_SCHEDULE[a.schedule];
    return /*#__PURE__*/React.createElement(TR, {
      key: a.id
    }, /*#__PURE__*/React.createElement(TD, null, /*#__PURE__*/React.createElement("div", {
      style: {
        fontWeight: 500,
        color: "var(--text-strong)"
      }
    }, a.id), /*#__PURE__*/React.createElement("div", {
      style: {
        fontSize: 11,
        color: "var(--text-subtle)",
        marginTop: 2
      }
    }, a.channel)), /*#__PURE__*/React.createElement(TD, {
      style: {
        color: "var(--text-muted)"
      }
    }, a.provider), /*#__PURE__*/React.createElement(TD, {
      style: {
        color: "var(--text-muted)"
      }
    }, a.models[0], a.models.length > 1 ? ` +${a.models.length - 1}` : ""), /*#__PURE__*/React.createElement(TD, null, /*#__PURE__*/React.createElement(Badge, {
      variant: hl.variant
    }, hl.label)), /*#__PURE__*/React.createElement(TD, {
      mono: true
    }, a.inFlight, "/", a.cap), /*#__PURE__*/React.createElement(TD, null, /*#__PURE__*/React.createElement(Badge, {
      variant: sc.variant
    }, sc.label)));
  })))));
}
function HealthPanel() {
  const {
    Card,
    CardHeader,
    CardTitle,
    CardContent,
    Badge
  } = window.HUAKAIDesignSystem_36f9be;
  const total = window.HK_ACCOUNTS.length;
  const healthy = window.HK_ACCOUNTS.filter(a => a.health === "operational").length;
  const ratio = Math.round(healthy / total * 100);
  const risky = window.HK_ACCOUNTS.filter(a => a.health !== "operational");
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexDirection: "column",
      gap: 24
    }
  }, /*#__PURE__*/React.createElement(Card, null, /*#__PURE__*/React.createElement(CardHeader, null, /*#__PURE__*/React.createElement(CardTitle, null, /*#__PURE__*/React.createElement(Icon, {
    name: "shield-alert",
    color: "#fbbf24"
  }), " \u5F02\u5E38\u544A\u8B66\u6761\u4EF6")), /*#__PURE__*/React.createElement(CardContent, {
    style: {
      display: "flex",
      flexDirection: "column",
      gap: 12
    }
  }, risky.map(a => /*#__PURE__*/React.createElement("div", {
    key: a.id,
    style: {
      borderRadius: 8,
      border: "1px solid var(--border)",
      background: "var(--bg-surface-2)",
      padding: 12
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      justifyContent: "space-between",
      alignItems: "center",
      gap: 8
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      fontWeight: 500,
      color: "var(--text-strong)"
    }
  }, a.id), /*#__PURE__*/React.createElement(Badge, {
    variant: window.HK_HEALTH[a.health].variant
  }, window.HK_HEALTH[a.health].label)), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 8,
      fontSize: 12,
      color: "var(--text-muted)"
    }
  }, "\u5E76\u53D1 ", a.inFlight, "/", a.cap, " \xB7 \u5931\u8D25\u8BA1\u6570 ", a.fail))))), /*#__PURE__*/React.createElement(Card, null, /*#__PURE__*/React.createElement(CardHeader, null, /*#__PURE__*/React.createElement(CardTitle, null, /*#__PURE__*/React.createElement(Icon, {
    name: "heart-pulse",
    color: "var(--primary-400)"
  }), " \u5065\u5EB7\u8D26\u53F7\u6BD4\u4F8B")), /*#__PURE__*/React.createElement(CardContent, null, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      justifyContent: "space-between",
      alignItems: "flex-end"
    }
  }, /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 30,
      fontWeight: 700,
      color: "var(--text-strong)",
      fontVariantNumeric: "tabular-nums"
    }
  }, ratio, "%"), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 4,
      fontSize: 14,
      color: "var(--text-muted)"
    }
  }, healthy, " / ", total, " \u5065\u5EB7")), /*#__PURE__*/React.createElement(Badge, {
    variant: "secondary"
  }, "\u964D\u7EA7 1 \xB7 \u5931\u8D25 1")), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 16,
      height: 12,
      borderRadius: 999,
      background: "var(--bg-surface-2)",
      overflow: "hidden"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      height: "100%",
      width: `${ratio}%`,
      borderRadius: 999,
      background: "var(--primary-500)",
      boxShadow: "var(--shadow-glow)"
    }
  })))));
}
function Dashboard() {
  const {
    StatCard
  } = window.HUAKAIDesignSystem_36f9be;
  const [spinning, setSpinning] = React.useState(false);
  const refresh = () => {
    setSpinning(true);
    setTimeout(() => setSpinning(false), 900);
  };
  const stats = [{
    title: "今日 Token 用量",
    value: "1,284,500",
    icon: "database-zap",
    description: "输入、输出、缓存合计",
    detail: "输入 820,400 / 输出 464,100",
    tone: "primary"
  }, {
    title: "今日成本",
    value: "$38.42",
    icon: "dollar-sign",
    description: "usage.actual_cost 汇总",
    detail: "未做本地币种换算",
    tone: "emerald"
  }, {
    title: "请求数",
    value: "9,317",
    icon: "zap",
    description: "今日 usage 记录数",
    detail: "待对账 12 条",
    tone: "blue"
  }, {
    title: "P95 结算耗时",
    value: "1.24s",
    icon: "clock-3",
    description: "settled − requested",
    detail: "P50 0.42s / P99 2.10s",
    tone: "amber"
  }, {
    title: "并发数",
    value: "14",
    icon: "activity",
    description: "当前飞行中请求",
    detail: "容量上限 40",
    tone: "slate"
  }, {
    title: "缓存读取占比",
    value: "87.4%",
    icon: "gauge",
    description: "read / (creation + read)",
    detail: "读取 612k / 创建 88k",
    tone: "primary"
  }];
  return /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement(PageHeader, {
    onRefresh: refresh,
    spinning: spinning
  }), /*#__PURE__*/React.createElement("section", {
    style: {
      display: "grid",
      gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
      gap: 16,
      marginBottom: 24
    }
  }, stats.map(s => /*#__PURE__*/React.createElement(StatCard, _extends({
    key: s.title
  }, s, {
    icon: /*#__PURE__*/React.createElement(Icon, {
      name: s.icon
    })
  })))), /*#__PURE__*/React.createElement(TrendPanel, null), /*#__PURE__*/React.createElement("section", {
    style: {
      display: "grid",
      gridTemplateColumns: "minmax(0,2fr) minmax(300px,1fr)",
      gap: 24
    }
  }, /*#__PURE__*/React.createElement(AccountTable, null), /*#__PURE__*/React.createElement(HealthPanel, null)));
}
window.Dashboard = Dashboard;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/console/Dashboard.jsx", error: String((e && e.message) || e) }); }

// ui_kits/console/Shell.jsx
try { (() => {
// HUAKAI console shell — two-portal grouped sidebar (运营台 / 用户门户), sticky header, breadcrumb.

// One nav row. level: 0 = top leaf, 1 = group header, 2 = sub-item.
function NavRow({
  level = 0,
  active = false,
  bright = false,
  onClick,
  title,
  children
}) {
  const [h, setH] = React.useState(false);
  const sub = level === 2;
  return /*#__PURE__*/React.createElement("button", {
    title: title,
    onClick: onClick,
    onMouseEnter: () => setH(true),
    onMouseLeave: () => setH(false),
    style: {
      display: "flex",
      alignItems: "center",
      gap: sub ? 10 : 12,
      width: "100%",
      minHeight: sub ? 34 : 40,
      padding: sub ? "0 12px 0 38px" : "0 12px",
      border: "none",
      borderRadius: 8,
      cursor: "pointer",
      textAlign: "left",
      fontFamily: "var(--font-sans)",
      fontSize: sub ? 13 : 14,
      fontWeight: level === 1 ? 600 : 500,
      background: active ? "rgba(20,184,166,0.10)" : h ? "rgba(148,163,184,0.10)" : "transparent",
      color: active ? "var(--primary-300)" : bright ? "var(--text-strong)" : "var(--text-muted)",
      boxShadow: active ? "inset 0 0 0 1px var(--accent-soft-border)" : "none",
      transition: "background var(--dur-fast), color var(--dur-fast)"
    }
  }, children);
}
function PortalSwitch({
  portal,
  onPortal
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 4,
      margin: "0 12px",
      padding: 4,
      borderRadius: 8,
      background: "var(--bg-surface-2)",
      border: "1px solid var(--border)"
    }
  }, [["ops", "运营台"], ["user", "用户门户"]].map(([k, label]) => {
    const on = portal === k;
    return /*#__PURE__*/React.createElement("button", {
      key: k,
      onClick: () => !on && onPortal(k),
      style: {
        flex: 1,
        minHeight: 34,
        border: "none",
        borderRadius: 6,
        cursor: on ? "default" : "pointer",
        fontSize: 13,
        fontWeight: 600,
        fontFamily: "var(--font-sans)",
        background: on ? "var(--primary-500)" : "transparent",
        color: on ? "#fff" : "var(--text-muted)",
        boxShadow: on ? "var(--shadow-glow)" : "none",
        transition: "background var(--dur-fast), color var(--dur-fast)"
      }
    }, label);
  }));
}
function NavTree({
  portal,
  activeId,
  onSelect
}) {
  const nav = window.HK_NAV[portal];
  const activeGroup = activeId.split(":")[1];
  const [open, setOpen] = React.useState({});
  React.useEffect(() => {
    setOpen(o => ({
      ...o,
      [activeGroup]: true
    }));
  }, [activeGroup, portal]);
  return /*#__PURE__*/React.createElement("nav", {
    style: {
      flex: 1,
      minHeight: 0,
      overflowY: "auto",
      padding: "10px 12px 16px"
    }
  }, /*#__PURE__*/React.createElement("ul", {
    style: {
      listStyle: "none",
      margin: 0,
      padding: 0,
      display: "flex",
      flexDirection: "column",
      gap: 2
    }
  }, nav.groups.map(g => {
    const gid = portal + ":" + g.label;
    if (!g.items) {
      const on = activeId === gid;
      return /*#__PURE__*/React.createElement("li", {
        key: g.label
      }, /*#__PURE__*/React.createElement(NavRow, {
        level: 0,
        active: on,
        bright: on,
        onClick: () => onSelect(gid)
      }, /*#__PURE__*/React.createElement(Icon, {
        name: g.icon
      }), /*#__PURE__*/React.createElement("span", null, g.label)));
    }
    const expanded = !!open[g.label];
    const groupActive = activeGroup === g.label;
    return /*#__PURE__*/React.createElement("li", {
      key: g.label
    }, /*#__PURE__*/React.createElement(NavRow, {
      level: 1,
      bright: groupActive,
      onClick: () => setOpen(o => ({
        ...o,
        [g.label]: !o[g.label]
      }))
    }, /*#__PURE__*/React.createElement(Icon, {
      name: g.icon
    }), /*#__PURE__*/React.createElement("span", {
      style: {
        flex: 1
      }
    }, g.label), /*#__PURE__*/React.createElement(Icon, {
      name: "chevron-right",
      size: 14,
      style: {
        transform: expanded ? "rotate(90deg)" : "none",
        transition: "transform var(--dur-fast)",
        opacity: 0.7
      }
    })), expanded && /*#__PURE__*/React.createElement("ul", {
      style: {
        listStyle: "none",
        margin: "2px 0 4px",
        padding: 0,
        display: "flex",
        flexDirection: "column",
        gap: 2
      }
    }, g.items.map(it => {
      const iid = gid + ":" + it;
      const on = activeId === iid;
      return /*#__PURE__*/React.createElement("li", {
        key: it
      }, /*#__PURE__*/React.createElement(NavRow, {
        level: 2,
        active: on,
        bright: on,
        onClick: () => onSelect(iid)
      }, /*#__PURE__*/React.createElement("span", {
        style: {
          width: 6,
          height: 6,
          borderRadius: "50%",
          flexShrink: 0,
          background: on ? "var(--primary-400)" : "var(--neutral-700)",
          boxShadow: on ? "var(--shadow-glow)" : "none"
        }
      }), /*#__PURE__*/React.createElement("span", null, it)));
    })));
  })));
}
function Sidebar({
  portal,
  portalLabel,
  activeId,
  onSelect,
  onPortal
}) {
  return /*#__PURE__*/React.createElement("aside", {
    style: {
      position: "fixed",
      insetBlock: 0,
      left: 0,
      width: "var(--sidebar-w)",
      zIndex: 20,
      display: "flex",
      flexDirection: "column",
      background: "var(--bg-surface)",
      borderRight: "1px solid var(--border)",
      boxShadow: "var(--shadow-card)"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      height: "var(--header-h)",
      flexShrink: 0,
      display: "flex",
      alignItems: "center",
      gap: 12,
      padding: "0 14px",
      borderBottom: "1px solid var(--border)"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: 40,
      height: 40,
      flexShrink: 0,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      borderRadius: 10,
      background: "var(--primary-500)",
      color: "#fff",
      fontWeight: 700,
      fontSize: 14,
      boxShadow: "var(--shadow-glow)"
    }
  }, "HK"), /*#__PURE__*/React.createElement("span", {
    style: {
      minWidth: 0
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      display: "block",
      fontSize: 16,
      fontWeight: 700,
      color: "var(--text-strong)"
    }
  }, "HUAKAI"), /*#__PURE__*/React.createElement("span", {
    style: {
      display: "block",
      fontSize: 12,
      color: "var(--text-muted)"
    }
  }, portalLabel))), /*#__PURE__*/React.createElement("div", {
    style: {
      padding: "12px 0",
      flexShrink: 0
    }
  }, /*#__PURE__*/React.createElement(PortalSwitch, {
    portal: portal,
    onPortal: onPortal
  })), /*#__PURE__*/React.createElement(NavTree, {
    portal: portal,
    activeId: activeId,
    onSelect: onSelect
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      borderTop: "1px solid var(--border)",
      padding: 12,
      flexShrink: 0
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 12,
      borderRadius: 8,
      background: "var(--bg-surface-2)",
      padding: 12
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "shield-check",
    color: "var(--primary-300)"
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      minWidth: 0
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 12,
      fontWeight: 600,
      color: "var(--text-strong)"
    }
  }, portalLabel), /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 11,
      color: "var(--text-muted)",
      display: "flex",
      alignItems: "center",
      gap: 6
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "activity",
    size: 12
  }), " \u672C\u5730\u5F00\u53D1 \xB7 v0.1.0")))));
}
function Header({
  loc
}) {
  const {
    StatusDot
  } = window.HUAKAIDesignSystem_36f9be;
  const crumbs = [loc.portalLabel, loc.groupLabel, loc.itemLabel].filter(Boolean);
  return /*#__PURE__*/React.createElement("header", {
    style: {
      position: "sticky",
      top: 0,
      zIndex: 10,
      minHeight: "var(--header-h)",
      display: "flex",
      alignItems: "center",
      justifyContent: "space-between",
      gap: 12,
      padding: "0 28px",
      borderBottom: "1px solid var(--border)",
      background: "rgba(255,255,255,0.85)",
      backdropFilter: "blur(16px)"
    }
  }, /*#__PURE__*/React.createElement("nav", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 8,
      fontSize: 13,
      minWidth: 0
    }
  }, crumbs.map((c, i) => {
    const last = i === crumbs.length - 1;
    return /*#__PURE__*/React.createElement(React.Fragment, {
      key: i
    }, i > 0 && /*#__PURE__*/React.createElement(Icon, {
      name: "chevron-right",
      size: 14,
      color: "var(--text-subtle)"
    }), /*#__PURE__*/React.createElement("span", {
      style: {
        color: last ? "var(--text-strong)" : "var(--text-muted)",
        fontWeight: last ? 600 : 500,
        whiteSpace: "nowrap"
      }
    }, c));
  })), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 12,
      flexShrink: 0
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 8,
      borderRadius: 8,
      border: "1px solid var(--success-border)",
      background: "var(--success-bg)",
      padding: "8px 12px",
      fontSize: 12,
      fontWeight: 500,
      color: "var(--success-fg)"
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "online",
    pulse: true
  }), " ", /*#__PURE__*/React.createElement("span", null, "\u540E\u7AEF\u5FC3\u8DF3"), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)"
    }
  }, "42ms")), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 8,
      borderRadius: 8,
      border: "1px solid var(--border)",
      background: "var(--bg-surface-2)",
      padding: "7px 10px",
      fontSize: 12,
      color: "var(--text-muted)"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: 20,
      height: 20,
      borderRadius: "50%",
      background: "var(--primary-500)",
      color: "#fff",
      fontSize: 10,
      fontWeight: 700,
      display: "flex",
      alignItems: "center",
      justifyContent: "center"
    }
  }, "H"), /*#__PURE__*/React.createElement("span", null, "\u7BA1\u7406\u5458"))));
}
function Shell({
  portal,
  portalLabel,
  activeId,
  onSelect,
  onPortal,
  loc,
  children
}) {
  return /*#__PURE__*/React.createElement("div", {
    style: {
      minHeight: "100vh",
      background: "var(--bg-app)",
      color: "var(--text-body)"
    }
  }, /*#__PURE__*/React.createElement(Sidebar, {
    portal: portal,
    portalLabel: portalLabel,
    activeId: activeId,
    onSelect: onSelect,
    onPortal: onPortal
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      minHeight: "100vh",
      display: "flex",
      flexDirection: "column",
      paddingLeft: "var(--sidebar-w)"
    }
  }, /*#__PURE__*/React.createElement(Header, {
    loc: loc
  }), /*#__PURE__*/React.createElement("main", {
    style: {
      flex: 1,
      padding: "28px"
    }
  }, children)));
}

// Generic on-brand placeholder for nodes without a built view.
function Placeholder({
  loc
}) {
  const {
    Card,
    CardContent,
    Badge,
    Button
  } = window.HUAKAIDesignSystem_36f9be;
  const title = loc.itemLabel || loc.groupLabel;
  return /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement("section", {
    style: {
      display: "flex",
      justifyContent: "space-between",
      alignItems: "center",
      gap: 16,
      flexWrap: "wrap",
      marginBottom: 24
    }
  }, /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 12,
      fontWeight: 500,
      color: "var(--primary-300)"
    }
  }, loc.portalLabel, " \xB7 ", loc.groupLabel), /*#__PURE__*/React.createElement("h1", {
    style: {
      margin: "4px 0 0",
      fontSize: 24,
      fontWeight: 700,
      color: "var(--text-strong)",
      display: "flex",
      alignItems: "center",
      gap: 10
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: loc.groupIcon,
    size: 22,
    color: "var(--text-muted)"
  }), " ", title)), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      gap: 10
    }
  }, /*#__PURE__*/React.createElement(Button, {
    variant: "outline",
    size: "sm",
    disabled: true
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "refresh-cw"
  }), " \u5237\u65B0"), /*#__PURE__*/React.createElement(Button, {
    size: "sm",
    disabled: true
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "plus"
  }), " \u65B0\u5EFA"))), /*#__PURE__*/React.createElement(Card, null, /*#__PURE__*/React.createElement(CardContent, {
    style: {
      padding: "56px 40px",
      textAlign: "center"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      width: 52,
      height: 52,
      margin: "0 auto",
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      borderRadius: 12,
      background: "var(--bg-surface-2)",
      border: "1px solid var(--border)"
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: loc.groupIcon,
    size: 24,
    color: "var(--text-subtle)"
  })), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 16,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      gap: 8
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      fontSize: 16,
      fontWeight: 600,
      color: "var(--text-body)"
    }
  }, title), /*#__PURE__*/React.createElement(Badge, {
    variant: "secondary",
    style: {
      background: "rgba(139,92,246,0.14)",
      color: "#a78bfa",
      borderColor: "rgba(139,92,246,0.32)"
    }
  }, "\u5360\u4F4D")), /*#__PURE__*/React.createElement("p", {
    style: {
      margin: "10px auto 0",
      maxWidth: 420,
      fontSize: 13.5,
      lineHeight: 1.6,
      color: "var(--text-subtle)"
    }
  }, "\u8BE5\u89C6\u56FE\u4E3A\u5BFC\u822A\u5E03\u5C40\u5360\u4F4D \u2014 \u9875\u9762\u4E0E\u540E\u7AEF\u63A5\u53E3\u5F85\u63A5\u5165\u3002\u4E0D\u4F1A\u7528\u672C\u5730\u5047\u6570\u636E\u8865\u9F50\u3002"))));
}
window.Shell = Shell;
window.Placeholder = Placeholder;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/console/Shell.jsx", error: String((e && e.message) || e) }); }

// ui_kits/console/data.js
try { (() => {
// Mock data for the HUAKAI console UI kit (no real backend).
window.HK_ACCOUNTS = [{
  id: "claude-pool-01",
  channel: "anthropic / oauth",
  provider: "Anthropic",
  models: ["claude-sonnet-4.5", "claude-opus-4.1"],
  health: "operational",
  schedule: "active",
  inFlight: 3,
  cap: 10,
  fail: 0
}, {
  id: "gpt-team-a",
  channel: "openai / api-key",
  provider: "OpenAI",
  models: ["gpt-5", "gpt-4.1-mini"],
  health: "cooling_down",
  schedule: "limited",
  inFlight: 8,
  cap: 8,
  fail: 2
}, {
  id: "vertex-eu-1",
  channel: "google / vertex",
  provider: "Google Vertex",
  models: ["gemini-2.5-pro"],
  health: "degraded",
  schedule: "active",
  inFlight: 1,
  cap: 6,
  fail: 1
}, {
  id: "bedrock-us",
  channel: "aws / bedrock",
  provider: "AWS Bedrock",
  models: ["claude-sonnet-4.5"],
  health: "operational",
  schedule: "active",
  inFlight: 2,
  cap: 12,
  fail: 0
}, {
  id: "router-or-1",
  channel: "openrouter / key",
  provider: "OpenRouter",
  models: ["mixed"],
  health: "failed",
  schedule: "requires_action",
  inFlight: 0,
  cap: 4,
  fail: 9
}];
window.HK_HEALTH = {
  operational: {
    label: "健康",
    variant: "success"
  },
  degraded: {
    label: "降级",
    variant: "secondary"
  },
  cooling_down: {
    label: "冷却中",
    variant: "warning"
  },
  failed: {
    label: "失败",
    variant: "destructive"
  }
};
window.HK_SCHEDULE = {
  active: {
    label: "可调度",
    variant: "outline"
  },
  limited: {
    label: "受限",
    variant: "secondary"
  },
  requires_action: {
    label: "需处理",
    variant: "destructive"
  }
};
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/console/data.js", error: String((e && e.message) || e) }); }

// ui_kits/console/icons.jsx
try { (() => {
// Lucide icon helper for the UI kit. Renders a 16px (or sized) stroke icon.
function Icon({
  name,
  size = 16,
  color,
  style
}) {
  const ref = React.useRef(null);
  React.useEffect(() => {
    if (ref.current && window.lucide) {
      ref.current.innerHTML = "";
      const el = document.createElement("i");
      el.setAttribute("data-lucide", name);
      ref.current.appendChild(el);
      window.lucide.createIcons({
        attrs: {
          width: size,
          height: size,
          stroke: color || "currentColor",
          "stroke-width": 2
        }
      });
    }
  }, [name, size, color]);
  return /*#__PURE__*/React.createElement("span", {
    ref: ref,
    style: {
      display: "inline-flex",
      lineHeight: 0,
      ...style
    }
  });
}
window.Icon = Icon;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/console/icons.jsx", error: String((e && e.message) || e) }); }

// ui_kits/console/nav.js
try { (() => {
// HUAKAI internal navigation IA — two portals, grouped.
// id scheme: top-level leaf = "<portal>:<group>"; sub-item = "<portal>:<group>:<item>".
window.HK_NAV = {
  ops: {
    label: "运营台",
    home: "运营总览",
    groups: [{
      label: "运营总览",
      icon: "layout-dashboard"
    }, {
      label: "账号池",
      icon: "database",
      items: ["上游账号", "账号健康", "出口代理", "TLS 指纹"]
    }, {
      label: "路由与分组",
      icon: "route",
      items: ["路由规则", "分组", "池绑定", "路由测试"]
    }, {
      label: "用户与租户",
      icon: "users",
      items: ["用户列表", "用户详情", "余额记录", "安全状态", "社交绑定"]
    }, {
      label: "模型与定价",
      icon: "boxes",
      items: ["模型列表", "模型注册", "上游模型同步", "定价规则", "缓存价格覆盖"]
    }, {
      label: "计费运营",
      icon: "receipt",
      items: ["订单", "订阅", "兑换码", "分销", "支付争议", "账单导出"]
    }, {
      label: "用量分析",
      icon: "bar-chart-3",
      items: ["请求明细", "排行榜", "性能指标", "健康评分", "成本分析"]
    }, {
      label: "内容运营",
      icon: "megaphone",
      items: ["公告", "站内信", "内容审核"]
    }, {
      label: "风控",
      icon: "shield-alert",
      items: ["风控总览", "风险事件", "拦截规则"]
    }, {
      label: "监控告警",
      icon: "bell",
      items: ["系统健康", "运维面板", "告警规则", "告警事件", "静默规则", "死信队列"]
    }, {
      label: "审计",
      icon: "file-check-2",
      items: ["审计事件", "用户活动", "凭据审计"]
    }, {
      label: "系统维护",
      icon: "settings",
      items: ["平台设置", "备份", "版本", "日志诊断"]
    }]
  },
  user: {
    label: "用户门户",
    home: "概览",
    groups: [{
      label: "概览",
      icon: "layout-dashboard"
    }, {
      label: "API Key",
      icon: "key-round",
      items: ["Key 列表", "Playground"]
    }, {
      label: "用量",
      icon: "bar-chart-3",
      items: ["用量概览", "请求明细", "媒体任务"]
    }, {
      label: "模型与渠道",
      icon: "layers",
      items: ["可用渠道", "我的分组"]
    }, {
      label: "钱包与订单",
      icon: "wallet",
      items: ["钱包", "订单", "订阅", "兑换", "签到"]
    }, {
      label: "邀请返利",
      icon: "gift",
      items: ["邀请概览", "返利记录"]
    }, {
      label: "账户",
      icon: "user-round",
      items: ["个人资料", "通知", "活动日志"]
    }]
  }
};

// Resolve an id to { portalLabel, groupLabel, groupIcon, itemLabel }.
window.HK_NAV_FIND = function (id) {
  const [portal, groupLabel, itemLabel] = id.split(":");
  const p = window.HK_NAV[portal];
  const g = p && p.groups.find(x => x.label === groupLabel);
  return {
    portal,
    portalLabel: p ? p.label : portal,
    groupLabel,
    groupIcon: g ? g.icon : "circle",
    itemLabel // undefined for top-level leaves
  };
};
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/console/nav.js", error: String((e && e.message) || e) }); }

// ui_kits/website/CTA.jsx
try { (() => {
// HUAKAI site — self-host CTA band.
function SiteDeploy() {
  const {
    Button
  } = window.HUAKAIDesignSystem_36f9be;
  return /*#__PURE__*/React.createElement("section", {
    id: "deploy",
    className: "hk-section"
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-container"
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      position: "relative",
      overflow: "hidden",
      borderRadius: 16,
      border: "1px solid var(--border-strong)",
      background: "var(--bg-surface)",
      boxShadow: "var(--shadow-card)",
      padding: "clamp(32px, 5vw, 56px)"
    }
  }, /*#__PURE__*/React.createElement("div", {
    "aria-hidden": "true",
    style: {
      position: "absolute",
      inset: 0,
      background: "radial-gradient(120% 140% at 85% 0%, rgba(200,242,74,0.16), transparent 55%)",
      pointerEvents: "none"
    }
  }), /*#__PURE__*/React.createElement("div", {
    style: {
      position: "relative",
      display: "flex",
      flexWrap: "wrap",
      alignItems: "center",
      justifyContent: "space-between",
      gap: 32
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      maxWidth: 520
    }
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-eyebrow"
  }, "\u81EA\u6258\u7BA1 \xB7 self-host"), /*#__PURE__*/React.createElement("h2", {
    className: "hk-h2",
    style: {
      marginTop: 8
    }
  }, "\u628A\u8D26\u53F7\u6C60\u63A5\u8FDB\u6765\uFF0C\u51E0\u5206\u949F\u8D77\u670D\u52A1"), /*#__PURE__*/React.createElement("p", {
    className: "hk-lead",
    style: {
      marginBottom: 0
    }
  }, "\u514B\u9686\u4ED3\u5E93\uFF0C\u914D\u7F6E\u4E0A\u6E38\u8D26\u53F7\uFF0C\u5355\u673A docker compose \u5373\u53EF\u62C9\u8D77\u7F51\u5173\u4E0E\u63A7\u5236\u53F0\u3002\u79C1\u94A5\u4E0E\u6D41\u91CF\u90FD\u7559\u5728\u4F60\u81EA\u5DF1\u7684\u673A\u5668\u4E0A\u3002")), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexDirection: "column",
      gap: 14,
      minWidth: 280,
      flex: 1
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 10,
      padding: "14px 16px",
      borderRadius: "var(--radius)",
      border: "1px solid var(--border)",
      background: "var(--neutral-950)",
      fontFamily: "var(--font-mono)",
      fontSize: 13,
      color: "var(--neutral-300)"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      color: "var(--text-subtle)"
    }
  }, "$"), /*#__PURE__*/React.createElement("span", {
    style: {
      flex: 1
    }
  }, "git clone \u2026/HUAKAI && docker compose up -d"), /*#__PURE__*/React.createElement(Icon, {
    name: "copy",
    size: 15,
    color: "var(--text-subtle)"
  })), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexWrap: "wrap",
      gap: 12
    }
  }, /*#__PURE__*/React.createElement(Button, {
    size: "lg",
    onClick: () => {
      window.location.href = "../console/index.html";
    }
  }, "\u8FDB\u5165\u63A7\u5236\u53F0 ", /*#__PURE__*/React.createElement(Icon, {
    name: "arrow-right"
  })), /*#__PURE__*/React.createElement("a", {
    href: "https://github.com/BloomingProsperity/HUAKAI",
    target: "_blank",
    rel: "noreferrer",
    className: "hk-outline-btn",
    style: {
      display: "inline-flex",
      alignItems: "center",
      gap: 8,
      height: "2.75rem",
      padding: "0 1.5rem",
      borderRadius: "var(--radius-md)",
      border: "1px solid var(--border-strong)",
      background: "transparent",
      color: "var(--text-body)",
      fontSize: 16,
      fontWeight: 500,
      textDecoration: "none"
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "book-open"
  }), " \u9605\u8BFB\u6587\u6863")))))));
}
window.SiteDeploy = SiteDeploy;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/website/CTA.jsx", error: String((e && e.message) || e) }); }

// ui_kits/website/Features.jsx
try { (() => {
// HUAKAI site — capability feature grid (6 cards).

const HK_TONES = {
  teal: {
    fg: "var(--primary-300)",
    bg: "rgba(200,242,74,0.12)",
    bd: "rgba(200,242,74,0.30)"
  },
  emerald: {
    fg: "#34d399",
    bg: "rgba(16,185,129,0.12)",
    bd: "rgba(16,185,129,0.30)"
  },
  amber: {
    fg: "#fbbf24",
    bg: "rgba(245,158,11,0.12)",
    bd: "rgba(245,158,11,0.30)"
  },
  blue: {
    fg: "#60a5fa",
    bg: "rgba(59,130,246,0.12)",
    bd: "rgba(59,130,246,0.30)"
  },
  violet: {
    fg: "#a78bfa",
    bg: "rgba(139,92,246,0.12)",
    bd: "rgba(139,92,246,0.30)"
  },
  slate: {
    fg: "var(--neutral-300)",
    bg: "var(--bg-surface-2)",
    bd: "var(--border)"
  }
};
const HK_FEATURES = [{
  icon: "git-merge",
  tone: "teal",
  title: "统一协议接口",
  desc: "POST /v1/chat/completions 与 Anthropic messages 双协议，单一入口，原生支持 SSE 流式。",
  foot: {
    type: "code",
    text: "OpenAI · Anthropic"
  }
}, {
  icon: "heart-pulse",
  tone: "emerald",
  title: "健康感知调度",
  desc: "按 operational / cooling_down / degraded 状态与并发上限路由，自动绕开失败账号。",
  foot: {
    type: "code",
    text: "并发 3/10 · 自动避障"
  }
}, {
  icon: "refresh-cw",
  tone: "amber",
  title: "限流与重试",
  desc: "rate-limit 感知退避与跨账号重试；触发上限的账号进入 cooling_down 并自动恢复。",
  foot: {
    type: "code",
    text: "退避 · 跨账号重试"
  }
}, {
  icon: "bar-chart-3",
  tone: "blue",
  title: "用量与计费核算",
  desc: "按 usage.actual_cost 汇总 token、成本与缓存命中率，tabular 精确，可对账。",
  foot: {
    type: "code",
    text: "$38.42 · 1,284,500 tokens"
  }
}, {
  icon: "file-check-2",
  tone: "violet",
  title: "审计链路",
  desc: "请求 hop-chain 与 Merkle 校验接口已预留，可逐跳追溯。后端实现进行中。",
  foot: {
    type: "badge",
    variant: "default",
    text: "MOCK"
  }
}, {
  icon: "server",
  tone: "slate",
  title: "自托管",
  desc: "Go 后端 + Next.js 控制台，单机 docker compose 起。私钥不出本地，MIT 许可。",
  foot: {
    type: "code",
    text: "Go · Next.js · MIT"
  }
}];
function FeatureCard({
  f
}) {
  const {
    Badge
  } = window.HUAKAIDesignSystem_36f9be;
  const t = HK_TONES[f.tone];
  const [h, setH] = React.useState(false);
  return /*#__PURE__*/React.createElement("div", {
    onMouseEnter: () => setH(true),
    onMouseLeave: () => setH(false),
    style: {
      display: "flex",
      flexDirection: "column",
      padding: 22,
      borderRadius: "var(--radius)",
      border: `1px solid ${h ? "var(--border-strong)" : "var(--border)"}`,
      background: "var(--bg-surface)",
      boxShadow: h ? "var(--shadow-card-hover)" : "var(--shadow-card)",
      transform: h ? "translateY(-2px)" : "none",
      transition: "transform var(--dur-base) var(--ease-out), border-color var(--dur-base), box-shadow var(--dur-base)"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: 40,
      height: 40,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      borderRadius: 10,
      background: t.bg,
      border: `1px solid ${t.bd}`,
      color: t.fg
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: f.icon,
    size: 19,
    color: t.fg
  })), /*#__PURE__*/React.createElement("h3", {
    style: {
      margin: "16px 0 0",
      fontSize: 16.5,
      fontWeight: 600,
      color: "var(--text-strong)"
    }
  }, f.title), /*#__PURE__*/React.createElement("p", {
    style: {
      margin: "8px 0 0",
      fontSize: 14,
      lineHeight: 1.6,
      color: "var(--text-muted)",
      flex: 1
    }
  }, f.desc), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 16
    }
  }, f.foot.type === "badge" ? /*#__PURE__*/React.createElement(Badge, {
    variant: f.foot.variant,
    style: {
      background: HK_TONES.violet.bg,
      color: HK_TONES.violet.fg,
      borderColor: HK_TONES.violet.bd
    }
  }, f.foot.text) : /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 12,
      color: "var(--text-subtle)"
    }
  }, f.foot.text)));
}
function SiteFeatures() {
  return /*#__PURE__*/React.createElement("section", {
    id: "features",
    className: "hk-section"
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-container"
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      maxWidth: 720
    }
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-eyebrow"
  }, "\u80FD\u529B \xB7 capabilities"), /*#__PURE__*/React.createElement("h2", {
    className: "hk-h2"
  }, "\u7F51\u5173\u8BE5\u505A\u7684\uFF0C\u5B83\u90FD\u5728\u4E00\u5904\u505A"), /*#__PURE__*/React.createElement("p", {
    className: "hk-lead"
  }, "\u4ECE\u534F\u8BAE\u9002\u914D\u5230\u8C03\u5EA6\u3001\u9650\u6D41\u3001\u8BA1\u8D39\u4E0E\u5BA1\u8BA1 \u2014 \u4E00\u4E2A\u8FDB\u7A0B\u6321\u5728\u4F60\u7684\u8D26\u53F7\u6C60\u524D\u9762\uFF0C\u628A\u8FD0\u7EF4\u6536\u53E3\u5230\u4E00\u5757\u9762\u677F\u3002")), /*#__PURE__*/React.createElement("div", {
    className: "hk-feature-grid",
    style: {
      marginTop: 34
    }
  }, HK_FEATURES.map(f => /*#__PURE__*/React.createElement(FeatureCard, {
    key: f.title,
    f: f
  })))));
}
window.SiteFeatures = SiteFeatures;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/website/Features.jsx", error: String((e && e.message) || e) }); }

// ui_kits/website/Footer.jsx
try { (() => {
// HUAKAI site — footer.
function SiteFooter() {
  const {
    StatusDot
  } = window.HUAKAIDesignSystem_36f9be;
  const cols = [{
    h: "产品",
    links: ["运营总览", "账号池", "Chat 调试器", "审计"]
  }, {
    h: "资源",
    links: ["文档", "GitHub", "更新日志", "协议适配"]
  }, {
    h: "关于",
    links: ["架构", "安全与自托管", "许可 · MIT"]
  }];
  return /*#__PURE__*/React.createElement("footer", {
    style: {
      borderTop: "1px solid var(--border)",
      background: "var(--bg-surface)",
      marginTop: 24
    }
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-container",
    style: {
      paddingTop: 48,
      paddingBottom: 40
    }
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-footer-grid"
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      maxWidth: 320
    }
  }, /*#__PURE__*/React.createElement(HKLogo, {
    sub: "\u534E\u51EF"
  }), /*#__PURE__*/React.createElement("p", {
    style: {
      margin: "16px 0 0",
      fontSize: 13,
      lineHeight: 1.6,
      color: "var(--text-subtle)"
    }
  }, "self-hosted AI gateway \xB7 account hub \xB7 admin ops. \u6321\u5728\u4F60\u81EA\u6709\u4E0A\u6E38\u8D26\u53F7\u524D\u7684\u7EDF\u4E00\u534F\u8BAE\u4E0E\u8C03\u5EA6\u5C42\u3002")), cols.map(c => /*#__PURE__*/React.createElement("div", {
    key: c.h
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 12,
      fontWeight: 600,
      textTransform: "uppercase",
      letterSpacing: "0.04em",
      color: "var(--text-subtle)"
    }
  }, c.h), /*#__PURE__*/React.createElement("ul", {
    style: {
      listStyle: "none",
      margin: "14px 0 0",
      padding: 0,
      display: "flex",
      flexDirection: "column",
      gap: 10
    }
  }, c.links.map(l => /*#__PURE__*/React.createElement("li", {
    key: l
  }, /*#__PURE__*/React.createElement("a", {
    href: "#",
    className: "hk-foot-link",
    style: {
      fontSize: 13.5,
      color: "var(--text-muted)",
      textDecoration: "none",
      transition: "color var(--dur-fast)"
    }
  }, l))))))), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 40,
      paddingTop: 20,
      borderTop: "1px solid var(--border)",
      display: "flex",
      flexWrap: "wrap",
      alignItems: "center",
      justifyContent: "space-between",
      gap: 12
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      fontSize: 12.5,
      color: "var(--text-subtle)",
      fontFamily: "var(--font-mono)"
    }
  }, "\xA9 2026 HUAKAI \u534E\u51EF \xB7 v0.1.0 \xB7 MIT"), /*#__PURE__*/React.createElement("span", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 8,
      fontSize: 12.5,
      color: "var(--text-muted)"
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "online",
    pulse: true
  }), " \u540E\u7AEF\u5FC3\u8DF3 \xB7 ", /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)"
    }
  }, "\u672C\u5730\u5F00\u53D1")))));
}
window.SiteFooter = SiteFooter;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/website/Footer.jsx", error: String((e && e.message) || e) }); }

// ui_kits/website/Hero.jsx
try { (() => {
// HUAKAI site — hero. Headline + request terminal + dispatch card + trust stats.

function CodeTerminal() {
  const {
    Badge
  } = window.HUAKAIDesignSystem_36f9be;
  const C = {
    mut: "#94a3b8",
    key: "#7dd3fc",
    str: "#e7c08a",
    method: "#bfe14a",
    txt: "#cbd5e1"
  };
  return /*#__PURE__*/React.createElement("div", {
    className: "hk-rise",
    style: {
      borderRadius: 12,
      border: "1px solid var(--border)",
      background: "var(--neutral-900)",
      boxShadow: "var(--shadow-card-hover)",
      overflow: "hidden"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 10,
      padding: "12px 16px",
      borderBottom: "1px solid var(--border)",
      background: "rgba(15,23,42,0.6)"
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "terminal",
    size: 15,
    color: "#bfe14a"
  }), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 12.5,
      color: "#cbd5e1"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.method
    }
  }, "POST"), " /v1/chat/completions"), /*#__PURE__*/React.createElement("span", {
    style: {
      marginLeft: "auto"
    }
  }, /*#__PURE__*/React.createElement(Badge, {
    variant: "outline",
    style: {
      fontSize: 11,
      color: "#cbd5e1",
      borderColor: "#3a3f4a",
      background: "transparent"
    }
  }, "SSE"))), /*#__PURE__*/React.createElement("pre", {
    style: {
      margin: 0,
      padding: "18px 18px 20px",
      fontFamily: "var(--font-mono)",
      fontSize: 12.5,
      lineHeight: 1.7,
      color: C.txt,
      whiteSpace: "pre-wrap",
      wordBreak: "break-word"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.mut
    }
  }, "$ "), "curl -N https://gw.local/v1/chat/completions \\", "\n", "  ", "-H ", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.str
    }
  }, "\"authorization: Bearer hk_live_\u2026\""), " \\", "\n", "  ", "-d ", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.str
    }
  }, "'", "{", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.key
    }
  }, "\"model\""), ":\"claude-sonnet-4.5\",", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.key
    }
  }, "\"stream\""), ":true", "}", "'"), "\n\n", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.mut
    }
  }, "\u2190 200 \xB7 "), /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.method
    }
  }, "text/event-stream"), "\n", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.mut
    }
  }, "data: "), "{", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.key
    }
  }, "\"delta\""), ":", "{", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.key
    }
  }, "\"content\""), ":", /*#__PURE__*/React.createElement("span", {
    style: {
      color: C.str
    }
  }, "\"pong\""), "}}", /*#__PURE__*/React.createElement("span", {
    className: "hk-cursor",
    style: {
      background: "#bfe14a"
    }
  })));
}
function DispatchCard() {
  const {
    StatusDot
  } = window.HUAKAIDesignSystem_36f9be;
  return /*#__PURE__*/React.createElement("div", {
    className: "hk-float",
    style: {
      position: "absolute",
      right: -18,
      bottom: -22,
      width: 248,
      borderRadius: 10,
      border: "1px solid var(--border-strong)",
      background: "var(--bg-surface)",
      boxShadow: "0 16px 48px rgba(13,42,72,0.18)",
      padding: 14
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 8,
      fontSize: 11,
      color: "var(--text-subtle)",
      textTransform: "uppercase",
      letterSpacing: "0.04em",
      fontWeight: 600
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "git-merge",
    size: 13,
    color: "var(--primary-400)"
  }), " dispatch"), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 8,
      fontFamily: "var(--font-mono)",
      fontSize: 13,
      fontWeight: 600,
      color: "var(--text-strong)"
    }
  }, "claude-pool-01"), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 3,
      fontFamily: "var(--font-mono)",
      fontSize: 11.5,
      color: "var(--text-muted)"
    }
  }, "anthropic / oauth"), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 12,
      display: "flex",
      alignItems: "center",
      justifyContent: "space-between"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 7,
      fontSize: 12,
      color: "var(--success-fg)"
    }
  }, /*#__PURE__*/React.createElement(StatusDot, {
    tone: "online",
    pulse: true
  }), " operational"), /*#__PURE__*/React.createElement("span", {
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 11.5,
      color: "var(--text-muted)"
    }
  }, "3/10 \xB7 42ms")));
}
function SiteHero() {
  const {
    Button
  } = window.HUAKAIDesignSystem_36f9be;
  const stats = [{
    v: "5",
    l: "已支持上游供应商"
  }, {
    v: "2",
    l: "协议接口 OpenAI · Anthropic"
  }, {
    v: "100%",
    l: "自托管 · 私钥不出本地"
  }];
  return /*#__PURE__*/React.createElement("section", {
    id: "top",
    className: "hk-hero"
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-container hk-hero-grid"
  }, /*#__PURE__*/React.createElement("div", null, /*#__PURE__*/React.createElement("div", {
    className: "hk-rise",
    style: {
      display: "inline-flex",
      alignItems: "center",
      gap: 9,
      padding: "5px 12px 5px 10px",
      borderRadius: "var(--radius-full)",
      border: "1px solid var(--accent-soft-border)",
      background: "var(--accent-soft-bg)",
      fontSize: 12.5,
      fontWeight: 500,
      color: "var(--accent-soft-text)"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: 7,
      height: 7,
      borderRadius: "50%",
      background: "var(--primary-400)",
      boxShadow: "var(--shadow-glow)"
    }
  }), "\u81EA\u6258\u7BA1 \xB7 operator-side AI Gateway"), /*#__PURE__*/React.createElement("h1", {
    className: "hk-h1 hk-rise",
    style: {
      margin: "22px 0 0",
      color: "var(--text-strong)",
      fontWeight: 700,
      letterSpacing: "-0.025em",
      lineHeight: 1.08
    }
  }, "\u7EDF\u4E00\u534F\u8BAE\u63A5\u53E3\uFF0C", /*#__PURE__*/React.createElement("br", null), "\u8C03\u5EA6\u4F60\u81EA\u5DF1\u7684", /*#__PURE__*/React.createElement("br", null), /*#__PURE__*/React.createElement("span", {
    style: {
      color: "var(--primary-400)"
    }
  }, "LLM \u8D26\u53F7\u6C60")), /*#__PURE__*/React.createElement("p", {
    className: "hk-rise",
    style: {
      margin: "24px 0 0",
      maxWidth: 520,
      fontSize: 17,
      lineHeight: 1.6,
      color: "var(--text-muted)"
    }
  }, "HUAKAI \u6321\u5728\u4F60\u81EA\u6709\u7684 Anthropic\u3001OpenAI\u3001Google Vertex\u3001AWS Bedrock\u3001OpenRouter \u8D26\u53F7\u524D\u9762 \u2014 \u4E00\u5957\u534F\u8BAE\u63A5\u53E3\u3001\u5065\u5EB7\u611F\u77E5\u8C03\u5EA6\u3001\u9650\u6D41\u91CD\u8BD5\uFF0C\u4EE5\u53CA\u6309 token \u4E0E\u6210\u672C\u7684\u7528\u91CF\u6838\u7B97\u3002"), /*#__PURE__*/React.createElement("div", {
    className: "hk-rise",
    style: {
      marginTop: 30,
      display: "flex",
      flexWrap: "wrap",
      gap: 12
    }
  }, /*#__PURE__*/React.createElement(Button, {
    size: "lg",
    onClick: () => {
      window.location.href = "../console/index.html";
    }
  }, "\u8FDB\u5165\u63A7\u5236\u53F0 ", /*#__PURE__*/React.createElement(Icon, {
    name: "arrow-right"
  })), /*#__PURE__*/React.createElement("a", {
    href: "https://github.com/BloomingProsperity/HUAKAI",
    target: "_blank",
    rel: "noreferrer",
    style: {
      display: "inline-flex",
      alignItems: "center",
      gap: 8,
      height: "2.75rem",
      padding: "0 1.75rem",
      borderRadius: "var(--radius-md)",
      border: "1px solid var(--border-strong)",
      background: "var(--bg-surface)",
      color: "var(--text-body)",
      fontSize: 16,
      fontWeight: 500,
      textDecoration: "none"
    },
    className: "hk-outline-btn"
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "github"
  }), " \u5728 GitHub \u67E5\u770B")), /*#__PURE__*/React.createElement("div", {
    className: "hk-rise",
    style: {
      marginTop: 44,
      display: "flex",
      flexWrap: "wrap",
      gap: 36,
      borderTop: "1px solid var(--border)",
      paddingTop: 24
    }
  }, stats.map(s => /*#__PURE__*/React.createElement("div", {
    key: s.l
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 28,
      fontWeight: 700,
      color: "var(--text-strong)",
      fontFamily: "var(--font-mono)",
      fontVariantNumeric: "tabular-nums",
      letterSpacing: "-0.02em"
    }
  }, s.v), /*#__PURE__*/React.createElement("div", {
    style: {
      marginTop: 4,
      fontSize: 12.5,
      color: "var(--text-subtle)"
    }
  }, s.l))))), /*#__PURE__*/React.createElement("div", {
    style: {
      position: "relative"
    },
    className: "hk-rise"
  }, /*#__PURE__*/React.createElement(CodeTerminal, null), /*#__PURE__*/React.createElement(DispatchCard, null))));
}
window.SiteHero = SiteHero;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/website/Hero.jsx", error: String((e && e.message) || e) }); }

// ui_kits/website/Nav.jsx
try { (() => {
// HUAKAI site — brand lockup + sticky top nav.

function HKLogo({
  size = 40,
  sub = "华凯"
}) {
  return /*#__PURE__*/React.createElement("a", {
    href: "#top",
    style: {
      display: "flex",
      alignItems: "center",
      gap: 12,
      textDecoration: "none"
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: size,
      height: size,
      flexShrink: 0,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      borderRadius: 10,
      background: "var(--primary-500)",
      color: "var(--text-on-primary)",
      fontWeight: 700,
      fontSize: size * 0.36,
      boxShadow: "var(--shadow-glow)",
      letterSpacing: "-0.02em"
    }
  }, "HK"), /*#__PURE__*/React.createElement("span", {
    style: {
      display: "flex",
      flexDirection: "column",
      lineHeight: 1.1
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      fontSize: 16,
      fontWeight: 700,
      color: "var(--text-strong)",
      letterSpacing: "-0.01em"
    }
  }, "HUAKAI"), sub ? /*#__PURE__*/React.createElement("span", {
    style: {
      fontSize: 12,
      color: "var(--text-muted)"
    }
  }, sub) : null));
}
window.HKLogo = HKLogo;
function SiteNav() {
  const {
    Button
  } = window.HUAKAIDesignSystem_36f9be;
  const [scrolled, setScrolled] = React.useState(false);
  React.useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 8);
    window.addEventListener("scroll", onScroll, {
      passive: true
    });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);
  const links = [{
    href: "#features",
    label: "能力"
  }, {
    href: "#providers",
    label: "供应商"
  }, {
    href: "#deploy",
    label: "自托管"
  }, {
    href: "#",
    label: "文档"
  }];
  return /*#__PURE__*/React.createElement("header", {
    style: {
      position: "sticky",
      top: 0,
      zIndex: 50,
      borderBottom: `1px solid ${scrolled ? "var(--border)" : "transparent"}`,
      background: scrolled ? "rgba(255,255,255,0.82)" : "rgba(255,255,255,0)",
      backdropFilter: scrolled ? "blur(16px)" : "none",
      transition: "background var(--dur-base) var(--ease-standard), border-color var(--dur-base)"
    }
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-container",
    style: {
      height: 68,
      display: "flex",
      alignItems: "center",
      justifyContent: "space-between",
      gap: 24
    }
  }, /*#__PURE__*/React.createElement(HKLogo, null), /*#__PURE__*/React.createElement("nav", {
    className: "hk-nav-links",
    style: {
      display: "flex",
      alignItems: "center",
      gap: 4
    }
  }, links.map(l => /*#__PURE__*/React.createElement("a", {
    key: l.label,
    href: l.href,
    className: "hk-nav-link",
    style: {
      padding: "8px 12px",
      borderRadius: 8,
      fontSize: 14,
      fontWeight: 500,
      color: "var(--text-muted)",
      textDecoration: "none",
      transition: "color var(--dur-fast), background var(--dur-fast)"
    }
  }, l.label))), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 10
    }
  }, /*#__PURE__*/React.createElement("a", {
    href: "https://github.com/BloomingProsperity/HUAKAI",
    target: "_blank",
    rel: "noreferrer",
    className: "hk-ghost-link",
    style: {
      display: "inline-flex",
      alignItems: "center",
      gap: 8,
      height: 40,
      padding: "0 14px",
      borderRadius: "var(--radius-md)",
      border: "1px solid var(--border-strong)",
      background: "transparent",
      color: "var(--text-body)",
      fontSize: 14,
      fontWeight: 500,
      textDecoration: "none",
      transition: "border-color var(--dur-fast), color var(--dur-fast)"
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "github"
  }), " ", /*#__PURE__*/React.createElement("span", {
    className: "hk-hide-sm"
  }, "GitHub")), /*#__PURE__*/React.createElement(Button, {
    onClick: () => {
      window.location.href = "../console/index.html";
    }
  }, "\u8FDB\u5165\u63A7\u5236\u53F0 ", /*#__PURE__*/React.createElement(Icon, {
    name: "arrow-right"
  })))));
}
window.SiteNav = SiteNav;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/website/Nav.jsx", error: String((e && e.message) || e) }); }

// ui_kits/website/Providers.jsx
try { (() => {
// HUAKAI site — supported upstream providers strip.

const HK_PROVIDERS = [{
  mark: "A",
  name: "Anthropic",
  channel: "anthropic / oauth",
  models: ["claude-sonnet-4.5", "claude-opus-4.1"]
}, {
  mark: "O",
  name: "OpenAI",
  channel: "openai / api-key",
  models: ["gpt-5", "gpt-4.1-mini"]
}, {
  mark: "G",
  name: "Google Vertex",
  channel: "google / vertex",
  models: ["gemini-2.5-pro"]
}, {
  mark: "B",
  name: "AWS Bedrock",
  channel: "aws / bedrock",
  models: ["claude-sonnet-4.5"]
}, {
  mark: "R",
  name: "OpenRouter",
  channel: "openrouter / key",
  models: ["多模型聚合"]
}];
function ProviderCard({
  p
}) {
  const [h, setH] = React.useState(false);
  return /*#__PURE__*/React.createElement("div", {
    onMouseEnter: () => setH(true),
    onMouseLeave: () => setH(false),
    style: {
      display: "flex",
      flexDirection: "column",
      gap: 12,
      padding: 18,
      borderRadius: "var(--radius)",
      border: `1px solid ${h ? "var(--accent-soft-border)" : "var(--border)"}`,
      background: "var(--bg-surface)",
      boxShadow: "var(--shadow-card)",
      transform: h ? "translateY(-2px)" : "none",
      transition: "transform var(--dur-base) var(--ease-out), border-color var(--dur-base)"
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      alignItems: "center",
      gap: 11
    }
  }, /*#__PURE__*/React.createElement("span", {
    style: {
      width: 36,
      height: 36,
      flexShrink: 0,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
      borderRadius: 9,
      background: "var(--bg-surface-2)",
      border: "1px solid var(--border)",
      fontFamily: "var(--font-mono)",
      fontSize: 16,
      fontWeight: 700,
      color: h ? "var(--primary-300)" : "var(--text-muted)",
      transition: "color var(--dur-base)"
    }
  }, p.mark), /*#__PURE__*/React.createElement("div", {
    style: {
      minWidth: 0
    }
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 14.5,
      fontWeight: 600,
      color: "var(--text-strong)"
    }
  }, p.name), /*#__PURE__*/React.createElement("div", {
    style: {
      fontSize: 11.5,
      fontFamily: "var(--font-mono)",
      color: "var(--text-subtle)"
    }
  }, p.channel))), /*#__PURE__*/React.createElement("div", {
    style: {
      display: "flex",
      flexWrap: "wrap",
      gap: 6
    }
  }, p.models.map(m => /*#__PURE__*/React.createElement("span", {
    key: m,
    style: {
      fontFamily: "var(--font-mono)",
      fontSize: 11,
      color: "var(--text-muted)",
      padding: "3px 8px",
      borderRadius: "var(--radius-sm)",
      background: "var(--bg-surface-2)",
      border: "1px solid var(--border)"
    }
  }, m))));
}
function SiteProviders() {
  return /*#__PURE__*/React.createElement("section", {
    id: "providers",
    className: "hk-section"
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-container"
  }, /*#__PURE__*/React.createElement("div", {
    style: {
      maxWidth: 720
    }
  }, /*#__PURE__*/React.createElement("div", {
    className: "hk-eyebrow"
  }, "\u4E0A\u6E38\u4F9B\u5E94\u5546 \xB7 upstream providers"), /*#__PURE__*/React.createElement("h2", {
    className: "hk-h2"
  }, "\u63A5\u4F60\u5DF2\u7ECF\u5728\u7528\u7684\u8D26\u53F7"), /*#__PURE__*/React.createElement("p", {
    className: "hk-lead"
  }, "HUAKAI \u4E0D\u8F6C\u552E\u989D\u5EA6\u3002\u5B83\u7528\u4F60\u81EA\u5DF1\u7684 provider \u8D26\u53F7\uFF0C\u6309\u5065\u5EB7\u5EA6\uFF08", /*#__PURE__*/React.createElement("span", {
    className: "hk-code"
  }, "operational"), " /", /*#__PURE__*/React.createElement("span", {
    className: "hk-code"
  }, "cooling_down"), " / ", /*#__PURE__*/React.createElement("span", {
    className: "hk-code"
  }, "degraded"), "\uFF09\u4E0E\u5E76\u53D1\u4E0A\u9650\u8C03\u5EA6\u3002")), /*#__PURE__*/React.createElement("div", {
    className: "hk-provider-grid",
    style: {
      marginTop: 32
    }
  }, HK_PROVIDERS.map(p => /*#__PURE__*/React.createElement(ProviderCard, {
    key: p.name,
    p: p
  }))), /*#__PURE__*/React.createElement("p", {
    style: {
      marginTop: 18,
      fontSize: 13,
      color: "var(--text-subtle)",
      display: "flex",
      alignItems: "center",
      gap: 8
    }
  }, /*#__PURE__*/React.createElement(Icon, {
    name: "plus",
    size: 14,
    color: "var(--text-subtle)"
  }), " \u66F4\u591A provider \u9002\u914D\u4E2D \u2014 \u534F\u8BAE\u5C42\u53EF\u6269\u5C55\u3002")));
}
window.SiteProviders = SiteProviders;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/website/Providers.jsx", error: String((e && e.message) || e) }); }

// ui_kits/website/icons.jsx
try { (() => {
// Lucide icon helper for the website kit — 16px (or sized) stroke icon, currentColor.
function Icon({
  name,
  size = 16,
  color,
  style
}) {
  const ref = React.useRef(null);
  React.useEffect(() => {
    if (ref.current && window.lucide) {
      ref.current.innerHTML = "";
      const el = document.createElement("i");
      el.setAttribute("data-lucide", name);
      ref.current.appendChild(el);
      window.lucide.createIcons({
        attrs: {
          width: size,
          height: size,
          stroke: color || "currentColor",
          "stroke-width": 2
        }
      });
    }
  }, [name, size, color]);
  return /*#__PURE__*/React.createElement("span", {
    ref: ref,
    style: {
      display: "inline-flex",
      lineHeight: 0,
      ...style
    }
  });
}
window.Icon = Icon;
})(); } catch (e) { __ds_ns.__errors.push({ path: "ui_kits/website/icons.jsx", error: String((e && e.message) || e) }); }

__ds_ns.Badge = __ds_scope.Badge;

__ds_ns.Button = __ds_scope.Button;

__ds_ns.Card = __ds_scope.Card;

__ds_ns.CardHeader = __ds_scope.CardHeader;

__ds_ns.CardTitle = __ds_scope.CardTitle;

__ds_ns.CardDescription = __ds_scope.CardDescription;

__ds_ns.CardContent = __ds_scope.CardContent;

__ds_ns.CardFooter = __ds_scope.CardFooter;

__ds_ns.Input = __ds_scope.Input;

__ds_ns.Label = __ds_scope.Label;

__ds_ns.StatusDot = __ds_scope.StatusDot;

__ds_ns.StatCard = __ds_scope.StatCard;

__ds_ns.Table = __ds_scope.Table;

__ds_ns.THead = __ds_scope.THead;

__ds_ns.TBody = __ds_scope.TBody;

__ds_ns.TR = __ds_scope.TR;

__ds_ns.TH = __ds_scope.TH;

__ds_ns.TD = __ds_scope.TD;

})();
