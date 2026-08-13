package membership

import (
	"errors"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidObservation = errors.New("membership observation is invalid")
	ErrStaleObservation   = errors.New("membership observation is stale")
	ErrIdentityConflict   = errors.New("logical Zone identity conflicts with an active Pod")
)

type Registry struct {
	mu      sync.RWMutex
	now     func() time.Time
	version uint64
	members map[string]Member
}

func NewRegistry(now func() time.Time) *Registry {
	if now == nil {
		now = time.Now
	}
	return &Registry{now: now, members: make(map[string]Member)}
}

func (registry *Registry) Apply(observation Observation) (Snapshot, bool, error) {
	incoming := Member(observation)
	if err := validateMember(incoming); err != nil {
		return registry.Snapshot(), false, err
	}
	if incoming.ObservedAt.IsZero() {
		incoming.ObservedAt = registry.now().UTC()
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	current, exists := registry.members[incoming.LogicalZoneID]
	if exists {
		if compareResourceVersion(incoming.ResourceVersion, current.ResourceVersion) < 0 ||
			(incoming.ResourceVersion == current.ResourceVersion && incoming.ObservedAt.Before(current.ObservedAt)) {
			return snapshotLocked(registry.version, registry.members), false, ErrStaleObservation
		}
		if incoming.IncarnationID == current.IncarnationID && incoming.PodUID != current.PodUID {
			return snapshotLocked(registry.version, registry.members), false, ErrIdentityConflict
		}
	}

	visibleChanged := !exists || externallyDifferent(current, incoming)
	if exists && current == incoming {
		return snapshotLocked(registry.version, registry.members), false, nil
	}
	next := make(map[string]Member, len(registry.members)+1)
	for logicalID, member := range registry.members {
		next[logicalID] = member
	}
	next[incoming.LogicalZoneID] = incoming
	registry.members = next
	if visibleChanged {
		registry.version++
	}
	return snapshotLocked(registry.version, registry.members), visibleChanged, nil
}

func (registry *Registry) Snapshot() Snapshot {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return snapshotLocked(registry.version, registry.members)
}

func validateMember(member Member) error {
	if strings.TrimSpace(member.LogicalZoneID) == "" || strings.TrimSpace(member.IncarnationID) == "" ||
		strings.TrimSpace(member.Endpoint) == "" || strings.TrimSpace(member.PodName) == "" ||
		strings.TrimSpace(member.PodUID) == "" || strings.TrimSpace(member.ResourceVersion) == "" ||
		member.State < StateHealthy || member.State > StateDraining || member.ConsecutiveFailures < 0 {
		return ErrInvalidObservation
	}
	return nil
}

func externallyDifferent(left, right Member) bool {
	return left.LogicalZoneID != right.LogicalZoneID || left.IncarnationID != right.IncarnationID ||
		left.Endpoint != right.Endpoint || left.State != right.State
}

func snapshotLocked(version uint64, members map[string]Member) Snapshot {
	result := Snapshot{AvailabilityVersion: version, Members: make([]Member, 0, len(members))}
	for _, member := range members {
		result.Members = append(result.Members, member)
	}
	sort.Slice(result.Members, func(i, j int) bool {
		return result.Members[i].LogicalZoneID < result.Members[j].LogicalZoneID
	})
	return result
}

func compareResourceVersion(left, right string) int {
	leftNumber, leftOK := new(big.Int).SetString(left, 10)
	rightNumber, rightOK := new(big.Int).SetString(right, 10)
	if leftOK && rightOK {
		return leftNumber.Cmp(rightNumber)
	}
	return strings.Compare(left, right)
}
