package player

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	eventv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/event"
	chapterv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/chapter"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

const (
	// CheckpointSchemaVersionV1 has no friend fields populated.
	CheckpointSchemaVersionV1 uint32 = 1
	// CheckpointSchemaVersion is the latest schema: friend_actions and
	// friend_task_credit_receipts are populated once a player's first
	// friend-related event lazily migrates them (see migrateFriendSchema).
	// State.Checkpoint emits V1 until that migration happens so players who
	// never touch the friend feature keep the smaller, unmigrated encoding.
	CheckpointSchemaVersion uint32 = 2
)

// NewInitialCheckpoint creates the durable aggregate committed atomically with
// a new account and Session by the local MySQL registration path.
func NewInitialCheckpoint(playerID uint64, now time.Time) *datav1.PlayerCheckpointV1 {
	nowMS := now.UnixMilli()
	return &datav1.PlayerCheckpointV1{
		SchemaVersion:            CheckpointSchemaVersionV1,
		PlayerId:                 playerID,
		LogicalShardId:           routing.ShardForPlayer(playerID),
		OwnerEpoch:               LocalOwnerEpoch,
		PlayerSeq:                0,
		CheckpointRevision:       1,
		CoinBalance:              InitialCoinBalance,
		Inventory:                []*datav1.InventoryStack{{ItemId: BasicFertilizerID, Quantity: 1}},
		Plots:                    initialPlotRecords(),
		CurrentChapter:           initialChapter(nowMS),
		LastAppliedConfigVersion: ServerConfigVersion,
		CreatedAtMs:              nowMS,
		UpdatedAtMs:              nowMS,
	}
}

func initialPlotRecords() []*datav1.PlotStateRecord {
	plots := make([]*datav1.PlotStateRecord, 0, InitialPlotCount)
	for plotID := InitialPlotID; plotID < InitialPlotID+InitialPlotCount; plotID++ {
		plots = append(plots, &datav1.PlotStateRecord{
			PlotId: plotID,
			State:  datav1.PlotRecordState_EMPTY,
		})
	}
	return plots
}

func initialChapter(nowMS int64) *datav1.ChapterStateRecord {
	return &datav1.ChapterStateRecord{
		ChapterId:            InitialChapterID,
		ChapterConfigVersion: ServerConfigVersion,
		Status:               datav1.ChapterRecordStatus_IN_PROGRESS,
		ActivatedAtMs:        nowMS,
		Tasks: []*datav1.TaskStateRecord{
			{TaskId: 1, TaskConfigVersion: ServerConfigVersion, Metric: datav1.TaskMetric_TASK_BUY_SEEDS, TargetValue: 3},
			{TaskId: 2, TaskConfigVersion: ServerConfigVersion, Metric: datav1.TaskMetric_TASK_PLANT, TargetValue: 1},
			{TaskId: 3, TaskConfigVersion: ServerConfigVersion, Metric: datav1.TaskMetric_TASK_APPLY_FERTILIZER, TargetValue: 1},
			{TaskId: 4, TaskConfigVersion: ServerConfigVersion, Metric: datav1.TaskMetric_TASK_HARVEST, TargetValue: 1},
			{TaskId: 5, TaskConfigVersion: ServerConfigVersion, Metric: datav1.TaskMetric_TASK_SELL_CROP, TargetValue: 1},
		},
	}
}

// MarshalCheckpoint returns the deterministic blob and its exact SHA-256.
func MarshalCheckpoint(checkpoint *datav1.PlayerCheckpointV1) ([]byte, [32]byte, error) {
	if err := ValidateCheckpoint(checkpoint); err != nil {
		return nil, [32]byte{}, err
	}
	body, err := (proto.MarshalOptions{Deterministic: true}).Marshal(checkpoint)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("marshal checkpoint: %w", err)
	}
	if len(body) > 4<<20 {
		return nil, [32]byte{}, errors.New("checkpoint exceeds 4 MiB")
	}
	return body, sha256.Sum256(body), nil
}

