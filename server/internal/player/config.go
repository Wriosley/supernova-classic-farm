package player

import (
	"errors"
	"sort"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

const (
	developmentShopEntryID               uint32 = 5001
	developmentSeedItemID                uint32 = 1001
	developmentSeedUnitPrice             int64  = 2
	developmentSeedPriceVersion          uint64 = 8
	developmentCropID                    uint32 = 2001
	developmentCropItemID                uint32 = 1002
	developmentMaturityScaled9           int64  = 100_000_000_000
	developmentGrowthRateScaled6         int64  = 1_000_000
	developmentBaseYield                 uint32 = 3
	developmentFertilizerModifierScaled6 int64  = 500_000
	developmentFertilizerDurationMS      int64  = 60_000
	developmentSellEntryID               uint32 = 5002
	developmentCropSellUnitPrice         int64  = 5
	developmentCropSellPriceVersion      uint64 = 9
	developmentNextChapterID             uint32 = 2
	developmentNextSeedItemID            uint32 = 1003
	developmentChapterRewardCoins        int64  = 10
)

type ShopEntry struct {
	ShopEntryID  uint32
	ItemID       uint32
	UnitPrice    int64
	PriceVersion uint64
	Enabled      bool
}

type CropConfig struct {
	SeedItemID            uint32
	CropID                uint32
	CropItemID            uint32
	ConfigVersion         uint64
	MaturityValueScaled9  int64
	BaseGrowthRateScaled6 int64
	BaseYield             uint32
	Enabled               bool
}

type FertilizerConfig struct {
	ItemID          uint32
	ConfigVersion   uint64
	ModifierScaled6 int64
	DurationMS      int64
	Enabled         bool
}

type SellRule struct {
	ShopEntryID  uint32
	ItemID       uint32
	UnitPrice    int64
	PriceVersion uint64
	Enabled      bool
}

type RewardItem struct {
	ItemID   uint32
	Quantity uint32
}

type ChapterConfig struct {
	ChapterID     uint32
	ConfigVersion uint64
	Tasks         []Task
	RewardCoins   int64
	RewardItems   []RewardItem
	NextChapterID uint32
}

func (r SellRule) View() *wsv1.ShopEntryView {
	return &wsv1.ShopEntryView{
		ShopEntryId: r.ShopEntryID, ItemId: r.ItemID, UnitPrice: r.UnitPrice,
		PriceVersion: r.PriceVersion, Enabled: r.Enabled,
	}
}

func (e ShopEntry) View() *wsv1.ShopEntryView {
	return &wsv1.ShopEntryView{
		ShopEntryId:  e.ShopEntryID,
		ItemId:       e.ItemID,
		UnitPrice:    e.UnitPrice,
		PriceVersion: e.PriceVersion,
		Enabled:      e.Enabled,
	}
}

// ConfigSnapshot is immutable after construction so one command can safely pin
// one pointer while a later publication atomically replaces the Zone snapshot.
type ConfigSnapshot struct {
	version     uint64
	shopEntries map[uint32]ShopEntry
	cropsBySeed map[uint32]CropConfig
	fertilizers map[uint32]FertilizerConfig
	sellRules   map[uint32]SellRule
	chapters    map[uint32]ChapterConfig
}

func NewConfigSnapshot(version uint64, entries []ShopEntry) (*ConfigSnapshot, error) {
	return NewConfigSnapshotWithCrops(version, entries, nil)
}

func NewConfigSnapshotWithCrops(
	version uint64,
	entries []ShopEntry,
	crops []CropConfig,
) (*ConfigSnapshot, error) {
	return NewConfigSnapshotWithContent(version, entries, crops, nil)
}

func NewConfigSnapshotWithContent(
	version uint64,
	entries []ShopEntry,
	crops []CropConfig,
	fertilizers []FertilizerConfig,
) (*ConfigSnapshot, error) {
	return NewConfigSnapshotWithEconomy(version, entries, crops, fertilizers, nil)
}

func NewConfigSnapshotWithEconomy(
	version uint64,
	entries []ShopEntry,
	crops []CropConfig,
	fertilizers []FertilizerConfig,
	sellRules []SellRule,
) (*ConfigSnapshot, error) {
	return NewConfigSnapshotWithChapters(
		version, entries, crops, fertilizers, sellRules, nil,
	)
}

func NewConfigSnapshotWithChapters(
	version uint64,
	entries []ShopEntry,
	crops []CropConfig,
	fertilizers []FertilizerConfig,
	sellRules []SellRule,
	chapters []ChapterConfig,
) (*ConfigSnapshot, error) {
	if version == 0 {
		return nil, errors.New("config version is required")
	}
	snapshot := &ConfigSnapshot{
		version:     version,
		shopEntries: make(map[uint32]ShopEntry, len(entries)),
		cropsBySeed: make(map[uint32]CropConfig, len(crops)),
		fertilizers: make(map[uint32]FertilizerConfig, len(fertilizers)),
		sellRules:   make(map[uint32]SellRule, len(sellRules)),
		chapters:    make(map[uint32]ChapterConfig, len(chapters)),
	}
	entryIDs := make(map[uint32]struct{}, len(entries)+len(sellRules))
	for _, entry := range entries {
		if entry.ShopEntryID == 0 || entry.ItemID == 0 ||
			entry.UnitPrice <= 0 || entry.PriceVersion == 0 {
			return nil, errors.New("shop entry is invalid")
		}
		if _, duplicate := snapshot.shopEntries[entry.ShopEntryID]; duplicate {
			return nil, errors.New("shop entry ID is duplicated")
		}
		snapshot.shopEntries[entry.ShopEntryID] = entry
		entryIDs[entry.ShopEntryID] = struct{}{}
	}
	for _, crop := range crops {
		if crop.SeedItemID == 0 || crop.CropID == 0 || crop.CropItemID == 0 ||
			crop.ConfigVersion == 0 || crop.MaturityValueScaled9 <= 0 ||
			crop.BaseGrowthRateScaled6 <= 0 || crop.BaseYield == 0 {
			return nil, errors.New("crop config is invalid")
		}
		if _, duplicate := snapshot.cropsBySeed[crop.SeedItemID]; duplicate {
			return nil, errors.New("crop seed item ID is duplicated")
		}
		snapshot.cropsBySeed[crop.SeedItemID] = crop
	}
	for _, fertilizer := range fertilizers {
		if fertilizer.ItemID == 0 || fertilizer.ConfigVersion == 0 ||
			fertilizer.ModifierScaled6 <= 0 || fertilizer.DurationMS <= 0 {
			return nil, errors.New("fertilizer config is invalid")
		}
		if _, duplicate := snapshot.fertilizers[fertilizer.ItemID]; duplicate {
			return nil, errors.New("fertilizer item ID is duplicated")
		}
		snapshot.fertilizers[fertilizer.ItemID] = fertilizer
	}
	for _, rule := range sellRules {
		if rule.ShopEntryID == 0 || rule.ItemID == 0 ||
			rule.UnitPrice <= 0 || rule.PriceVersion == 0 {
			return nil, errors.New("sell rule is invalid")
		}
		if _, duplicate := entryIDs[rule.ShopEntryID]; duplicate {
			return nil, errors.New("shop entry ID is duplicated")
		}
		if _, duplicate := snapshot.sellRules[rule.ItemID]; duplicate {
			return nil, errors.New("sell item ID is duplicated")
		}
		entryIDs[rule.ShopEntryID] = struct{}{}
		snapshot.sellRules[rule.ItemID] = rule
	}
	for _, chapter := range chapters {
		if chapter.ChapterID == 0 || chapter.ConfigVersion == 0 ||
			chapter.RewardCoins < 0 {
			return nil, errors.New("chapter config is invalid")
		}
		if _, duplicate := snapshot.chapters[chapter.ChapterID]; duplicate {
			return nil, errors.New("chapter ID is duplicated")
		}
		taskIDs := make(map[uint32]struct{}, len(chapter.Tasks))
		for _, task := range chapter.Tasks {
			if task.ID == 0 || task.Target == 0 {
				return nil, errors.New("chapter task is invalid")
			}
			if _, duplicate := taskIDs[task.ID]; duplicate {
				return nil, errors.New("chapter task ID is duplicated")
			}
			taskIDs[task.ID] = struct{}{}
		}
		itemIDs := make(map[uint32]struct{}, len(chapter.RewardItems))
		for _, item := range chapter.RewardItems {
			if item.ItemID == 0 || item.Quantity == 0 {
				return nil, errors.New("chapter reward item is invalid")
			}
			if _, duplicate := itemIDs[item.ItemID]; duplicate {
				return nil, errors.New("chapter reward item ID is duplicated")
			}
			itemIDs[item.ItemID] = struct{}{}
		}
		chapter.Tasks = append([]Task(nil), chapter.Tasks...)
		chapter.RewardItems = append([]RewardItem(nil), chapter.RewardItems...)
		sort.Slice(chapter.Tasks, func(i, j int) bool {
			return chapter.Tasks[i].ID < chapter.Tasks[j].ID
		})
		sort.Slice(chapter.RewardItems, func(i, j int) bool {
			return chapter.RewardItems[i].ItemID < chapter.RewardItems[j].ItemID
		})
		snapshot.chapters[chapter.ChapterID] = chapter
	}
	for _, chapter := range snapshot.chapters {
		if chapter.NextChapterID != 0 {
			if _, exists := snapshot.chapters[chapter.NextChapterID]; !exists {
				return nil, errors.New("next chapter config is unavailable")
			}
		}
	}
	return snapshot, nil
}

func NewDevelopmentConfigSnapshot() *ConfigSnapshot {
	snapshot, err := NewConfigSnapshotWithChapters(ServerConfigVersion, []ShopEntry{{
		ShopEntryID:  developmentShopEntryID,
		ItemID:       developmentSeedItemID,
		UnitPrice:    developmentSeedUnitPrice,
		PriceVersion: developmentSeedPriceVersion,
		Enabled:      true,
	}}, []CropConfig{{
		SeedItemID: developmentSeedItemID, CropID: developmentCropID,
		CropItemID: developmentCropItemID, ConfigVersion: ServerConfigVersion,
		MaturityValueScaled9:  developmentMaturityScaled9,
		BaseGrowthRateScaled6: developmentGrowthRateScaled6,
		BaseYield:             developmentBaseYield, Enabled: true,
	}}, []FertilizerConfig{{
		ItemID: BasicFertilizerID, ConfigVersion: ServerConfigVersion,
		ModifierScaled6: developmentFertilizerModifierScaled6,
		DurationMS:      developmentFertilizerDurationMS, Enabled: true,
	}}, []SellRule{{
		ShopEntryID: developmentSellEntryID, ItemID: developmentCropItemID,
		UnitPrice:    developmentCropSellUnitPrice,
		PriceVersion: developmentCropSellPriceVersion, Enabled: true,
	}}, []ChapterConfig{
		{
			ChapterID: InitialChapterID, ConfigVersion: ServerConfigVersion,
			Tasks: []Task{
				{ID: 1, Target: 3}, {ID: 2, Target: 1}, {ID: 3, Target: 1},
				{ID: 4, Target: 1}, {ID: 5, Target: 1},
			},
			RewardCoins: developmentChapterRewardCoins,
			RewardItems: []RewardItem{
				{ItemID: BasicFertilizerID, Quantity: 1},
				{ItemID: developmentNextSeedItemID, Quantity: 3},
			},
			NextChapterID: developmentNextChapterID,
		},
		{
			ChapterID: developmentNextChapterID, ConfigVersion: ServerConfigVersion,
		},
	})
	if err != nil {
		panic(err)
	}
	return snapshot
}

func (c *ConfigSnapshot) Version() uint64 {
	if c == nil {
		return 0
	}
	return c.version
}

func (c *ConfigSnapshot) ShopEntry(shopEntryID uint32) (ShopEntry, bool) {
	if c == nil {
		return ShopEntry{}, false
	}
	entry, exists := c.shopEntries[shopEntryID]
	return entry, exists
}

func (c *ConfigSnapshot) CropForSeed(seedItemID uint32) (CropConfig, bool) {
	if c == nil {
		return CropConfig{}, false
	}
	crop, exists := c.cropsBySeed[seedItemID]
	return crop, exists
}

func (c *ConfigSnapshot) Fertilizer(itemID uint32) (FertilizerConfig, bool) {
	if c == nil {
		return FertilizerConfig{}, false
	}
	fertilizer, exists := c.fertilizers[itemID]
	return fertilizer, exists
}

func (c *ConfigSnapshot) SellRule(itemID uint32) (SellRule, bool) {
	if c == nil {
		return SellRule{}, false
	}
	rule, exists := c.sellRules[itemID]
	return rule, exists
}

func (c *ConfigSnapshot) Chapter(chapterID uint32) (ChapterConfig, bool) {
	if c == nil {
		return ChapterConfig{}, false
	}
	chapter, exists := c.chapters[chapterID]
	if !exists {
		return ChapterConfig{}, false
	}
	chapter.Tasks = append([]Task(nil), chapter.Tasks...)
	chapter.RewardItems = append([]RewardItem(nil), chapter.RewardItems...)
	return chapter, true
}

func (c *ConfigSnapshot) ActiveShopEntries() []*wsv1.ShopEntryView {
	if c == nil {
		return nil
	}
	ids := make([]int, 0, len(c.shopEntries)+len(c.sellRules))
	views := make(map[uint32]*wsv1.ShopEntryView, len(c.shopEntries)+len(c.sellRules))
	for id, entry := range c.shopEntries {
		if entry.Enabled {
			ids = append(ids, int(id))
			views[id] = entry.View()
		}
	}
	for _, rule := range c.sellRules {
		if rule.Enabled {
			ids = append(ids, int(rule.ShopEntryID))
			views[rule.ShopEntryID] = rule.View()
		}
	}
	sort.Ints(ids)
	entries := make([]*wsv1.ShopEntryView, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, views[uint32(id)])
	}
	return entries
}
