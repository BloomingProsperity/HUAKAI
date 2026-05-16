# Upstream Policy Monitor Runbook

## Purpose

`tools/upstream-policy-monitor` is POL-1 L0 monitoring. It watches public official vendor policy/blog/status surfaces and GitHub API metadata for terms that may indicate upstream policy movement: bans, restrictions, TOS changes, API deprecations, shutdowns, migrations, third-party language, reverse-proxy language, disables, refunds, and blocked access.

This tool is neutral monitoring. It does not read reference-project source, does not call model/product APIs, and does not register cron by itself.

## Local dry-run

Dry-run is the default and uses fixtures committed with the tool:

```bash
tools/upstream-policy-monitor/run.sh </dev/null
```

To avoid writing a sample alert while testing:

```bash
tools/upstream-policy-monitor/run.sh --no-write </dev/null
```

Expected summary shape:

```text
mode=dry-run sources_scanned=9 fetch_errors=0 hits=<n>
alert=<path-or-none>
```

## Live run

Live mode must be explicit:

```bash
tools/upstream-policy-monitor/run.sh --live --output-dir docs/alerts </dev/null
```

Live mode fetches:

- Anthropic, OpenAI, and Google official public policy/blog pages.
- Anthropic, OpenAI, and Google official public status JSON.
- GitHub API metadata for the configured official CLI discussion/issue surfaces.

If `gh` is installed, GitHub targets use `gh api`. Otherwise the tool falls back to `urllib` against `https://api.github.com/`. Set `GITHUB_TOKEN` locally if the Owner wants higher GitHub API rate limits.

## Output

Positive matches are written to:

```text
docs/alerts/YYYY-MM-DD-upstream-policy-alert.md
```

No alert file is written when there are no keyword hits. The alert includes source, keyword, locator, snippet, mode, active keywords, and any fetch errors observed during the same run.

## Owner triage

When an alert appears:

1. Open the upstream locator in the alert and verify the source directly.
2. Decide whether the policy movement is operationally material.
3. If material, decide whether to trigger Phase ADV-1 or an L1-L5 configuration review from the 2026-05-16 all-vendor roadmap.
4. If it is only informational, leave the alert as an audit trail and do not change runtime behavior.
5. If it is noisy, tune the next run with `--ignore-keyword` or adjust the default keyword list in a small reviewed patch.

## False-positive reduction

Use these controls before changing code:

```bash
tools/upstream-policy-monitor/run.sh --live --ignore-keyword plus --ignore-keyword gemini --no-write </dev/null
```

Use `--max-hits-per-source` to reduce repeated snippets from a single noisy page. Use `--keyword` to add temporary event-specific terms during a known vendor rollout.

Any permanent keyword change should be reviewed as a small POL-1 tool patch because it changes monitoring sensitivity.

## Scheduling

Use `tools/upstream-policy-monitor/cron.example` as a template only. HUAKAI does not register cron or systemd timers from this repo. Owner decides where the schedule runs, where logs go, and whether outbound network access is allowed from that machine.

## Checks

Run the local verification set after edits:

```bash
python3 -m py_compile tools/upstream-policy-monitor/run.py < /dev/null
bash -n tools/upstream-policy-monitor/run.sh < /dev/null
python3 -m unittest tools/upstream-policy-monitor/test_run.py < /dev/null
```
