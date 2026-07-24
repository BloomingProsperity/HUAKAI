#!/usr/bin/env python3
"""项目治理校验器的判别性测试。"""

from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path


sys.path.insert(0, str(Path(__file__).resolve().parent))
import check


class ProjectGovernanceCheckTest(unittest.TestCase):
    def test_duplicate_paths_are_reported(self) -> None:
        self.assertEqual(
            check.duplicate_paths(["a.go", "b.go", "a.go"]),
            ["a.go"],
        )

    def test_feature_ids_are_limited_to_the_stable_feature_section(self) -> None:
        text = """
ADR-001
### 14.2 稳定功能与支撑编号
| `ACC-001` | 账号 |
| `BUILD-001` | 构建 |
### 14.3 完整性证明
ADR-002
"""
        self.assertEqual(
            check.feature_ids_from_whitepaper(text),
            {"ACC-001", "BUILD-001"},
        )

    def test_engineering_scope_excludes_tests_but_includes_checker(self) -> None:
        self.assertFalse(
            check.is_engineering_asset(
                "backend/deploy/hermes-runner/test_main_auth.py"
            )
        )
        self.assertTrue(
            check.is_engineering_asset("tools/project_governance/check.py")
        )
        self.assertTrue(
            check.is_engineering_asset("backend/docker-compose.staging.yml")
        )

    def test_missing_markdown_target_is_reported_and_can_recover(self) -> None:
        cache_root = Path.home() / ".cache"
        cache_root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=cache_root) as temp_dir:
            root = Path(temp_dir)
            docs = root / "docs"
            docs.mkdir()
            source = docs / "入口.md"
            source.write_text("[目标](目标.md)\n", encoding="utf-8")

            errors = check.markdown_link_errors(root)
            self.assertEqual(len(errors), 1)
            self.assertIn("目标.md", errors[0])

            (docs / "目标.md").write_text("# 目标\n", encoding="utf-8")
            self.assertEqual(check.markdown_link_errors(root), [])

    def test_image_and_angle_bracket_link_targets_are_checked(self) -> None:
        cache_root = Path.home() / ".cache"
        cache_root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=cache_root) as temp_dir:
            root = Path(temp_dir)
            docs = root / "docs"
            docs.mkdir()
            (docs / "含空格文件.md").write_text("# 目标\n", encoding="utf-8")
            source = docs / "入口.md"
            source.write_text(
                "[说明](<含空格文件.md>)\n![图片](缺失.png)\n",
                encoding="utf-8",
            )

            errors = check.markdown_link_errors(root)
            self.assertEqual(len(errors), 1)
            self.assertIn("缺失.png", errors[0])

            (docs / "缺失.png").write_bytes(b"png")
            self.assertEqual(check.markdown_link_errors(root), [])

    def test_external_uri_schemes_are_not_treated_as_relative_paths(self) -> None:
        cache_root = Path.home() / ".cache"
        cache_root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=cache_root) as temp_dir:
            root = Path(temp_dir)
            source = root / "入口.md"
            source.write_text(
                "[下载](ftp://example.com/file)\n"
                "[应用](app://project/item)\n",
                encoding="utf-8",
            )
            self.assertEqual(check.markdown_link_errors(root), [])


if __name__ == "__main__":
    unittest.main()
