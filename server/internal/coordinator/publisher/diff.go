package publisher

import (
	"encoding/hex"
	"fmt"
	"strings"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func SnapshotProto(snapshot routing.Snapshot) (*datav1.ShardMapSnapshot, error) {
	if len(snapshot.Entries) != int(routing.ShardCount) {
		return nil, fmt.Errorf("snapshot has %d routes", len(snapshot.Entries))
	}
	result := &datav1.ShardMapSnapshot{ShardCount: snapshot.ShardCount, HashAlgorithmVersion: snapshot.HashAlgorithmVersion,
		AssignmentAlgorithmVersion: snapshot.AssignmentAlgorithmVersion, MapVersion: snapshot.MapVersion,
		CommittedTerm: snapshot.CommittedTerm, CommittedIndex: snapshot.CommittedIndex,
		Entries: make([]*datav1.ShardRouteEntry, len(snapshot.Entries))}
	for i, entry := range snapshot.Entries {
		encoded, err := RouteProto(entry)
		if err != nil {
			return nil, err
		}
		result.Entries[i] = encoded
	}
	return result, nil
}

func RouteProto(entry routing.RouteEntry) (*datav1.ShardRouteEntry, error) {
	state := datav1.ShardRouteState_SHARD_ROUTE_STATE_UNSPECIFIED
	switch entry.State {
	case routing.RouteStateActive:
		state = datav1.ShardRouteState_ACTIVE
	case routing.RouteStatePreparing:
		state = datav1.ShardRouteState_PREPARING
	case routing.RouteStateUnassigned:
		state = datav1.ShardRouteState_UNASSIGNED
	default:
		return nil, fmt.Errorf("unknown route state %q", entry.State)
	}
	lease, err := uuidBytes(entry.LeaseID)
	if err != nil {
		return nil, fmt.Errorf("route %d lease: %w", entry.ShardID, err)
	}
	transition, err := uuidBytes(entry.TransitionID)
	if err != nil {
		return nil, fmt.Errorf("route %d transition: %w", entry.ShardID, err)
	}
	var owner, previous *string
	if entry.OwnerZoneID != "" {
		value := entry.OwnerZoneID
		owner = &value
	}
	if entry.PreviousOwnerZoneID != "" {
		value := entry.PreviousOwnerZoneID
		previous = &value
	}
	return &datav1.ShardRouteEntry{ShardId: entry.ShardID, OwnerZoneId: owner, OwnerEndpoint: entry.OwnerEndpoint,
		OwnerEpoch: entry.OwnerEpoch, RouteVersion: entry.RouteVersion, State: state, LeaseTerm: entry.LeaseTerm,
		LeaseId: lease, LeaseExpiresAtMs: entry.LeaseExpiresAt.UTC().UnixMilli(), PreviousOwnerZoneId: previous,
		TransitionId: transition, UpdatedAtMs: entry.UpdatedAt.UTC().UnixMilli()}, nil
}

func uuidBytes(value string) ([]byte, error) {
	value = strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	if value == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(value)
	if err != nil || len(b) != 16 {
		return nil, fmt.Errorf("invalid UUID")
	}
	return b, nil
}