// UnmarshalCheckpoint verifies the stored digest before validating the aggregate.
func UnmarshalCheckpoint(body []byte, digest []byte) (*datav1.PlayerCheckpointV1, error) {
	if len(body) == 0 || len(body) > 4<<20 {
		return nil, errors.New("checkpoint blob size is invalid")
	}
	actual := sha256.Sum256(body)
	if len(digest) != len(actual) || !equalBytes(actual[:], digest) {
		return nil, errors.New("checkpoint SHA-256 mismatch")
	}
	checkpoint := &datav1.PlayerCheckpointV1{}
	if err := proto.Unmarshal(body, checkpoint); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}
	if err := ValidateCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	return checkpoint, nil
}

func ValidateCheckpoint(checkpoint *datav1.PlayerCheckpointV1) error {
	switch {
	case checkpoint == nil:
		return errors.New("checkpoint is required")
	case checkpoint.SchemaVersion != CheckpointSchemaVersionV1 && checkpoint.SchemaVersion != CheckpointSchemaVersion:
		return fmt.Errorf("unsupported checkpoint schema %d", checkpoint.SchemaVersion)
	case checkpoint.PlayerId == 0:
		return errors.New("checkpoint player_id is required")
	case checkpoint.LogicalShardId != routing.ShardForPlayer(checkpoint.PlayerId):
		return errors.New("checkpoint logical_shard_id does not match player_id")
	case checkpoint.OwnerEpoch == 0:
		return errors.New("checkpoint owner_epoch is required")
	case checkpoint.CheckpointRevision == 0:
		return errors.New("checkpoint_revision is required")
	case checkpoint.CoinBalance < 0:
		return errors.New("checkpoint coin balance is negative")
	case checkpoint.CurrentChapter == nil:
		return errors.New("checkpoint current chapter is required")
	case checkpoint.CurrentChapter.ChapterId == 0 ||
		checkpoint.CurrentChapter.ChapterConfigVersion == 0 ||
		checkpoint.CurrentChapter.ActivatedAtMs <= 0:
		return errors.New("checkpoint current chapter fields are invalid")
	case checkpoint.CurrentChapter.Status != datav1.ChapterRecordStatus_IN_PROGRESS &&
		checkpoint.CurrentChapter.Status != datav1.ChapterRecordStatus_CLAIMABLE &&
		checkpoint.CurrentChapter.Status != datav1.ChapterRecordStatus_CLAIMED:
		return errors.New("checkpoint chapter status is invalid")
	case checkpoint.LastAppliedConfigVersion == 0:
		return errors.New("checkpoint config version is required")
	case checkpoint.CreatedAtMs <= 0 || checkpoint.UpdatedAtMs < checkpoint.CreatedAtMs:
		return errors.New("checkpoint timestamps are invalid")
	}
	if len(checkpoint.Inventory) > 100 {
		return errors.New("checkpoint inventory type limit exceeded")
	}
	inventoryIDs := make(map[uint32]struct{}, len(checkpoint.Inventory))
	for _, item := range checkpoint.Inventory {
		if item == nil || item.ItemId == 0 || item.Quantity == 0 || item.Quantity > 300 {
			return errors.New("checkpoint contains invalid inventory")
		}
		if _, duplicate := inventoryIDs[item.ItemId]; duplicate {
			return errors.New("checkpoint contains duplicate inventory item")
		}
		inventoryIDs[item.ItemId] = struct{}{}
	}
	plotIDs := make(map[uint32]struct{}, len(checkpoint.Plots))
	for _, plot := range checkpoint.Plots {
		if err := validatePlotRecord(plot); err != nil {
			return err
		}
		if _, duplicate := plotIDs[plot.PlotId]; duplicate {
			return errors.New("checkpoint contains duplicate plot")
		}
		plotIDs[plot.PlotId] = struct{}{}
	}
	var previousOutbox *datav1.PendingOutboxRecord
	outboxIDs := make(map[string]struct{}, len(checkpoint.PendingOutbox))
	for _, pending := range checkpoint.PendingOutbox {
		if err := validatePendingOutbox(checkpoint, pending); err != nil {
			return err
		}
		if _, duplicate := outboxIDs[string(pending.EventId)]; duplicate {
			return errors.New("checkpoint contains duplicate Outbox event")
		}
		if previousOutbox != nil &&
			(previousOutbox.CreatedAtMs > pending.CreatedAtMs ||
				(previousOutbox.CreatedAtMs == pending.CreatedAtMs &&
					bytes.Compare(previousOutbox.EventId, pending.EventId) >= 0)) {
			return errors.New("checkpoint pending Outbox is not sorted")
		}
		outboxIDs[string(pending.EventId)] = struct{}{}
		previousOutbox = pending
	}
	return nil
}

