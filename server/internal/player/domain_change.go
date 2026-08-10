package player

import "sort"

// DomainChanges 描述一次成功提交后需要对外公开的领域变化。
// 业务代码只报告“改了哪些地块”，不关心访客、Gate 或 Push。
type DomainChanges struct {
	plotIDs map[uint32]struct{}
}

// PlotChanged 记录一块公开地块发生变化。
func (c DomainChanges) PlotChanged(plotID uint32) DomainChanges {
	if plotID == 0 {
		return c
	}
	if c.plotIDs == nil {
		c.plotIDs = make(map[uint32]struct{}, 1)
	}
	c.plotIDs[plotID] = struct{}{}
	return c
}

// Merge 合并另一组变化。
func (c DomainChanges) Merge(other DomainChanges) DomainChanges {
	if len(other.plotIDs) == 0 {
		return c
	}
	for plotID := range other.plotIDs {
		c = c.PlotChanged(plotID)
	}
	return c
}

// Empty 表示没有公开地块变化。
func (c DomainChanges) Empty() bool {
	return len(c.plotIDs) == 0
}

// PlotIDs 返回去重后的升序地块 ID；空变化返回 nil。
func (c DomainChanges) PlotIDs() []uint32 {
	if len(c.plotIDs) == 0 {
		return nil
	}
	ids := make([]uint32, 0, len(c.plotIDs))
	for plotID := range c.plotIDs {
		ids = append(ids, plotID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// DomainChangesFromPlotIDs 从原始 plot ID 列表构造去重升序变化。
func DomainChangesFromPlotIDs(plotIDs []uint32) DomainChanges {
	var changes DomainChanges
	for _, plotID := range plotIDs {
		changes = changes.PlotChanged(plotID)
	}
	return changes
}
