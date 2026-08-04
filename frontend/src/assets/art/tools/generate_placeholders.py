"""Generate deterministic, project-owned MVP pixel-art placeholders."""

from __future__ import annotations

import json
import struct
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RUNTIME = ROOT / "runtime"

C = {
    "clear": (0, 0, 0, 0),
    "outline": (36, 46, 51, 255),
    "green_dark": (62, 73, 62, 255),
    "leaf": (80, 126, 75, 255),
    "grass": (111, 166, 88, 255),
    "grass_light": (157, 200, 104, 255),
    "soil_dark": (91, 63, 48, 255),
    "soil": (139, 91, 59, 255),
    "soil_light": (181, 125, 75, 255),
    "wood_dark": (82, 53, 42, 255),
    "wood": (139, 83, 54, 255),
    "straw": (215, 160, 82, 255),
    "gold": (238, 198, 93, 255),
    "cream": (244, 226, 159, 255),
    "red": (193, 63, 60, 255),
    "blue": (70, 126, 180, 255),
    "highlight": (236, 239, 224, 255),
    "shade": (36, 46, 51, 150),
}


class Canvas:
    def __init__(self, width: int, height: int, fill=C["clear"]):
        self.width, self.height = width, height
        self.pixels = [fill] * (width * height)

    def rect(self, x: int, y: int, w: int, h: int, color):
        for yy in range(max(0, y), min(self.height, y + h)):
            for xx in range(max(0, x), min(self.width, x + w)):
                self.pixels[yy * self.width + xx] = color
        return self

    def put(self, x: int, y: int, color):
        return self.rect(x, y, 1, 1, color)

    def blit(self, other: "Canvas", x: int, y: int):
        for yy in range(other.height):
            for xx in range(other.width):
                color = other.pixels[yy * other.width + xx]
                if color[3]:
                    self.put(x + xx, y + yy, color)
        return self

    def scaled(self, factor: int) -> "Canvas":
        output = Canvas(self.width * factor, self.height * factor)
        for y in range(self.height):
            for x in range(self.width):
                output.rect(
                    x * factor,
                    y * factor,
                    factor,
                    factor,
                    self.pixels[y * self.width + x],
                )
        return output

    def save(self, path: Path):
        path.parent.mkdir(parents=True, exist_ok=True)
        raw = b"".join(
            b"\x00" + bytes(v for px in self.pixels[y * self.width:(y + 1) * self.width] for v in px)
            for y in range(self.height)
        )

        def chunk(kind, data):
            return struct.pack(">I", len(data)) + kind + data + struct.pack(
                ">I", zlib.crc32(kind + data) & 0xFFFFFFFF
            )

        png = b"\x89PNG\r\n\x1a\n"
        png += chunk(b"IHDR", struct.pack(">IIBBBBB", self.width, self.height, 8, 6, 0, 0, 0))
        png += chunk(b"IDAT", zlib.compress(raw, 9))
        png += chunk(b"IEND", b"")
        path.write_bytes(png)


def terrain(kind: str) -> Canvas:
    c = Canvas(16, 16, C["grass"])
    for x, y in ((2, 3), (11, 2), (7, 9), (14, 12), (3, 14)):
        c.put(x, y, C["grass_light"])
    if kind == "path":
        c.rect(0, 4, 16, 9, C["soil"]).rect(0, 4, 16, 1, C["soil_light"])
        c.rect(0, 12, 16, 1, C["soil_dark"])
    elif kind == "fence":
        c.rect(2, 2, 3, 14, C["wood_dark"]).rect(3, 2, 1, 12, C["wood"])
        c.rect(11, 2, 3, 14, C["wood_dark"]).rect(12, 2, 1, 12, C["wood"])
        c.rect(0, 6, 16, 4, C["wood_dark"]).rect(0, 7, 16, 2, C["wood"])
    return c


