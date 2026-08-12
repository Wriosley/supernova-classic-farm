# MVP art inventory

Status values: `ready` means a reviewed runtime placeholder exists, `gap`
means business/configuration input is still required, and `later` is outside
the first single-player slice.

## Farm scene and plots

| Asset ID | Scope | Size | Frames | Anchor | Business mapping | Status |
|---|---|---:|---:|---|---|---|
| `terrain.grass` | MVP | 16x16 | 1 | top-left | farm background | ready |
| `terrain.path` | MVP | 16x16 | 1 | top-left | navigation decoration | ready |
| `terrain.fence` | MVP | 16x16 | 1 | bottom-center | farm boundary | ready |
| `plot.empty` | MVP | 32x32 | 1 | bottom-center | `EMPTY` | ready |
| `plot.growing` | MVP | 32x32 | 1 | bottom-center | `GROWING` base | ready |
| `plot.mature` | MVP | 32x32 | 1 | bottom-center | `MATURE` base (bed + ready markers only; the crop sprite is overlaid) | ready |
| `plot.need-cleanup` | MVP | 32x32 | 1 | bottom-center | `NEED_CLEANUP` | ready |
| `plot.selected` | MVP | 32x32 | 1 | bottom-center | selected plot overlay | ready |
| `plot.disabled` | MVP | 32x32 | 1 | bottom-center | rejected action overlay | ready |
| farmhouse/warehouse/shop | optional | 32x48 each | 1 | bottom-center | screen landmarks | gap |
| trees/rocks/flowers | optional | 16x16/32x32 | 1 | bottom-center | scene decoration | gap |

## Crops and inventory

The business design does not yet name the first-chapter crop or the
second-chapter reward crop. `crop.demo-*` is an explicit placeholder and must
be remapped only after configuration chooses real crop IDs.

The `crop.<name>-mature` sprites do map to configured crops: the client picks
one by `crop_id` and falls back to `crop.demo-mature` for any crop it does not
know, so a server-side crop addition never blanks a mature plot. Seedling and
growing stages are still shared across crops.

| Asset ID | Scope | Size | Frames | Anchor | Business mapping | Status |
|---|---|---:|---:|---|---|---|
| `crop.demo-seedling` | MVP | 16x16 | 1 | bottom-center | early display ratio | ready |
| `crop.demo-growing` | MVP | 16x16 | 1 | bottom-center | middle display ratio | ready |
| `crop.demo-near-mature` | MVP | 16x16 | 1 | bottom-center | late display ratio | ready |
| `crop.demo-mature` | MVP | 16x16 | 1 | bottom-center | mature visual, crop 2001 and unknown crops | ready |
| `crop.carrot-mature` | MVP | 16x16 | 1 | bottom-center | 胡萝卜, crop 2002 | ready |
| `crop.white-radish-mature` | MVP | 16x16 | 1 | bottom-center | 白萝卜, crop 2003 | ready |
| `crop.corn-mature` | MVP | 16x16 | 1 | bottom-center | 玉米, crop 2004 | ready |
| `crop.tomato-mature` | MVP | 16x16 | 1 | bottom-center | 番茄, crop 2005 | ready |
| `crop.potato-mature` | MVP | 16x16 | 1 | bottom-center | 土豆, crop 2006 | ready |
| `crop.eggplant-mature` | MVP | 16x16 | 1 | bottom-center | 茄子, crop 2007 | ready |
| `crop.strawberry-mature` | MVP | 16x16 | 1 | bottom-center | 草莓, crop 2008 | ready |
| `crop.pumpkin-mature` | MVP | 16x16 | 1 | bottom-center | 南瓜, crop 2009 | ready |
| `crop.watermelon-mature` | MVP | 16x16 | 1 | bottom-center | 西瓜, crop 2010 | ready |
| `crop.grape-mature` | MVP | 16x16 | 1 | bottom-center | 葡萄, crop 2011 | ready |
| `item.demo-seed` | MVP | 16x16 | 1 | center | shop/warehouse seed | ready |
| `item.demo-crop` | MVP | 16x16 | 1 | center | harvested/sold item | ready |
| `item.fertilizer-basic` | MVP | 16x16 | 1 | center | initial and reward fertilizer | ready |
| `item.coin` | MVP | 16x16 | 1 | center | currency | ready |
| chapter-two crop set | MVP blocker | same as above | 6 | varies | reward seed and next chapter | gap |

