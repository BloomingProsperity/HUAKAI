#!/usr/bin/env python3
"""HUAKAI upstream policy monitor.

Default mode is dry-run: read local fixtures, scan for policy-risk keywords,
and write a sample alert only when matches are found. Live mode requires the
explicit --live flag and fetches only public vendor pages, status JSON, and
GitHub API metadata.
"""

from __future__ import annotations

import argparse
import datetime as dt
import html
import json
import os
import re
import shutil
import subprocess
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Sequence


TOOL_DIR = Path(__file__).resolve().parent
REPO_ROOT = TOOL_DIR.parents[1]
DEFAULT_FIXTURE_DIR = TOOL_DIR / "fixtures"
DEFAULT_ALERT_DIR = REPO_ROOT / "docs" / "alerts"
USER_AGENT = "HUAKAI-UpstreamPolicyMonitor/0.1 (+owner-operated)"

DEFAULT_KEYWORDS: tuple[str, ...] = (
    "ban",
    "restrict",
    "tos update",
    "terms of service",
    "api deprecation",
    "deprecation",
    "shutdown",
    "migration",
    "third-party",
    "third party",
    "reverse",
    "disable",
    "disabled",
    "refund",
    "blocked",
    "oauth",
    "subscription",
    "chatgpt subscription",
    "plus",
    "wrap",
    "gemini",
    "code assist",
    "antigravity",
)


@dataclass(frozen=True)
class Target:
    name: str
    kind: str
    locator: str
    fixture: str
    note: str

    @property
    def display_locator(self) -> str:
        if self.kind == "github_api":
            return f"gh api {self.locator}"
        return self.locator


@dataclass(frozen=True)
class FetchResult:
    target: Target
    content: str
    fetched_from: str


@dataclass(frozen=True)
class FetchError:
    target: Target
    message: str


@dataclass(frozen=True)
class Hit:
    source: str
    source_kind: str
    locator: str
    keyword: str
    snippet: str


TARGETS: tuple[Target, ...] = (
    Target(
        "Anthropic API news",
        "url",
        "https://www.anthropic.com/news",
        "anthropic_news.html",
        "Official news page; watch for OAuth, subscription, third-party, and API policy language.",
    ),
    Target(
        "OpenAI API blog",
        "url",
        "https://openai.com/blog",
        "openai_blog.html",
        "Official blog; watch for ChatGPT subscription, Plus, API, and wrapper policy language.",
    ),
    Target(
        "Google Cloud AI blog",
        "url",
        "https://blog.google/products/google-cloud-ai/",
        "google_cloud_ai_blog.html",
        "Official Google Cloud AI blog; watch for Gemini, Code Assist, Antigravity, and restriction language.",
    ),
    Target(
        "Google Gemini CLI discussions",
        "github_api",
        "repos/google-gemini/gemini-cli/discussions",
        "google_gemini_cli_discussions.json",
        "GitHub API metadata for Gemini CLI discussions.",
    ),
    Target(
        "Anthropic Claude Code issues",
        "github_api",
        "repos/anthropics/claude-code/issues",
        "anthropic_claude_code_issues.json",
        "GitHub API metadata for Claude Code issues.",
    ),
    Target(
        "OpenAI Codex discussions",
        "github_api",
        "repos/openai/codex/discussions",
        "openai_codex_discussions.json",
        "GitHub API metadata for Codex CLI discussions.",
    ),
    Target(
        "Anthropic status",
        "url",
        "https://status.anthropic.com/api/v2/summary.json",
        "status_anthropic_summary.json",
        "Official Anthropic status summary JSON.",
    ),
    Target(
        "OpenAI status",
        "url",
        "https://status.openai.com/api/v2/summary.json",
        "status_openai_summary.json",
        "Official OpenAI status summary JSON.",
    ),
    Target(
        "Google Cloud status",
        "url",
        "https://status.cloud.google.com/incidents.json",
        "status_google_cloud_incidents.json",
        "Official Google Cloud status incidents JSON.",
    ),
)