def plot(state: str) -> Canvas:
    c = Canvas(32, 32)
    c.rect(2, 8, 28, 21, C["soil_dark"]).rect(3, 7, 26, 20, C["soil"])
    for y in (11, 17, 23):
        c.rect(5, y, 22, 2, C["soil_light"])
    if state == "growing":
        for x in (9, 16, 23):
            c.rect(x, 16, 2, 7, C["green_dark"]).rect(x - 2, 15, 2, 3, C["leaf"]).rect(x + 2, 14, 2, 3, C["leaf"])
    elif state == "mature":
        for x in (9, 16, 23):
            c.rect(x, 13, 2, 10, C["green_dark"]).rect(x - 2, 11, 6, 5, C["gold"]).rect(x - 1, 10, 4, 1, C["cream"])
    elif state == "need-cleanup":
        for x in (8, 15, 22):
            c.rect(x, 17, 2, 7, C["straw"]).rect(x - 2, 16, 6, 2, C["straw"])
    elif state == "selected":
        c.rect(0, 5, 32, 2, C["highlight"]).rect(0, 29, 32, 2, C["highlight"])
        c.rect(0, 5, 2, 26, C["highlight"]).rect(30, 5, 2, 26, C["highlight"])
    elif state == "disabled":
        for i in range(4, 28):
            c.rect(i, i, 2, 2, C["red"]).rect(30 - i, i, 2, 2, C["red"])
    return c


def crop(stage: str) -> Canvas:
    c = Canvas(16, 16)
    if stage == "seedling":
        c.rect(7, 10, 2, 5, C["green_dark"]).rect(4, 8, 3, 3, C["leaf"]).rect(9, 7, 3, 3, C["leaf"])
    elif stage == "growing":
        c.rect(7, 5, 2, 10, C["green_dark"])
        c.rect(3, 7, 4, 3, C["leaf"]).rect(9, 5, 4, 3, C["leaf"]).rect(4, 11, 3, 2, C["leaf"])
    elif stage == "near-mature":
        c.rect(7, 4, 2, 11, C["green_dark"])
        c.rect(3, 5, 4, 3, C["leaf"]).rect(9, 4, 4, 3, C["leaf"])
        c.rect(5, 8, 6, 5, C["gold"]).rect(6, 7, 4, 1, C["cream"])
    else:
        c.rect(7, 3, 2, 12, C["green_dark"])
        c.rect(2, 5, 4, 3, C["leaf"]).rect(10, 4, 4, 3, C["leaf"])
        c.rect(4, 7, 8, 7, C["gold"]).rect(5, 6, 6, 2, C["cream"]).rect(6, 13, 4, 1, C["soil_dark"])
    return c


def icon(kind: str) -> Canvas:
    c = Canvas(16, 16)
    if kind == "seed":
        c.rect(4, 4, 8, 10, C["cream"]).rect(5, 3, 6, 2, C["wood"]).rect(6, 7, 4, 5, C["soil"])
        c.rect(7, 6, 2, 2, C["leaf"])
    elif kind == "crop":
        c.rect(4, 5, 8, 8, C["gold"]).rect(5, 4, 6, 2, C["cream"]).rect(7, 2, 2, 3, C["green_dark"])
    elif kind == "fertilizer":
        c.rect(3, 5, 10, 9, C["cream"]).rect(4, 3, 8, 3, C["blue"]).rect(6, 8, 4, 4, C["leaf"])
    elif kind == "coin":
        c.rect(3, 4, 10, 9, C["soil_dark"]).rect(3, 3, 10, 9, C["gold"]).rect(5, 5, 6, 5, C["cream"]).rect(7, 6, 2, 3, C["gold"])
    elif kind == "check":
        c.rect(2, 7, 3, 3, C["leaf"]).rect(5, 9, 3, 3, C["leaf"]).rect(8, 6, 3, 3, C["leaf"]).rect(11, 3, 3, 3, C["leaf"])
    elif kind == "lock":
        c.rect(3, 7, 10, 8, C["gold"]).rect(5, 3, 6, 6, C["outline"]).rect(7, 5, 2, 3, C["clear"])
    elif kind == "mail":
        c.rect(2, 4, 12, 9, C["cream"]).rect(3, 5, 5, 4, C["gold"]).rect(8, 5, 5, 4, C["gold"]).rect(6, 8, 4, 3, C["wood"])
    elif kind == "warning":
        c.rect(7, 2, 2, 2, C["gold"]).rect(5, 4, 6, 3, C["gold"]).rect(3, 7, 10, 4, C["gold"]).rect(2, 11, 12, 3, C["gold"])
        c.rect(7, 6, 2, 4, C["outline"]).rect(7, 11, 2, 2, C["outline"])
    elif kind == "connection":
        c.rect(2, 4, 3, 3, C["blue"]).rect(5, 2, 6, 3, C["blue"]).rect(11, 4, 3, 3, C["blue"])
        c.rect(5, 7, 6, 3, C["blue"]).rect(7, 11, 2, 3, C["blue"])
    return c