func validatePendingOutbox(
	checkpoint *datav1.PlayerCheckpointV1,
	pending *datav1.PendingOutboxRecord,
) error {
	if pending == nil || len(pending.EventId) != 16 || allZero(pending.EventId) ||
		pending.EventType != datav1.OutboxEventType_CREATE_REWARD_MAIL ||
		pending.EventContractVersion != 1 ||
		pending.AggregatePlayerId != checkpoint.PlayerId ||
		len(pending.CausedByRequestId) != 16 || allZero(pending.CausedByRequestId) ||
		pending.CreatedOwnerEpoch == 0 ||
		pending.CreatedPlayerSeq == 0 || pending.CreatedPlayerSeq > checkpoint.PlayerSeq ||
		pending.CreatedAtMs <= 0 || len(pending.Payload) == 0 ||
		len(pending.Payload) > 48<<10 || len(pending.PayloadSha256) != sha256.Size {
		return errors.New("checkpoint contains invalid pending Outbox")
	}
	digest := sha256.Sum256(pending.Payload)
	if !bytes.Equal(digest[:], pending.PayloadSha256) {
		return errors.New("pending Outbox payload digest mismatch")
	}
	payload := &eventv1.CreateRewardMailV1{}
	if proto.Unmarshal(pending.Payload, payload) != nil ||
		payload.RecipientPlayerId != checkpoint.PlayerId ||
		payload.SubjectTextKey != "mail.chapter_reward.subject" ||
		payload.BodyTextKey != "mail.chapter_reward.body" ||
		payload.Source == nil || payload.Source.ChapterId == 0 ||
		payload.Source.ChapterConfigVersion == 0 ||
		!bytes.Equal(payload.Source.RequestId, pending.CausedByRequestId) ||
		len(payload.Attachments) == 0 || len(payload.Attachments) > 100 {
		return errors.New("pending reward-mail payload is invalid")
	}
	var previousItemID uint32
	for _, attachment := range payload.Attachments {
		if attachment == nil || attachment.ItemId == 0 || attachment.Quantity == 0 ||
			attachment.ItemId <= previousItemID {
			return errors.New("pending reward-mail attachments are invalid")
		}
		previousItemID = attachment.ItemId
	}
	return nil
}

func allZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}

