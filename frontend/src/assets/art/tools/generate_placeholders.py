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
    "carrot": (232, 126, 48, 255),
    "carrot_dark": (186, 88, 32, 255),
    "carrot_light": (245, 166, 88, 255),
    "radish": (238, 240, 230, 255),
    "radish_shade": (194, 202, 190, 255),
    "tomato_light": (226, 104, 88, 255),
    "eggplant": (110, 66, 148, 255),
    "eggplant_light": (150, 104, 186, 255),
    "pumpkin": (236, 146, 54, 255),
    "pumpkin_dark": (196, 104, 32, 255),
    "melon": (86, 152, 78, 255),
    "melon_dark": (46, 98, 54, 255),
    "grape": (124, 88, 172, 255),
    "grape_dark": (86, 58, 132, 255),
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
        # No crop is drawn into the bed: the client overlays the sprite of the
        # crop that actually grew there, so the base only marks "ready".
        for x, y in ((3, 9), (26, 9), (3, 22), (26, 22)):
            c.rect(x, y, 3, 3, C["gold"]).rect(x, y, 2, 1, C["cream"])
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


def leafy_top(c: Canvas) -> Canvas:
    """Shared three-frond top for root crops."""
    return (
        c.rect(4, 1, 2, 4, C["leaf"])
        .rect(7, 0, 2, 5, C["green_dark"])
        .rect(10, 1, 2, 4, C["leaf"])
        .rect(5, 3, 6, 2, C["leaf"])
    )


def crop_mature(kind: str) -> Canvas:
    """One mature sprite per crop.

    At 16 px only silhouette and hue survive, so every crop keeps a distinct
    outline plus its own colour ramp; light comes from the upper left.
    """
    c = Canvas(16, 16)
    if kind == "carrot":
        leafy_top(c)
        c.rect(4, 5, 8, 3, C["carrot"]).rect(5, 8, 6, 3, C["carrot"])
        c.rect(6, 11, 4, 2, C["carrot"]).rect(7, 13, 2, 2, C["carrot_dark"])
        c.rect(5, 5, 2, 2, C["carrot_light"]).rect(10, 6, 1, 4, C["carrot_dark"])
    elif kind == "white-radish":
        leafy_top(c)
        c.rect(4, 5, 8, 4, C["radish"]).rect(5, 9, 6, 3, C["radish"])
        c.rect(6, 12, 4, 2, C["radish"]).rect(7, 14, 2, 1, C["radish_shade"])
        c.rect(5, 5, 2, 3, C["highlight"]).rect(10, 6, 1, 5, C["radish_shade"])
    elif kind == "corn":
        c.rect(2, 4, 3, 8, C["leaf"]).rect(11, 4, 3, 8, C["leaf"])
        c.rect(7, 0, 2, 2, C["straw"])
        c.rect(5, 2, 6, 12, C["gold"]).rect(5, 2, 2, 11, C["cream"])
        for x, y in ((7, 4), (9, 6), (7, 8), (9, 10), (7, 12)):
            c.put(x, y, C["straw"])
    elif kind == "tomato":
        c.rect(7, 1, 2, 3, C["green_dark"]).rect(5, 3, 6, 2, C["leaf"])
        c.rect(5, 5, 6, 1, C["red"]).rect(4, 6, 8, 6, C["red"]).rect(5, 12, 6, 1, C["red"])
        c.rect(5, 6, 2, 2, C["tomato_light"])
    elif kind == "potato":
        c.rect(6, 2, 4, 2, C["leaf"]).rect(4, 3, 2, 2, C["leaf"]).rect(10, 3, 2, 2, C["leaf"])
        c.rect(5, 6, 6, 1, C["soil_light"]).rect(4, 7, 8, 5, C["soil_light"])
        c.rect(5, 12, 6, 1, C["soil"])
        for x, y in ((6, 9), (9, 8), (8, 11)):
            c.put(x, y, C["soil"])
        c.rect(5, 7, 2, 2, C["straw"])
    elif kind == "eggplant":
        c.rect(7, 0, 2, 3, C["green_dark"]).rect(5, 2, 6, 2, C["leaf"])
        c.rect(5, 4, 6, 1, C["eggplant"]).rect(4, 5, 8, 8, C["eggplant"])
        c.rect(5, 13, 6, 1, C["eggplant"])
        c.rect(5, 6, 2, 3, C["eggplant_light"])
    elif kind == "strawberry":
        c.rect(7, 0, 2, 2, C["green_dark"]).rect(4, 2, 8, 2, C["leaf"])
        c.rect(4, 4, 8, 4, C["red"]).rect(5, 8, 6, 2, C["red"])
        c.rect(6, 10, 4, 2, C["red"]).rect(7, 12, 2, 1, C["red"])
        for x, y in ((5, 5), (9, 5), (7, 7), (6, 9), (9, 8)):
            c.put(x, y, C["cream"])
    elif kind == "pumpkin":
        c.rect(7, 1, 2, 3, C["wood_dark"]).rect(9, 2, 2, 1, C["leaf"])
        c.rect(3, 4, 10, 1, C["pumpkin"]).rect(2, 5, 12, 7, C["pumpkin"])
        c.rect(3, 12, 10, 1, C["pumpkin"])
        c.rect(5, 5, 1, 7, C["pumpkin_dark"]).rect(10, 5, 1, 7, C["pumpkin_dark"])
        c.rect(3, 6, 1, 3, C["straw"])
    elif kind == "watermelon":
        c.rect(7, 2, 2, 2, C["wood_dark"])
        c.rect(4, 4, 8, 1, C["melon"]).rect(3, 5, 10, 7, C["melon"])
        c.rect(4, 12, 8, 1, C["melon"])
        for x in (5, 8, 11):
            c.rect(x, 5, 1, 7, C["melon_dark"])
        c.rect(4, 6, 1, 3, C["grass_light"])
    elif kind == "grape":
        c.rect(9, 0, 4, 3, C["leaf"]).rect(7, 1, 1, 4, C["wood_dark"])
        c.rect(4, 5, 9, 3, C["grape"]).rect(5, 8, 7, 3, C["grape"])
        c.rect(6, 11, 5, 2, C["grape"]).rect(7, 13, 2, 1, C["grape_dark"])
        for x in (6, 9, 12):
            c.rect(x, 5, 1, 3, C["grape_dark"])
        for x in (7, 10):
            c.rect(x, 8, 1, 3, C["grape_dark"])
        c.rect(4, 5, 2, 2, C["eggplant_light"])
    else:
        return crop("mature")
    return c


