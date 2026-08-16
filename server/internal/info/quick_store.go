package info

import (
	"sort"
	"sync"
	"time"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
)

const maxRememberedMailEvents = 1024
const maxOfflineVisitors = 50

type presenceProjection struct {
	update *infov1.PresenceLeaseUpdate
}

type farmProjection struct {
	update *infov1.FarmQuickInfoUpdate
}

type mailboxProjection struct {
	known          bool
	count          uint32
	cursorMS       int64
	calculatedAtMS int64
	events         map[string]int64
}

type offlineVisitorsProjection struct {
	version   uint64
	visitors  map[uint64]uint64 // visitor id -> first version in this pending batch
	truncated bool
}

type QuickStore struct {
	mu                sync.RWMutex
	presence          map[uint64]presenceProjection
	farm              map[uint64]farmProjection
	mailbox           map[uint64]*mailboxProjection
	farmSeen          map[uint64]map[uint64]uint64 // owner -> viewer -> checkpoint revision
	offlineVisitors   map[uint64]*offlineVisitorsProjection
	publicWatermarkMS int64
	now               func() time.Time
}

func NewQuickStore(now func() time.Time) *QuickStore {
	if now == nil {
		now = time.Now
	}
	return &QuickStore{presence: make(map[uint64]presenceProjection), farm: make(map[uint64]farmProjection), mailbox: make(map[uint64]*mailboxProjection), farmSeen: make(map[uint64]map[uint64]uint64), offlineVisitors: make(map[uint64]*offlineVisitorsProjection), now: now}
}

func (s *QuickStore) UpdatePresence(update *infov1.PresenceLeaseUpdate) bool {
	if update == nil || update.GetPlayerId() == 0 || update.GetOwnerEpoch() == 0 || update.GetSourceSeq() == 0 || update.GetLogicalZoneId() == "" || update.GetIncarnationId() == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.presence[update.GetPlayerId()]
	if exists {
		old := current.update
		if update.GetOwnerEpoch() < old.GetOwnerEpoch() {
			return false
		}
		if update.GetOwnerEpoch() == old.GetOwnerEpoch() && update.GetIncarnationId() == old.GetIncarnationId() && update.GetSourceSeq() <= old.GetSourceSeq() {
			return false
		}
		if update.GetOwnerEpoch() == old.GetOwnerEpoch() && update.GetIncarnationId() != old.GetIncarnationId() && update.GetLastSeenAtMs() < old.GetLastSeenAtMs() {
			return false
		}
	}
	s.presence[update.GetPlayerId()] = presenceProjection{update: clonePresence(update)}
	return true
}

func (s *QuickStore) UpdateFarm(update *infov1.FarmQuickInfoUpdate) bool {
	if update == nil || update.GetPlayerId() == 0 || update.GetOwnerEpoch() == 0 || update.GetCheckpointRevision() == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.farm[update.GetPlayerId()]; ok {
		old := current.update
		if update.GetOwnerEpoch() < old.GetOwnerEpoch() || (update.GetOwnerEpoch() == old.GetOwnerEpoch() && update.GetCheckpointRevision() <= old.GetCheckpointRevision()) {
			return false
		}
	}
	s.farm[update.GetPlayerId()] = farmProjection{update: cloneFarm(update)}
	return true
}

func (s *QuickStore) BatchGet(playerIDs []uint64) []*infov1.PlayerQuickInfo {
	ids := normalizeIDs(playerIDs)
	nowMS := s.now().UTC().UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*infov1.PlayerQuickInfo, 0, len(ids))
	for _, id := range ids {
		view := &infov1.PlayerQuickInfo{PlayerId: id}
		if p, ok := s.presence[id]; ok {
			view.PresenceKnown = true
			view.OnlineUntilMs = p.update.GetOnlineUntilMs()
			view.LastSeenAtMs = p.update.GetLastSeenAtMs()
			view.Online = p.update.GetOnline() && p.update.GetOnlineUntilMs() > nowMS
		}
		if f, ok := s.farm[id]; ok {
			view.FarmSummaryKnown = true
			view.HasGrowingCrop = f.update.GetHasGrowingCrop()
			view.EarliestMatureAtMs = f.update.GetEarliestMatureAtMs()
			view.HasMatureCropCandidate = f.update.GetHasMatureCropCandidate()
			view.OwnerEpoch = f.update.GetOwnerEpoch()
			view.CheckpointRevision = f.update.GetCheckpointRevision()
		}
		out = append(out, view)
	}
	return out
}