func validatePlotRecord(plot *datav1.PlotStateRecord) error {
	if plot == nil || plot.PlotId == 0 {
		return errors.New("checkpoint contains invalid plot")
	}
	switch plot.State {
	case datav1.PlotRecordState_EMPTY:
		if plot.CropId != 0 || plot.CropItemId != 0 || plot.CropConfigVersion != 0 ||
			plot.PlantedAtMs != 0 || plot.MaturityValue != nil ||
			plot.BaseGrowthRate != nil || plot.BaseYield != 0 ||
			plot.StolenQuantity != 0 || plot.SettledGrowthValue != nil ||
			plot.LastSettledAtMs != 0 || plot.EstimatedMatureAtMs != nil ||
			plot.FertilizerEffect != nil || plot.PestEffect != nil ||
			plot.StealCount != 0 || plot.StealQuantity != 0 ||
			plot.MaxStealTimes != 0 || plot.ProtectedOwnerYield != 0 {
			return errors.New("EMPTY plot contains crop fields")
		}
	case datav1.PlotRecordState_GROWING:
		if !validCropIdentity(plot) ||
			plot.MaturityValue == nil || plot.MaturityValue.ScaledValue <= 0 ||
			plot.BaseGrowthRate == nil || plot.BaseGrowthRate.ScaledValue <= 0 ||
			plot.SettledGrowthValue == nil || plot.SettledGrowthValue.ScaledValue < 0 ||
			plot.SettledGrowthValue.ScaledValue >= plot.MaturityValue.ScaledValue ||
			plot.LastSettledAtMs < plot.PlantedAtMs ||
			plot.EstimatedMatureAtMs == nil ||
			plot.GetEstimatedMatureAtMs() <= plot.LastSettledAtMs {
			return errors.New("GROWING plot fields are invalid")
		}
		if err := validateTimedEffect(plot.FertilizerEffect, datav1.EffectKind_FERTILIZER, plot.PlantedAtMs); err != nil {
			return err
		}
		if err := validateTimedEffect(plot.PestEffect, datav1.EffectKind_PEST, plot.PlantedAtMs); err != nil {
			return err
		}
		if plot.StealCount > plot.MaxStealTimes {
			return errors.New("GROWING plot steal_count exceeds max_steal_times")
		}
	case datav1.PlotRecordState_MATURE:
		if !validCropIdentity(plot) ||
			plot.MaturityValue == nil || plot.MaturityValue.ScaledValue <= 0 ||
			plot.BaseGrowthRate == nil || plot.BaseGrowthRate.ScaledValue <= 0 ||
			plot.SettledGrowthValue == nil ||
			plot.SettledGrowthValue.ScaledValue != plot.MaturityValue.ScaledValue ||
			plot.LastSettledAtMs < plot.PlantedAtMs ||
			plot.EstimatedMatureAtMs != nil ||
			plot.FertilizerEffect != nil || plot.PestEffect != nil {
			return errors.New("MATURE plot fields are invalid")
		}
		if plot.StealCount > plot.MaxStealTimes {
			return errors.New("MATURE plot steal_count exceeds max_steal_times")
		}
	case datav1.PlotRecordState_NEED_CLEANUP:
		if !validCropIdentity(plot) ||
			plot.MaturityValue != nil || plot.BaseGrowthRate != nil ||
			plot.SettledGrowthValue != nil || plot.LastSettledAtMs != 0 ||
			plot.EstimatedMatureAtMs != nil ||
			plot.FertilizerEffect != nil || plot.PestEffect != nil {
			return errors.New("NEED_CLEANUP plot fields are invalid")
		}
		if plot.StealCount > plot.MaxStealTimes {
			return errors.New("NEED_CLEANUP plot steal_count exceeds max_steal_times")
		}
	default:
		return errors.New("checkpoint plot state is invalid")
	}
	return nil
}

func validateTimedEffect(effect *datav1.TimedEffectRecord, kind datav1.EffectKind, plantedAtMS int64) error {
	if effect == nil {
		return nil
	}
	if len(effect.EffectInstanceId) != 16 || effect.EffectKind != kind ||
		effect.EffectItemOrPestId == 0 || effect.ConfigVersion == 0 ||
		effect.Modifier == nil || effect.Modifier.ScaledValue == 0 ||
		effect.StartAtMs < plantedAtMS || effect.EndAtMs <= effect.StartAtMs {
		return errors.New("plot timed effect is invalid")
	}
	if kind == datav1.EffectKind_FERTILIZER && effect.SourcePlayerId != nil {
		return errors.New("fertilizer effect has source player")
	}
	if kind == datav1.EffectKind_FERTILIZER && effect.Modifier.ScaledValue <= 0 {
		return errors.New("fertilizer effect modifier is not positive")
	}
	return nil
}

func validCropIdentity(plot *datav1.PlotStateRecord) bool {
	return plot.CropId != 0 && plot.CropItemId != 0 &&
		plot.CropConfigVersion != 0 && plot.PlantedAtMs > 0 &&
		plot.BaseYield > 0 && plot.StolenQuantity <= plot.BaseYield
}

