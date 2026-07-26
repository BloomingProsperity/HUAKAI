#!/usr/bin/env python3
"""项目治理校验器的判别性测试。"""

from __future__ import annotations

import re
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

    def test_frontend_capability_counts_exclude_page_specs_and_reject_aliases(self) -> None:
        text = """
### 19.2.2 首装、登录和账号安全
| 原子能力与身份 | 入口 | 数据 | 状态 |
| --- | --- | --- | --- |
| 登录 | /login | session | 已有可接；待做前端。 |
| 安全设置 | /security | secret | 已有但未接 UI；待组合。 |
#### 19.8.1.1 租户管理页面规格
| 视图或动作 | 必须展示 | 交互与失败合同 |
| --- | --- | --- |
| 列表 | 名称 | 不属于原子能力统计。 |
### 19.9 资金、订阅和用户自助
| 功能 | 当前证据 | 状态 | 后端差量 |
| --- | --- | --- | --- |
| 钱包 | /wallet | 后端部分具备 | 待补聚合。 |
### 19.11 全局前端交互合同
"""
        counts, errors = check.frontend_capability_counts(text)
        self.assertEqual(errors, [])
        self.assertEqual(
            counts,
            {
                "已有可接": 1,
                "已有但未接 UI": 1,
                "后端部分具备": 1,
            },
        )

        alias_text = text.replace("已有可接；", "已有 API；", 1)
        _, alias_errors = check.frontend_capability_counts(alias_text)
        self.assertEqual(len(alias_errors), 1)
        self.assertIn("标准状态", alias_errors[0])

        missing_status = text.replace("已有可接；待做前端。", "待做前端。", 1)
        _, missing_errors = check.frontend_capability_counts(missing_status)
        self.assertEqual(len(missing_errors), 1)
        self.assertIn("标准状态", missing_errors[0])

        dual_status = text.replace(
            "已有可接；待做前端。",
            "已有可接；已有但未接 UI；待做前端。",
            1,
        )
        _, dual_errors = check.frontend_capability_counts(dual_status)
        self.assertEqual(len(dual_errors), 1)
        self.assertIn("标准状态", dual_errors[0])

    def test_frontend_capability_baseline_rejects_coordinated_shrink(self) -> None:
        handbook_path = Path(__file__).resolve().parents[2] / "docs/HUAKAI工程设计手册.md"
        handbook = handbook_path.read_text(encoding="utf-8")
        self.assertEqual(check.frontend_capability_contract_errors(handbook), [])

        start = handbook.index("### 19.2.2 首装、登录和账号安全")
        end = handbook.index("### 19.11 全局前端交互合同", start)
        lines = handbook[start:end].splitlines(keepends=True)
        removed = False
        for index, line in enumerate(lines):
            if "已有可接" in line and line.startswith("|"):
                del lines[index]
                removed = True
                break
        self.assertTrue(removed, "测试素材应至少有一项“已有可接”能力")
        shrunk = handbook[:start] + "".join(lines) + handbook[end:]
        shrunk = re.sub(
            r"^\| 已有可接 \| 56 \|",
            "| 已有可接 | 55 |",
            shrunk,
            count=1,
            flags=re.MULTILINE,
        )
        shrunk = re.sub(
            r"^\| \*\*合计\*\* \| \*\*158\*\* \|",
            "| **合计** | **157** |",
            shrunk,
            count=1,
            flags=re.MULTILINE,
        )

        errors = check.frontend_capability_contract_errors(shrunk)
        self.assertTrue(
            any("已有可接 期望 56，实际 55" in error for error in errors),
            errors,
        )
        self.assertTrue(
            any("期望 158，实际 157" in error for error in errors),
            errors,
        )


if __name__ == "__main__":
    unittest.main()