def dog(breed: str, mood: str) -> Canvas:
    """Sitting guard dog, 32x32, front view.

    The two breeds must differ at a glance (coat colour plus the shepherd's
    white blaze), and the mood must read without text: fed dogs keep their ears
    and tail up with an open mouth, a hungry dog droops all three.
    """
    if breed == "shepherd-dog":
        coat, coat_dark, coat_light = C["outline"], (24, 32, 36, 255), (72, 84, 90, 255)
        belly = C["highlight"]
    else:
        coat, coat_dark, coat_light = C["straw"], C["wood"], C["gold"]
        belly = C["cream"]
    sad = mood == "sad"

    c = Canvas(32, 32)
    if sad:
        c.rect(5, 9, 4, 8, coat_dark).rect(23, 9, 4, 8, coat_dark)
    else:
        c.rect(6, 2, 4, 8, coat_dark).rect(22, 2, 4, 8, coat_dark)

    c.rect(10, 4, 12, 1, coat).rect(9, 5, 14, 12, coat).rect(9, 16, 14, 2, coat_dark)
    if breed == "shepherd-dog":
        c.rect(15, 4, 3, 9, belly)

    if sad:
        c.rect(11, 8, 3, 1, coat_dark).rect(18, 8, 3, 1, coat_dark)
        c.put(13, 11, C["outline"]).put(14, 10, C["outline"])
        c.put(18, 10, C["outline"]).put(19, 11, C["outline"])
        c.rect(20, 12, 1, 2, C["blue"])
    else:
        c.rect(13, 9, 2, 3, C["outline"]).rect(18, 9, 2, 3, C["outline"])
        c.put(13, 9, C["highlight"]).put(18, 9, C["highlight"])

    c.rect(13, 13, 7, 4, belly).rect(15, 13, 3, 2, C["outline"])
    if sad:
        c.rect(14, 16, 5, 1, C["outline"]).put(13, 15, C["outline"]).put(19, 15, C["outline"])
    else:
        c.rect(15, 16, 3, 1, C["outline"]).rect(16, 17, 2, 2, C["red"])

    c.rect(11, 18, 11, 9, coat).rect(13, 19, 6, 7, belly)
    c.rect(9, 26, 6, 3, coat_light).rect(17, 26, 6, 3, coat_light)
    if sad:
        c.rect(23, 23, 4, 3, coat).rect(26, 25, 3, 4, coat_dark)
    else:
        c.rect(23, 17, 3, 4, coat).rect(25, 13, 3, 5, coat_dark)
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


