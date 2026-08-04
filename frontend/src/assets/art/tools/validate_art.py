"""Validate the engine-neutral art manifest and PNG runtime files."""

from __future__ import annotations

import json
import struct
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def png_info(path: Path) -> tuple[int, int, int]:
    data = path.read_bytes()
    if data[:8] != b"\x89PNG\r\n\x1a\n" or data[12:16] != b"IHDR":
        raise ValueError("not a PNG")
    width, height, bit_depth, color_type = struct.unpack(">IIBB", data[16:26])
    return width, height, color_type


def main() -> int:
    errors: list[str] = []
    manifest_path = ROOT / "manifest.json"
    ledger = (ROOT / "licenses/SOURCES.md").read_text(encoding="utf-8")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    ids: set[str] = set()

    if manifest.get("logicalTile") != 16 or manifest.get("filter") != "nearest":
        errors.append("manifest must retain the 16px/nearest baseline")

    for asset in manifest.get("assets", []):
        asset_id = asset.get("id", "")
        if asset_id in ids:
            errors.append(f"duplicate id: {asset_id}")
        ids.add(asset_id)
        if not asset_id or asset_id.lower() != asset_id or " " in asset_id:
            errors.append(f"invalid id: {asset_id!r}")

        path = ROOT / asset.get("file", "")
        if not path.is_file():
            errors.append(f"{asset_id}: missing {path.relative_to(ROOT)}")
            continue
        try:
            width, height, color_type = png_info(path)
        except (OSError, ValueError, struct.error) as exc:
            errors.append(f"{asset_id}: {exc}")
            continue
        if (width, height) != (asset.get("width"), asset.get("height")):
            errors.append(f"{asset_id}: manifest dimensions do not match PNG")
        if color_type not in (4, 6):
            errors.append(f"{asset_id}: PNG lacks an alpha channel")
        frame_width = asset.get("frameWidth", width)
        frame_height = asset.get("frameHeight", height)
        if width % frame_width or height % frame_height:
            errors.append(f"{asset_id}: sheet is not divisible by frame dimensions")
        if asset.get("source", "") not in ledger:
            errors.append(f"{asset_id}: source is absent from license ledger")

    runtime_pngs = {p.relative_to(ROOT).as_posix() for p in (ROOT / "runtime").rglob("*.png")}
    listed_pngs = {a["file"] for a in manifest.get("assets", [])}
    for orphan in sorted(runtime_pngs - listed_pngs):
        errors.append(f"unlisted runtime PNG: {orphan}")

    if errors:
        print("Art validation failed:")
        for error in errors:
            print(f"- {error}")
        return 1
    print(f"Art validation passed: {len(ids)} assets, {len(runtime_pngs)} PNG files.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
