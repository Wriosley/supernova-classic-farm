package player

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

const (
	tcaplusOutboxPending   uint32 = 1
	tcaplusOutboxDelivered uint32 = 3
)

type tcaplusDurableClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
}

// TcaplusDurableCheckpointStore composes the single-record checkpoint CAS
// with Tcaplus ShardFence validation and idempotent PlayerOutbox persistence.
// Cross-record atomicity is intentionally replaced by ordered, retryable steps.
type TcaplusDurableCheckpointStore struct {
	checkpoints *TcaplusCheckpointStore
	client      tcaplusDurableClient
	zoneID      uint32
	ownerZoneID string
}

func NewTcaplusDurableCheckpointStore(
	checkpoints *TcaplusCheckpointStore,
	client tcaplusDurableClient,
	zoneID uint32,
	ownerZoneID string,
) (*TcaplusDurableCheckpointStore, error) {
	if checkpoints == nil || client == nil || zoneID == 0 || ownerZoneID == "" {
		return nil, errors.New("complete durable Tcaplus checkpoint configuration is required")
	}
	return &TcaplusDurableCheckpointStore{
		checkpoints: checkpoints, client: client,
		zoneID: zoneID, ownerZoneID: ownerZoneID,
	}, nil
}

func (s *TcaplusDurableCheckpointStore) Create(
	ctx context.Context,
	checkpoint *datav1.PlayerCheckpointV1,
) (CheckpointWriteResult, error) {
	return s.CreateInitial(ctx, checkpoint)
}

// CreateInitial 先校验当前 Zone 仍是 Shard Fence Owner，再 Insert-if-absent。
func (s *TcaplusDurableCheckpointStore) CreateInitial(
	ctx context.Context,
	checkpoint *datav1.PlayerCheckpointV1,
) (CheckpointWriteResult, error) {
	if checkpoint == nil {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict},
			errors.New("checkpoint is required")
	}
	fence := &tcaplusv1.ShardFence{
		LogicalShardId: checkpoint.LogicalShardId,
	}
	if err := s.client.DoGet(fence, &option.PBOpt{Ctx: ctx}, s.zoneID); err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
			fmt.Errorf("load Tcaplus create fence: %w", err)
	}
	if fence.OwnerZoneId != s.ownerZoneID ||
		fence.OwnerEpoch != checkpoint.OwnerEpoch {
		return CheckpointWriteResult{Status: CheckpointWriteFenced}, nil
	}
	return s.checkpoints.CreateInitial(ctx, checkpoint)
}

func (s *TcaplusDurableCheckpointStore) Load(
	ctx context.Context,
	playerID uint64,
) (LoadedCheckpoint, error) {
	loaded, err := s.checkpoints.Load(ctx, playerID)
	if err != nil {
		return LoadedCheckpoint{}, err
	}
	if loaded.State == nil || len(loaded.State.PendingOutbox) == 0 {
		return loaded, nil
	}
	retained := loaded.State.PendingOutbox[:0]
	changed := false
	for _, pending := range loaded.State.PendingOutbox {
		row, found, err := s.loadOutbox(ctx, pending.EventId)
		if err != nil {
			return LoadedCheckpoint{}, err
		}
		if !found {
			checkpoint, err := loaded.State.Checkpoint()
			if err != nil {
				return LoadedCheckpoint{}, err
			}
			if err := s.ensureOutbox(ctx, checkpoint, pending); err != nil {
				return LoadedCheckpoint{}, err
			}
			retained = append(retained, pending)
			continue
		}
		if row.RelayStatus == tcaplusOutboxDelivered {
			changed = true
			continue
		}
		retained = append(retained, pending)
	}
	loaded.State.PendingOutbox = retained
	if changed {
		loaded.State.CheckpointRevision++
	}
	return loaded, nil
}