def tool(kind: str) -> Canvas:
    if kind == "seed":
        return icon("seed")
    if kind == "fertilizer":
        return icon("fertilizer")

    c = Canvas(16, 16)
    if kind == "shovel":
        c.rect(3, 2, 4, 3, C["wood_dark"]).rect(4, 4, 3, 7, C["wood"])
        c.rect(7, 9, 4, 2, C["outline"]).rect(9, 10, 4, 4, C["highlight"])
        c.rect(10, 14, 3, 1, C["outline"])
    elif kind == "hand":
        c.rect(4, 6, 8, 8, C["outline"]).rect(5, 5, 2, 8, C["cream"])
        c.rect(7, 3, 2, 9, C["cream"]).rect(9, 4, 2, 8, C["cream"])
        c.rect(11, 6, 2, 6, C["cream"]).rect(2, 8, 4, 3, C["cream"])
        c.rect(5, 13, 7, 2, C["soil_light"])
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


MATURE_CROPS = [
    "carrot", "white-radish", "corn", "tomato", "potato",
    "eggplant", "strawberry", "pumpkin", "watermelon", "grape",
]

ASSETS = [
    ("terrain.grass", "terrain/grass.png", terrain("grass"), "local-mvp-placeholders", "top-left"),
    ("terrain.path", "terrain/path.png", terrain("path"), "local-mvp-placeholders", "top-left"),
    ("terrain.fence", "terrain/fence.png", terrain("fence"), "local-mvp-placeholders", "bottom-center"),
    *[(f"plot.{s}", f"plots/{s}.png", plot(s), "local-mvp-placeholders", "bottom-center")
      for s in ("empty", "growing", "mature", "need-cleanup", "selected", "disabled")],
    *[(f"crop.demo-{s}", f"crops/demo-{s}.png", crop(s), "local-mvp-placeholders", "bottom-center")
      for s in ("seedling", "growing", "near-mature", "mature")],
    *[(f"crop.{s}-mature", f"crops/{s}-mature.png", crop_mature(s), "local-mvp-placeholders", "bottom-center")
      for s in MATURE_CROPS],
    *[(f"pet.{breed}{'-sad' if mood == 'sad' else ''}", f"pets/{breed}{'-sad' if mood == 'sad' else ''}.png",
       dog(breed, mood), "local-mvp-placeholders", "bottom-center")
      for breed in ("village-dog", "shepherd-dog") for mood in ("fed", "sad")],
    ("item.demo-seed", "items/demo-seed.png", icon("seed"), "local-mvp-placeholders", "center"),
    ("item.demo-crop", "items/demo-crop.png", icon("crop"), "local-mvp-placeholders", "center"),
    ("item.fertilizer-basic", "items/fertilizer-basic.png", icon("fertilizer"), "local-mvp-placeholders", "center"),
    ("item.coin", "items/coin.png", icon("coin"), "local-mvp-placeholders", "center"),
    *[(f"tool.{s}", f"tools/{s}.png", tool(s), "local-mvp-placeholders", "center")
      for s in ("seed", "fertilizer", "shovel", "hand")],
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

    # Reviewing per-crop silhouettes needs them side by side and large.
    crops = ["demo"] + MATURE_CROPS
    crop_sheet = Canvas(len(crops) * 20 + 4, 24, C["green_dark"])
    for index, name in enumerate(crops):
        canvas = crop("mature") if name == "demo" else crop_mature(name)
        crop_sheet.blit(canvas, 4 + index * 20, 4)
    crop_sheet.scaled(6).save(ROOT / "references/crop-mature-6x.png")

    pet_sheet = Canvas(4 * 36 + 4, 40, C["green_dark"])
    for index, (breed, mood) in enumerate(
        (b, m) for b in ("village-dog", "shepherd-dog") for m in ("fed", "sad")
    ):
        pet_sheet.blit(dog(breed, mood), 4 + index * 36, 4)
    pet_sheet.scaled(4).save(ROOT / "references/pets-4x.png")


if __name__ == "__main__":
    main()
