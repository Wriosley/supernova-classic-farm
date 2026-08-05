package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

type TcaplusControlClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
	DoDelete(proto.Message, *option.PBOpt, ...uint32) error
	Traverse(proto.Message) ([]proto.Message, error)
}

type TcaplusControlStore struct {
	client TcaplusControlClient
	zoneID uint32
}

func NewTcaplusControlStore(
	client TcaplusControlClient,
	zoneID uint32,
) (*TcaplusControlStore, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus control client and zone are required")
	}
	return &TcaplusControlStore{client: client, zoneID: zoneID}, nil
}

func (s *TcaplusControlStore) EnsureStaticFences(
	ctx context.Context,
	snapshot Snapshot,
	now time.Time,
) (int, error) {
	if err := validateStaticFenceSnapshot(snapshot); err != nil {
		return 0, err
	}
	messages, err := s.client.Traverse(&tcaplusv1.ShardFence{})
	if err != nil {
		return 0, fmt.Errorf("traverse Tcaplus fences for bootstrap: %w", err)
	}
	existing := make(map[uint32]*tcaplusv1.ShardFence, len(messages))
	for _, message := range messages {
		record, ok := message.(*tcaplusv1.ShardFence)
		if !ok {
			return 0, errors.New("Tcaplus fence traversal returned wrong record type")
		}
		existing[record.LogicalShardId] = record
	}

	type fenceJob struct {
		shardID uint32
		target  RouteEntry
		record  *tcaplusv1.ShardFence
	}
	jobs := make(chan fenceJob)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var updated atomic.Int64
	var firstErr error
	var errOnce sync.Once
	var workers sync.WaitGroup
	const bootstrapWorkers = 32
	for range bootstrapWorkers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				if workerCtx.Err() != nil {
					continue
				}
				if ensureErr := s.ensureStaticFence(
					workerCtx, job.shardID, job.target, job.record, now,
				); ensureErr != nil {
					errOnce.Do(func() {
						firstErr = ensureErr
						cancel()
					})
					continue
				}
				updated.Add(1)
			}
		}()
	}

	for shardID, target := range snapshot.Entries {
		record := existing[uint32(shardID)]
		if record != nil {
			if record.OwnerEpoch != 1 || record.RouteVersion != 1 {
				// A migration has already advanced this fence. LoadFences hydrates
				// the active route from it after static bootstrap is complete.
				continue
			}
			if record.OwnerZoneId == target.OwnerZoneID {
				continue
			}
		}
		select {
		case jobs <- fenceJob{
			shardID: uint32(shardID), target: target, record: record,
		}:
		case <-workerCtx.Done():
			break
		}
	}
	close(jobs)
	workers.Wait()
	return int(updated.Load()), firstErr
}

func (s *TcaplusControlStore) ensureStaticFence(
	ctx context.Context,
	shardID uint32,
	target RouteEntry,
	record *tcaplusv1.ShardFence,
	now time.Time,
) error {
	if record == nil {
		record = &tcaplusv1.ShardFence{
			LogicalShardId: shardID, OwnerZoneId: target.OwnerZoneID,
			OwnerEpoch: 1, RouteVersion: 1,
			TransitionId: staticFenceTransitionID(shardID, target.OwnerZoneID),
			FencedAtMs:   now.UTC().UnixMilli(),
		}
		if err := s.client.DoInsert(record, tcaplusInsertOpt(ctx), s.zoneID); err != nil {
			if tcaplusdb.IsAlreadyExists(err) {
				existing, _, exists, loadErr := s.loadFence(ctx, shardID)
				if loadErr == nil && exists &&
					existing.OwnerEpoch == 1 &&
					existing.RouteVersion == 1 &&
					existing.OwnerZoneId == target.OwnerZoneID {
					return nil
				}
				return fmt.Errorf("Tcaplus fence %d changed concurrently", shardID)
			}
			return fmt.Errorf("insert Tcaplus fence %d: %w", shardID, err)
		}
		return nil
	}
	if record.OwnerEpoch != 1 || record.RouteVersion != 1 ||
		(record.OwnerZoneId != DefaultZoneID &&
			record.OwnerZoneId != target.OwnerZoneID) {
		return fmt.Errorf(
			"Tcaplus fence %d is not an epoch-one static bootstrap row", shardID,
		)
	}
	current, version, found, err := s.loadFence(ctx, shardID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("Tcaplus fence %d disappeared during bootstrap", shardID)
	}
	if current.OwnerEpoch != 1 || current.RouteVersion != 1 ||
		(current.OwnerZoneId != DefaultZoneID &&
			current.OwnerZoneId != target.OwnerZoneID) {
		return fmt.Errorf(
			"Tcaplus fence %d changed during static bootstrap", shardID,
		)
	}
	if current.OwnerZoneId == target.OwnerZoneID {
		return nil
	}
	current.OwnerZoneId = target.OwnerZoneID
	current.TransitionId = staticFenceTransitionID(shardID, target.OwnerZoneID)
	current.FencedAtMs = now.UTC().UnixMilli()
	if err := s.client.DoUpdate(
		current, tcaplusUpdateOpt(ctx, version), s.zoneID,
	); err != nil {
		return fmt.Errorf("update Tcaplus fence %d: %w", shardID, err)
	}
	return nil
}