func (s *TcaplusDurableCheckpointStore) SaveCAS(
	ctx context.Context,
	write CheckpointWrite,
) (CheckpointWriteResult, error) {
	if write.Checkpoint == nil {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict},
			errors.New("checkpoint is required")
	}
	fence := &tcaplusv1.ShardFence{
		LogicalShardId: write.Checkpoint.LogicalShardId,
	}
	if err := s.client.DoGet(fence, &option.PBOpt{Ctx: ctx}, s.zoneID); err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
			fmt.Errorf("load Tcaplus checkpoint fence: %w", err)
	}
	if fence.OwnerZoneId != s.ownerZoneID ||
		fence.OwnerEpoch != write.Checkpoint.OwnerEpoch {
		return CheckpointWriteResult{Status: CheckpointWriteFenced}, nil
	}
	result, err := s.checkpoints.SaveCAS(ctx, write)
	if writeErr := checkpointWriteError(result, err); writeErr != nil {
		return result, err
	}
	pending := append(
		[]*datav1.PendingOutboxRecord(nil),
		write.Checkpoint.PendingOutbox...,
	)
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].CreatedAtMs != pending[j].CreatedAtMs {
			return pending[i].CreatedAtMs < pending[j].CreatedAtMs
		}
		return bytes.Compare(pending[i].EventId, pending[j].EventId) < 0
	})
	for _, item := range pending {
		if err := s.ensureOutbox(ctx, write.Checkpoint, item); err != nil {
			return CheckpointWriteResult{
				Status:   CheckpointWriteRetryableFailure,
				NewToken: cloneStoreToken(result.NewToken),
			}, err
		}
	}
	return result, nil
}

func (s *TcaplusDurableCheckpointStore) ensureOutbox(
	ctx context.Context,
	checkpoint *datav1.PlayerCheckpointV1,
	pending *datav1.PendingOutboxRecord,
) error {
	if err := validatePendingOutbox(checkpoint, pending); err != nil {
		return err
	}
	existing, found, err := s.loadOutbox(ctx, pending.EventId)
	if err != nil {
		return err
	}
	expected := outboxRecord(checkpoint, pending)
	if found {
		if !sameOutboxImmutable(existing, expected) {
			return errors.New("Tcaplus pending Outbox immutable row conflict")
		}
		return nil
	}
	opt := &option.PBOpt{
		Ctx: ctx, ResultFlag: option.TcaplusResultFlagAllNewValue,
	}
	if err := s.client.DoInsert(expected, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			reloaded, exists, loadErr := s.loadOutbox(ctx, pending.EventId)
			if loadErr == nil && exists && sameOutboxImmutable(reloaded, expected) {
				return nil
			}
		}
		return fmt.Errorf("insert Tcaplus pending Outbox: %w", err)
	}
	return nil
}

func (s *TcaplusDurableCheckpointStore) loadOutbox(
	ctx context.Context,
	eventID []byte,
) (*tcaplusv1.PlayerOutbox, bool, error) {
	record := &tcaplusv1.PlayerOutbox{EventId: append([]byte(nil), eventID...)}
	if err := s.client.DoGet(record, &option.PBOpt{Ctx: ctx}, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("load Tcaplus PlayerOutbox: %w", err)
	}
	return record, true, nil
}

func outboxRecord(
	checkpoint *datav1.PlayerCheckpointV1,
	pending *datav1.PendingOutboxRecord,
) *tcaplusv1.PlayerOutbox {
	return &tcaplusv1.PlayerOutbox{
		EventId:              append([]byte(nil), pending.EventId...),
		AggregatePlayerId:    checkpoint.PlayerId,
		LogicalShardId:       checkpoint.LogicalShardId,
		EventType:            uint32(pending.EventType),
		EventContractVersion: pending.EventContractVersion,
		CausedByRequestId:    append([]byte(nil), pending.CausedByRequestId...),
		CreatedOwnerEpoch:    pending.CreatedOwnerEpoch,
		CreatedPlayerSeq:     pending.CreatedPlayerSeq,
		CreatedAtMs:          pending.CreatedAtMs,
		Payload:              append([]byte(nil), pending.Payload...),
		PayloadSha256:        append([]byte(nil), pending.PayloadSha256...),
		RelayStatus:          tcaplusOutboxPending,
		NextAttemptAtMs:      pending.CreatedAtMs,
	}
}

func sameOutboxImmutable(a, b *tcaplusv1.PlayerOutbox) bool {
	return a != nil && b != nil &&
		bytes.Equal(a.EventId, b.EventId) &&
		a.DbShardId == b.DbShardId &&
		a.AggregatePlayerId == b.AggregatePlayerId &&
		a.LogicalShardId == b.LogicalShardId &&
		a.EventType == b.EventType &&
		a.EventContractVersion == b.EventContractVersion &&
		bytes.Equal(a.CausedByRequestId, b.CausedByRequestId) &&
		a.CreatedOwnerEpoch == b.CreatedOwnerEpoch &&
		a.CreatedPlayerSeq == b.CreatedPlayerSeq &&
		a.CreatedAtMs == b.CreatedAtMs &&
		bytes.Equal(a.Payload, b.Payload) &&
		bytes.Equal(a.PayloadSha256, b.PayloadSha256)
}