// BatchGetForViewer adds the per-viewer offline red-dot decision to the normal
// quick projection. Online owners are deliberately excluded: their maturity
// notifications are delivered actively by their Zone.
func (s *QuickStore) BatchGetForViewer(playerIDs []uint64, viewer uint64) []*infov1.PlayerQuickInfo {
	views := s.BatchGet(playerIDs)
	if viewer == 0 {
		return views
	}
	nowMS := s.now().UTC().UnixMilli()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, view := range views {
		if view.GetOnline() || !view.GetFarmSummaryKnown() {
			continue
		}
		candidate := view.GetHasMatureCropCandidate() || (view.GetHasGrowingCrop() && view.GetEarliestMatureAtMs() > 0 && view.GetEarliestMatureAtMs() <= nowMS)
		seen := uint64(0)
		if byViewer := s.farmSeen[view.GetPlayerId()]; byViewer != nil {
			seen = byViewer[viewer]
		}
		view.ShowOfflineFarmRedDot = candidate && seen < view.GetCheckpointRevision()
	}
	return views
}

func (s *QuickStore) RecordOfflineFarmVisit(visitor, owner uint64) (bool, uint64) {
	if visitor == 0 || owner == 0 || visitor == owner {
		return false, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	revision := uint64(0)
	if farm := s.farm[owner].update; farm != nil {
		revision = farm.GetCheckpointRevision()
	}
	seen := s.farmSeen[owner]
	if seen == nil {
		seen = make(map[uint64]uint64)
		s.farmSeen[owner] = seen
	}
	if revision > seen[visitor] {
		seen[visitor] = revision
	}
	nowMS := s.now().UTC().UnixMilli()
	if p := s.presence[owner].update; p != nil && p.GetOnline() && p.GetOnlineUntilMs() > nowMS {
		return false, revision
	}
	list := s.offlineVisitors[owner]
	if list == nil {
		list = &offlineVisitorsProjection{visitors: make(map[uint64]uint64)}
		s.offlineVisitors[owner] = list
	}
	if _, duplicate := list.visitors[visitor]; duplicate {
		return true, revision
	}
	list.version++
	if len(list.visitors) >= maxOfflineVisitors {
		list.truncated = true
		return true, revision
	}
	list.visitors[visitor] = list.version
	return true, revision
}

func (s *QuickStore) OfflineVisitors(owner uint64) ([]uint64, uint64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.offlineVisitors[owner]
	if list == nil {
		return nil, 0, false
	}
	ids := make([]uint64, 0, len(list.visitors))
	for id := range list.visitors {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, list.version, list.truncated
}

func (s *QuickStore) AckOfflineVisitors(owner, version uint64) bool {
	if owner == 0 || version == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.offlineVisitors[owner]
	if list == nil {
		return true
	}
	for id, addedVersion := range list.visitors {
		if addedVersion <= version {
			delete(list.visitors, id)
		}
	}
	if version >= list.version {
		list.truncated = false
	}
	return true
}

func (s *QuickStore) ApplyMailEvent(playerID uint64, mailID string, createdAtMS int64) (known bool, count uint32, applied bool) {
	if playerID == 0 || mailID == "" || createdAtMS <= 0 {
		return false, 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.mailbox[playerID]
	if m == nil || !m.known {
		return false, 0, false
	}
	if createdAtMS <= m.cursorMS {
		return true, m.count, false
	}
	if _, duplicate := m.events[mailID]; duplicate {
		return true, m.count, false
	}
	if len(m.events) >= maxRememberedMailEvents {
		m.known = false
		m.events = nil
		return false, 0, false
	}
	m.events[mailID] = createdAtMS
	m.count++
	return true, m.count, true
}

func (s *QuickStore) SetMailbox(playerID uint64, count uint32, cursorMS, calculatedAtMS int64) bool {
	if playerID == 0 || cursorMS < 0 || calculatedAtMS <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.mailbox[playerID]
	if current != nil && calculatedAtMS < current.calculatedAtMS {
		return false
	}
	events := make(map[string]int64)
	if current != nil {
		for id, createdAt := range current.events {
			if createdAt > cursorMS {
				events[id] = createdAt
			}
		}
	}
	s.mailbox[playerID] = &mailboxProjection{known: true, count: count, cursorMS: cursorMS, calculatedAtMS: calculatedAtMS, events: events}
	return true
}

func (s *QuickStore) Mailbox(playerID uint64) (known bool, count uint32, cursorMS int64, publicRefresh bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.mailbox[playerID]
	if m == nil || !m.known {
		return false, 0, 0, false
	}
	return true, m.count, m.cursorMS, s.publicWatermarkMS > m.calculatedAtMS
}

func (s *QuickStore) AdvancePublicWatermark(publishedAtMS int64) bool {
	if publishedAtMS <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if publishedAtMS <= s.publicWatermarkMS {
		return false
	}
	s.publicWatermarkMS = publishedAtMS
	return true
}

func normalizeIDs(ids []uint64) []uint64 {
	seen := make(map[uint64]struct{}, len(ids))
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func clonePresence(in *infov1.PresenceLeaseUpdate) *infov1.PresenceLeaseUpdate {
	out := *in
	return &out
}
func cloneFarm(in *infov1.FarmQuickInfoUpdate) *infov1.FarmQuickInfoUpdate { out := *in; return &out }