## Pets

The deployed pet sits next to the farm. The client picks the sprite by
`pet_id`, and swaps to the `-sad` variant once `food_active_until_ms` has
passed so a hungry guard dog is readable without its caption.

| Asset ID | Scope | Size | Frames | Anchor | Business mapping | Status |
|---|---|---:|---:|---|---|---|
| `pet.village-dog` | MVP | 32x32 | 1 | bottom-center | 田园犬, pet 1, food active | ready |
| `pet.village-dog-sad` | MVP | 32x32 | 1 | bottom-center | 田园犬, pet 1, hungry | ready |
| `pet.shepherd-dog` | MVP | 32x32 | 1 | bottom-center | 牧羊犬, pet 2, food active | ready |
| `pet.shepherd-dog-sad` | MVP | 32x32 | 1 | bottom-center | 牧羊犬, pet 2, hungry | ready |

The client swaps the seedling/growing/near-mature sprites from elapsed-time
ratio. Those stages are display-only: they do not create server state,
`player_seq`, or Dirty writes.

## UI and feedback

| Asset ID | Scope | Size | Frames | Anchor | Business mapping | Status |
|---|---|---:|---:|---|---|---|
| `ui.panel` | MVP | 16x16 | 1 | top-left | 9-slice panel source | ready |
| `ui.button-primary` | MVP | 32x16 | 1 | center | actionable button | ready |
| `ui.slot` | MVP | 18x18 | 1 | center | shop/warehouse/task reward | ready |
| `ui.check` | MVP | 16x16 | 1 | center | completed task | ready |
| `ui.lock` | MVP | 16x16 | 1 | center | unavailable action | ready |
| `ui.mail` | MVP | 16x16 | 1 | center | reward mail pending | ready |
| `ui.warning` | MVP | 16x16 | 1 | center | error/resync state | ready |
| `ui.connection` | MVP | 16x16 | 1 | center | connecting/reconnecting | ready |
| `tool.seed` | MVP | 16x16 | 1 | center | plant tool and desktop cursor | ready |
| `tool.fertilizer` | MVP | 16x16 | 1 | center | fertilize tool and desktop cursor | ready |
| `tool.shovel` | MVP | 16x16 | 1 | center | cleanup tool and desktop cursor | ready |
| `tool.hand` | MVP | 16x16 | 1 | center | harvest tool and desktop cursor | ready |
| `effect.fertilized` | MVP | 16x16 | 2 | center | active speed buff | ready |
| purchase/plant/harvest/sell feedback | MVP | CSS + icons | n/a | n/a | successful commands | ready |
| mature/reward/cleanup burst | optional | 16x16 | 2-6 | center | state transition feedback | gap |

The five chapter tasks reuse item/action icons with CSS progress and status;
there is no separate rasterized text. Login forms, countdown text, quantities,
errors, and state-resync copy are HTML/CSS.

## Deferred assets

- Pests, catch-pest actions, and friend avatars are `later`.
- Cross-player fertilization, weather, pets, random-yield effects, seasonal
  terrain, animals, and complete player animation sets are `later`.
- Architecture-only concepts such as Zone, Shard, Dirty Queue, and Coordinator
  have no player-facing art requirement.

## Required product decisions before final art

1. First- and second-chapter crop IDs and colors.
2. Four initial plots in a responsive 2x2 farm grid are selected for the MVP.
3. Portrait/landscape design viewport and safe-area policy.
4. Vue DOM/CSS rendering is selected for the first interactive H5 slice.
