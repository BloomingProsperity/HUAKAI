import argparse
import json
import re
import sys
from dataclasses import dataclass
from importlib.metadata import Distribution, distributions
from pathlib import Path
from typing import Iterable
from urllib.parse import quote


_MEANINGLESS_LICENSES = {
    "",
    "DUAL LICENSE",
    "LICENSE",
    "N/A",
    "NONE",
    "SEE LICENSE FILE",
    "SEE LICENSE IN LICENSE",
    "UNKNOWN",
}
_ALLOWED_LICENSE_PARTS = (
    re.compile(r"MIT(?: LICENSE|-0|-CMU)?", re.IGNORECASE),
    re.compile(
        r"APACHE(?: 2\.0|-2\.0| LICENSE, VERSION 2\.0| SOFTWARE LICENSE)",
        re.IGNORECASE,
    ),
    re.compile(r"BSD(?: LICENSE|-[23]-CLAUSE)?", re.IGNORECASE),
    re.compile(r"ISC LICENSE \(ISCL\)", re.IGNORECASE),
    re.compile(r"MPL-2\.0", re.IGNORECASE),
    re.compile(
        r"MOZILLA PUBLIC LICENSE 2\.0 \(MPL 2\.0\)", re.IGNORECASE
    ),
    re.compile(r"PSF-2\.0", re.IGNORECASE),
)


class InventoryError(RuntimeError):
    pass


@dataclass(frozen=True)
class PackageRecord:
    name: str
    version: str
    license: str
    policy: str


def resolve_license(metadata) -> str:
    expression = str(metadata.get("License-Expression") or "").strip()
    if expression:
        return expression

    declared = str(metadata.get("License") or "").strip()
    if declared.upper() not in _MEANINGLESS_LICENSES:
        return declared

    classifiers = {
        value.rsplit("::", 1)[-1].strip()
        for value in (metadata.get_all("Classifier") or [])
        if value.startswith("License :: OSI Approved ::")
    }
    classifiers.discard("")
    if classifiers:
        return " OR ".join(sorted(classifiers))
    raise InventoryError("依赖许可证缺失或无法判定")


def license_is_allowed(value: str) -> bool:
    parts = re.split(r"\s+(?:AND|OR)\s+", value.strip(), flags=re.IGNORECASE)
    return bool(parts) and all(
        any(pattern.fullmatch(part.strip()) for pattern in _ALLOWED_LICENSE_PARTS)
        for part in parts
    )


def collect_package_records(
    installed: Iterable[Distribution] | None = None,
) -> list[PackageRecord]:
    records: dict[str, PackageRecord] = {}
    for distribution in installed if installed is not None else distributions():
        raw_name = str(distribution.metadata.get("Name") or "").strip()
        version = str(distribution.version or "").strip()
        if not raw_name or not version:
            raise InventoryError("依赖名称或版本缺失")
        name = canonical_package_name(raw_name)
        license_value = resolve_license(distribution.metadata)
        if not license_is_allowed(license_value):
            raise InventoryError(
                f"依赖 {raw_name}=={version} 的许可证未通过允许清单：{license_value}"
            )
        record = PackageRecord(
            name=name,
            version=version,
            license=license_value,
            policy=(
                "mpl_file_level"
                if "MPL" in license_value.upper()
                or "MOZILLA PUBLIC LICENSE" in license_value.upper()
                else "permissive"
            ),
        )
        previous = records.get(name)
        if previous is not None and previous != record:
            raise InventoryError(f"依赖 {name} 存在多个版本或许可证声明")
        records[name] = record
    if not records:
        raise InventoryError("未发现可写入清单的 Python 依赖")
    return [records[name] for name in sorted(records)]


def canonical_package_name(value: str) -> str:
    return re.sub(r"[-_.]+", "-", value).lower()


def build_license_inventory(records: list[PackageRecord]) -> dict:
    return {
        "schema_version": 1,
        "component": "huakai-hermes-runner",
        "python_packages": [
            {
                "name": record.name,
                "version": record.version,
                "license": record.license,
                "policy": record.policy,
            }
            for record in records
        ],
    }


def build_cyclonedx(records: list[PackageRecord]) -> dict:
    components = []
    for record in records:
        purl = f"pkg:pypi/{record.name}@{quote(record.version, safe='.-_+')}"
        components.append(
            {
                "type": "library",
                "bom-ref": purl,
                "name": record.name,
                "version": record.version,
                "purl": purl,
                "licenses": [{"license": {"name": record.license}}],
                "properties": [
                    {"name": "huakai:license-policy", "value": record.policy}
                ],
            }
        )
    return {
        "$schema": "https://cyclonedx.org/schema/bom-1.5.schema.json",
        "bomFormat": "CycloneDX",
        "specVersion": "1.5",
        "version": 1,
        "metadata": {
            "component": {
                "type": "application",
                "name": "huakai-hermes-runner",
                "version": "0.19.0",
            }
        },
        "components": components,
    }


def write_json(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--sbom", required=True, type=Path)
    parser.add_argument("--licenses", required=True, type=Path)
    args = parser.parse_args(argv)
    try:
        records = collect_package_records()
        write_json(args.sbom, build_cyclonedx(records))
        write_json(args.licenses, build_license_inventory(records))
    except (InventoryError, OSError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
