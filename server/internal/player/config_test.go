package player

import (
	"testing"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
)

func TestConfigSnapshotRejectsInvalidAndDuplicateShopEntries(t *testing.T) {
	tests := []struct {
		name    string
		version uint64
		entries []ShopEntry
	}{
		{name: "missing version"},
		{name: "invalid entry", version: 1, entries: []ShopEntry{{ShopEntryID: 1}}},
		{name: "duplicate entry", version: 1, entries: []ShopEntry{
			{ShopEntryID: 1, ItemID: 1, UnitPrice: 1, PriceVersion: 1},
			{ShopEntryID: 1, ItemID: 2, UnitPrice: 1, PriceVersion: 1},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewConfigSnapshot(test.version, test.entries); err == nil {
				t.Fatal("NewConfigSnapshot succeeded for invalid input")
			}
		})
	}
}

func TestConfigSnapshotRejectsInvalidAndDuplicateCropSeeds(t *testing.T) {
	valid := CropConfig{
		SeedItemID: 1, CropID: 2, CropItemID: 3, ConfigVersion: 1,
		MaturityValueScaled9: 100, BaseGrowthRateScaled6: 10, BaseYield: 1,
	}
	if _, err := NewConfigSnapshotWithCrops(1, nil, []CropConfig{{SeedItemID: 1}}); err == nil {
		t.Fatal("invalid crop config was accepted")
	}
	if _, err := NewConfigSnapshotWithCrops(1, nil, []CropConfig{valid, valid}); err == nil {
		t.Fatal("duplicate crop seed was accepted")
	}
}

func TestConfigSnapshotRejectsInvalidAndDuplicateFertilizers(t *testing.T) {
	valid := FertilizerConfig{
		ItemID: 1, ConfigVersion: 1, ModifierScaled6: 500_000, DurationMS: 60_000,
	}
	if _, err := NewConfigSnapshotWithContent(1, nil, nil, []FertilizerConfig{{ItemID: 1}}); err == nil {
		t.Fatal("invalid fertilizer config was accepted")
	}
	if _, err := NewConfigSnapshotWithContent(1, nil, nil, []FertilizerConfig{valid, valid}); err == nil {
		t.Fatal("duplicate fertilizer item was accepted")
	}
}

func TestConfigSnapshotRejectsInvalidAndDuplicateSellRules(t *testing.T) {
	valid := SellRule{
		ShopEntryID: 2, ItemID: 1002, UnitPrice: 5, PriceVersion: 9,
	}
	if _, err := NewConfigSnapshotWithEconomy(
		1, nil, nil, nil, []SellRule{{ShopEntryID: 2, ItemID: 1002}},
	); err == nil {
		t.Fatal("invalid sell rule was accepted")
	}
	if _, err := NewConfigSnapshotWithEconomy(
		1, nil, nil, nil, []SellRule{valid, valid},
	); err == nil {
		t.Fatal("duplicate sell item was accepted")
	}
	if _, err := NewConfigSnapshotWithEconomy(
		1,
		[]ShopEntry{{ShopEntryID: 2, ItemID: 1001, UnitPrice: 2, PriceVersion: 8}},
		nil, nil, []SellRule{valid},
	); err == nil {
		t.Fatal("duplicate buy/sell shop entry ID was accepted")
	}
}

func TestDevelopmentShopIncludesSeedCropAndFertilizerQuotesInStableOrder(t *testing.T) {
	entries := NewDevelopmentConfigSnapshot().ActiveShopEntries()
	if len(entries) != 3 ||
		entries[0].GetShopEntryId() != developmentShopEntryID ||
		entries[1].GetShopEntryId() != developmentSellEntryID ||
		entries[1].GetItemId() != developmentCropItemID ||
		entries[1].GetUnitPrice() != developmentCropSellUnitPrice ||
		entries[1].GetPriceVersion() != developmentCropSellPriceVersion ||
		entries[2].GetShopEntryId() != developmentFertilizerShopEntryID ||
		entries[2].GetItemId() != BasicFertilizerID ||
		entries[2].GetUnitPrice() != developmentFertilizerUnitPrice ||
		entries[2].GetPriceVersion() != developmentFertilizerPriceVersion {
		t.Fatalf("development shop entries = %+v", entries)
	}
}

func TestDevelopmentChapterTwoDefinesFriendTasks(t *testing.T) {
	chapter, exists := NewDevelopmentConfigSnapshot().Chapter(developmentNextChapterID)
	if !exists || len(chapter.Tasks) != 3 {
		t.Fatalf("chapter two tasks = %+v", chapter.Tasks)
	}
	want := []datav1.TaskMetric{
		datav1.TaskMetric_TASK_ADD_FRIEND,
		datav1.TaskMetric_TASK_STEAL_CROP,
		datav1.TaskMetric_TASK_APPLY_PEST_TO_FRIEND,
	}
	for index, task := range chapter.Tasks {
		if task.Target != 1 || taskMetric(task.ID) != want[index] {
			t.Fatalf("chapter two task %d = %+v", index, task)
		}
	}
}

func TestConfigSnapshotRejectsInvalidChapterGraph(t *testing.T) {
	if _, err := NewConfigSnapshotWithChapters(
		1, nil, nil, nil, nil,
		[]ChapterConfig{{ChapterID: 1, ConfigVersion: 1, NextChapterID: 2}},
	); err == nil {
		t.Fatal("missing next chapter was accepted")
	}
	if _, err := NewConfigSnapshotWithChapters(
		1, nil, nil, nil, nil,
		[]ChapterConfig{{
			ChapterID: 1, ConfigVersion: 1,
			Tasks: []Task{{ID: 1, Target: 1}, {ID: 1, Target: 2}},
		}},
	); err == nil {
		t.Fatal("duplicate chapter task was accepted")
	}
	if _, err := NewConfigSnapshotWithChapters(
		1, nil, nil, nil, nil,
		[]ChapterConfig{{
			ChapterID: 1, ConfigVersion: 1,
			RewardItems: []RewardItem{{ItemID: 1, Quantity: 1}, {ItemID: 1, Quantity: 2}},
		}},
	); err == nil {
		t.Fatal("duplicate chapter reward item was accepted")
	}
}