// StateFromCheckpoint builds the online projection after the persisted
// checkpoint has passed envelope and digest validation.
func StateFromCheckpoint(checkpoint *datav1.PlayerCheckpointV1) (*State, error) {
	if err := ValidateCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	state := &State{
		PlayerID:             checkpoint.PlayerId,
		OwnerEpoch:           checkpoint.OwnerEpoch,
		PlayerSeq:            checkpoint.PlayerSeq,
		CheckpointRevision:   checkpoint.CheckpointRevision,
		Coins:                checkpoint.CoinBalance,
		Inventory:            make(map[uint32]uint32, len(checkpoint.Inventory)),
		Plots:                make(map[uint32]*Plot, len(checkpoint.Plots)),
		ChapterID:            checkpoint.CurrentChapter.ChapterId,
		ChapterConfigVersion: checkpoint.CurrentChapter.ChapterConfigVersion,
		Chapter:              chapterStatusFromRecord(checkpoint.CurrentChapter.Status),
		ChapterActivatedAtMS: checkpoint.CurrentChapter.ActivatedAtMs,
		ConfigVersion:        checkpoint.LastAppliedConfigVersion,
		CreatedAtMS:          checkpoint.CreatedAtMs,
		UpdatedAtMS:          checkpoint.UpdatedAtMs,
	}
	for _, item := range checkpoint.Inventory {
		if item == nil || item.ItemId == 0 || item.Quantity == 0 {
			return nil, errors.New("checkpoint contains invalid inventory")
		}
		if _, exists := state.Inventory[item.ItemId]; exists {
			return nil, errors.New("checkpoint contains duplicate inventory item")
		}
		state.Inventory[item.ItemId] = item.Quantity
	}
	for _, plot := range checkpoint.Plots {
		if plot == nil || plot.PlotId == 0 {
			return nil, errors.New("checkpoint contains invalid plot")
		}
		if _, exists := state.Plots[plot.PlotId]; exists {
			return nil, errors.New("checkpoint contains duplicate plot")
		}
		state.Plots[plot.PlotId] = plotFromRecord(plot)
	}
	for _, task := range checkpoint.CurrentChapter.Tasks {
		if task == nil || task.TaskId == 0 || task.TargetValue == 0 {
			return nil, errors.New("checkpoint contains invalid task")
		}
		state.Tasks = append(state.Tasks, Task{ID: task.TaskId, Current: task.CurrentValue, Target: task.TargetValue})
	}
	for _, result := range checkpoint.RecentResults {
		if result == nil {
			return nil, errors.New("checkpoint contains nil idempotency result")
		}
		state.RecentResults = append(state.RecentResults, proto.Clone(result).(*datav1.IdempotencyResultRecord))
	}
	for _, pending := range checkpoint.PendingOutbox {
		state.PendingOutbox = append(
			state.PendingOutbox,
			proto.Clone(pending).(*datav1.PendingOutboxRecord),
		)
	}
	if checkpoint.FriendActions != nil {
		state.FriendActions = proto.Clone(checkpoint.FriendActions).(*datav1.FriendActionState)
	}
	for _, reservation := range checkpoint.FriendReservations {
		state.FriendReservations = append(
			state.FriendReservations,
			proto.Clone(reservation).(*datav1.FriendResourceReservation),
		)
	}
	for _, receipt := range checkpoint.FriendReceipts {
		state.FriendReceipts = append(
			state.FriendReceipts,
			proto.Clone(receipt).(*datav1.FriendInteractionReceipt),
		)
	}
	for _, receipt := range checkpoint.FriendTaskCreditReceipts {
		state.FriendTaskCreditReceipts = append(
			state.FriendTaskCreditReceipts,
			proto.Clone(receipt).(*datav1.FriendTaskCreditReceipt),
		)
	}
	return state, nil
}

