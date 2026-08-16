package visit

import (
	"bytes"
	"errors"
	"sync"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/connection"
)

// VisitTTL is the eviction window measured from the last accepted heartbeat
// (or Enter, which counts as the first heartbeat). H5 is expected to send a
// heartbeat every 30 seconds, giving three missed heartbeats of slack.
const VisitTTL = 90 * time.Second

var (
	ErrVisitNotFound     = errors.New("visit not found")
	ErrVisitExpired      = errors.New("visit expired")
	ErrInvalidGateTarget = errors.New("invalid gate target")
)

// VisitRecord is one owner-side lease granted to a visiting player.
type VisitRecord struct {
	OwnerPlayerID   uint64
	VisitorPlayerID uint64
	VisitID         []byte
	GateID          string
	GateEndpoint    string
	LastHeartbeatAt time.Time
	ExpiresAt       time.Time
	RequestID       string
}

func (r VisitRecord) clone() VisitRecord {
	r.VisitID = append([]byte(nil), r.VisitID...)
	return r
}

type ownerVisits struct {
	byVisitor map[uint64]*VisitRecord
	byVisitID map[string]*VisitRecord
}

// Registry is the per-owner, per-Zone in-memory VisitorRegistry: it never
// touches Tcaplus, so it resets on process restart and every visitor must
// re-ENTER (matching the design's "Owner restart replaces the full
// snapshot" acceptance criterion).
type Registry struct {
	mu     sync.Mutex
	owners map[uint64]*ownerVisits
}

func NewRegistry() *Registry {
	return &Registry{owners: make(map[uint64]*ownerVisits)}
}

