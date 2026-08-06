package player

import (
	"testing"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

// baseStealablePlot returns a MATURE plot that satisfies every CanSteal
// rule, so each test below only needs to break exactly one rule.
func baseStealablePlot() *Plot {
	return &Plot{
		State:         plotv1.PlotState_MATURE,
		StealQuantity: 1, MaxStealTimes: 2, StealCount: 0,
		StolenQuantity: 0, ProtectedOwnerYield: 1, BaseYield: 5,
	}
}

func TestFriendPlotEligibilityBoundaries(t *testing.T) {
	growing := &Plot{State: plotv1.PlotState_GROWING}
	if !CanApplyPest(growing) || CanApplyPest(nil) {
		t.Fatal("unexpected CanApplyPest boundary result")
	}
	growing.PestEffect = &datav1.TimedEffectRecord{SourcePlayerId: uint64Ptr(7)}
	if CanApplyPest(growing) {
		t.Fatal("active pest must prevent applying another pest")
	}
	if CanCatchPest(growing, 7) {
		t.Fatal("pest source must not catch its own pest")
	}
	if !CanCatchPest(growing, 8) {
		t.Fatal("another friend should be able to catch the pest")
	}
	if !CanHelpClean(&Plot{State: plotv1.PlotState_NEED_CLEANUP}) ||
		CanHelpClean(&Plot{State: plotv1.PlotState_EMPTY}) {
		t.Fatal("CanHelpClean must accept only NEED_CLEANUP")
	}
}

func TestCanStealExactBoundaryRules(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Plot)
		want   bool
	}{
		{"fully satisfied", func(*Plot) {}, true},
		{"not mature (growing)", func(p *Plot) { p.State = plotv1.PlotState_GROWING }, false},
		{"not mature (empty)", func(p *Plot) { p.State = plotv1.PlotState_EMPTY }, false},
		{"zero steal_quantity", func(p *Plot) { p.StealQuantity = 0 }, false},
		{"zero max_steal_times", func(p *Plot) { p.MaxStealTimes = 0 }, false},
		{"steal_count below max is stealable", func(p *Plot) { p.StealCount = 1 }, true},
		{"steal_count equals max is exhausted", func(p *Plot) { p.StealCount = 2 }, false},
		{"steal_count above max is exhausted", func(p *Plot) { p.StealCount = 3 }, false},
		{
			"base_yield exactly covers stolen+steal+protected",
			func(p *Plot) { p.BaseYield = 2 }, // required = 0 (stolen) + 1 (steal) + 1 (protected)
			true,
		},
		{
			"base_yield one below required is insufficient",
			func(p *Plot) { p.BaseYield = 1 },
			false,
		},
		{
			"large stolen_quantity leaves insufficient protected yield",
			func(p *Plot) { p.StolenQuantity = 4; p.BaseYield = 5 },
			false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plot := baseStealablePlot()
			test.mutate(plot)
			if got := CanSteal(plot); got != test.want {
				t.Fatalf("CanSteal(%+v) = %v, want %v", plot, got, test.want)
			}
		})
	}
}

func TestCanStealRejectsNilPlot(t *testing.T) {
	if CanSteal(nil) {
		t.Fatal("CanSteal(nil) = true, want false")
	}
}

// TestCanStealHandlesLargeCountersWithoutOverflow exercises near-uint32-max
// StolenQuantity/StealQuantity/ProtectedOwnerYield to confirm the uint64
// intermediate sum in CanSteal never wraps around silently the way a
// uint32 sum could.
func TestCanStealHandlesLargeCountersWithoutOverflow(t *testing.T) {
	const nearMaxUint32 = ^uint32(0) - 1
	plot := &Plot{
		State: plotv1.PlotState_MATURE, StealQuantity: nearMaxUint32,
		MaxStealTimes: 1, StealCount: 0, StolenQuantity: nearMaxUint32,
		ProtectedOwnerYield: nearMaxUint32, BaseYield: nearMaxUint32,
	}
	if CanSteal(plot) {
		t.Fatal("CanSteal with base_yield far below the required sum = true, want false")
	}
}
