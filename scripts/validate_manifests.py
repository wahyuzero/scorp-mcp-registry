#!/usr/bin/env python3
"""validate_manifests.py — validates every servers/*/manifest.json against
schema/v1.json and cross-checks the registry.json index.

Exit 0 = everything consistent; exit 1 = any validation failure.
"""

import json
import sys
from pathlib import Path

import jsonschema

ROOT = Path(__file__).resolve().parent.parent
SCHEMA = ROOT / "schema" / "v1.json"
REGISTRY = ROOT / "registry.json"
SERVERS = ROOT / "servers"


def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def main() -> int:
    failures = []

    schema = load(SCHEMA)
    validator = jsonschema.Draft7Validator(schema)

    entries = []
    for manifest_path in sorted(SERVERS.glob("*/manifest.json")):
        name_dir = manifest_path.parent.name
        try:
            manifest = load(manifest_path)
        except json.JSONDecodeError as exc:
            failures.append(f"{manifest_path}: invalid JSON ({exc})")
            continue

        errors = sorted(validator.iter_errors(manifest), key=lambda e: e.json_path)
        for err in errors:
            failures.append(f"{manifest_path}: {err.json_path}: {err.message}")

        if manifest.get("name") != name_dir:
            failures.append(
                f"{manifest_path}: name '{manifest.get('name')}' does not match directory '{name_dir}'"
            )
        entries.append(
            {
                "id": manifest.get("id", name_dir),
                "name": manifest.get("name", name_dir),
                "version": manifest.get("version"),
                "description": manifest.get("description"),
                "origin": manifest.get("origin"),
                "source_dir": f"servers/{name_dir}",
                "health": manifest.get("health", {}).get("status"),
                "tools": [t.get("name") for t in manifest.get("tools", [])],
                "drift": manifest.get("drift", {}).get("status", "unknown"),
                "manifest_url": (
                    "https://raw.githubusercontent.com/wahyuzero/scorp-mcp-registry/main/"
                    f"servers/{name_dir}/manifest.json"
                ),
                "artifacts": manifest.get("artifacts", {}),
            }
        )
        if manifest.get("variant"):
            entries[-1]["variant"] = manifest["variant"]

    if REGISTRY.exists():
        registry = load(REGISTRY)
        indexed = {e["name"] for e in registry.get("servers", [])}
        on_disk = {e["name"] for e in entries}
        for missing in sorted(on_disk - indexed):
            failures.append(f"registry.json: server '{missing}' exists on disk but is not indexed")
        for orphan in sorted(indexed - on_disk):
            failures.append(f"registry.json: server '{orphan}' is indexed but has no manifest")
    else:
        failures.append("registry.json: index file missing")

    if failures:
        print("MANIFEST VALIDATION FAILED:")
        for f in failures:
            print(f"  - {f}")
        return 1

    print(f"All {len(entries)} manifests valid and indexed in registry.json.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
