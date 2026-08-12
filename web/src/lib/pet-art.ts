import villageDog from '../../../frontend/src/assets/art/runtime/pets/village-dog.png'
import villageDogSad from '../../../frontend/src/assets/art/runtime/pets/village-dog-sad.png'
import shepherdDog from '../../../frontend/src/assets/art/runtime/pets/shepherd-dog.png'
import shepherdDogSad from '../../../frontend/src/assets/art/runtime/pets/shepherd-dog-sad.png'

export type DeployedPet = {
  petId: number
  name: string
  foodActiveUntilMs: bigint
}

// Pet IDs come from the server's development pet config: 1 田园犬, 2 牧羊犬.
// Unknown pets borrow the village dog rather than rendering nothing.
const SPRITES_BY_PET_ID: Record<number, { fed: string; hungry: string }> = {
  1: { fed: villageDog, hungry: villageDogSad },
  2: { fed: shepherdDog, hungry: shepherdDogSad },
}

export function petSprite(petId: number, hungry: boolean): string {
  const pair = SPRITES_BY_PET_ID[petId] ?? SPRITES_BY_PET_ID[1]
  return hungry ? pair.hungry : pair.fed
}

export function deployedPetFromPublic(view?: {
  activePetId?: number
  petName?: string
  foodActiveUntilMs?: bigint
} | null): DeployedPet | undefined {
  const petId = view?.activePetId ?? 0
  if (!petId) {
    return undefined
  }
  return {
    petId,
    name: view?.petName || `宠物#${petId}`,
    foodActiveUntilMs: view?.foodActiveUntilMs ?? 0n,
  }
}