func (s *State) Checkpoint() (*datav1.PlayerCheckpointV1, error) {
	if s == nil {
		return nil, errors.New("state is required")
	}
	inventoryIDs := make([]int, 0, len(s.Inventory))
	for itemID, quantity := range s.Inventory {
		if itemID == 0 || quantity == 0 {
			return nil, errors.New("state contains invalid inventory")
		}
		inventoryIDs = append(inventoryIDs, int(itemID))
	}
	sort.Ints(inventoryIDs)
	inventory := make([]*datav1.InventoryStack, 0, len(inventoryIDs))
	for _, itemID := range inventoryIDs {
		inventory = append(inventory, &datav1.InventoryStack{
			ItemId: uint32(itemID), Quantity: s.Inventory[uint32(itemID)],
		})
	}
	plotIDs := make([]uint32, 0, len(s.Plots))
	for plotID := range s.Plots {
		plotIDs = append(plotIDs, plotID)
	}
	sort.Slice(plotIDs, func(i, j int) bool { return plotIDs[i] < plotIDs[j] })
	plots := make([]*datav1.PlotStateRecord, 0, len(plotIDs))
	for _, plotID := range plotIDs {
		record, err := s.Plots[plotID].Record()
		if err != nil {
			return nil, err
		}
		plots = append(plots, record)
	}
	tasks := append([]Task(nil), s.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	taskRecords := make([]*datav1.TaskStateRecord, 0, len(tasks))
	for _, task := range tasks {
		taskRecords = append(taskRecords, &datav1.TaskStateRecord{
			TaskId: task.ID, TaskConfigVersion: s.ConfigVersion,
			Metric: taskMetric(task.ID), CurrentValue: task.Current,
			TargetValue: task.Target, Completed: task.Current >= task.Target,
		})
	}
	recent := make([]*datav1.IdempotencyResultRecord, 0, len(s.RecentResults))
	for _, result := range s.RecentResults {
		recent = append(recent, proto.Clone(result).(*datav1.IdempotencyResultRecord))
	}
	pendingOutbox := make([]*datav1.PendingOutboxRecord, 0, len(s.PendingOutbox))
	for _, pending := range s.PendingOutbox {
		pendingOutbox = append(
			pendingOutbox,
			proto.Clone(pending).(*datav1.PendingOutboxRecord),
		)
	}
	sort.Slice(pendingOutbox, func(i, j int) bool {
		if pendingOutbox[i].CreatedAtMs != pendingOutbox[j].CreatedAtMs {
			return pendingOutbox[i].CreatedAtMs < pendingOutbox[j].CreatedAtMs
		}
		return bytes.Compare(pendingOutbox[i].EventId, pendingOutbox[j].EventId) < 0
	})
	schemaVersion := CheckpointSchemaVersionV1
	if s.FriendActions != nil {
		schemaVersion = CheckpointSchemaVersion
	}
	checkpoint := &datav1.PlayerCheckpointV1{
		SchemaVersion: schemaVersion, PlayerId: s.PlayerID,
		LogicalShardId: routing.ShardForPlayer(s.PlayerID), OwnerEpoch: s.OwnerEpoch,
		PlayerSeq: s.PlayerSeq, CheckpointRevision: s.CheckpointRevision,
		CoinBalance: s.Coins, Inventory: inventory, Plots: plots,
		CurrentChapter: &datav1.ChapterStateRecord{
			ChapterId: s.ChapterID, ChapterConfigVersion: s.ChapterConfigVersion,
			Status:        chapterStatusToRecord(s.Chapter),
			ActivatedAtMs: s.ChapterActivatedAtMS, Tasks: taskRecords,
		},
		RecentResults: recent, PendingOutbox: pendingOutbox,
		LastAppliedConfigVersion: s.ConfigVersion,
		CreatedAtMs:              s.CreatedAtMS, UpdatedAtMs: s.UpdatedAtMS,
	}
	if s.FriendActions != nil {
		checkpoint.FriendActions = proto.Clone(s.FriendActions).(*datav1.FriendActionState)
	}
	for _, reservation := range s.FriendReservations {
		checkpoint.FriendReservations = append(
			checkpoint.FriendReservations,
			proto.Clone(reservation).(*datav1.FriendResourceReservation),
		)
	}
	sort.Slice(checkpoint.FriendReservations, func(i, j int) bool {
		return bytes.Compare(
			checkpoint.FriendReservations[i].InteractionId,
			checkpoint.FriendReservations[j].InteractionId,
		) < 0
	})
	for _, receipt := range s.FriendReceipts {
		checkpoint.FriendReceipts = append(
			checkpoint.FriendReceipts,
			proto.Clone(receipt).(*datav1.FriendInteractionReceipt),
		)
	}
	sort.Slice(checkpoint.FriendReceipts, func(i, j int) bool {
		left, right := checkpoint.FriendReceipts[i], checkpoint.FriendReceipts[j]
		if cmp := bytes.Compare(left.InteractionId, right.InteractionId); cmp != 0 {
			return cmp < 0
		}
		return left.Role < right.Role
	})
	for _, receipt := range s.FriendTaskCreditReceipts {
		checkpoint.FriendTaskCreditReceipts = append(
			checkpoint.FriendTaskCreditReceipts,
			proto.Clone(receipt).(*datav1.FriendTaskCreditReceipt),
		)
	}
	sort.Slice(checkpoint.FriendTaskCreditReceipts, func(i, j int) bool {
		return bytes.Compare(
			checkpoint.FriendTaskCreditReceipts[i].RelationId,
			checkpoint.FriendTaskCreditReceipts[j].RelationId,
		) < 0
	})
	if err := ValidateCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	return checkpoint, nil
}

func plotFromRecord(record *datav1.PlotStateRecord) *Plot {
	plot := &Plot{
		ID: record.PlotId, State: plotStateFromRecord(record.State),
		CropID: record.CropId, CropItemID: record.CropItemId,
		CropConfigVersion: record.CropConfigVersion, PlantedAtMS: record.PlantedAtMs,
		BaseYield: record.BaseYield, StolenQuantity: record.StolenQuantity,
		LastSettledAtMS: record.LastSettledAtMs,
		StealCount:      record.StealCount, StealQuantity: record.StealQuantity,
		MaxStealTimes: record.MaxStealTimes, ProtectedOwnerYield: record.ProtectedOwnerYield,
	}
	if record.MaturityValue != nil {
		plot.MaturityValueScaled9 = record.MaturityValue.ScaledValue
	}
	if record.BaseGrowthRate != nil {
		plot.BaseGrowthRateScaled6 = record.BaseGrowthRate.ScaledValue
	}
	if record.SettledGrowthValue != nil {
		plot.SettledGrowthValueScaled9 = record.SettledGrowthValue.ScaledValue
	}
	if record.EstimatedMatureAtMs != nil {
		estimate := record.GetEstimatedMatureAtMs()
		plot.EstimatedMatureAtMS = &estimate
	}
	if record.FertilizerEffect != nil {
		plot.FertilizerEffect = proto.Clone(record.FertilizerEffect).(*datav1.TimedEffectRecord)
	}
	if record.PestEffect != nil {
		plot.PestEffect = proto.Clone(record.PestEffect).(*datav1.TimedEffectRecord)
	}
	return plot
}

func (p *Plot) Record() (*datav1.PlotStateRecord, error) {
	if p == nil || p.ID == 0 {
		return nil, errors.New("state contains invalid plot")
	}
	record := &datav1.PlotStateRecord{
		PlotId: p.ID, State: plotStateToRecord(p.State),
		CropId: p.CropID, CropItemId: p.CropItemID,
		CropConfigVersion: p.CropConfigVersion, PlantedAtMs: p.PlantedAtMS,
		BaseYield: p.BaseYield, StolenQuantity: p.StolenQuantity,
		LastSettledAtMs: p.LastSettledAtMS,
		StealCount:      p.StealCount, StealQuantity: p.StealQuantity,
		MaxStealTimes: p.MaxStealTimes, ProtectedOwnerYield: p.ProtectedOwnerYield,
	}
	if p.State == plotv1.PlotState_GROWING || p.State == plotv1.PlotState_MATURE {
		record.MaturityValue = &datav1.GrowthDecimal9{ScaledValue: p.MaturityValueScaled9}
		record.BaseGrowthRate = &datav1.RateDecimal6{ScaledValue: p.BaseGrowthRateScaled6}
		record.SettledGrowthValue = &datav1.GrowthDecimal9{ScaledValue: p.SettledGrowthValueScaled9}
	}
	if p.EstimatedMatureAtMS != nil {
		estimate := *p.EstimatedMatureAtMS
		record.EstimatedMatureAtMs = &estimate
	}
	if p.FertilizerEffect != nil {
		record.FertilizerEffect = proto.Clone(p.FertilizerEffect).(*datav1.TimedEffectRecord)
	}
	if p.PestEffect != nil {
		record.PestEffect = proto.Clone(p.PestEffect).(*datav1.TimedEffectRecord)
	}
	return record, nil
}

func plotStateFromRecord(state datav1.PlotRecordState) plotv1.PlotState {
	switch state {
	case datav1.PlotRecordState_EMPTY:
		return plotv1.PlotState_EMPTY
	case datav1.PlotRecordState_GROWING:
		return plotv1.PlotState_GROWING
	case datav1.PlotRecordState_MATURE:
		return plotv1.PlotState_MATURE
	case datav1.PlotRecordState_NEED_CLEANUP:
		return plotv1.PlotState_NEED_CLEANUP
	default:
		return plotv1.PlotState_UNSPECIFIED
	}
}

func plotStateToRecord(state plotv1.PlotState) datav1.PlotRecordState {
	switch state {
	case plotv1.PlotState_EMPTY:
		return datav1.PlotRecordState_EMPTY
	case plotv1.PlotState_GROWING:
		return datav1.PlotRecordState_GROWING
	case plotv1.PlotState_MATURE:
		return datav1.PlotRecordState_MATURE
	case plotv1.PlotState_NEED_CLEANUP:
		return datav1.PlotRecordState_NEED_CLEANUP
	default:
		return datav1.PlotRecordState_PLOT_RECORD_STATE_UNSPECIFIED
	}
}

func taskMetric(taskID uint32) datav1.TaskMetric {
	switch taskID {
	case 1:
		return datav1.TaskMetric_TASK_BUY_SEEDS
	case 2:
		return datav1.TaskMetric_TASK_PLANT
	case 3:
		return datav1.TaskMetric_TASK_APPLY_FERTILIZER
	case 4:
		return datav1.TaskMetric_TASK_HARVEST
	case 5:
		return datav1.TaskMetric_TASK_SELL_CROP
	case 6:
		return datav1.TaskMetric_TASK_ADD_FRIEND
	case 7:
		return datav1.TaskMetric_TASK_STEAL_CROP
	case 8:
		return datav1.TaskMetric_TASK_APPLY_PEST_TO_FRIEND
	default:
		return datav1.TaskMetric_TASK_METRIC_UNSPECIFIED
	}
}

func chapterStatusFromRecord(status datav1.ChapterRecordStatus) chapterv1.ChapterStatus {
	switch status {
	case datav1.ChapterRecordStatus_IN_PROGRESS:
		return chapterv1.ChapterStatus_IN_PROGRESS
	case datav1.ChapterRecordStatus_CLAIMABLE:
		return chapterv1.ChapterStatus_CLAIMABLE
	case datav1.ChapterRecordStatus_CLAIMED:
		return chapterv1.ChapterStatus_CLAIMED
	default:
		return chapterv1.ChapterStatus_UNSPECIFIED
	}
}

func chapterStatusToRecord(status chapterv1.ChapterStatus) datav1.ChapterRecordStatus {
	switch status {
	case chapterv1.ChapterStatus_IN_PROGRESS:
		return datav1.ChapterRecordStatus_IN_PROGRESS
	case chapterv1.ChapterStatus_CLAIMABLE:
		return datav1.ChapterRecordStatus_CLAIMABLE
	case chapterv1.ChapterStatus_CLAIMED:
		return datav1.ChapterRecordStatus_CLAIMED
	default:
		return datav1.ChapterRecordStatus_CHAPTER_RECORD_STATUS_UNSPECIFIED
	}
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for i := range left {
		different |= left[i] ^ right[i]
	}
	return different == 0
}
