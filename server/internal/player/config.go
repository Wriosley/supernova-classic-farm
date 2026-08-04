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
	if version == 0 {
		return nil, errors.New("config version is required")
	}
	snapshot := &ConfigSnapshot{
		version:     version,
		shopEntries: make(map[uint32]ShopEntry, len(entries)),
		cropsBySeed: make(map[uint32]CropConfig, len(crops)),
		fertilizers: make(map[uint32]FertilizerConfig, len(fertilizers)),
	}
	for _, entry := range entries {
		if entry.ShopEntryID == 0 || entry.ItemID == 0 ||
			entry.UnitPrice <= 0 || entry.PriceVersion == 0 {
			return nil, errors.New("shop entry is invalid")
		}
		if _, duplicate := snapshot.shopEntries[entry.ShopEntryID]; duplicate {
			return nil, errors.New("shop entry ID is duplicated")
		}
		snapshot.shopEntries[entry.ShopEntryID] = entry
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
	return snapshot, nil
}

func NewDevelopmentConfigSnapshot() *ConfigSnapshot {
	snapshot, err := NewConfigSnapshotWithContent(ServerConfigVersion, []ShopEntry{{
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
	}})
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

func (c *ConfigSnapshot) ActiveShopEntries() []*wsv1.ShopEntryView {
	if c == nil {
		return nil
	}
	ids := make([]int, 0, len(c.shopEntries))
	for id, entry := range c.shopEntries {
		if entry.Enabled {
			ids = append(ids, int(id))
		}
	}
	sort.Ints(ids)
	entries := make([]*wsv1.ShopEntryView, 0, len(ids))
	for _, id := range ids {
		entry := c.shopEntries[uint32(id)]
		entries = append(entries, entry.View())
	}
	return entries
}
