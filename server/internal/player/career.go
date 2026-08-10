package player

import (
	"fmt"
	"math"
	"sort"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

func ensureCareer(state *State) *datav1.PlayerCareerRecord {
	if state.Career == nil {
		state.Career = &datav1.PlayerCareerRecord{}
	}
	return state.Career
}

func ensureCompendium(state *State) *datav1.CropCompendiumRecord {
	if state.CropCompendium == nil {
		state.CropCompendium = &datav1.CropCompendiumRecord{}
	}
	return state.CropCompendium
}

func checkedAddUint64(current uint64, delta uint64) (uint64, error) {
	if delta > math.MaxUint64-current {
		return current, fmt.Errorf("uint64 overflow")
	}
	return current + delta, nil
}

func addHarvestedCareer(state *State, quantity uint32) error {
	if quantity == 0 {
		return nil
	}
	career := ensureCareer(state)
	next, err := checkedAddUint64(career.TotalHarvestedCropQuantity, uint64(quantity))
	if err != nil {
		return err
	}
	career.TotalHarvestedCropQuantity = next
	return nil
}

func addStolenCareer(state *State, quantity uint32) error {
	if quantity == 0 {
		return nil
	}
	career := ensureCareer(state)
	next, err := checkedAddUint64(career.TotalStolenCropQuantity, uint64(quantity))
	if err != nil {
		return err
	}
	career.TotalStolenCropQuantity = next
	return nil
}

func unlockCrop(state *State, cropID uint32) {
	if cropID == 0 {
		return
	}
	compendium := ensureCompendium(state)
	for _, id := range compendium.UnlockedCropIds {
		if id == cropID {
			return
		}
	}
	compendium.UnlockedCropIds = append(compendium.UnlockedCropIds, cropID)
	sort.Slice(compendium.UnlockedCropIds, func(i, j int) bool {
		return compendium.UnlockedCropIds[i] < compendium.UnlockedCropIds[j]
	})
}

func careerView(state *State) *wsv1.PlayerCareerView {
	if state == nil || state.Career == nil {
		return &wsv1.PlayerCareerView{}
	}
	return &wsv1.PlayerCareerView{
		TotalHarvestedCropQuantity: state.Career.TotalHarvestedCropQuantity,
		TotalStolenCropQuantity:    state.Career.TotalStolenCropQuantity,
	}
}

func compendiumView(state *State) *wsv1.CropCompendiumView {
	if state == nil || state.CropCompendium == nil {
		return &wsv1.CropCompendiumView{}
	}
	return &wsv1.CropCompendiumView{
		UnlockedCropIds: append([]uint32(nil), state.CropCompendium.UnlockedCropIds...),
	}
}

func normalizeCompendium(record *datav1.CropCompendiumRecord) *datav1.CropCompendiumRecord {
	if record == nil {
		return nil
	}
	out := &datav1.CropCompendiumRecord{
		UnlockedCropIds: append([]uint32(nil), record.UnlockedCropIds...),
	}
	sort.Slice(out.UnlockedCropIds, func(i, j int) bool {
		return out.UnlockedCropIds[i] < out.UnlockedCropIds[j]
	})
	unique := out.UnlockedCropIds[:0]
	var last uint32
	for i, id := range out.UnlockedCropIds {
		if id == 0 {
			continue
		}
		if i > 0 && id == last {
			continue
		}
		unique = append(unique, id)
		last = id
	}
	out.UnlockedCropIds = unique
	return out
}
