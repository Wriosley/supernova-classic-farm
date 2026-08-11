package player

import (
	"errors"
	"math"
	"math/big"
	"sort"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	plotv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws/plot"
)

// materializeDueMaturities checks plots in stable ID order. Each independently
// materialized maturity is one business-state transition and advances both
// PlayerSeq and CheckpointRevision exactly once.
func (s *State) materializeDueMaturities(now time.Time) ([]MaturityEvent, error) {
	if s == nil {
		return nil, errors.New("state is required")
	}
	plotIDs := make([]uint32, 0, len(s.Plots))
	for plotID := range s.Plots {
		plotIDs = append(plotIDs, plotID)
	}
	sort.Slice(plotIDs, func(i, j int) bool { return plotIDs[i] < plotIDs[j] })
	events := make([]MaturityEvent, 0)
	for _, plotID := range plotIDs {
		plot := s.Plots[plotID]
		if plot == nil || plot.State != plotv1.PlotState_GROWING {
			continue
		}
		if plot.EstimatedMatureAtMS != nil && now.UnixMilli() < *plot.EstimatedMatureAtMS {
			continue
		}
		didMature, err := settleGrowingPlot(plot, now.UnixMilli())
		if err != nil {
			return events, err
		}
		if didMature {
			s.PlayerSeq++
			s.CheckpointRevision++
			s.UpdatedAtMS = maxInt64(now.UnixMilli(), s.UpdatedAtMS)
			events = append(events, MaturityEvent{
				PlayerID: s.PlayerID, OwnerEpoch: s.OwnerEpoch, PlayerSeq: s.PlayerSeq,
				ServerTimeMS: now.UnixMilli(), Plot: plot.View(), Stealable: CanSteal(plot),
			})
		}
	}
	return events, nil
}

// settleGrowingPlot applies exact millisecond fixed-point growth. RateScaled6
// multiplied by elapsed milliseconds already has GrowthScaled9 units.
func settleGrowingPlot(plot *Plot, serverNowMS int64) (bool, error) {
	if plot == nil || plot.State != plotv1.PlotState_GROWING {
		return false, errors.New("plot is not growing")
	}
	if plot.MaturityValueScaled9 <= 0 || plot.BaseGrowthRateScaled6 <= 0 ||
		plot.SettledGrowthValueScaled9 < 0 ||
		plot.SettledGrowthValueScaled9 >= plot.MaturityValueScaled9 ||
		plot.LastSettledAtMS < plot.PlantedAtMS {
		return false, errors.New("growing plot fields are invalid")
	}
	effectiveNowMS := maxInt64(serverNowMS, plot.LastSettledAtMS)
	remaining := plot.MaturityValueScaled9 - plot.SettledGrowthValueScaled9
	delta, err := growthBetween(plot, plot.LastSettledAtMS, effectiveNowMS)
	if err != nil {
		return false, err
	}
	if delta.Cmp(big.NewInt(remaining)) >= 0 {
		plot.SettledGrowthValueScaled9 = plot.MaturityValueScaled9
		plot.LastSettledAtMS = effectiveNowMS
		plot.State = plotv1.PlotState_MATURE
		plot.EstimatedMatureAtMS = nil
		plot.FertilizerEffect = nil
		plot.PestEffect = nil
		return true, nil
	}
	plot.SettledGrowthValueScaled9 += delta.Int64()
	plot.LastSettledAtMS = effectiveNowMS
	if plot.FertilizerEffect != nil && plot.FertilizerEffect.EndAtMs <= effectiveNowMS {
		plot.FertilizerEffect = nil
	}
	if plot.PestEffect != nil && plot.PestEffect.EndAtMs <= effectiveNowMS {
		plot.PestEffect = nil
	}
	estimate, err := estimatePlotMatureAtMS(plot, effectiveNowMS)
	if err != nil {
		return false, err
	}
	plot.EstimatedMatureAtMS = &estimate
	return false, nil
}

