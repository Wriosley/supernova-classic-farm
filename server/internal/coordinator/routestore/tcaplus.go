package routestore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

const shardMapMetaID uint32 = 1

type TcaplusClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
	Traverse(proto.Message) ([]proto.Message, error)
}

type TcaplusStore struct {
	client TcaplusClient
	zoneID uint32
}

func NewTcaplusStore(client TcaplusClient, zoneID uint32) (*TcaplusStore, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus route client and zone are required")
	}
	return &TcaplusStore{client: client, zoneID: zoneID}, nil
}

func (s *TcaplusStore) Load(ctx context.Context) (Snapshot, error) {
	meta, metaVersion, found, err := s.loadMeta(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	routes, routesFound, err := s.loadRoutes(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if !found && !routesFound {
		return Snapshot{}, ErrRouteStoreEmpty
	}
	if !found || !routesFound {
		return Snapshot{}, fmt.Errorf("%w: metadata and routes are not both present", ErrRouteStoreCorrupt)
	}
	if meta.HasPendingCommit {
		if err := s.recoverPending(ctx, meta, metaVersion); err != nil {
			return Snapshot{}, err
		}
		meta, _, found, err = s.loadMeta(ctx)
		if err != nil || !found {
			return Snapshot{}, fmt.Errorf("reload finalized route metadata: %w", err)
		}
		routes, routesFound, err = s.loadRoutes(ctx)
		if err != nil || !routesFound {
			return Snapshot{}, fmt.Errorf("reload finalized routes: %w", err)
		}
	}
	return snapshotFromRecords(meta, routes)
}

func (s *TcaplusStore) BootstrapIfEmpty(ctx context.Context, candidate Snapshot) (Snapshot, bool, error) {
	if err := validateSnapshot(candidate); err != nil {
		return Snapshot{}, false, err
	}
	_, _, metaFound, err := s.loadMeta(ctx)
	if err != nil {
		return Snapshot{}, false, err
	}
	if metaFound {
		loaded, loadErr := s.Load(ctx)
		return loaded, false, loadErr
	}
	for _, entry := range candidate.Entries {
		record, err := routeRecord(entry, candidate.Metadata.MapVersion)
		if err != nil {
			return Snapshot{}, false, err
		}
		if err := s.client.DoInsert(record, &option.PBOpt{Ctx: ctx}, s.zoneID); err != nil {
			if tcaplusdb.IsAlreadyExists(err) {
				_, version, found, loadErr := s.loadRoute(ctx, entry.ShardID)
				if loadErr != nil || !found {
					return Snapshot{}, false, fmt.Errorf("load interrupted bootstrap route %d: %w", entry.ShardID, loadErr)
				}
				if updateErr := s.client.DoUpdate(record, &option.PBOpt{Ctx: ctx, Version: version}, s.zoneID); updateErr != nil {
					return Snapshot{}, false, fmt.Errorf("replace interrupted bootstrap route %d: %w", entry.ShardID, updateErr)
				}
				continue
			}
			return Snapshot{}, false, fmt.Errorf("insert route %d: %w", entry.ShardID, err)
		}
	}
	meta := metaRecord(candidate.Metadata)
	if err := s.client.DoInsert(meta, &option.PBOpt{Ctx: ctx}, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			loaded, loadErr := s.Load(ctx)
			return loaded, false, loadErr
		}
		return Snapshot{}, false, fmt.Errorf("insert route metadata: %w", err)
	}
	loaded, err := s.Load(ctx)
	return loaded, err == nil, err
}

func (s *TcaplusStore) CommitPreparing(ctx context.Context, entry routing.RouteEntry, expected uint64) (Snapshot, error) {
	return s.commit(ctx, entry, expected, validatePreparing)
}

func (s *TcaplusStore) CommitActive(ctx context.Context, entry routing.RouteEntry, expected uint64) (Snapshot, error) {
	return s.commit(ctx, entry, expected, validateActive)
}

func (s *TcaplusStore) RestoreSource(ctx context.Context, entry routing.RouteEntry, expected uint64) (Snapshot, error) {
	return s.commit(ctx, entry, expected, validateRestoredSource)
}

func (s *TcaplusStore) commit(ctx context.Context, target routing.RouteEntry, expected uint64, validate func(routing.RouteEntry, routing.RouteEntry) error) (Snapshot, error) {
	meta, metaVersion, found, err := s.loadMeta(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if !found || target.ShardID >= routing.ShardCount {
		return Snapshot{}, ErrRouteConflict
	}
	current, routeVersion, found, err := s.loadRoute(ctx, target.ShardID)
	if err != nil {
		return Snapshot{}, err
	}
	if !found {
		return Snapshot{}, fmt.Errorf("%w: route %d is missing", ErrRouteStoreCorrupt, target.ShardID)
	}
	if meta.HasPendingCommit {
		pending, pendingErr := pendingRoute(meta)
		if pendingErr != nil {
			return Snapshot{}, pendingErr
		}
		if pending == target && meta.PendingMapVersion == expected+1 {
			return s.Load(ctx)
		}
		return Snapshot{}, ErrRouteConflict
	}
	currentDomain, err := routeEntry(current)
	if err != nil {
		return Snapshot{}, err
	}
	if meta.MapVersion != expected {
		if currentDomain == target && current.CommittedMapVersion == expected+1 && meta.MapVersion == expected+1 {
			return s.Load(ctx)
		}
		return Snapshot{}, ErrRouteConflict
	}
	if err := validate(currentDomain, target); err != nil {
		return Snapshot{}, err
	}
	targetRecord, err := routeRecord(target, expected+1)
	if err != nil {
		return Snapshot{}, err
	}
	setPending(meta, target, expected+1)
	metaOpt := &option.PBOpt{Ctx: ctx, Version: metaVersion}
	if err := s.client.DoUpdate(meta, metaOpt, s.zoneID); err != nil {
		return Snapshot{}, fmt.Errorf("set pending route intent: %w", err)
	}
	if err := s.client.DoUpdate(targetRecord, &option.PBOpt{Ctx: ctx, Version: routeVersion}, s.zoneID); err != nil {
		return Snapshot{}, fmt.Errorf("commit route row: %w", err)
	}
	if err := s.finalizeMeta(ctx, meta, metaOpt.Version); err != nil {
		return Snapshot{}, err
	}
	return s.Load(ctx)
}

func (s *TcaplusStore) recoverPending(ctx context.Context, meta *tcaplusv1.ShardMapMeta, metaVersion int32) error {
	target, err := pendingRoute(meta)
	if err != nil {
		return err
	}
	if target.ShardID >= routing.ShardCount {
		return fmt.Errorf("%w: pending shard is outside route set", ErrRouteStoreCorrupt)
	}
	currentRecord, routeVersion, found, err := s.loadRoute(ctx, target.ShardID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: pending route %d is missing", ErrRouteStoreCorrupt, target.ShardID)
	}
	current, err := routeEntry(currentRecord)
	if err != nil {
		return err
	}
	targetRecord, err := routeRecord(target, meta.PendingMapVersion)
	if err != nil {
		return err
	}
	if !proto.Equal(currentRecord, targetRecord) {
		legalOld := validatePreparing(current, target) == nil ||
			validateActive(current, target) == nil || validateRestoredSource(current, target) == nil
		if !legalOld || currentRecord.CommittedMapVersion != meta.MapVersion {
			return fmt.Errorf("%w: pending route %d conflicts with stored route", ErrRouteStoreCorrupt, target.ShardID)
		}
		if err := s.client.DoUpdate(targetRecord, &option.PBOpt{Ctx: ctx, Version: routeVersion}, s.zoneID); err != nil {
			return fmt.Errorf("recover pending route row: %w", err)
		}
	}
	return s.finalizeMeta(ctx, meta, metaVersion)
}

func (s *TcaplusStore) finalizeMeta(ctx context.Context, meta *tcaplusv1.ShardMapMeta, version int32) error {
	meta.MapVersion = meta.PendingMapVersion
	meta.UpdatedAtMs = meta.PendingUpdatedAtMs
	clearPending(meta)
	if err := s.client.DoUpdate(meta, &option.PBOpt{Ctx: ctx, Version: version}, s.zoneID); err != nil {
		return fmt.Errorf("finalize route metadata: %w", err)
	}
	return nil
}

func (s *TcaplusStore) loadMeta(ctx context.Context) (*tcaplusv1.ShardMapMeta, int32, bool, error) {
	record := &tcaplusv1.ShardMapMeta{MapId: shardMapMetaID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, false, nil
		}
		return nil, 0, false, fmt.Errorf("get route metadata: %w", err)
	}
	return record, opt.Version, true, nil
}

func (s *TcaplusStore) loadRoute(ctx context.Context, shardID uint32) (*tcaplusv1.ShardRoute, int32, bool, error) {
	record := &tcaplusv1.ShardRoute{LogicalShardId: shardID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, false, nil
		}
		return nil, 0, false, fmt.Errorf("get route %d: %w", shardID, err)
	}
	return record, opt.Version, true, nil
}

