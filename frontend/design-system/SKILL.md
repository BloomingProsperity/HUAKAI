---
name: huakai-design
description: Use this skill to generate well-branded interfaces and assets for HUAKAI (华凯) — a self-hosted AI Gateway / Account Hub / Admin Ops console — either for production or throwaway prototypes/mocks/etc. Contains essential design guidelines, colors, type, fonts, assets, and UI kit components for prototyping.
user-invocable: true
---

Read the README.md file within this skill, and explore the other available files.
If creating visual artifacts (slides, mocks, throwaway prototypes, etc), copy assets out and create static HTML files for the user to view. If working on production code, you can copy assets and read the rules here to become an expert in designing with this brand.
If the user invokes this skill without any other guidance, ask them what they want to build or design, ask some questions, and act as an expert designer who outputs HTML artifacts _or_ production code, depending on the need.

## Quick reference
- **Brand:** HUAKAI (华凯) — operator-side AI Gateway console. Dense, dark, data-first admin UI.
- **Theme:** dark by default (`<html class="dark">`, app bg `#020617`).
- **Accent:** one teal — `--primary-500 #14b8a6`. Use only for live/active/selected affordances.
- **Neutrals:** slate scale (`--neutral-50…950`). Surfaces: app 950 / card 900 / inset 800 / border 800.
- **Semantic:** success #10b981 · warning #f59e0b · danger #ef4444 · info #3b82f6.
- **Type:** system font stack (no webfonts); monospace + `tabular-nums` for all data (IDs, latencies, counts).
- **Voice:** terse zh-CN with English technical terms left inline; impersonal/system voice; honest about mocked vs real data; no emoji.
- **Radius 8px**, hairline 1px borders, soft `--shadow-card`; teal glow reserved for live elements only.
- **Icons:** Lucide, 16px stroke. Load from `https://unpkg.com/lucide@0.469.0/dist/umd/lucide.min.js`.

## Files
- `styles.css` — link this; it imports all tokens (`tokens/*.css`).
- `components/core/*`, `components/data/*` — React primitives (Button, Badge, StatusDot, Card, Input, StatCard, Table). Mount via `window.HUAKAIDesignSystem_36f9be` after loading `_ds_bundle.js`.
- `ui_kits/console/` — full interactive console recreation (Dashboard / Accounts / Chat). Best reference for composition.
- `guidelines/*.card.html` — foundation specimens.

## Building an HTML artifact
1. Link `styles.css` and add `class="dark"` to `<html>`.
2. Load React 18.3.1 + Babel (pinned) + `_ds_bundle.js` + Lucide CDN.
3. Read components from `const { Card, StatCard, Badge, ... } = window.HUAKAIDesignSystem_36f9be`.
4. Follow the voice + number-formatting rules in README.md. Don't invent data to fill charts — show an honest empty state.
