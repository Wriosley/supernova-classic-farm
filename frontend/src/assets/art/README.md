# Classic Farm pixel-art workspace

This directory owns art used by the H5 client. The current baseline is a
top-down 16 x 16 pixel grid for the accepted single-player loop.

## Directory roles

- `source/`: editable originals and downloaded source packages. Runtime code
  must not import from here.
- `runtime/`: reviewed PNG and JSON files that client code may import.
- `references/`: visual references and contact sheets; never runtime inputs.
- `licenses/`: source, license, attribution, and modification records.
- `inventory.md`: required assets mapped to business states.
- `manifest.json`: stable asset IDs and machine-readable runtime metadata.

## Runtime specification

- Use lossless RGBA PNG for sprites and pixel UI.
- Use a 16 x 16 logical tile. Larger objects must use whole-tile dimensions.
- Render only at integer scale (normally 3x or 4x), at integer coordinates,
  with nearest-neighbor filtering and mipmaps disabled.
- Sprite sheets use equal cells. Packed atlases require JSON Hash metadata,
  at least 1 px spacing, and should stay within 1024 x 1024 for the MVP.
- UI text stays in HTML/CSS; do not bake localized words into images.
- Use lowercase kebab-case filenames. Application code refers to stable IDs
  from `manifest.json`, not to source-pack filenames.
- Do not use JPEG, lossy WebP, or GIF for pixel sprites. A lossless WebP may
  be generated later as an optional delivery variant while PNG remains the
  reference.

## Source admission

1. Save the creator page URL and the license text available at download time.
2. Confirm commercial use, modification, attribution, and redistribution
   conditions. Search-result summaries are not license evidence.
3. Scan archives and inspect dimensions before extraction.
4. Choose one visual baseline. Re-palette and re-outline any secondary source.
5. Record every runtime derivative in `licenses/SOURCES.md`.

Downloaded packs with a no-redistribution condition must not be committed as
raw archives or loose source sprites. Only integrated game derivatives may be
committed when their license permits it.

## Visual baseline

- Camera: top-down/three-quarter top-down, never side-view platformer.
- Pixel density: 16 x 16 terrain and item cells.
- Light: upper-left.
- Outline: dark colored outline, not pure black.
- Palette: the local MVP placeholder palette in `source/palette.gpl`.
- State clarity is more important than decoration. `EMPTY`, `GROWING`,
  `MATURE`, and `NEED_CLEANUP` plots must remain distinguishable without text.

## Export and review

The repository currently has no selected renderer. MVP files therefore remain
engine-neutral individual PNGs plus a manifest. Atlas conversion happens only
after the Vue rendering approach is chosen.

Run `python frontend/src/assets/art/tools/validate_art.py` from the repository
root to validate the manifest, PNG dimensions, transparency, IDs, and source
records. The validator uses only the Python standard library.