def parse_args(argv: Sequence[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Scan official upstream policy/status/GitHub surfaces for policy-risk keywords.",
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument(
        "--live",
        action="store_true",
        help="Fetch public upstream sources. Without this flag the tool uses local fixtures.",
    )
    mode.add_argument(
        "--dry-run",
        action="store_true",
        help="Use local fixtures. This is the default and exists for explicitness.",
    )
    parser.add_argument(
        "--fixtures-dir",
        default=str(DEFAULT_FIXTURE_DIR),
        help="Directory containing dry-run fixture files.",
    )
    parser.add_argument(
        "--output-dir",
        default=str(DEFAULT_ALERT_DIR),
        help="Directory where positive alert markdown files are written.",
    )
    parser.add_argument(
        "--date",
        default=None,
        help="Alert date in YYYY-MM-DD. Defaults to current UTC date.",
    )
    parser.add_argument(
        "--keyword",
        action="append",
        default=[],
        help="Add an extra keyword or phrase to scan for. May be repeated.",
    )
    parser.add_argument(
        "--ignore-keyword",
        action="append",
        default=[],
        help="Remove a default keyword or phrase for this run. May be repeated.",
    )
    parser.add_argument(
        "--max-hits-per-source",
        type=int,
        default=8,
        help="Maximum keyword snippets to include for each source.",
    )
    parser.add_argument(
        "--timeout-seconds",
        type=int,
        default=20,
        help="Network timeout for each live source.",
    )
    parser.add_argument(
        "--no-write",
        action="store_true",
        help="Scan and print summary without writing docs/alerts output.",
    )
    parser.add_argument(
        "--fail-on-fetch-error",
        action="store_true",
        help="Return non-zero in live mode if any source cannot be fetched.",
    )
    return parser.parse_args(argv)


def active_keywords(extra: Iterable[str], ignored: Iterable[str]) -> tuple[str, ...]:
    ignored_normalized = {item.casefold().strip() for item in ignored if item.strip()}
    ordered: list[str] = []
    seen: set[str] = set()
    for keyword in tuple(DEFAULT_KEYWORDS) + tuple(extra):
        normalized = keyword.casefold().strip()
        if not normalized or normalized in ignored_normalized or normalized in seen:
            continue
        seen.add(normalized)
        ordered.append(keyword.strip())
    return tuple(ordered)


def fetch_url(url: str, timeout_seconds: int) -> str:
    request = urllib.request.Request(
        url,
        headers={
            "User-Agent": USER_AGENT,
            "Accept": "application/json, text/html, application/xml;q=0.9, */*;q=0.8",
        },
    )
    with urllib.request.urlopen(request, timeout=timeout_seconds) as response:
        raw = response.read()
        charset = response.headers.get_content_charset() or "utf-8"
    return raw.decode(charset, errors="replace")


def fetch_github_api(path: str, timeout_seconds: int) -> str:
    gh = shutil.which("gh")
    if gh:
        completed = subprocess.run(
            [gh, "api", path],
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=timeout_seconds,
            check=False,
        )
        if completed.returncode != 0:
            stderr = completed.stderr.strip() or "gh api returned a non-zero status"
            raise RuntimeError(stderr)
        return completed.stdout

    url = f"https://api.github.com/{path.lstrip('/')}"
    request = urllib.request.Request(
        url,
        headers={
            "User-Agent": USER_AGENT,
            "Accept": "application/vnd.github+json",
        },
    )
    token = os.environ.get("GITHUB_TOKEN")
    if token:
        request.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(request, timeout=timeout_seconds) as response:
        return response.read().decode("utf-8", errors="replace")


def fetch_live_target(target: Target, timeout_seconds: int) -> FetchResult:
    if target.kind == "github_api":
        return FetchResult(target, fetch_github_api(target.locator, timeout_seconds), target.display_locator)
    return FetchResult(target, fetch_url(target.locator, timeout_seconds), target.locator)


def load_fixture_target(target: Target, fixture_dir: Path) -> FetchResult:
    fixture_path = fixture_dir / target.fixture
    return FetchResult(target, fixture_path.read_text(encoding="utf-8"), str(fixture_path))


def collect_sources(
    *,
    live: bool,
    fixture_dir: Path,
    timeout_seconds: int,
) -> tuple[list[FetchResult], list[FetchError]]:
    results: list[FetchResult] = []
    errors: list[FetchError] = []
    for target in TARGETS:
        try:
            result = fetch_live_target(target, timeout_seconds) if live else load_fixture_target(target, fixture_dir)
            results.append(result)
        except (OSError, RuntimeError, subprocess.TimeoutExpired, urllib.error.URLError) as exc:
            errors.append(FetchError(target, str(exc)))
    return results, errors


def extract_text(content: str) -> str:
    stripped = content.lstrip()
    if stripped.startswith("{") or stripped.startswith("["):
        try:
            parsed = json.loads(content)
            return json.dumps(parsed, ensure_ascii=False, sort_keys=True)
        except json.JSONDecodeError:
            pass

    without_scripts = re.sub(r"(?is)<(script|style).*?</\1>", " ", content)
    without_tags = re.sub(r"(?s)<[^>]+>", " ", without_scripts)
    unescaped = html.unescape(without_tags)
    return re.sub(r"\s+", " ", unescaped).strip()


def make_snippet(text: str, start: int, end: int, radius: int = 120) -> str:
    left = max(0, start - radius)
    right = min(len(text), end + radius)
    snippet = text[left:right].strip()
    if left > 0:
        snippet = f"... {snippet}"
    if right < len(text):
        snippet = f"{snippet} ..."
    return snippet.replace("|", "\\|")


def scan_source(target: Target, content: str, keywords: Sequence[str], max_hits: int) -> list[Hit]:
    if max_hits <= 0:
        return []

    text = extract_text(content)
    hits: list[Hit] = []
    seen: set[tuple[str, str]] = set()
    for keyword in keywords:
        pattern = re.compile(re.escape(keyword), re.IGNORECASE)
        for match in pattern.finditer(text):
            snippet = make_snippet(text, match.start(), match.end())
            dedupe_key = (keyword.casefold(), snippet.casefold())
            if dedupe_key in seen:
                continue
            seen.add(dedupe_key)
            hits.append(
                Hit(
                    source=target.name,
                    source_kind=target.kind,
                    locator=target.display_locator,
                    keyword=keyword,
                    snippet=snippet,
                )
            )
            if len(hits) >= max_hits:
                return hits
    return hits


def scan_results(results: Sequence[FetchResult], keywords: Sequence[str], max_hits: int) -> list[Hit]:
    hits: list[Hit] = []
    for result in results:
        hits.extend(scan_source(result.target, result.content, keywords, max_hits))
    return hits


def alert_path_for(output_dir: Path, run_date: str) -> Path:
    return output_dir / f"{run_date}-upstream-policy-alert.md"


def write_alert(
    *,
    hits: Sequence[Hit],
    fetch_errors: Sequence[FetchError],
    output_dir: Path,
    run_date: str,
    live: bool,
    keywords: Sequence[str],
) -> Path | None:
    if not hits:
        return None

    output_dir.mkdir(parents=True, exist_ok=True)
    path = alert_path_for(output_dir, run_date)
    mode = "live" if live else "dry-run"
    lines: list[str] = [
        f"# Upstream Policy Alert ({run_date})",
        "",
        "| Field | Value |",
        "| --- | --- |",
        f"| Mode | {mode} |",
        f"| Positive hits | {len(hits)} |",
        f"| Sources configured | {len(TARGETS)} |",
        f"| Active keywords | {', '.join(keywords)} |",
        "",
        "## Owner triage",
        "",
        "1. Read each hit and verify the upstream page directly from the locator.",
        "2. Decide whether this changes HUAKAI risk posture.",
        "3. If material, trigger the relevant Phase ADV-1 or L1-L5 configuration review.",
        "4. If noisy, tune keywords or add a local ignore for the next scheduled run.",
        "",
        "## Hits",
        "",
        "| Source | Keyword | Locator | Snippet |",
        "| --- | --- | --- | --- |",
    ]
    for hit in hits:
        lines.append(f"| {hit.source} | `{hit.keyword}` | `{hit.locator}` | {hit.snippet} |")

    if fetch_errors:
        lines.extend(
            [
                "",
                "## Fetch errors",
                "",
                "| Source | Error |",
                "| --- | --- |",
            ]
        )
        for error in fetch_errors:
            message = error.message.replace("|", "\\|").replace("\n", " ")
            lines.append(f"| {error.target.name} | {message} |")

    lines.extend(
        [
            "",
            "## Tool boundary",
            "",
            "This alert was produced by `tools/upstream-policy-monitor`. It monitors public official pages, status JSON, and GitHub API metadata only. It is not evidence from reference-project source code.",
            "",
        ]
    )
    path.write_text("\n".join(lines), encoding="utf-8")
    return path


def utc_date() -> str:
    return dt.datetime.now(dt.timezone.utc).date().isoformat()


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    live = bool(args.live)
    run_date = args.date or utc_date()
    keywords = active_keywords(args.keyword, args.ignore_keyword)
    fixture_dir = Path(args.fixtures_dir)
    output_dir = Path(args.output_dir)

    results, fetch_errors = collect_sources(
        live=live,
        fixture_dir=fixture_dir,
        timeout_seconds=args.timeout_seconds,
    )
    hits = scan_results(results, keywords, max(0, args.max_hits_per_source))
    alert_path = None
    if not args.no_write:
        alert_path = write_alert(
            hits=hits,
            fetch_errors=fetch_errors,
            output_dir=output_dir,
            run_date=run_date,
            live=live,
            keywords=keywords,
        )

    mode = "live" if live else "dry-run"
    print(f"mode={mode} sources_scanned={len(results)} fetch_errors={len(fetch_errors)} hits={len(hits)}")
    if alert_path:
        print(f"alert={alert_path}")
    elif hits and args.no_write:
        print("alert=not-written (--no-write)")
    else:
        print("alert=none")

    if fetch_errors:
        for error in fetch_errors:
            print(f"warning: {error.target.name}: {error.message}", file=sys.stderr)
    if args.fail_on_fetch_error and fetch_errors:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
