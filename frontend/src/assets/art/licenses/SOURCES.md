# Art source and license ledger

Checked: 2026-07-31. A candidate is not approved merely because it appears in
this ledger. Before importing external pixels, retain the license bundled with
the actual download and re-check that its terms match the recorded page.

## Approved runtime sources

### local-mvp-placeholders

- Source: generated specifically for this repository by the project AI agent
  from geometric pixel primitives; no third-party image was supplied as input.
- Files: all current files under `runtime/`.
- License status: project-owned development placeholders.
- AI record: generated 2026-07-30, extended with four tool icons on
  2026-07-31, extended with ten per-crop mature sprites plus a crop-free
  mature plot bed on 2026-08-12, and extended with four guard-dog sprites
  (two breeds, fed and hungry) on 2026-08-12, all from the deterministic local
  Python script; no image-generation model and no third-party
  training-restricted source.
- Modification: replace freely when a final visual baseline is selected.

## Audited external candidates

### Kettoman — Free Pixel Farming Base Pack

- URL: https://kettoman.itch.io/pixel-farming-base-pack-animals-crops-tileset
- Page claims: 16x16 transparent PNG; free personal/commercial use; credit
  appreciated but not required; no resale or redistribution as-is.
- Coverage: grass, dirt, tilled/wet soil, three multi-stage crops, nature,
  three animated animals.
- Fit: best content match among reviewed free 16x16 packs.
- Decision: `candidate`, not downloaded. The page download is gated through
  itch.io and redistribution terms make committing its raw pack inappropriate.
  Obtain it manually from the creator page and retain its bundled license.

### Cocophany — Bloomseed

- URL: https://cocophany.itch.io/bloomseed
- Page claims: 16x16 top-down; commercial modification allowed; attribution
  required; pack and modified pack contents may not be redistributed.
- Coverage: grass, paths, tilled/wet soil, trees, rocks, crates and props.
- Fit: strong environment supplement, but attribution and style-normalization
  work make it secondary.
- Decision: `secondary-candidate`, not downloaded.

### VectoRaith — Farming Sim Asset Pack

- URL: https://vectoraith.itch.io/farming-sim-asset-pack
- Page claims: commercial use and modification allowed; no redistribution,
  resale, sublicensing, NFT use, or AI training.
- Coverage: 23 four-stage crops, buildings, farmer, animals, four seasons;
  16x16 originals plus 32x32 and 48x48 upscales.
- Fit: most complete pack, but its no-redistribution condition and broad scope
  exceed the current MVP.
- Decision: `reserve-candidate`, not downloaded. Never upload this pack or a
  derivative as a reference to an image-generation service.

### Kenney — Pixel UI Pack

- URL: https://kenney.nl/assets/pixel-ui-pack
- Alternate page: https://kenney-assets.itch.io/ui-pack
- Page claims: CC0 1.0 Universal; commercial use, modification, and use
  without attribution are allowed.
- Coverage: panels, buttons, cursors, bars, check marks, scrollers; separate
  sprites and source files.
- Fit: safest candidate for final pixel UI after the frontend viewport and
  9-slice requirements are known.
- Decision: `approved-candidate`, not downloaded in this pass because the
  official download flow is interactive. Prefer the official Kenney source.

### Kenney — Pixel Platformer: Farm Expansion

- Official record: https://opengameart.org/content/pixel-platformer-farm-expansion
- Audit archive: downloaded to the operating-system temporary directory only;
  it is deliberately not committed.
- SHA-256: `72C5EE0DDA3DFA1B95FF25C74A7FF6878E58851276A7766551218CBD55DA6D61`.
- Bundled license: CC0 1.0; commercial use is explicitly allowed and credit is
  optional.
- Contents checked: 125 archive entries, including 112 separate RGBA PNG
  tiles; every separate tile is 18x18, plus packed sheets and Construct files.
- Decision: `rejected-for-mvp`. Despite the safe license, it is a side-view
  platformer pack on an 18px grid, not the selected top-down 16px baseline.
  This verifies why license alone is not enough to admit a pack.

### Jofra — Mini Farm Asset Pack

- URL: https://jofra.itch.io/mini-farm
- Page claims: CC0/public domain; commercial use without attribution; no
  resale or redistribution as individual files.
- Coverage: tiles, crops, character, animals, farm buildings.
- Fit: content match, but the page is marked deactivated/WIP and the archive
  is unusually small.
- Decision: `fallback-only`.

### OpenGameArt — LPC Crops

- URL: https://opengameart.org/content/lpc-crops
- Page claims: fifty crops with five growth frames; the complete attribution
  statement in `CREDITS-crops.txt` is mandatory.
- Coverage: excellent crop breadth at LPC 32x32 scale.
- Fit: poor fit with the selected 16x16 baseline and higher attribution/
  repixeling cost.
- Decision: `rejected-for-mvp`.

## Admission checklist

- [ ] Download from the creator/official host, not a mirror.
- [ ] Save bundled LICENSE/CREDITS and a dated creator-page record.
- [ ] Scan archive; reject executable or unexpected file types.
- [ ] Record image dimensions, alpha mode, frame layout, and palette.
- [ ] Confirm commercial, modification, attribution, and redistribution terms.
- [ ] Confirm whether committing extracted files is allowed.
- [ ] Compare outline, light direction, saturation, and pixel density.
- [ ] Add each accepted derivative to `manifest.json`.