func growthBetween(plot *Plot, startMS, endMS int64) (*big.Int, error) {
	if endMS < startMS {
		return nil, errors.New("growth interval is invalid")
	}
	boundaries := []int64{startMS, endMS}
	for _, effect := range []*datav1.TimedEffectRecord{plot.FertilizerEffect, plot.PestEffect} {
		if effect == nil {
			continue
		}
		if effect.StartAtMs > startMS && effect.StartAtMs < endMS {
			boundaries = append(boundaries, effect.StartAtMs)
		}
		if effect.EndAtMs > startMS && effect.EndAtMs < endMS {
			boundaries = append(boundaries, effect.EndAtMs)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
	total := new(big.Int)
	for index := 0; index+1 < len(boundaries); index++ {
		left, right := boundaries[index], boundaries[index+1]
		if right <= left {
			continue
		}
		rate, err := effectiveRateScaled6(plot, left)
		if err != nil {
			return nil, err
		}
		part := new(big.Int).Mul(big.NewInt(right-left), big.NewInt(rate))
		total.Add(total, part)
	}
	return total, nil
}

func effectiveRateScaled6(plot *Plot, atMS int64) (int64, error) {
	rate := big.NewInt(plot.BaseGrowthRateScaled6)
	for _, effect := range []*datav1.TimedEffectRecord{plot.FertilizerEffect, plot.PestEffect} {
		if effect != nil && effect.StartAtMs <= atMS && atMS < effect.EndAtMs {
			if effect.Modifier == nil {
				return 0, errors.New("effect modifier is required")
			}
			rate.Add(rate, big.NewInt(effect.Modifier.ScaledValue))
		}
	}
	if !rate.IsInt64() || rate.Sign() <= 0 {
		return 0, errors.New("effective growth rate is invalid")
	}
	return rate.Int64(), nil
}

func estimatePlotMatureAtMS(plot *Plot, fromMS int64) (int64, error) {
	remaining := plot.MaturityValueScaled9 - plot.SettledGrowthValueScaled9
	if remaining <= 0 {
		return 0, errors.New("plot has no remaining growth")
	}
	boundaries := make([]int64, 0, 4)
	for _, effect := range []*datav1.TimedEffectRecord{plot.FertilizerEffect, plot.PestEffect} {
		if effect == nil {
			continue
		}
		if effect.StartAtMs > fromMS {
			boundaries = append(boundaries, effect.StartAtMs)
		}
		if effect.EndAtMs > fromMS {
			boundaries = append(boundaries, effect.EndAtMs)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
	cursor := fromMS
	remainingBig := big.NewInt(remaining)
	for _, boundary := range boundaries {
		if boundary <= cursor {
			continue
		}
		rate, err := effectiveRateScaled6(plot, cursor)
		if err != nil {
			return 0, err
		}
		capacity := new(big.Int).Mul(big.NewInt(boundary-cursor), big.NewInt(rate))
		if capacity.Cmp(remainingBig) >= 0 {
			return addGrowthDuration(cursor, remainingBig, rate)
		}
		remainingBig.Sub(remainingBig, capacity)
		cursor = boundary
	}
	rate, err := effectiveRateScaled6(plot, cursor)
	if err != nil {
		return 0, err
	}
	return addGrowthDuration(cursor, remainingBig, rate)
}

func addGrowthDuration(fromMS int64, remaining *big.Int, rateScaled6 int64) (int64, error) {
	if remaining.Sign() <= 0 || rateScaled6 <= 0 {
		return 0, errors.New("growth duration inputs are invalid")
	}
	divisor := big.NewInt(rateScaled6)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(remaining, divisor, remainder)
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, errors.New("growth duration overflows")
	}
	duration := quotient.Int64()
	if duration <= 0 || fromMS > math.MaxInt64-duration {
		return 0, errors.New("growth maturity timestamp overflows")
	}
	return fromMS + duration, nil
}

func estimateMatureAtMS(fromMS, remainingGrowthScaled9, rateScaled6 int64) (int64, error) {
	if remainingGrowthScaled9 <= 0 || rateScaled6 <= 0 {
		return 0, errors.New("growth estimate inputs are invalid")
	}
	durationMS := remainingGrowthScaled9 / rateScaled6
	if remainingGrowthScaled9%rateScaled6 != 0 {
		durationMS++
	}
	if durationMS <= 0 || fromMS > math.MaxInt64-durationMS {
		return 0, errors.New("growth maturity timestamp overflows")
	}
	return fromMS + durationMS, nil
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