func (s *TcaplusStore) loadRoutes(ctx context.Context) ([]*tcaplusv1.ShardRoute, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	messages, err := s.client.Traverse(&tcaplusv1.ShardRoute{})
	if err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("traverse routes: %w", err)
	}
	routes := make([]*tcaplusv1.ShardRoute, 0, len(messages))
	for _, message := range messages {
		record, ok := message.(*tcaplusv1.ShardRoute)
		if !ok {
			return nil, false, fmt.Errorf("%w: route traversal returned %T", ErrRouteStoreCorrupt, message)
		}
		routes = append(routes, proto.Clone(record).(*tcaplusv1.ShardRoute))
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].LogicalShardId < routes[j].LogicalShardId })
	return routes, true, nil
}

func snapshotFromRecords(meta *tcaplusv1.ShardMapMeta, records []*tcaplusv1.ShardRoute) (Snapshot, error) {
	entries := make([]routing.RouteEntry, len(records))
	for index, record := range records {
		if record.LogicalShardId != uint32(index) || record.CommittedMapVersion > meta.MapVersion {
			return Snapshot{}, fmt.Errorf("%w: route %d is unordered or ahead of map", ErrRouteStoreCorrupt, index)
		}
		entry, err := routeEntry(record)
		if err != nil {
			return Snapshot{}, err
		}
		entries[index] = entry
	}
	snapshot := Snapshot{Metadata: Metadata{
		ShardCount: meta.ShardCount, HashAlgorithmVersion: meta.HashAlgorithmVersion,
		AssignmentAlgorithmVersion: meta.AssignmentAlgorithmVersion,
		MapVersion:                 meta.MapVersion, UpdatedAt: time.UnixMilli(meta.UpdatedAtMs).UTC(),
	}, Entries: entries}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func metaRecord(metadata Metadata) *tcaplusv1.ShardMapMeta {
	return &tcaplusv1.ShardMapMeta{MapId: shardMapMetaID, ShardCount: metadata.ShardCount,
		HashAlgorithmVersion:       metadata.HashAlgorithmVersion,
		AssignmentAlgorithmVersion: metadata.AssignmentAlgorithmVersion,
		MapVersion:                 metadata.MapVersion, UpdatedAtMs: metadata.UpdatedAt.UTC().UnixMilli()}
}

func routeRecord(entry routing.RouteEntry, committedMapVersion uint64) (*tcaplusv1.ShardRoute, error) {
	leaseID, err := optionalUUIDBytes(entry.LeaseID)
	if err != nil {
		return nil, err
	}
	transitionID, err := optionalUUIDBytes(entry.TransitionID)
	if err != nil {
		return nil, err
	}
	return &tcaplusv1.ShardRoute{LogicalShardId: entry.ShardID, OwnerZoneId: entry.OwnerZoneID,
		OwnerEndpoint: entry.OwnerEndpoint, OwnerEpoch: entry.OwnerEpoch,
		RouteVersion: entry.RouteVersion, CommittedMapVersion: committedMapVersion,
		State: string(entry.State), LeaseTerm: entry.LeaseTerm, LeaseId: leaseID,
		LeaseExpiresAtMs:    entry.LeaseExpiresAt.UTC().UnixMilli(),
		PreviousOwnerZoneId: entry.PreviousOwnerZoneID, TransitionId: transitionID,
		UpdatedAtMs: entry.UpdatedAt.UTC().UnixMilli()}, nil
}

func routeEntry(record *tcaplusv1.ShardRoute) (routing.RouteEntry, error) {
	entry := routing.RouteEntry{ShardID: record.LogicalShardId, OwnerZoneID: record.OwnerZoneId,
		OwnerEndpoint: record.OwnerEndpoint, OwnerEpoch: record.OwnerEpoch,
		RouteVersion: record.RouteVersion, State: routing.RouteState(record.State),
		LeaseTerm: record.LeaseTerm, LeaseID: routing.FormatUUIDBytes(record.LeaseId),
		LeaseExpiresAt:      time.UnixMilli(record.LeaseExpiresAtMs).UTC(),
		PreviousOwnerZoneID: record.PreviousOwnerZoneId,
		TransitionID:        routing.FormatUUIDBytes(record.TransitionId),
		UpdatedAt:           time.UnixMilli(record.UpdatedAtMs).UTC()}
	if err := validateStoredEntry(entry); err != nil {
		return routing.RouteEntry{}, fmt.Errorf("%w: %v", ErrRouteStoreCorrupt, err)
	}
	return entry, nil
}

func setPending(meta *tcaplusv1.ShardMapMeta, entry routing.RouteEntry, mapVersion uint64) {
	meta.HasPendingCommit = true
	meta.PendingShardId = entry.ShardID
	meta.PendingMapVersion = mapVersion
	meta.PendingRouteVersion = entry.RouteVersion
	meta.PendingTransitionId, _ = optionalUUIDBytes(entry.TransitionID)
	meta.PendingState = string(entry.State)
	meta.PendingOwnerZoneId = entry.OwnerZoneID
	meta.PendingOwnerEndpoint = entry.OwnerEndpoint
	meta.PendingOwnerEpoch = entry.OwnerEpoch
	meta.PendingLeaseTerm = entry.LeaseTerm
	meta.PendingLeaseId, _ = optionalUUIDBytes(entry.LeaseID)
	meta.PendingLeaseExpiresAtMs = entry.LeaseExpiresAt.UTC().UnixMilli()
	meta.PendingPreviousOwnerZoneId = entry.PreviousOwnerZoneID
	meta.PendingUpdatedAtMs = entry.UpdatedAt.UTC().UnixMilli()
}

func pendingRoute(meta *tcaplusv1.ShardMapMeta) (routing.RouteEntry, error) {
	if !meta.HasPendingCommit || meta.PendingMapVersion != meta.MapVersion+1 ||
		meta.PendingRouteVersion == 0 || meta.PendingOwnerZoneId == "" ||
		meta.PendingOwnerEndpoint == "" || meta.PendingOwnerEpoch == 0 ||
		meta.PendingLeaseTerm == 0 || len(meta.PendingLeaseId) != 16 ||
		meta.PendingLeaseExpiresAtMs == 0 || meta.PendingUpdatedAtMs == 0 {
		return routing.RouteEntry{}, fmt.Errorf("%w: pending route intent is incomplete", ErrRouteStoreCorrupt)
	}
	entry := routing.RouteEntry{ShardID: meta.PendingShardId,
		OwnerZoneID: meta.PendingOwnerZoneId, OwnerEndpoint: meta.PendingOwnerEndpoint,
		OwnerEpoch: meta.PendingOwnerEpoch, RouteVersion: meta.PendingRouteVersion,
		State: routing.RouteState(meta.PendingState), LeaseTerm: meta.PendingLeaseTerm,
		LeaseID:             routing.FormatUUIDBytes(meta.PendingLeaseId),
		LeaseExpiresAt:      time.UnixMilli(meta.PendingLeaseExpiresAtMs).UTC(),
		PreviousOwnerZoneID: meta.PendingPreviousOwnerZoneId,
		TransitionID:        routing.FormatUUIDBytes(meta.PendingTransitionId),
		UpdatedAt:           time.UnixMilli(meta.PendingUpdatedAtMs).UTC()}
	if err := validateStoredEntry(entry); err != nil {
		return routing.RouteEntry{}, fmt.Errorf("%w: pending route: %v", ErrRouteStoreCorrupt, err)
	}
	return entry, nil
}

func clearPending(meta *tcaplusv1.ShardMapMeta) {
	meta.HasPendingCommit = false
	meta.PendingShardId = 0
	meta.PendingMapVersion = 0
	meta.PendingRouteVersion = 0
	meta.PendingTransitionId = nil
	meta.PendingState = ""
	meta.PendingOwnerZoneId = ""
	meta.PendingOwnerEndpoint = ""
	meta.PendingOwnerEpoch = 0
	meta.PendingLeaseTerm = 0
	meta.PendingLeaseId = nil
	meta.PendingLeaseExpiresAtMs = 0
	meta.PendingPreviousOwnerZoneId = ""
	meta.PendingUpdatedAtMs = 0
}

func optionalUUIDBytes(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	return routing.ParseUUIDBytes(value)
}
