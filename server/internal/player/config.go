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
	developmentFertilizerShopEntryID     uint32 = 5003
	developmentFertilizerUnitPrice       int64  = 2
	developmentFertilizerPriceVersion    uint64 = 10
	developmentCropID                    uint32 = 2001
	developmentCropItemID                uint32 = 1002
	developmentMaturityScaled9           int64  = 100_000_000_000
	developmentGrowthRateScaled6         int64  = 1_000_000
	developmentBaseYield                 uint32 = 3
	developmentFertilizerModifierScaled6 int64  = 500_000
	developmentFertilizerDurationMS      int64  = 60_000
	developmentPestID                    uint32 = 1
	developmentPestModifierScaled6       int64  = -300_000
	developmentPestDurationMS            int64  = 120_000
	developmentSellEntryID               uint32 = 5002
	developmentCropSellUnitPrice         int64  = 5
	developmentCropSellPriceVersion      uint64 = 9
	developmentNextChapterID             uint32 = 2
	developmentNextSeedItemID            uint32 = 1003
	developmentPumpkinSeedItemID         uint32 = 1012
	developmentWatermelonSeedItemID      uint32 = 1013
	developmentChapterRewardCoins        int64  = 10
	developmentStealQuantity             uint32 = 1
	developmentMaxStealTimes             uint32 = 1
	developmentProtectedOwnerYield       uint32 = 2

	developmentVillageDogPetID         uint32 = 1
	developmentShepherdDogPetID        uint32 = 2
	developmentPetGuardProbabilityBPS  uint32 = 1000
	developmentVillageDogPriceCoins    int64  = 5
	developmentShepherdDogPriceCoins   int64  = 10
	developmentVillageDogPenaltyCoins  int64  = 2
	developmentShepherdDogPenaltyCoins int64  = 4
	developmentPetFoodItemID           uint32 = 1004
	developmentPetFoodShopEntryID      uint32 = 5004
	developmentPetFoodUnitPrice        int64  = 5
	developmentPetFoodPriceVersion     uint64 = 11
	developmentPetFoodDurationSeconds  uint64 = 86400
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
	Name                  string
	ConfigVersion         uint64
	MaturityValueScaled9  int64
	BaseGrowthRateScaled6 int64
	BaseYield             uint32
	Enabled               bool

	// Steal fields are frozen onto the Plot at plant time (see player.plant
	// and docs/contracts/data-model.md §18). Zero (the struct default)
	// means the crop is not stealable, matching pre-Phase-5 CropConfig
	// callers that never set them.
	StealQuantity       uint32
	MaxStealTimes       uint32
	ProtectedOwnerYield uint32
}

type FertilizerConfig struct {
	ItemID          uint32
	ConfigVersion   uint64
	ModifierScaled6 int64
	DurationMS      int64
	Enabled         bool
}

type PestConfig struct {
	PestID          uint32
	ConfigVersion   uint64
	ModifierScaled6 int64
	DurationMS      int64
	Enabled         bool
}

// PetConfig 描述可购买宠物的权威参数；概率使用万分比 BPS，禁止浮点。
type PetConfig struct {
	PetID               uint32
	Name                string
	PriceCoins          int64
	GuardProbabilityBPS uint32
	GuardPenaltyCoins   int64
	ConfigVersion       uint64
	Enabled             bool
}

// PetFoodConfig 描述狗粮物品与喂食时长；购买价格走 ShopEntry。
type PetFoodConfig struct {
	ItemID          uint32
	DurationSeconds uint64
	ConfigVersion   uint64
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
	pests       map[uint32]PestConfig
	pets        map[uint32]PetConfig
	petFood     map[uint32]PetFoodConfig
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
	return newConfigSnapshot(version, entries, crops, fertilizers, nil, nil, nil, sellRules, chapters)
}

// NewConfigSnapshotWithPests exposes the complete immutable content needed by
// friend pest interactions while preserving the older constructor signatures.
func NewConfigSnapshotWithPests(
	version uint64,
	entries []ShopEntry,
	crops []CropConfig,
	fertilizers []FertilizerConfig,
	pests []PestConfig,
	sellRules []SellRule,
	chapters []ChapterConfig,
) (*ConfigSnapshot, error) {
	return newConfigSnapshot(version, entries, crops, fertilizers, pests, nil, nil, sellRules, chapters)
}

