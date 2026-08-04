// Generate the four deterministic tool PNGs on hosts without Python.
import { deflateSync } from "node:zlib";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const colors = {
  clear: [0, 0, 0, 0],
  outline: [36, 46, 51, 255],
  leaf: [80, 126, 75, 255],
  soil: [139, 91, 59, 255],
  soilLight: [181, 125, 75, 255],
  woodDark: [82, 53, 42, 255],
  wood: [139, 83, 54, 255],
  cream: [244, 226, 159, 255],
  blue: [70, 126, 180, 255],
  highlight: [236, 239, 224, 255],
};

function crc32(buffer) {
  let crc = 0xffffffff;
  for (const byte of buffer) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) {
      crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0);
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function chunk(kind, data) {
  const name = Buffer.from(kind);
  const size = Buffer.alloc(4);
  size.writeUInt32BE(data.length);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE(crc32(Buffer.concat([name, data])));
  return Buffer.concat([size, name, data, checksum]);
}

function canvas() {
  const pixels = Array.from({ length: 16 * 16 }, () => [...colors.clear]);
  const rect = (x, y, width, height, color) => {
    for (let row = Math.max(0, y); row < Math.min(16, y + height); row++) {
      for (let column = Math.max(0, x); column < Math.min(16, x + width); column++) {
        pixels[row * 16 + column] = color;
      }
    }
  };
  return { pixels, rect };
}

function tool(kind) {
  const image = canvas();
  const { rect } = image;
  if (kind === "seed") {
    rect(4, 4, 8, 10, colors.cream); rect(5, 3, 6, 2, colors.wood);
    rect(6, 7, 4, 5, colors.soil); rect(7, 6, 2, 2, colors.leaf);
  } else if (kind === "fertilizer") {
    rect(3, 5, 10, 9, colors.cream); rect(4, 3, 8, 3, colors.blue);
    rect(6, 8, 4, 4, colors.leaf);
  } else if (kind === "shovel") {
    rect(3, 2, 4, 3, colors.woodDark); rect(4, 4, 3, 7, colors.wood);
    rect(7, 9, 4, 2, colors.outline); rect(9, 10, 4, 4, colors.highlight);
    rect(10, 14, 3, 1, colors.outline);
  } else {
    rect(4, 6, 8, 8, colors.outline); rect(5, 5, 2, 8, colors.cream);
    rect(7, 3, 2, 9, colors.cream); rect(9, 4, 2, 8, colors.cream);
    rect(11, 6, 2, 6, colors.cream); rect(2, 8, 4, 3, colors.cream);
    rect(5, 13, 7, 2, colors.soilLight);
  }
  return image.pixels;
}

function savePng(path, pixels) {
  const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  const header = Buffer.alloc(13);
  header.writeUInt32BE(16, 0);
  header.writeUInt32BE(16, 4);
  header.set([8, 6, 0, 0, 0], 8);
  const rows = [];
  for (let y = 0; y < 16; y++) {
    rows.push(Buffer.from([0, ...pixels.slice(y * 16, (y + 1) * 16).flat()]));
  }
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, Buffer.concat([
    signature,
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(Buffer.concat(rows), { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]));
}

for (const kind of ["seed", "fertilizer", "shovel", "hand"]) {
  savePng(resolve(root, "runtime", "tools", `${kind}.png`), tool(kind));
}