// Enter grants or refreshes one visitor's lease on owner's farm.
//
//   - A retry carrying the same non-empty request_id as the current lease
//     returns the same visit_id and simply extends the TTL (idempotent).
//   - Any other Enter for a visitor who already holds a different lease on
//     this owner replaces it with a brand new visit_id.
func (r *Registry) Enter(
	owner, visitor uint64,
	gateID, requestID string,
	now time.Time, gateEndpoints ...string,
) (visitID []byte, expiresAt time.Time, newlyCreated bool, err error) {
	gateEndpoint := "http://legacy-gate:8081"
	if len(gateEndpoints) > 0 && gateEndpoints[0] != "" {
		gateEndpoint = gateEndpoints[0]
	}
	if gateID == "" || !connection.ValidGateEndpoint(gateEndpoint) {
		return nil, time.Time{}, false, ErrInvalidGateTarget
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ow := r.owners[owner]
	if ow == nil {
		ow = &ownerVisits{
			byVisitor: make(map[uint64]*VisitRecord),
			byVisitID: make(map[string]*VisitRecord),
		}
		r.owners[owner] = ow
	}
	if existing := ow.byVisitor[visitor]; existing != nil {
		if requestID != "" && existing.RequestID == requestID {
			if existing.GateID != gateID || existing.GateEndpoint != gateEndpoint {
				return nil, time.Time{}, false, ErrVisitNotFound
			}
			existing.LastHeartbeatAt = now
			existing.ExpiresAt = now.Add(VisitTTL)
			return append([]byte(nil), existing.VisitID...), existing.ExpiresAt, false, nil
		}
		delete(ow.byVisitID, string(existing.VisitID))
		delete(ow.byVisitor, visitor)
	}
	id, genErr := newRandomID()
	if genErr != nil {
		return nil, time.Time{}, false, genErr
	}
	record := &VisitRecord{
		OwnerPlayerID: owner, VisitorPlayerID: visitor, VisitID: id,
		GateID: gateID, GateEndpoint: gateEndpoint, LastHeartbeatAt: now, ExpiresAt: now.Add(VisitTTL),
		RequestID: requestID,
	}
	ow.byVisitor[visitor] = record
	ow.byVisitID[string(id)] = record
	return append([]byte(nil), id...), record.ExpiresAt, true, nil
}

// Refresh extends visitor's lease when visitID matches the current one.
func (r *Registry) Refresh(
	owner, visitor uint64,
	visitID []byte,
	gateID string,
	now time.Time, gateEndpoints ...string,
) (time.Time, error) {
	gateEndpoint := "http://legacy-gate:8081"
	if len(gateEndpoints) > 0 && gateEndpoints[0] != "" {
		gateEndpoint = gateEndpoints[0]
	}
	if gateID == "" || !connection.ValidGateEndpoint(gateEndpoint) {
		return time.Time{}, ErrInvalidGateTarget
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ow := r.lookupLocked(owner, visitor, visitID)
	if record == nil {
		return time.Time{}, ErrVisitNotFound
	}
	if !record.ExpiresAt.After(now) {
		r.removeLocked(ow, owner, visitor, record)
		return time.Time{}, ErrVisitExpired
	}
	if record.GateID != gateID || record.GateEndpoint != gateEndpoint {
		return time.Time{}, ErrVisitNotFound
	}
	record.LastHeartbeatAt = now
	record.ExpiresAt = now.Add(VisitTTL)
	return record.ExpiresAt, nil
}

// Validate reports whether visitor currently holds a live lease with exactly
// visitID, without extending the TTL. GetPublicFarmSnapshot uses this: only
// HEARTBEAT should renew the lease.
func (r *Registry) Validate(owner, visitor uint64, visitID []byte, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, _ := r.lookupLocked(owner, visitor, visitID)
	if record == nil {
		return ErrVisitNotFound
	}
	if !record.ExpiresAt.After(now) {
		return ErrVisitExpired
	}
	return nil
}

// Exit removes visitor's lease from owner's farm when visitID matches,
// returning the removed record for a presence LEFT tip.
func (r *Registry) Exit(owner, visitor uint64, visitID []byte) (VisitRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ow := r.lookupLocked(owner, visitor, visitID)
	if record == nil {
		return VisitRecord{}, ErrVisitNotFound
	}
	removed := record.clone()
	r.removeLocked(ow, owner, visitor, record)
	return removed, nil
}

// EvictExpired removes every lease whose TTL has passed as of now and
// returns the removed records so the caller can push presence LEFT tips.
func (r *Registry) EvictExpired(now time.Time) []VisitRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []VisitRecord
	for owner, ow := range r.owners {
		for visitor, record := range ow.byVisitor {
			if !record.ExpiresAt.After(now) {
				removed = append(removed, record.clone())
				delete(ow.byVisitor, visitor)
				delete(ow.byVisitID, string(record.VisitID))
			}
		}
		if len(ow.byVisitor) == 0 {
			delete(r.owners, owner)
		}
	}
	return removed
}

// ListVisitors returns every current visitor of owner (Phase 4 fan-out).
func (r *Registry) ListVisitors(owner uint64) []VisitRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	ow := r.owners[owner]
	if ow == nil {
		return nil
	}
	result := make([]VisitRecord, 0, len(ow.byVisitor))
	for _, record := range ow.byVisitor {
		result = append(result, record.clone())
	}
	return result
}

// HasVisitors reports whether owner currently has at least one unexpired visit.
func (r *Registry) HasVisitors(ownerPlayerID uint64, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ow := r.owners[ownerPlayerID]
	if ow == nil {
		return false
	}
	for _, record := range ow.byVisitor {
		if record.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

func (r *Registry) lookupLocked(owner, visitor uint64, visitID []byte) (*VisitRecord, *ownerVisits) {
	ow := r.owners[owner]
	if ow == nil {
		return nil, nil
	}
	record := ow.byVisitor[visitor]
	if record == nil || !bytes.Equal(record.VisitID, visitID) {
		return nil, ow
	}
	return record, ow
}

func (r *Registry) removeLocked(ow *ownerVisits, owner, visitor uint64, record *VisitRecord) {
	delete(ow.byVisitor, visitor)
	delete(ow.byVisitID, string(record.VisitID))
	if len(ow.byVisitor) == 0 {
		delete(r.owners, owner)
	}
}