func (s *TcaplusControlStore) LoadFences(ctx context.Context) ([]ShardFence, error) {
	messages, err := s.client.Traverse(&tcaplusv1.ShardFence{})
	if err != nil {
		return nil, fmt.Errorf("traverse Tcaplus fences: %w", err)
	}
	fences := make([]ShardFence, 0, len(messages))
	for _, message := range messages {
		record, ok := message.(*tcaplusv1.ShardFence)
		if !ok {
			return nil, errors.New("Tcaplus fence traversal returned wrong record type")
		}
		fences = append(fences, ShardFence{
			ShardID: record.LogicalShardId, OwnerZoneID: record.OwnerZoneId,
			OwnerEpoch: record.OwnerEpoch, RouteVersion: record.RouteVersion,
			TransitionID: formatUUIDBytes(record.TransitionId),
		})
	}
	sort.Slice(fences, func(i, j int) bool { return fences[i].ShardID < fences[j].ShardID })
	if len(fences) != int(ShardCount) {
		return nil, fmt.Errorf("expected %d Tcaplus fences, got %d", ShardCount, len(fences))
	}
	for index, fence := range fences {
		if fence.ShardID != uint32(index) {
			return nil, fmt.Errorf("Tcaplus fence order mismatch at %d", index)
		}
	}
	return fences, nil
}

func (s *TcaplusControlStore) LoadFence(
	ctx context.Context,
	shardID uint32,
) (ShardFence, error) {
	record, _, found, err := s.loadFence(ctx, shardID)
	if err != nil {
		return ShardFence{}, err
	}
	if !found {
		return ShardFence{}, ErrFenceConflict
	}
	return ShardFence{
		ShardID: record.LogicalShardId, OwnerZoneID: record.OwnerZoneId,
		OwnerEpoch: record.OwnerEpoch, RouteVersion: record.RouteVersion,
		TransitionID: formatUUIDBytes(record.TransitionId),
	}, nil
}

func (s *TcaplusControlStore) AdvanceFence(
	ctx context.Context,
	prepared RouteEntry,
) error {
	if prepared.ShardID >= ShardCount ||
		prepared.State != RouteStatePreparing ||
		prepared.OwnerZoneID == "" ||
		prepared.PreviousOwnerZoneID == "" ||
		prepared.OwnerEpoch < 2 ||
		prepared.RouteVersion < 2 ||
		prepared.TransitionID == "" {
		return errors.New("committed PREPARING route is required")
	}
	transition, err := parseUUIDBytes(prepared.TransitionID)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 8; attempt++ {
		record, version, found, err := s.loadFence(ctx, prepared.ShardID)
		if err != nil {
			return err
		}
		if !found {
			return ErrFenceConflict
		}
		if record.OwnerZoneId == prepared.OwnerZoneID &&
			record.OwnerEpoch == prepared.OwnerEpoch &&
			record.RouteVersion == prepared.RouteVersion &&
			bytes.Equal(record.TransitionId, transition) {
			return nil
		}
		if record.OwnerZoneId != prepared.PreviousOwnerZoneID ||
			record.OwnerEpoch >= prepared.OwnerEpoch ||
			record.RouteVersion >= prepared.RouteVersion {
			return ErrFenceConflict
		}
		record.OwnerZoneId = prepared.OwnerZoneID
		record.OwnerEpoch = prepared.OwnerEpoch
		record.RouteVersion = prepared.RouteVersion
		record.TransitionId = transition
		record.FencedAtMs = time.Now().UTC().UnixMilli()
		if err := s.client.DoUpdate(
			record, tcaplusUpdateOpt(ctx, version), s.zoneID,
		); err == nil {
			return nil
		}
	}
	return ErrFenceConflict
}

