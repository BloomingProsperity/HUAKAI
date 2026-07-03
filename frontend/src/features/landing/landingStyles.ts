// HUAKAI 首页样式 —— 由 Claude Design 原型 index.html 的 <style> + token CSS 移植合并。
// 全部 scope 到 `.hk-site`(避免污染 app 其它主题);LandingPage 把整页包在 .hk-site 里。
// 浅天蓝主题(渐变取自参考站)+ 柠檬绿点缀;品牌 HK 字标保留品牌青(--primary-500)。
export const LANDING_CSS = `
.hk-site {
  /* —— 浅天蓝页面主题(:root override 等价,scope 到本页)—— */
  --bg-app: linear-gradient(180deg, #F4FBFF 0%, #DCF1FF 38%, #C3E9FF 70%, #ABDFFF 100%);
  --bg-surface: #ffffff;
  --bg-surface-2: #eef6fc;
  --bg-surface-hover: #f4f9fd;
  --text-strong: #0d1b26;
  --text-body: #233039;
  --text-muted: #586a76;
  --text-subtle: #8696a1;
  --border: #d8e8f3;
  --border-strong: #bdd4e4;
  --accent: #a8db22;
  --accent-hover: #95c419;
  --text-on-primary: #14210a;
  --ring: #8fbf1a;
  --primary-300: #6f9a12;
  --primary-400: #5c8410;
  --accent-soft-bg: rgba(140,190,30,0.16);
  --accent-soft-border: rgba(120,165,25,0.40);
  --accent-soft-text: #4c7011;
  --shadow-card: 0 1px 3px rgba(13,42,72,0.06), 0 1px 2px rgba(13,42,72,0.08);
  --shadow-card-hover: 0 14px 40px rgba(13,42,72,0.12);
  --shadow-glow: 0 0 20px rgba(140,190,30,0.30);

  /* —— 基础 token(主题 override 未覆盖,取自 DS token 文件)—— */
  --primary-500: #14b8a6;
  --neutral-300: #cbd5e1;
  --neutral-900: #0f172a;
  --neutral-950: #020617;
  --success-fg: #15803d;
  --font-sans: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", "Roboto", "Helvetica Neue", Arial, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
  --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
  --radius: 0.5rem;
  --radius-md: calc(0.5rem - 2px);
  --radius-sm: calc(0.5rem - 4px);
  --radius-full: 9999px;
  --ease-out: cubic-bezier(0.16, 1, 0.3, 1);
  --ease-standard: cubic-bezier(0.4, 0, 0.2, 1);
  --dur-fast: 0.15s;
  --dur-base: 0.2s;
  --dur-slow: 0.3s;

  background: var(--bg-app);
  background-attachment: fixed;
  min-height: 100vh;
  color: var(--text-body);
  font-family: var(--font-sans);
  -webkit-font-smoothing: antialiased;
  text-rendering: optimizeLegibility;
}
.hk-site * { box-sizing: border-box; }
.hk-site ::selection { background: rgba(120,165,25,0.28); }

.hk-site .hk-container { width: 100%; max-width: 1200px; margin: 0 auto; padding: 0 24px; }

/* —— Hero —— */
.hk-site .hk-hero { position: relative; padding: clamp(48px, 8vh, 96px) 0 88px; overflow: hidden; }
.hk-site .hk-hero::before {
  content: ""; position: absolute; inset: 0; pointer-events: none;
  background: radial-gradient(80% 50% at 50% -10%, rgba(255,255,255,0.55), transparent 60%);
}
.hk-site .hk-hero::after {
  content: ""; position: absolute; inset: 0; pointer-events: none; opacity: 0.3;
  background-image: linear-gradient(var(--border) 1px, transparent 1px), linear-gradient(90deg, var(--border) 1px, transparent 1px);
  background-size: 48px 48px;
  -webkit-mask-image: radial-gradient(68% 58% at 50% 0%, #000, transparent 76%);
  mask-image: radial-gradient(68% 58% at 50% 0%, #000, transparent 76%);
}
.hk-site .hk-hero > .hk-container { position: relative; z-index: 1; }
.hk-site .hk-hero-grid { display: grid; grid-template-columns: 1.05fr 0.95fr; gap: 56px; align-items: center; }
.hk-site .hk-h1 { font-size: clamp(40px, 5.2vw, 62px); }

/* —— Section typography —— */
.hk-site .hk-section { padding: clamp(44px, 6.5vh, 78px) 0; }
.hk-site .hk-eyebrow { font-family: var(--font-mono); font-size: 12px; font-weight: 600; text-transform: uppercase; letter-spacing: 0.06em; color: var(--primary-400); }
.hk-site .hk-h2 { margin: 12px 0 0; font-size: clamp(26px, 3.3vw, 36px); font-weight: 700; letter-spacing: -0.02em; line-height: 1.15; color: var(--text-strong); }
.hk-site .hk-lead { margin: 16px 0 0; font-size: 16px; line-height: 1.6; color: var(--text-muted); }
.hk-site .hk-code { font-family: var(--font-mono); font-size: 0.85em; color: var(--primary-300); background: var(--accent-soft-bg); border: 1px solid var(--accent-soft-border); padding: 1px 6px; border-radius: 4px; margin: 0 2px; white-space: nowrap; }

/* —— Grids —— */
.hk-site .hk-provider-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(208px, 1fr)); gap: 16px; }
.hk-site .hk-feature-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 18px; }
.hk-site .hk-footer-grid { display: grid; grid-template-columns: 1.5fr 1fr 1fr 1fr; gap: 32px; }

/* —— Terminal cursor —— */
.hk-site .hk-cursor { display: inline-block; width: 7px; height: 14px; margin-left: 3px; vertical-align: -2px; border-radius: 1px; }

/* —— Hover states —— */
.hk-site .hk-nav-link:hover { color: var(--text-body); background: var(--bg-surface-2); }
.hk-site .hk-ghost-link:hover { border-color: var(--accent-soft-border); color: var(--primary-300); }
.hk-site .hk-outline-btn:hover { border-color: var(--accent-soft-border); background: var(--accent-soft-bg); color: var(--accent-soft-text); }
.hk-site .hk-foot-link:hover { color: var(--primary-300); }
.hk-site .hk-btn:hover { background: var(--accent-hover); transform: translateY(-1px); }

/* —— 状态点脉冲 —— */
.hk-site .hk-statusdot-pulse { animation: hk-dot-pulse 1.8s var(--ease-out) infinite; }
@keyframes hk-dot-pulse { 0% { box-shadow: 0 0 0 0 rgba(21,128,61,0.5); } 70% { box-shadow: 0 0 0 6px rgba(21,128,61,0); } 100% { box-shadow: 0 0 0 0 rgba(21,128,61,0); } }

/* —— Motion(尊重 reduced-motion)—— */
@media (prefers-reduced-motion: no-preference) {
  .hk-site .hk-rise { opacity: 0; transform: translateY(12px); animation: hk-rise 0.55s var(--ease-out) forwards; }
  .hk-site .hk-cursor { animation: hk-blink 1.1s steps(1) infinite; }
}
@keyframes hk-rise { to { opacity: 1; transform: none; } }
@keyframes hk-blink { 50% { opacity: 0; } }

/* —— Responsive —— */
@media (max-width: 980px) {
  .hk-site .hk-hero-grid { grid-template-columns: 1fr; gap: 40px; }
  .hk-site .hk-feature-grid { grid-template-columns: repeat(2, 1fr); }
  .hk-site .hk-nav-links { display: none; }
  .hk-site .hk-float { position: static !important; right: auto !important; bottom: auto !important; width: auto !important; margin-top: 16px; box-shadow: var(--shadow-card) !important; }
}
@media (max-width: 600px) {
  .hk-site .hk-feature-grid { grid-template-columns: 1fr; }
  .hk-site .hk-hide-sm { display: none; }
}
`