def ui(kind: str) -> Canvas:
    if kind == "button":
        c = Canvas(32, 16)
        return c.rect(1, 2, 30, 12, C["outline"]).rect(2, 2, 28, 10, C["wood"]).rect(3, 3, 26, 2, C["soil_light"])
    size = 18 if kind == "slot" else 16
    c = Canvas(size, size)
    c.rect(0, 0, size, size, C["outline"]).rect(2, 2, size - 4, size - 4, C["cream"])
    if kind == "panel":
        c.rect(3, 3, size - 6, size - 6, C["wood"])
    return c


ASSETS = [
    ("terrain.grass", "terrain/grass.png", terrain("grass"), "local-mvp-placeholders", "top-left"),
    ("terrain.path", "terrain/path.png", terrain("path"), "local-mvp-placeholders", "top-left"),
    ("terrain.fence", "terrain/fence.png", terrain("fence"), "local-mvp-placeholders", "bottom-center"),
    *[(f"plot.{s}", f"plots/{s}.png", plot(s), "local-mvp-placeholders", "bottom-center")
      for s in ("empty", "growing", "mature", "need-cleanup", "selected", "disabled")],
    *[(f"crop.demo-{s}", f"crops/demo-{s}.png", crop(s), "local-mvp-placeholders", "bottom-center")
      for s in ("seedling", "growing", "near-mature", "mature")],
    ("item.demo-seed", "items/demo-seed.png", icon("seed"), "local-mvp-placeholders", "center"),
    ("item.demo-crop", "items/demo-crop.png", icon("crop"), "local-mvp-placeholders", "center"),
    ("item.fertilizer-basic", "items/fertilizer-basic.png", icon("fertilizer"), "local-mvp-placeholders", "center"),
    ("item.coin", "items/coin.png", icon("coin"), "local-mvp-placeholders", "center"),
    ("ui.panel", "ui/panel.png", ui("panel"), "local-mvp-placeholders", "top-left"),
    ("ui.button-primary", "ui/button-primary.png", ui("button"), "local-mvp-placeholders", "center"),
    ("ui.slot", "ui/slot.png", ui("slot"), "local-mvp-placeholders", "center"),
    *[(f"ui.{s}", f"ui/{s}.png", icon(s), "local-mvp-placeholders", "center")
      for s in ("check", "lock", "mail", "warning", "connection")],
]


def main():
    entries = []
    for asset_id, relative, canvas, source, anchor in ASSETS:
        canvas.save(RUNTIME / relative)
        entries.append({
            "id": asset_id,
            "file": f"runtime/{relative}",
            "width": canvas.width,
            "height": canvas.height,
            "frames": 1,
            "anchor": anchor,
            "source": source,
            "status": "placeholder",
        })

    effect = Canvas(32, 16)
    effect.blit(icon("fertilizer"), 0, 0).blit(icon("fertilizer"), 16, 0)
    effect.rect(25, 1, 2, 2, C["highlight"]).rect(29, 5, 1, 1, C["highlight"])
    effect.save(RUNTIME / "effects/fertilized.png")
    entries.append({
        "id": "effect.fertilized", "file": "runtime/effects/fertilized.png",
        "width": 32, "height": 16, "frames": 2, "frameWidth": 16,
        "frameHeight": 16, "anchor": "center",
        "source": "local-mvp-placeholders", "status": "placeholder",
    })

    manifest = {
        "schemaVersion": 1,
        "logicalTile": 16,
        "filter": "nearest",
        "assets": sorted(entries, key=lambda item: item["id"]),
    }
    (ROOT / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")

    sheet = Canvas(256, 160, C["green_dark"])
    x = y = 8
    for _, _, canvas, _, _ in ASSETS:
        if x + max(canvas.width, 32) > 248:
            x, y = 8, y + 40
        sheet.blit(canvas, x, y)
        x += max(canvas.width, 32) + 8
    sheet.save(ROOT / "references/mvp-contact-sheet.png")
    sheet.scaled(4).save(ROOT / "references/mvp-contact-sheet-4x.png")


if __name__ == "__main__":
    main()
