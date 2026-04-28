# Canonical Skill Source

The skill definitions in this directory are a **mirror** of [`/.agents/skills/`](../../.agents/skills/) for Claude Code's auto-discovery.

**Do not edit files here.** Edit the canonical version under `.agents/skills/` only.

## Why a mirror exists

- `.agents/skills/` is the tool-agnostic canonical location referenced by [`AGENTS.md`](../../AGENTS.md), [`CLAUDE.md`](../../CLAUDE.md), and [`GEMINI.md`](../../GEMINI.md).
- `.claude/skills/` is where Claude Code natively looks for skill files.

## Drift control

A Phase 1 task is open to either:

1. Replace this directory with a generated mirror (build script / git hook) that fails CI on drift, or
2. Move the canonical source here once Claude Code becomes the primary execution surface.

Until then, treat this directory as **read-only**. If you make a change here by accident, copy it back to `.agents/skills/` and re-sync.