func (s *TcaplusControlStore) loadFence(
	ctx context.Context,
	shardID uint32,
) (*tcaplusv1.ShardFence, int32, bool, error) {
	if err := validShardID(shardID); err != nil {
		return nil, 0, false, err
	}
	record := &tcaplusv1.ShardFence{LogicalShardId: shardID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, false, nil
		}
		return nil, 0, false, fmt.Errorf("get Tcaplus fence: %w", err)
	}
	return record, opt.Version, true, nil
}

func (s *TcaplusControlStore) UpsertProgress(
	ctx context.Context,
	row MigrationProgressRow,
) error {
	if err := validateOpenProgressRow(row); err != nil {
		return err
	}
	record, err := progressRecord(row)
	if err != nil {
		return err
	}
	current, version, found, err := s.loadProgressRecord(ctx, row.ShardID)
	if err != nil {
		return err
	}
	if found {
		proto.Reset(current)
		proto.Merge(current, record)
		return s.client.DoUpdate(current, tcaplusUpdateOpt(ctx, version), s.zoneID)
	}
	if err := s.client.DoInsert(record, tcaplusInsertOpt(ctx), s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return s.UpsertProgress(ctx, row)
		}
		return fmt.Errorf("insert Tcaplus migration progress: %w", err)
	}
	return nil
}

func (s *TcaplusControlStore) LoadProgress(
	ctx context.Context,
	shardID uint32,
) (MigrationProgressRow, bool, error) {
	record, _, found, err := s.loadProgressRecord(ctx, shardID)
	if err != nil || !found {
		return MigrationProgressRow{}, found, err
	}
	row, err := progressRow(record)
	return row, err == nil, err
}

func (s *TcaplusControlStore) LoadOpenProgress(
	ctx context.Context,
) ([]MigrationProgressRow, error) {
	messages, err := s.client.Traverse(&tcaplusv1.MigrationProgress{})
	if err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("traverse Tcaplus migration progress: %w", err)
	}
	rows := make([]MigrationProgressRow, 0, len(messages))
	for _, message := range messages {
		record, ok := message.(*tcaplusv1.MigrationProgress)
		if !ok || record.Status != MigrationStatusOpen {
			continue
		}
		row, err := progressRow(record)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ShardID < rows[j].ShardID })
	return rows, nil
}

func (s *TcaplusControlStore) MarkAbandoned(
	ctx context.Context,
	shardID uint32,
	transitionID string,
	now time.Time,
) error {
	record, version, found, err := s.loadProgressRecord(ctx, shardID)
	if err != nil {
		return err
	}
	if !found || record.Status != MigrationStatusOpen {
		return errors.New("open migration progress not found")
	}
	transition, err := parseUUIDBytes(transitionID)
	if err != nil || !bytes.Equal(transition, record.TransitionId) {
		return errors.New("migration transition does not match")
	}
	if record.Step == MigrationStepFenceAdvanced ||
		record.Step == MigrationStepTargetPrepared {
		return ErrFenceAlreadyAdvanced
	}
	record.Status = MigrationStatusAbandoned
	record.UpdatedAtMs = now.UTC().UnixMilli()
	return s.client.DoUpdate(record, tcaplusUpdateOpt(ctx, version), s.zoneID)
}

func (s *TcaplusControlStore) DeleteOpenProgress(
	ctx context.Context,
	shardID uint32,
	transitionID string,
) error {
	record, version, found, err := s.loadProgressRecord(ctx, shardID)
	if err != nil {
		return err
	}
	if !found || record.Status != MigrationStatusOpen {
		return errors.New("open migration progress not found")
	}
	transition, err := parseUUIDBytes(transitionID)
	if err != nil || !bytes.Equal(transition, record.TransitionId) {
		return errors.New("migration transition does not match")
	}
	return s.client.DoDelete(record, tcaplusUpdateOpt(ctx, version), s.zoneID)
}

