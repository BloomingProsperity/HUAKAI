#!/usr/bin/env python3
"""校验项目文档、源码责任索引和仓库文件是否保持一致。"""

from __future__ import annotations

import re
import sys
from collections import Counter
from pathlib import Path
from urllib.parse import unquote


INDEX_ROW_RE = re.compile(r"^\| `([^`]+)` \|", re.MULTILINE)
FEATURE_ID_RE = re.compile(r"\b[A-Z][A-Z0-9_-]*-\d{3}\b")
MARKDOWN_LINK_RE = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
URI_SCHEME_RE = re.compile(r"^[A-Za-z][A-Za-z0-9+.-]*:")


def relative_path(path: Path, root: Path) -> str:
    return path.relative_to(root).as_posix()


def backend_go_files(root: Path) -> set[str]:
    files: set[str] = set()
    for path in (root / "backend").rglob("*.go"):
        rel = relative_path(path, root)
        if path.name.endswith("_test.go"):
            continue
        if rel.startswith("backend/pkg/external/") or "/vendor/" in rel:
            continue
        files.add(rel)
    return files


def rust_files(root: Path) -> set[str]:
    base = root / "exploratory/rust-core-gateway/merged"
    return {
        relative_path(path, root)
        for path in base.rglob("*.rs")
        if "tests" not in path.relative_to(base).parts and "target" not in path.parts
    }


def sql_files(root: Path) -> set[str]:
    return {
        relative_path(path, root)
        for path in (root / "backend").rglob("*.sql")
        if "vendor" not in path.parts
    }


def is_engineering_asset(rel: str) -> bool:
    path = Path(rel)
    suffix = path.suffix

    if rel.startswith(".github/workflows/"):
        return suffix in {".yml", ".yaml"}
    if rel in {
        "backend/Caddyfile",
        "backend/Dockerfile",
    }:
        return True
    if path.parent.as_posix() == "backend" and path.name.startswith("docker-compose"):
        return suffix in {".yml", ".yaml"}
    if rel.startswith("backend/scripts/"):
        return suffix in {".sh", ".ps1", ".txt"}
    if rel.startswith("scripts/"):
        return suffix in {".sh", ".ps1", ".py"}
    if rel.startswith((".claude/hooks/", ".gemini/hooks/")):
        return suffix in {".sh", ".py"}
    if rel.startswith(".coordination/"):
        return suffix in {".sh", ".py", ".service"}
    if rel.startswith("backend/deploy/"):
        return path.name == "Dockerfile" or (
            suffix in {".sh", ".py", ".txt", ".in"}
            and not path.name.startswith("test_")
        )
    if rel.startswith("exploratory/rust-core-gateway/merged/tools/"):
        return suffix in {".sh", ".py"}
    if rel.startswith("tools/fingerprint-collector/"):
        return (
            (suffix == ".go" and not path.name.endswith("_test.go"))
            or rel == "tools/fingerprint-collector/verify-capture.sh"
        )
    if rel in {
        "tools/upstream-policy-monitor/run.py",
        "tools/upstream-policy-monitor/run.sh",
    }:
        return True
    if rel.startswith("tools/project_governance/"):
        return suffix == ".py" and not path.name.startswith("test_")
    return False


def engineering_files(root: Path) -> set[str]:
    files: set[str] = set()
    ignored_dirs = {".git", "target", "__pycache__", "vendor"}
    for path in root.rglob("*"):
        if not path.is_file():
            continue
        rel_path = path.relative_to(root)
        if any(part in ignored_dirs for part in rel_path.parts):
            continue
        rel = rel_path.as_posix()
        if is_engineering_asset(rel):
            files.add(rel)
    return files


def expected_index_files(root: Path) -> dict[str, set[str]]:
    return {
        "后端 Go 生产文件": backend_go_files(root),
        "Rust": rust_files(root),
        "SQL": sql_files(root),
        "工程工具与部署": engineering_files(root),
    }


def duplicate_paths(paths: list[str]) -> list[str]:
    return sorted(path for path, count in Counter(paths).items() if count > 1)


def feature_ids_from_whitepaper(text: str) -> set[str]:
    start = text.find("### 14.2 稳定功能与支撑编号")
    end = text.find("### 14.3 完整性证明")
    if start < 0 or end < 0 or end <= start:
        return set()
    return set(FEATURE_ID_RE.findall(text[start:end]))


