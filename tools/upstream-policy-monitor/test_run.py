#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("run.py")
SPEC = importlib.util.spec_from_file_location("upstream_policy_monitor_run", MODULE_PATH)
assert SPEC is not None
monitor = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = monitor
assert SPEC.loader is not None
SPEC.loader.exec_module(monitor)


class UpstreamPolicyMonitorTest(unittest.TestCase):
    def test_scan_source_finds_policy_terms_in_html_fixture(self) -> None:
        target = monitor.TARGETS[0]
        fixture = Path(monitor.DEFAULT_FIXTURE_DIR) / target.fixture
        hits = monitor.scan_source(target, fixture.read_text(encoding="utf-8"), monitor.DEFAULT_KEYWORDS, 8)
        keywords = {hit.keyword.casefold() for hit in hits}

        self.assertIn("oauth", keywords)
        self.assertIn("third-party", keywords)
        self.assertIn("subscription", keywords)

    def test_dry_run_main_writes_alert_to_selected_dir(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            code = monitor.main(["--date", "2026-05-16", "--output-dir", temp_dir])

            self.assertEqual(code, 0)
            alert_path = Path(temp_dir) / "2026-05-16-upstream-policy-alert.md"
            self.assertTrue(alert_path.exists())
            alert = alert_path.read_text(encoding="utf-8")
            self.assertIn("Anthropic API news", alert)
            self.assertIn("Google Cloud AI blog", alert)
            self.assertIn("Tool boundary", alert)

    def test_no_hits_do_not_write_alert(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            path = monitor.write_alert(
                hits=[],
                fetch_errors=[],
                output_dir=Path(temp_dir),
                run_date="2026-05-16",
                live=False,
                keywords=monitor.DEFAULT_KEYWORDS,
            )

            self.assertIsNone(path)
            self.assertFalse((Path(temp_dir) / "2026-05-16-upstream-policy-alert.md").exists())


if __name__ == "__main__":
    unittest.main()
