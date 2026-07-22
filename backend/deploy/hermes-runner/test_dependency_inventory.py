from email.message import Message
import json
from pathlib import Path
import tempfile
import types
import unittest

import dependency_inventory


class DependencyInventoryTests(unittest.TestCase):
    def test_license_expression_has_priority(self):
        metadata = _metadata(
            name="example",
            expression="MIT OR Apache-2.0",
            declared="UNKNOWN",
            classifiers=["License :: OSI Approved :: BSD License"],
        )

        self.assertEqual(
            dependency_inventory.resolve_license(metadata),
            "MIT OR Apache-2.0",
        )

    def test_unknown_declared_license_falls_back_to_osi_classifier(self):
        metadata = _metadata(
            name="example",
            declared="UNKNOWN",
            classifiers=["License :: OSI Approved :: ISC License (ISCL)"],
        )

        self.assertEqual(
            dependency_inventory.resolve_license(metadata),
            "ISC License (ISCL)",
        )

    def test_unknown_or_unreviewed_license_fails_closed(self):
        with self.assertRaises(dependency_inventory.InventoryError):
            dependency_inventory.collect_package_records(
                [_distribution("unknown", "1.0.0", declared="UNKNOWN")]
            )

        with self.assertRaises(dependency_inventory.InventoryError):
            dependency_inventory.collect_package_records(
                [_distribution("copyleft", "1.0.0", expression="GPL-3.0")]
            )

    def test_mpl_dependency_is_retained_with_file_level_policy(self):
        records = dependency_inventory.collect_package_records(
            [_distribution("certifi", "2026.5.20", expression="MPL-2.0")]
        )

        self.assertEqual(len(records), 1)
        self.assertEqual(records[0].policy, "mpl_file_level")

    def test_outputs_are_sorted_and_cyclonedx_links_match(self):
        records = dependency_inventory.collect_package_records(
            [
                _distribution("Zeta_Package", "2.0.0", expression="MIT"),
                _distribution("alpha.package", "1.0.0", expression="BSD-3-Clause"),
            ]
        )
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            licenses = Path(root) / "licenses.json"
            sbom = Path(root) / "sbom.cdx.json"
            dependency_inventory.write_json(
                licenses, dependency_inventory.build_license_inventory(records)
            )
            dependency_inventory.write_json(
                sbom, dependency_inventory.build_cyclonedx(records)
            )
            license_data = json.loads(licenses.read_text(encoding="utf-8"))
            sbom_data = json.loads(sbom.read_text(encoding="utf-8"))

        self.assertEqual(
            [item["name"] for item in license_data["python_packages"]],
            ["alpha-package", "zeta-package"],
        )
        self.assertEqual(sbom_data["bomFormat"], "CycloneDX")
        self.assertEqual(
            sbom_data["components"][0]["bom-ref"],
            sbom_data["components"][0]["purl"],
        )


def _distribution(name, version, *, expression="", declared="", classifiers=None):
    return types.SimpleNamespace(
        metadata=_metadata(
            name=name,
            expression=expression,
            declared=declared,
            classifiers=classifiers,
        ),
        version=version,
    )


def _metadata(name, *, expression="", declared="", classifiers=None):
    metadata = Message()
    metadata["Name"] = name
    if expression:
        metadata["License-Expression"] = expression
    if declared:
        metadata["License"] = declared
    for classifier in classifiers or []:
        metadata["Classifier"] = classifier
    return metadata


if __name__ == "__main__":
    unittest.main()