def feature_ids_from_index(text: str) -> set[str]:
    ids: set[str] = set()
    for line in text.splitlines():
        if INDEX_ROW_RE.match(line):
            ids.update(FEATURE_ID_RE.findall(line))
    return ids


def declared_count(text: str, label: str) -> int | None:
    match = re.search(rf"- {re.escape(label)}：(\d+)", text)
    return int(match.group(1)) if match else None


def markdown_link_errors(root: Path) -> list[str]:
    errors: list[str] = []
    markdown_files = [
        path
        for path in root.rglob("*.md")
        if not any(part in {".git", "target", "vendor"} for part in path.parts)
    ]
    for source in markdown_files:
        text = source.read_text(encoding="utf-8")
        for raw_target in MARKDOWN_LINK_RE.findall(text):
            target = raw_target.strip()
            if target.startswith("<"):
                close = target.find(">")
                target = target[1:close] if close >= 0 else target
            else:
                target = target.split(maxsplit=1)[0]
            if not target or target.startswith("#") or URI_SCHEME_RE.match(target):
                continue
            target = unquote(target.split("#", 1)[0].split("?", 1)[0])
            if not target:
                continue
            resolved = root / target.lstrip("/") if target.startswith("/") else source.parent / target
            if not resolved.exists():
                errors.append(
                    f"{relative_path(source, root)}：相对链接目标不存在：{raw_target}"
                )
    return sorted(errors)


def run_checks(root: Path) -> list[str]:
    errors: list[str] = []
    index_path = root / "docs/源码责任索引.md"
    whitepaper_path = root / "docs/HUAKAI项目与架构白皮书.md"

    if not index_path.exists() or not whitepaper_path.exists():
        return ["缺少白皮书或源码责任索引，无法执行治理校验"]

    index_text = index_path.read_text(encoding="utf-8")
    whitepaper_text = whitepaper_path.read_text(encoding="utf-8")
    indexed_list = INDEX_ROW_RE.findall(index_text)
    indexed = set(indexed_list)

    for path in duplicate_paths(indexed_list):
        errors.append(f"源码责任索引存在重复路径：{path}")

    for rel in sorted(indexed):
        if not (root / rel).exists():
            errors.append(f"源码责任索引指向不存在的路径：{rel}")

    categories = expected_index_files(root)
    expected = set().union(*categories.values())
    for rel in sorted(expected - indexed):
        errors.append(f"源码责任索引漏收当前范围文件：{rel}")
    for rel in sorted(indexed - expected):
        errors.append(f"源码责任索引包含范围外或已失效文件：{rel}")

    expected_counts = {
        "索引文件总数": len(expected),
        **{label: len(paths) for label, paths in categories.items()},
    }
    for label, actual in expected_counts.items():
        declared = declared_count(index_text, label)
        if declared is None:
            errors.append(f"源码责任索引缺少统计项：{label}")
        elif declared != actual:
            errors.append(f"源码责任索引统计错误：{label} 声明 {declared}，实际 {actual}")

    whitepaper_ids = feature_ids_from_whitepaper(whitepaper_text)
    index_ids = feature_ids_from_index(index_text)
    if not whitepaper_ids:
        errors.append("白皮书未提取到稳定功能编号")
    for feature_id in sorted(whitepaper_ids - index_ids):
        errors.append(f"白皮书功能编号未被源码责任索引使用：{feature_id}")
    for feature_id in sorted(index_ids - whitepaper_ids):
        errors.append(f"源码责任索引使用了白皮书未定义的功能编号：{feature_id}")

    errors.extend(markdown_link_errors(root))
    return errors


def main() -> int:
    root = Path(__file__).resolve().parents[2]
    errors = run_checks(root)
    if errors:
        print("项目治理一致性校验失败：", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    categories = expected_index_files(root)
    total = sum(len(paths) for paths in categories.values())
    print(
        "项目治理一致性校验通过："
        f"{total} 个责任条目，"
        f"{len(feature_ids_from_whitepaper((root / 'docs/HUAKAI项目与架构白皮书.md').read_text(encoding='utf-8')))} "
        "个功能编号，Markdown 相对链接有效。"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
