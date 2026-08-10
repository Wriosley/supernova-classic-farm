package player

import (
	"testing"
)

func TestDomainChangesEmpty(t *testing.T) {
	var changes DomainChanges
	if !changes.Empty() || changes.PlotIDs() != nil {
		t.Fatalf("empty DomainChanges = %+v ids=%v", changes, changes.PlotIDs())
	}
}

func TestDomainChangesSinglePlot(t *testing.T) {
	changes := DomainChanges{}.PlotChanged(3)
	ids := changes.PlotIDs()
	if len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("PlotIDs = %v, want [3]", ids)
	}
}

func TestDomainChangesDedupAndSort(t *testing.T) {
	changes := DomainChanges{}.
		PlotChanged(5).
		PlotChanged(2).
		PlotChanged(5).
		PlotChanged(1).
		Merge(DomainChanges{}.PlotChanged(2))
	ids := changes.PlotIDs()
	want := []uint32{1, 2, 5}
	if len(ids) != len(want) {
		t.Fatalf("PlotIDs = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("PlotIDs = %v, want %v", ids, want)
		}
	}
}

func TestDomainChangesIgnoresZeroPlotID(t *testing.T) {
	changes := DomainChanges{}.PlotChanged(0).PlotChanged(4)
	ids := changes.PlotIDs()
	if len(ids) != 1 || ids[0] != 4 {
		t.Fatalf("PlotIDs = %v, want [4]", ids)
	}
}
