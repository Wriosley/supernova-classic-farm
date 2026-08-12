import cropMatureDemo from '../../../frontend/src/assets/art/runtime/crops/demo-mature.png'
import cropMatureCarrot from '../../../frontend/src/assets/art/runtime/crops/carrot-mature.png'
import cropMatureWhiteRadish from '../../../frontend/src/assets/art/runtime/crops/white-radish-mature.png'
import cropMatureCorn from '../../../frontend/src/assets/art/runtime/crops/corn-mature.png'
import cropMatureTomato from '../../../frontend/src/assets/art/runtime/crops/tomato-mature.png'
import cropMaturePotato from '../../../frontend/src/assets/art/runtime/crops/potato-mature.png'
import cropMatureEggplant from '../../../frontend/src/assets/art/runtime/crops/eggplant-mature.png'
import cropMatureStrawberry from '../../../frontend/src/assets/art/runtime/crops/strawberry-mature.png'
import cropMaturePumpkin from '../../../frontend/src/assets/art/runtime/crops/pumpkin-mature.png'
import cropMatureWatermelon from '../../../frontend/src/assets/art/runtime/crops/watermelon-mature.png'
import cropMatureGrape from '../../../frontend/src/assets/art/runtime/crops/grape-mature.png'

// Crop IDs come from the server's development crop config. A crop the client
// has no sprite for still renders — it falls back to the demo crop — so adding
// a crop server-side never blanks out a mature plot.
const MATURE_SPRITE_BY_CROP_ID: Record<number, string> = {
  2001: cropMatureDemo,
  2002: cropMatureCarrot,
  2003: cropMatureWhiteRadish,
  2004: cropMatureCorn,
  2005: cropMatureTomato,
  2006: cropMaturePotato,
  2007: cropMatureEggplant,
  2008: cropMatureStrawberry,
  2009: cropMaturePumpkin,
  2010: cropMatureWatermelon,
  2011: cropMatureGrape,
}

export function matureCropSprite(cropId: number): string {
  return MATURE_SPRITE_BY_CROP_ID[cropId] ?? cropMatureDemo
}
