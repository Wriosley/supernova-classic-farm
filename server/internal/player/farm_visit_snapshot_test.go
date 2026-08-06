package player

import (
	"bytes"
	"context"
	"testing"
	"time"

	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

func TestBuildPublicFarmSnapshotStripsPrivateFieldsAndSetsSeqZero(t *testing.T) {
	const ownerID = uint64(7)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	snapshot, err := runtime.BuildPublicFarmSnapshot(context.Background(), ownerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("BuildPublicFarmSnapshot: %v", err)
	}
	if snapshot.GetOwnerPlayerId() != ownerID {
		t.Fatalf("owner_player_id = %d, want %d", snapshot.GetOwnerPlayerId(), ownerID)
	}
	if snapshot.GetVersion().GetFarmViewSeq() != 0 {
		t.Fatalf("farm_view_seq = %d, want 0 in Phase 3", snapshot.GetVersion().GetFarmViewSeq())
	}
	if len(snapshot.GetVersion().GetFarmViewEpoch()) != 16 {
		t.Fatalf("farm_view_epoch length = %d, want 16", len(snapshot.GetVersion().GetFarmViewEpoch()))
	}
	if len(snapshot.GetPlots()) != int(InitialPlotCount) {
		t.Fatalf("plots = %d, want %d", len(snapshot.GetPlots()), InitialPlotCount)
	}
	for _, plot := range snapshot.GetPlots() {
		if plot.GetPlotState() != plotv1.PlotState_EMPTY {
			t.Fatalf("fresh plot state = %v, want EMPTY", plot.GetPlotState())
		}
	}
}

func TestBuildPublicFarmSnapshotEpochRotatesOnEachActorIncarnation(t *testing.T) {
	const ownerID = uint64(9)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

	first := NewRuntime()
	first.now = func() time.Time { return now }
	snapshot1, err := first.BuildPublicFarmSnapshot(context.Background(), ownerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("first BuildPublicFarmSnapshot: %v", err)
	}
	// Calling again on the same still-active Actor must not rotate the epoch.
	snapshot1Again, err := first.BuildPublicFarmSnapshot(context.Background(), ownerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("second BuildPublicFarmSnapshot on same Actor: %v", err)
	}
	if !bytes.Equal(snapshot1.GetVersion().GetFarmViewEpoch(), snapshot1Again.GetVersion().GetFarmViewEpoch()) {
		t.Fatalf("farm_view_epoch changed across calls on the same Actor incarnation")
	}
	first.Close()

	// A brand new Runtime simulates a Zone restart: the Actor map is empty,
	// so the next activation is a new incarnation and must mint a new epoch.
	second := NewRuntime()
	second.now = func() time.Time { return now }
	defer second.Close()
	snapshot2, err := second.BuildPublicFarmSnapshot(context.Background(), ownerID, LocalOwnerEpoch)
	if err != nil {
		t.Fatalf("BuildPublicFarmSnapshot after restart: %v", err)
	}
	if bytes.Equal(snapshot1.GetVersion().GetFarmViewEpoch(), snapshot2.GetVersion().GetFarmViewEpoch()) {
		t.Fatalf("farm_view_epoch did not change across a simulated Zone restart")
	}
}

func TestBuildPublicFarmSnapshotRejectsMismatchedOwnerEpoch(t *testing.T) {
	const ownerID = uint64(11)
	now := time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)
	runtime := NewRuntime()
	runtime.now = func() time.Time { return now }
	defer runtime.Close()

	if _, err := runtime.BuildPublicFarmSnapshot(context.Background(), ownerID, LocalOwnerEpoch); err != nil {
		t.Fatalf("prime Actor: %v", err)
	}
	if _, err := runtime.BuildPublicFarmSnapshot(context.Background(), ownerID, LocalOwnerEpoch+1); err != ErrNotOwner {
		t.Fatalf("BuildPublicFarmSnapshot(stale epoch) = %v, want ErrNotOwner", err)
	}
}

func TestBuildPublicFarmSnapshotRejectsZeroOwnerEpoch(t *testing.T) {
	runtime := NewRuntime()
	defer runtime.Close()
	if _, err := runtime.BuildPublicFarmSnapshot(context.Background(), 13, 0); err != ErrNotOwner {
		t.Fatalf("BuildPublicFarmSnapshot(ownerEpoch=0) = %v, want ErrNotOwner", err)
	}
}
