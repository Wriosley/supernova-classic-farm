package player

import (
	"context"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

// publishFarmViewChanges 在 owner Actor mailbox 内根据 DomainChanges 生成
// 有序 FarmViewPatch，再交给 Dispatcher 异步投递。mailbox 外不再读取 Actor State。
func (r *Runtime) publishFarmViewChanges(
	ctx context.Context, a *runtimeActor, ownerPlayerID uint64, changes DomainChanges,
) *wsv1.FarmViewPatch {
	plotIDs := changes.PlotIDs()
	if a == nil || len(plotIDs) == 0 {
		return nil
	}
	var patch *wsv1.FarmViewPatch
	err := a.mailbox.Do(ctx, func() {
		a.farmViewSeq++
		patch = buildFarmViewPatch(
			ownerPlayerID, a.farmViewEpoch, a.farmViewSeq, plotIDs, a.state.Plots,
		)
	})
	if err != nil || patch == nil {
		return nil
	}
	r.mu.Lock()
	dispatcher := r.farmView
	r.mu.Unlock()
	if dispatcher != nil {
		dispatcher.Enqueue(ownerPlayerID, patch)
	}
	return patch
}
