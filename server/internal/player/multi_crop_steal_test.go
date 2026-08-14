package player

import (
	"testing"
	"time"
)

func TestStealLimitsFromBaseYieldTable(t *testing.T) {
	cases := []struct {
		base, protected, max, qty uint32
	}{
		{3, 2, 1, 1},
		{4, 2, 2, 1},
		{5, 3, 2, 1},
		{6, 3, 3, 1},
	}
	for _, tc := range cases {
		if got := protectedOwnerYieldFromBaseYield(tc.base); got != tc.protected {
			t.Fatalf("base=%d protected=%d want %d", tc.base, got, tc.protected)
		}
		if got := maxStealTimesFromBaseYield(tc.base); got != tc.max {
			t.Fatalf("base=%d max=%d want %d", tc.base, got, tc.max)
		}
		if got := stealQuantityFromBaseYield(tc.base); got != tc.qty {
			t.Fatalf("base=%d qty=%d want %d", tc.base, got, tc.qty)
		}
	}
}

func TestDevelopmentConfigAllCropsStealable(t *testing.T) {
	config := NewDevelopmentConfigSnapshot()
	crops := config.ActiveCropCatalog()
	if len(crops) < 11 {
		t.Fatalf("expected >=11 crops, got %d", len(crops))
	}
	for _, cropView := range crops {
		crop, ok := config.CropByID(cropView.CropId)
		if !ok {
			t.Fatalf("missing crop id %d", cropView.CropId)
		}
		if crop.StealQuantity != 1 || crop.MaxStealTimes == 0 || crop.ProtectedOwnerYield == 0 {
			t.Fatalf("crop %s (%d) steal config incomplete: %+v", crop.Name, crop.CropItemID, crop)
		}
		wantProtected := protectedOwnerYieldFromBaseYield(crop.BaseYield)
		wantMax := maxStealTimesFromBaseYield(crop.BaseYield)
		if crop.ProtectedOwnerYield != wantProtected || crop.MaxStealTimes != wantMax {
			t.Fatalf("crop %s base=%d protected=%d max=%d want %d/%d",
				crop.Name, crop.BaseYield, crop.ProtectedOwnerYield, crop.MaxStealTimes, wantProtected, wantMax)
		}
	}
}

func TestApplyStealRejectsCropMismatchAndSameVisitorTwice(t *testing.T) {
	const ownerID = uint64(41)
	const plotID = uint32(1)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	state := ownerStateWithMaturePlot(ownerID, plotID, now)
	store := &recordingCheckpointStore{state: state}
	runtime := NewRuntime()
	runtime.store = store
	runtime.SetNow(func() time.Time { return now })
	defer runtime.Close()

	_, _, _, _, err := applySteal(t, runtime, ownerID, 7, interactionIDFixture(0x51), plotID, 9999)
	if err != ErrStealNotAvailable {
		t.Fatalf("crop mismatch err=%v", err)
	}

	if _, _, _, _, err := applySteal(t, runtime, ownerID, 7, interactionIDFixture(0x52), plotID, 4001); err != nil {
		t.Fatalf("first steal: %v", err)
	}
	if _, _, _, _, err := applySteal(t, runtime, ownerID, 7, interactionIDFixture(0x53), plotID, 4001); err != ErrStealNotAvailable {
		t.Fatalf("same visitor second steal err=%v", err)
	}
	if _, _, _, _, err := applySteal(t, runtime, ownerID, 8, interactionIDFixture(0x54), plotID, 4001); err != nil {
		t.Fatalf("other visitor steal: %v", err)
	}
}
