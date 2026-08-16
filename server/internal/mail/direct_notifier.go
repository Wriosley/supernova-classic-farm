package mail

import (
	"context"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/reddot"
)

// DirectRedDotNotifier sends best-effort mail indicators directly to the
// recipient's current Owner Zone.
type DirectRedDotNotifier struct{ delivery *reddot.Delivery }

func NewDirectRedDotNotifier(delivery *reddot.Delivery) *DirectRedDotNotifier {
	return &DirectRedDotNotifier{delivery: delivery}
}

func (n *DirectRedDotNotifier) SetMailRedDot(ctx context.Context, playerID uint64, notificationID string, count uint32) error {
	if n == nil || n.delivery == nil {
		return nil
	}
	n.delivery.Deliver(ctx, []uint64{playerID}, &wsv1.RedDotChangedPush{
		NotificationId: notificationID,
		Category:       wsv1.RedDotCategory_RED_DOT_CATEGORY_MAIL,
		Operation:      wsv1.RedDotOperation_RED_DOT_OPERATION_SET,
		Count:          count,
	})
	return nil
}