func NewConfigSnapshotWithPets(
	version uint64,
	entries []ShopEntry,
	crops []CropConfig,
	fertilizers []FertilizerConfig,
	pests []PestConfig,
	pets []PetConfig,
	petFood []PetFoodConfig,
	sellRules []SellRule,
	chapters []ChapterConfig,
) (*ConfigSnapshot, error) {
	return newConfigSnapshot(version, entries, crops, fertilizers, pests, pets, petFood, sellRules, chapters)
}

func newConfigSnapshot(
	version uint64,
	entries []ShopEntry,
	crops []CropConfig,
	fertilizers []FertilizerConfig,
	pests []PestConfig,
	pets []PetConfig,
	petFood []PetFoodConfig,
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
		pests:       make(map[uint32]PestConfig, len(pests)),
		pets:        make(map[uint32]PetConfig, len(pets)),
		petFood:     make(map[uint32]PetFoodConfig, len(petFood)),
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
	cropIDs := make(map[uint32]struct{}, len(crops))
	cropItemIDs := make(map[uint32]struct{}, len(crops))
	for _, crop := range crops {
		if _, duplicate := cropIDs[crop.CropID]; duplicate {
			return nil, errors.New("crop ID is duplicated")
		}
		if _, duplicate := cropItemIDs[crop.CropItemID]; duplicate {
			return nil, errors.New("crop item ID is duplicated")
		}
		cropIDs[crop.CropID] = struct{}{}
		cropItemIDs[crop.CropItemID] = struct{}{}
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
	for _, pest := range pests {
		if pest.PestID == 0 || pest.ConfigVersion == 0 ||
			pest.ModifierScaled6 >= 0 || pest.DurationMS <= 0 {
			return nil, errors.New("pest config is invalid")
		}
		if _, duplicate := snapshot.pests[pest.PestID]; duplicate {
			return nil, errors.New("pest ID is duplicated")
		}
		snapshot.pests[pest.PestID] = pest
	}
	for _, pet := range pets {
		if pet.PetID == 0 || pet.Name == "" || pet.PriceCoins <= 0 ||
			pet.GuardProbabilityBPS == 0 || pet.GuardProbabilityBPS > 10000 ||
			pet.GuardPenaltyCoins < 0 || pet.ConfigVersion == 0 {
			return nil, errors.New("pet config is invalid")
		}
		if _, duplicate := snapshot.pets[pet.PetID]; duplicate {
			return nil, errors.New("pet ID is duplicated")
		}
		snapshot.pets[pet.PetID] = pet
	}
	for _, food := range petFood {
		if food.ItemID == 0 || food.DurationSeconds == 0 || food.ConfigVersion == 0 {
			return nil, errors.New("pet food config is invalid")
		}
		if _, duplicate := snapshot.petFood[food.ItemID]; duplicate {
			return nil, errors.New("pet food item ID is duplicated")
		}
		snapshot.petFood[food.ItemID] = food
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

type developmentCropDef struct {
	Name             string
	CropID           uint32
	SeedItemID       uint32
	CropItemID       uint32
	SeedShopEntryID  uint32
	SellShopEntryID  uint32
	SeedPrice        int64
	SeedPriceVersion uint64
	MaturityScaled9  int64
	BaseYield        uint32
	Stealable        bool
}

// developmentExtraCrops 是除演示作物外的 10 种配置驱动作物。
func developmentExtraCrops() []developmentCropDef {
	return []developmentCropDef{
		{Name: "胡萝卜", CropID: 2002, SeedItemID: 1005, CropItemID: 1015, SeedShopEntryID: 5005, SellShopEntryID: 5015, SeedPrice: 3, SeedPriceVersion: 12, MaturityScaled9: 60_000_000_000, BaseYield: 3},
		{Name: "白萝卜", CropID: 2003, SeedItemID: 1006, CropItemID: 1016, SeedShopEntryID: 5006, SellShopEntryID: 5016, SeedPrice: 3, SeedPriceVersion: 12, MaturityScaled9: 70_000_000_000, BaseYield: 3},
		{Name: "玉米", CropID: 2004, SeedItemID: 1007, CropItemID: 1017, SeedShopEntryID: 5007, SellShopEntryID: 5017, SeedPrice: 4, SeedPriceVersion: 12, MaturityScaled9: 80_000_000_000, BaseYield: 3},
		{Name: "番茄", CropID: 2005, SeedItemID: 1008, CropItemID: 1018, SeedShopEntryID: 5008, SellShopEntryID: 5018, SeedPrice: 4, SeedPriceVersion: 12, MaturityScaled9: 90_000_000_000, BaseYield: 4},
		{Name: "土豆", CropID: 2006, SeedItemID: 1009, CropItemID: 1019, SeedShopEntryID: 5009, SellShopEntryID: 5019, SeedPrice: 4, SeedPriceVersion: 12, MaturityScaled9: 100_000_000_000, BaseYield: 4},
		{Name: "茄子", CropID: 2007, SeedItemID: 1010, CropItemID: 1020, SeedShopEntryID: 5010, SellShopEntryID: 5020, SeedPrice: 4, SeedPriceVersion: 12, MaturityScaled9: 110_000_000_000, BaseYield: 5},
		{Name: "草莓", CropID: 2008, SeedItemID: 1011, CropItemID: 1021, SeedShopEntryID: 5011, SellShopEntryID: 5021, SeedPrice: 5, SeedPriceVersion: 12, MaturityScaled9: 120_000_000_000, BaseYield: 4},
		{Name: "南瓜", CropID: 2009, SeedItemID: 1012, CropItemID: 1022, SeedShopEntryID: 5012, SellShopEntryID: 5022, SeedPrice: 5, SeedPriceVersion: 12, MaturityScaled9: 130_000_000_000, BaseYield: 5},
		{Name: "西瓜", CropID: 2010, SeedItemID: 1013, CropItemID: 1023, SeedShopEntryID: 5013, SellShopEntryID: 5023, SeedPrice: 5, SeedPriceVersion: 12, MaturityScaled9: 140_000_000_000, BaseYield: 5},
		{Name: "葡萄", CropID: 2011, SeedItemID: 1014, CropItemID: 1024, SeedShopEntryID: 5014, SellShopEntryID: 5024, SeedPrice: 5, SeedPriceVersion: 12, MaturityScaled9: 150_000_000_000, BaseYield: 6},
	}
}

func NewDevelopmentConfigSnapshot() *ConfigSnapshot {
	shopEntries := []ShopEntry{
		{
			ShopEntryID:  developmentShopEntryID,
			ItemID:       developmentSeedItemID,
			UnitPrice:    developmentSeedUnitPrice,
			PriceVersion: developmentSeedPriceVersion,
			Enabled:      true,
		},
		{
			ShopEntryID:  developmentFertilizerShopEntryID,
			ItemID:       BasicFertilizerID,
			UnitPrice:    developmentFertilizerUnitPrice,
			PriceVersion: developmentFertilizerPriceVersion,
			Enabled:      true,
		},
		{
			ShopEntryID:  developmentPetFoodShopEntryID,
			ItemID:       developmentPetFoodItemID,
			UnitPrice:    developmentPetFoodUnitPrice,
			PriceVersion: developmentPetFoodPriceVersion,
			Enabled:      true,
		},
	}
	crops := []CropConfig{{
		Name: "演示作物", SeedItemID: developmentSeedItemID, CropID: developmentCropID,
		CropItemID: developmentCropItemID, ConfigVersion: ServerConfigVersion,
		MaturityValueScaled9:  developmentMaturityScaled9,
		BaseGrowthRateScaled6: developmentGrowthRateScaled6,
		BaseYield:             developmentBaseYield, Enabled: true,
		StealQuantity: developmentStealQuantity, MaxStealTimes: developmentMaxStealTimes,
		ProtectedOwnerYield: developmentProtectedOwnerYield,
	}}
	sellRules := []SellRule{{
		ShopEntryID: developmentSellEntryID, ItemID: developmentCropItemID,
		UnitPrice:    developmentCropSellUnitPrice,
		PriceVersion: developmentCropSellPriceVersion, Enabled: true,
	}}
	for _, def := range developmentExtraCrops() {
		shopEntries = append(shopEntries, ShopEntry{
			ShopEntryID: def.SeedShopEntryID, ItemID: def.SeedItemID,
			UnitPrice: def.SeedPrice, PriceVersion: def.SeedPriceVersion, Enabled: true,
		})
		crops = append(crops, CropConfig{
			Name: def.Name, SeedItemID: def.SeedItemID, CropID: def.CropID,
			CropItemID: def.CropItemID, ConfigVersion: ServerConfigVersion,
			MaturityValueScaled9: def.MaturityScaled9, BaseGrowthRateScaled6: developmentGrowthRateScaled6,
			BaseYield: def.BaseYield, Enabled: true,
			StealQuantity:       stealQuantityFromBaseYield(def.BaseYield),
			MaxStealTimes:       maxStealTimesFromBaseYield(def.BaseYield),
			ProtectedOwnerYield: protectedOwnerYieldFromBaseYield(def.BaseYield),
		})
		sellRules = append(sellRules, SellRule{
			ShopEntryID: def.SellShopEntryID, ItemID: def.CropItemID,
			UnitPrice: developmentCropSellUnitPrice, PriceVersion: developmentCropSellPriceVersion,
			Enabled: true,
		})
	}
	snapshot, err := NewConfigSnapshotWithPets(ServerConfigVersion, shopEntries, crops, []FertilizerConfig{{
		ItemID: BasicFertilizerID, ConfigVersion: ServerConfigVersion,
		ModifierScaled6: developmentFertilizerModifierScaled6,
		DurationMS:      developmentFertilizerDurationMS, Enabled: true,
	}}, []PestConfig{{
		PestID: developmentPestID, ConfigVersion: ServerConfigVersion,
		ModifierScaled6: developmentPestModifierScaled6,
		DurationMS:      developmentPestDurationMS, Enabled: true,
	}}, []PetConfig{
		{
			PetID: developmentVillageDogPetID, Name: "田园犬",
			PriceCoins:          developmentVillageDogPriceCoins,
			GuardProbabilityBPS: developmentPetGuardProbabilityBPS,
			GuardPenaltyCoins:   developmentVillageDogPenaltyCoins,
			ConfigVersion:       ServerConfigVersion, Enabled: true,
		},
		{
			PetID: developmentShepherdDogPetID, Name: "牧羊犬",
			PriceCoins:          developmentShepherdDogPriceCoins,
			GuardProbabilityBPS: developmentPetGuardProbabilityBPS,
			GuardPenaltyCoins:   developmentShepherdDogPenaltyCoins,
			ConfigVersion:       ServerConfigVersion, Enabled: true,
		},
	}, []PetFoodConfig{{
		ItemID: developmentPetFoodItemID, DurationSeconds: developmentPetFoodDurationSeconds,
		ConfigVersion: ServerConfigVersion, Enabled: true,
	}}, sellRules, []ChapterConfig{
		{
			ChapterID: InitialChapterID, ConfigVersion: ServerConfigVersion,
			Tasks: []Task{
				{ID: 1, Target: 3}, {ID: 2, Target: 1}, {ID: 3, Target: 1},
				{ID: 4, Target: 1}, {ID: 5, Target: 1},
			},
			RewardCoins: developmentChapterRewardCoins,
			RewardItems: []RewardItem{
				{ItemID: BasicFertilizerID, Quantity: 1},
				{ItemID: developmentPumpkinSeedItemID, Quantity: 3},
			},
			NextChapterID: developmentNextChapterID,
		},
		{
			ChapterID: developmentNextChapterID, ConfigVersion: ServerConfigVersion,
			Tasks: []Task{
				{ID: 6, Target: 1},
				{ID: 7, Target: 1},
				{ID: 8, Target: 1},
			},
			RewardCoins: developmentChapterRewardCoins,
			RewardItems: []RewardItem{
				{ItemID: BasicFertilizerID, Quantity: 5},
				{ItemID: developmentWatermelonSeedItemID, Quantity: 10},
			},
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

func (c *ConfigSnapshot) Pest(pestID uint32) (PestConfig, bool) {
	if c == nil {
		return PestConfig{}, false
	}
	pest, exists := c.pests[pestID]
	return pest, exists
}

func (c *ConfigSnapshot) Pet(petID uint32) (PetConfig, bool) {
	if c == nil {
		return PetConfig{}, false
	}
	pet, exists := c.pets[petID]
	return pet, exists
}

func (c *ConfigSnapshot) PetFood(itemID uint32) (PetFoodConfig, bool) {
	if c == nil {
		return PetFoodConfig{}, false
	}
	food, exists := c.petFood[itemID]
	return food, exists
}

func (c *ConfigSnapshot) ActivePets() []PetConfig {
	if c == nil {
		return nil
	}
	ids := make([]int, 0, len(c.pets))
	for id, pet := range c.pets {
		if pet.Enabled {
			ids = append(ids, int(id))
		}
	}
	sort.Ints(ids)
	out := make([]PetConfig, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.pets[uint32(id)])
	}
	return out
}

func (c *ConfigSnapshot) PrimaryPetFood() (PetFoodConfig, ShopEntry, bool) {
	if c == nil {
		return PetFoodConfig{}, ShopEntry{}, false
	}
	for _, entry := range c.shopEntries {
		if food, exists := c.petFood[entry.ItemID]; exists && food.Enabled && entry.Enabled {
			return food, entry, true
		}
	}
	return PetFoodConfig{}, ShopEntry{}, false
}

func (c *ConfigSnapshot) SellRule(itemID uint32) (SellRule, bool) {
	if c == nil {
		return SellRule{}, false
	}
	rule, exists := c.sellRules[itemID]
	return rule, exists
}

// IsCropItem reports whether itemID is a configured harvest crop (has a sell rule).
func (c *ConfigSnapshot) IsCropItem(itemID uint32) bool {
	rule, exists := c.SellRule(itemID)
	return exists && rule.Enabled
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

// SoleStealableCrop is retained for legacy unit fixtures that still assert
// a deterministic stealable crop identity. Steal Saga entry points must not
// call this: crop comes from the visitor request / FriendInteraction row.
func (c *ConfigSnapshot) SoleStealableCrop() (cropItemID uint32, stealQuantity uint32, ok bool) {
	if c == nil {
		return 0, 0, false
	}
	seedItemIDs := make([]int, 0, len(c.cropsBySeed))
	for seedItemID := range c.cropsBySeed {
		seedItemIDs = append(seedItemIDs, int(seedItemID))
	}
	sort.Ints(seedItemIDs)
	for _, seedItemID := range seedItemIDs {
		crop := c.cropsBySeed[uint32(seedItemID)]
		if crop.StealQuantity == 0 || crop.MaxStealTimes == 0 {
			continue
		}
		if !ok || crop.CropItemID < cropItemID {
			cropItemID, stealQuantity, ok = crop.CropItemID, crop.StealQuantity, true
		}
	}
	return cropItemID, stealQuantity, ok
}

// protectedOwnerYieldFromBaseYield is ceil(base_yield / 2).
func protectedOwnerYieldFromBaseYield(baseYield uint32) uint32 {
	if baseYield == 0 {
		return 0
	}
	return (baseYield + 1) / 2
}

func maxStealTimesFromBaseYield(baseYield uint32) uint32 {
	protected := protectedOwnerYieldFromBaseYield(baseYield)
	if baseYield <= protected {
		return 0
	}
	return baseYield - protected
}

func stealQuantityFromBaseYield(baseYield uint32) uint32 {
	if maxStealTimesFromBaseYield(baseYield) == 0 {
		return 0
	}
	return 1
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

func (c *ConfigSnapshot) CropByID(cropID uint32) (CropConfig, bool) {
	if c == nil {
		return CropConfig{}, false
	}
	for _, crop := range c.cropsBySeed {
		if crop.CropID == cropID {
			return crop, true
		}
	}
	return CropConfig{}, false
}

func (c *ConfigSnapshot) shopEntryForItem(itemID uint32) (ShopEntry, bool) {
	if c == nil {
		return ShopEntry{}, false
	}
	for _, entry := range c.shopEntries {
		if entry.ItemID == itemID && entry.Enabled {
			return entry, true
		}
	}
	return ShopEntry{}, false
}

// ActiveCropCatalog 按 crop_id 升序返回启用作物目录，供商店与图鉴展示。
func (c *ConfigSnapshot) ActiveCropCatalog() []*wsv1.CropCatalogEntryView {
	if c == nil {
		return nil
	}
	ids := make([]int, 0, len(c.cropsBySeed))
	byID := make(map[uint32]CropConfig, len(c.cropsBySeed))
	for _, crop := range c.cropsBySeed {
		if !crop.Enabled {
			continue
		}
		ids = append(ids, int(crop.CropID))
		byID[crop.CropID] = crop
	}
	sort.Ints(ids)
	out := make([]*wsv1.CropCatalogEntryView, 0, len(ids))
	for _, id := range ids {
		crop := byID[uint32(id)]
		entry := &wsv1.CropCatalogEntryView{
			CropId: crop.CropID, Name: crop.Name,
			SeedItemId: crop.SeedItemID, CropItemId: crop.CropItemID,
			MaturitySeconds: uint64(crop.MaturityValueScaled9 / 1_000_000_000),
			BaseYield:       crop.BaseYield,
		}
		if shop, ok := c.shopEntryForItem(crop.SeedItemID); ok {
			entry.SeedUnitPrice = shop.UnitPrice
			entry.SeedPriceVersion = shop.PriceVersion
			entry.SeedShopEntryId = shop.ShopEntryID
		}
		if sell, ok := c.SellRule(crop.CropItemID); ok {
			entry.SellUnitPrice = sell.UnitPrice
			entry.SellPriceVersion = sell.PriceVersion
		}
		out = append(out, entry)
	}
	return out
}