func (s *TcaplusControlStore) LoadAbandonedEpoch(
	ctx context.Context,
	shardID uint32,
) (uint64, bool, error) {
	record, _, found, err := s.loadProgressRecord(ctx, shardID)
	if err != nil || !found || record.Status != MigrationStatusAbandoned {
		return 0, false, err
	}
	return record.PreparedOwnerEpoch, true, nil
}

func (s *TcaplusControlStore) loadProgressRecord(
	ctx context.Context,
	shardID uint32,
) (*tcaplusv1.MigrationProgress, int32, bool, error) {
	if err := validShardID(shardID); err != nil {
		return nil, 0, false, err
	}
	record := &tcaplusv1.MigrationProgress{LogicalShardId: shardID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, false, nil
		}
		return nil, 0, false, err
	}
	return record, opt.Version, true, nil
}

func progressRecord(row MigrationProgressRow) (*tcaplusv1.MigrationProgress, error) {
	transition, err := parseUUIDBytes(row.TransitionID)
	if err != nil {
		return nil, err
	}
	sourceLease, err := parseUUIDBytes(row.SourceLeaseID)
	if err != nil {
		return nil, fmt.Errorf("parse source lease ID: %w", err)
	}
	preparedLease, err := parseUUIDBytes(row.PreparedLeaseID)
	if err != nil {
		return nil, fmt.Errorf("parse prepared lease ID: %w", err)
	}
	players, err := json.Marshal(row.Players)
	if err != nil {
		return nil, err
	}
	if row.Status == "" {
		row.Status = MigrationStatusOpen
	}
	if row.UpdatedAtMS <= 0 {
		row.UpdatedAtMS = time.Now().UTC().UnixMilli()
	}
	if row.PreparedLeaseTerm == 0 {
		row.PreparedLeaseTerm = 1
	}
	return &tcaplusv1.MigrationProgress{
		LogicalShardId: row.ShardID, TransitionId: transition,
		Status: row.Status, Step: row.Step,
		SourceZoneId: row.SourceZoneID, SourceEndpoint: row.SourceEndpoint,
		SourceOwnerEpoch: row.SourceOwnerEpoch, SourceRouteVersion: row.SourceRouteVersion,
		SourceLeaseId: sourceLease,
		TargetZoneId:  row.TargetZoneID, TargetEndpoint: row.TargetEndpoint,
		PreparedOwnerEpoch:   row.PreparedOwnerEpoch,
		PreparedRouteVersion: row.PreparedRouteVersion,
		PreparedLeaseId:      preparedLease,
		PreparedLeaseTerm:    row.PreparedLeaseTerm,
		PlayersJson:          players, UpdatedAtMs: row.UpdatedAtMS,
	}, nil
}

func progressRow(record *tcaplusv1.MigrationProgress) (MigrationProgressRow, error) {
	var players []MigrationPlayer
	if err := json.Unmarshal(record.PlayersJson, &players); err != nil {
		return MigrationProgressRow{}, err
	}
	return MigrationProgressRow{
		ShardID:      record.LogicalShardId,
		TransitionID: formatUUIDBytes(record.TransitionId),
		Status:       record.Status, Step: record.Step,
		SourceZoneID: record.SourceZoneId, SourceEndpoint: record.SourceEndpoint,
		SourceOwnerEpoch:   record.SourceOwnerEpoch,
		SourceRouteVersion: record.SourceRouteVersion,
		SourceLeaseID:      formatUUIDBytes(record.SourceLeaseId),
		TargetZoneID:       record.TargetZoneId, TargetEndpoint: record.TargetEndpoint,
		PreparedOwnerEpoch:   record.PreparedOwnerEpoch,
		PreparedRouteVersion: record.PreparedRouteVersion,
		PreparedLeaseID:      formatUUIDBytes(record.PreparedLeaseId),
		PreparedLeaseTerm:    record.PreparedLeaseTerm,
		Players:              players, UpdatedAtMS: record.UpdatedAtMs,
	}, nil
}

func tcaplusInsertOpt(ctx context.Context) *option.PBOpt {
	return &option.PBOpt{Ctx: ctx, ResultFlag: option.TcaplusResultFlagAllNewValue}
}

func tcaplusUpdateOpt(ctx context.Context, version int32) *option.PBOpt {
	return &option.PBOpt{
		Ctx: ctx, Version: version,
		VersionPolicy: option.CheckDataVersionAutoIncrease,
		ResultFlag:    option.TcaplusResultFlagAllNewValue,
	}
}
