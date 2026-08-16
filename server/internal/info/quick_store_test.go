package info

import (
	"testing"
	"time"

	infov1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/info"
)

func TestQuickStorePresenceLeaseExpiresAndRejectsOldSequence(t *testing.T) {
	now := time.UnixMilli(1000)
	s := NewQuickStore(func() time.Time { return now })
	u := &infov1.PresenceLeaseUpdate{PlayerId: 1, Online: true, OnlineUntilMs: 2000, LastSeenAtMs: 1000, LogicalZoneId: "z", IncarnationId: "i", OwnerEpoch: 1, SourceSeq: 2}
	if !s.UpdatePresence(u) {
		t.Fatal("initial update rejected")
	}
	u.SourceSeq = 1
	if s.UpdatePresence(u) {
		t.Fatal("old sequence accepted")
	}
	if got := s.BatchGet([]uint64{1})[0]; !got.GetOnline() || !got.GetPresenceKnown() {
		t.Fatalf("view=%+v", got)
	}
	now = time.UnixMilli(2001)
	if got := s.BatchGet([]uint64{1})[0]; got.GetOnline() {
		t.Fatalf("expired view=%+v", got)
	}
}

func TestQuickStoreMailEventIsIdempotentAndCursorRejectsDelayedEvent(t *testing.T) {
	s := NewQuickStore(nil)
	if !s.SetMailbox(7, 2, 100, 1000) {
		t.Fatal("set baseline")
	}
	known, count, applied := s.ApplyMailEvent(7, "m1", 200)
	if !known || !applied || count != 3 {
		t.Fatalf("first=%v %d %v", known, count, applied)
	}
	known, count, applied = s.ApplyMailEvent(7, "m1", 200)
	if !known || applied || count != 3 {
		t.Fatalf("duplicate=%v %d %v", known, count, applied)
	}
	if !s.SetMailbox(7, 0, 300, 1100) {
		t.Fatal("clear")
	}
	known, count, applied = s.ApplyMailEvent(7, "delayed", 250)
	if !known || applied || count != 0 {
		t.Fatalf("delayed=%v %d %v", known, count, applied)
	}
}

func TestQuickStoreUnknownMailboxRequiresRepair(t *testing.T) {
	s := NewQuickStore(nil)
	known, _, applied := s.ApplyMailEvent(9, "m", 100)
	if known || applied {
		t.Fatal("unknown cache guessed a count")
	}
}

func TestOfflineFarmRedDotIsSuppressedByVisitUntilNewRevision(t *testing.T) {
	now := time.UnixMilli(10_000)
	s := NewQuickStore(func() time.Time { return now })
	if !s.UpdateFarm(&infov1.FarmQuickInfoUpdate{PlayerId: 20, OwnerEpoch: 1, CheckpointRevision: 4, HasMatureCropCandidate: true}) {
		t.Fatal("farm update rejected")
	}
	if got := s.BatchGetForViewer([]uint64{20}, 10)[0]; !got.GetShowOfflineFarmRedDot() {
		t.Fatalf("initial view=%+v", got)
	}
	recorded, revision := s.RecordOfflineFarmVisit(10, 20)
	if !recorded || revision != 4 {
		t.Fatalf("recorded=%v revision=%d", recorded, revision)
	}
	if got := s.BatchGetForViewer([]uint64{20}, 10)[0]; got.GetShowOfflineFarmRedDot() {
		t.Fatalf("visited view=%+v", got)
	}
	if !s.UpdateFarm(&infov1.FarmQuickInfoUpdate{PlayerId: 20, OwnerEpoch: 1, CheckpointRevision: 5, HasMatureCropCandidate: true}) {
		t.Fatal("new farm revision rejected")
	}
	if got := s.BatchGetForViewer([]uint64{20}, 10)[0]; !got.GetShowOfflineFarmRedDot() {
		t.Fatalf("new revision view=%+v", got)
	}
}

func TestOfflineVisitorsDeduplicateAndVersionedAckPreservesNewArrival(t *testing.T) {
	s := NewQuickStore(nil)
	s.RecordOfflineFarmVisit(10, 20)
	s.RecordOfflineFarmVisit(10, 20)
	ids, version, _ := s.OfflineVisitors(20)
	if len(ids) != 1 || version != 1 {
		t.Fatalf("ids=%v version=%d", ids, version)
	}
	s.RecordOfflineFarmVisit(11, 20)
	if !s.AckOfflineVisitors(20, version) {
		t.Fatal("ack rejected")
	}
	ids, version, _ = s.OfflineVisitors(20)
	if len(ids) != 1 || ids[0] != 11 || version != 2 {
		t.Fatalf("remaining ids=%v version=%d", ids, version)
	}
}

func TestOnlineOwnerDoesNotAccumulateOfflineVisitors(t *testing.T) {
	now := time.UnixMilli(1000)
	s := NewQuickStore(func() time.Time { return now })
	s.UpdatePresence(&infov1.PresenceLeaseUpdate{PlayerId: 20, Online: true, OnlineUntilMs: 2000, LastSeenAtMs: 1000, LogicalZoneId: "z", IncarnationId: "i", OwnerEpoch: 1, SourceSeq: 1})
	recorded, _ := s.RecordOfflineFarmVisit(10, 20)
	ids, _, _ := s.OfflineVisitors(20)
	if recorded || len(ids) != 0 {
		t.Fatalf("recorded=%v ids=%v", recorded, ids)
	}
}
