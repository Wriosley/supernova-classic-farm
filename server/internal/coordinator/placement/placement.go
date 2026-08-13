// Package placement calculates desired logical-shard ownership. Its output is
// planning input only and never grants ownership.
package placement

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

// Candidate identifies one logical Zone and its current command endpoint.
type Candidate struct {
	LogicalZoneID string
	Endpoint      string
}

// DesiredEntry is the deterministic proposed owner of one logical shard.
type DesiredEntry struct {
	ShardID       uint32
	OwnerZoneID   string
	OwnerEndpoint string
}

// Compute returns deterministic Rendezvous placement for all logical shards.
// The result is Desired state only; callers must not treat it as routing
// authority.
func Compute(
	shardCount uint32,
	assignmentVersion uint32,
	candidates []Candidate,
) ([]DesiredEntry, error) {
	if shardCount != routing.ShardCount {
		return nil, fmt.Errorf("unsupported shard count %d", shardCount)
	}
	if assignmentVersion != routing.AssignmentAlgorithmVersion {
		return nil, fmt.Errorf("unsupported assignment algorithm version %d", assignmentVersion)
	}
	zones, err := normalizeCandidates(candidates)
	if err != nil {
		return nil, err
	}

	desired := make([]DesiredEntry, shardCount)
	for shardID := uint32(0); shardID < shardCount; shardID++ {
		owner := routing.RendezvousOwner(shardID, zones)
		desired[shardID] = DesiredEntry{
			ShardID:       shardID,
			OwnerZoneID:   owner.ZoneID,
			OwnerEndpoint: owner.Endpoint,
		}
	}
	return desired, nil
}

func normalizeCandidates(candidates []Candidate) ([]routing.ZoneCandidate, error) {
	if len(candidates) == 0 {
		return nil, errors.New("at least one Zone candidate is required")
	}
	byID := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		zoneID := strings.TrimSpace(candidate.LogicalZoneID)
		endpoint := strings.TrimSpace(candidate.Endpoint)
		if zoneID == "" || endpoint == "" {
			return nil, errors.New("Zone candidate ID and endpoint are required")
		}
		if previous, exists := byID[zoneID]; exists && previous != endpoint {
			return nil, fmt.Errorf("Zone candidate %q has conflicting endpoints", zoneID)
		}
		byID[zoneID] = endpoint
	}

	zones := make([]routing.ZoneCandidate, 0, len(byID))
	for zoneID, endpoint := range byID {
		zones = append(zones, routing.ZoneCandidate{ZoneID: zoneID, Endpoint: endpoint})
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].ZoneID < zones[j].ZoneID })
	return zones, nil
}
