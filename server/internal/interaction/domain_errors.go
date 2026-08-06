package interaction

import (
	"errors"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
)

// visitorDomainErrorCode classifies a VisitorSteps.ReserveSteal error as a
// stable, deterministic domain rejection (safe to release-and-abort) versus
// an ambiguous/transport failure (safe only to retry via deferRetry). This
// is the package's only dependency on internal/player, and it is one-way:
// player never imports interaction, so there is no cycle.
func visitorDomainErrorCode(err error) (wsv1.ErrorCode, bool) {
	switch {
	case errors.Is(err, player.ErrStealInventoryCapacity):
		return wsv1.ErrorCode_INVENTORY_CAPACITY_EXCEEDED, true
	case errors.Is(err, player.ErrStealReservationConflict):
		return wsv1.ErrorCode_REQUEST_ID_CONFLICT, true
	case errors.Is(err, player.ErrInsufficientActionChance):
		return wsv1.ErrorCode_INSUFFICIENT_ACTION_CHANCE, true
	case errors.Is(err, player.ErrActionReservationConflict):
		return wsv1.ErrorCode_REQUEST_ID_CONFLICT, true
	case errors.Is(err, player.ErrPestAlreadyPresent):
		return wsv1.ErrorCode_PEST_ALREADY_PRESENT, true
	case errors.Is(err, player.ErrPestSourceForbidden):
		return wsv1.ErrorCode_PEST_SOURCE_FORBIDDEN, true
	case errors.Is(err, player.ErrPlotNotEligible):
		return wsv1.ErrorCode_PLOT_NOT_ELIGIBLE, true
	default:
		return wsv1.ErrorCode_ERROR_UNSPECIFIED, false
	}
}
