package player

import (
	"errors"
	"time"

	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

// ActorTickResult is the mailbox-owned outcome of one maturity Tick. Runtime
// consumes it for Dirty marking, notifications, and deadline rescheduling
// without reading plot state itself.
type ActorTickResult struct {
	DirtyRevision  uint64
	MaturityEvents []MaturityEvent
	DomainChanges  DomainChanges
	NextTickAt     *time.Time
}

// nextTickAt returns the earliest EstimatedMatureAtMS among GROWING plots.
// ok is false when the Actor has no future maturity deadline.
func (a *runtimeActor) nextTickAt() (time.Time, bool) {
	if a == nil || a.state == nil {
		return time.Time{}, false
	}
	var earliest int64
	found := false
	for _, plot := range a.state.Plots {
		if plot == nil || plot.State != plotv1.PlotState_GROWING || plot.EstimatedMatureAtMS == nil {
			continue
		}
		estimate := *plot.EstimatedMatureAtMS
		if !found || estimate < earliest {
			earliest = estimate
			found = true
		}
	}
	if !found {
		return time.Time{}, false
	}
	return time.UnixMilli(earliest).UTC(), true
}

// tick settles due maturities inside the Actor and reports the next deadline.
// Callers must already own the Actor mailbox.
func (a *runtimeActor) tick(now time.Time) (ActorTickResult, error) {
	if a == nil || a.state == nil {
		return ActorTickResult{}, errors.New("actor state is required")
	}
	events, err := a.state.materializeDueMaturities(now)
	if err != nil {
		return ActorTickResult{}, err
	}
	result := ActorTickResult{
		DirtyRevision:  a.state.CheckpointRevision,
		MaturityEvents: events,
		DomainChanges:  DomainChangesFromPlotIDs(maturedPlotIDs(events)),
	}
	if deadline, ok := a.nextTickAt(); ok {
		next := deadline
		result.NextTickAt = &next
	}
	return result, nil
}
