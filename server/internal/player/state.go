// Package player owns the minimal in-memory player aggregate and its client projection.
package player

import (
	"sort"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

const (
	InitialCoinBalance  int64  = 10
	BasicFertilizerID   uint32 = 1
	InitialPlotID       uint32 = 1
	InitialPlotCount    uint32 = 4
	InitialChapterID    uint32 = 1
	ServerConfigVersion uint64 = 1
)

type Task struct {
	ID      uint32
	Current uint32
	Target  uint32
}

type Plot struct {
	ID                        uint32
	State                     plotv1.PlotState
	CropID                    uint32
	CropItemID                uint32
	CropConfigVersion         uint64
	PlantedAtMS               int64
	MaturityValueScaled9      int64
	BaseGrowthRateScaled6     int64
	BaseYield                 uint32
	StolenQuantity            uint32
	SettledGrowthValueScaled9 int64
	LastSettledAtMS           int64
	EstimatedMatureAtMS       *int64
	FertilizerEffect          *datav1.TimedEffectRecord
	PestEffect                *datav1.TimedEffectRecord
}

type State struct {
	PlayerID             uint64
	PlayerSeq            uint64
	CheckpointRevision   uint64
	Coins                int64
	Inventory            map[uint32]uint32
	Plots                map[uint32]*Plot
	ChapterID            uint32
	ChapterConfigVersion uint64
	Chapter              chapterv1.ChapterStatus
	ChapterActivatedAtMS int64
	Tasks                []Task
	ConfigVersion        uint64
	CreatedAtMS          int64
	UpdatedAtMS          int64
	RecentResults        []*datav1.IdempotencyResultRecord
	PendingOutbox        []*datav1.PendingOutboxRecord
}

// NewDevelopmentState is a lazy, in-memory development adapter. It is not
// durable registration, checkpoint recovery, or proof of persistence.
func NewDevelopmentState(playerID uint64) *State {
	nowMS := time.Now().UnixMilli()
	return &State{
		PlayerID:             playerID,
		CheckpointRevision:   1,
		Coins:                InitialCoinBalance,
		Inventory:            map[uint32]uint32{BasicFertilizerID: 1},
		Plots:                newInitialPlots(),
		ChapterID:            InitialChapterID,
		ChapterConfigVersion: ServerConfigVersion,
		Chapter:              chapterv1.ChapterStatus_IN_PROGRESS,
		ChapterActivatedAtMS: nowMS,
		Tasks: []Task{
			{ID: 1, Target: 3}, // buy seeds
			{ID: 2, Target: 1}, // plant
			{ID: 3, Target: 1}, // fertilize
			{ID: 4, Target: 1}, // harvest
			{ID: 5, Target: 1}, // sell crop
		},
		ConfigVersion: ServerConfigVersion,
		CreatedAtMS:   nowMS,
		UpdatedAtMS:   nowMS,
	}
}

func newInitialPlots() map[uint32]*Plot {
	plots := make(map[uint32]*Plot, InitialPlotCount)
	for plotID := InitialPlotID; plotID < InitialPlotID+InitialPlotCount; plotID++ {
		plots[plotID] = &Plot{ID: plotID, State: plotv1.PlotState_EMPTY}
	}
	return plots
}

func (s *State) Snapshot() *wsv1.PlayerSnapshot {
	itemIDs := make([]int, 0, len(s.Inventory))
	for itemID, quantity := range s.Inventory {
		if quantity > 0 {
			itemIDs = append(itemIDs, int(itemID))
		}
	}
	sort.Ints(itemIDs)
	inventory := make([]*wsv1.ItemStackView, 0, len(itemIDs))
	for _, itemID := range itemIDs {
		inventory = append(inventory, &wsv1.ItemStackView{
			ItemId:   uint32(itemID),
			Quantity: s.Inventory[uint32(itemID)],
		})
	}

	plotIDs := make([]uint32, 0, len(s.Plots))
	for plotID := range s.Plots {
		plotIDs = append(plotIDs, plotID)
	}
	sort.Slice(plotIDs, func(i, j int) bool { return plotIDs[i] < plotIDs[j] })
	plots := make([]*wsv1.PlotView, 0, len(plotIDs))
	for _, plotID := range plotIDs {
		plots = append(plots, s.Plots[plotID].View())
	}

	tasks := append([]Task(nil), s.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	taskViews := make([]*wsv1.TaskProgressView, 0, len(tasks))
	for _, task := range tasks {
		taskViews = append(taskViews, &wsv1.TaskProgressView{
			TaskId:       task.ID,
			CurrentValue: task.Current,
			TargetValue:  task.Target,
			Completed:    task.Current >= task.Target,
		})
	}

	return &wsv1.PlayerSnapshot{
		PlayerId:    s.PlayerID,
		CoinBalance: s.Coins,
		Inventory:   inventory,
		Plots:       plots,
		CurrentChapter: &wsv1.ChapterView{
			ChapterId: s.ChapterID,
			Status:    s.Chapter,
			Tasks:     taskViews,
		},
		ServerConfigVersion: s.ConfigVersion,
	}
}

func (p *Plot) View() *wsv1.PlotView {
	if p == nil {
		return nil
	}
	view := &wsv1.PlotView{
		PlotId: p.ID, PlotState: p.State, CropId: p.CropID,
		CropConfigVersion: p.CropConfigVersion, PlantedAtMs: p.PlantedAtMS,
	}
	if p.EstimatedMatureAtMS != nil {
		view.EstimatedMatureAtMs = *p.EstimatedMatureAtMS
	}
	if p.State == plotv1.PlotState_MATURE && p.BaseYield >= p.StolenQuantity {
		view.HarvestableQuantity = p.BaseYield - p.StolenQuantity
	}
	if p.FertilizerEffect != nil {
		view.FertilizerEffect = effectView(p.FertilizerEffect)
	}
	if p.PestEffect != nil {
		view.PestEffect = effectView(p.PestEffect)
	}
	return view
}

func effectView(effect *datav1.TimedEffectRecord) *wsv1.EffectView {
	if effect == nil {
		return nil
	}
	view := &wsv1.EffectView{
		EffectInstanceId:    formatUUIDBytes(effect.EffectInstanceId),
		EffectItemId:        effect.EffectItemOrPestId,
		EffectConfigVersion: effect.ConfigVersion,
		StartAtMs:           effect.StartAtMs,
		EndAtMs:             effect.EndAtMs,
	}
	if effect.SourcePlayerId != nil {
		source := effect.GetSourcePlayerId()
		view.SourcePlayerId = &source
	}
	return view
}
