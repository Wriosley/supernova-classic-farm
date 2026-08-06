package interaction

import (
	"context"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
)

type recordingActionOwner struct {
	request *rpcv1.ApplyVisitorActionRequest
}

func (o *recordingActionOwner) ApplyVisitorAction(
	_ context.Context, request *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	o.request = request
	return &rpcv1.ApplyVisitorActionResponse{
		ResultDigestSha256: []byte{1},
		ResultPayload:      []byte{2},
	}, nil
}

func TestActionSagaApplyPestCompletesAndPersistsActionFields(t *testing.T) {
	const visitorID, ownerID = uint64(7), uint64(11)
	visitorRuntime, visitorStore := newVisitorRuntime(visitorID)
	defer visitorRuntime.Close()
	store := NewMemoryStore()
	owner := &recordingActionOwner{}
	saga, err := NewActionSaga(store, visitorRuntime, owner)
	if err != nil {
		t.Fatalf("NewActionSaga: %v", err)
	}
	interactionID := fixedVisitID(0x71)
	response, err := saga.Execute(context.Background(), ActionRequest{
		InteractionID: interactionID, VisitorPlayerID: visitorID,
		VisitorOwnerEpoch: 1, OwnerPlayerID: ownerID, VisitID: fixedVisitID(0x72),
		Action: datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND,
		PlotID: 1, PestID: 1,
	}, time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if response.GetInteractionId() == nil || owner.request.GetPestId() != 1 ||
		owner.request.GetAction() != datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND {
		t.Fatalf("owner request or response missing action fields: request=%+v response=%+v", owner.request, response)
	}
	record, _, err := store.Get(context.Background(), interactionID)
	if err != nil {
		t.Fatalf("Get interaction: %v", err)
	}
	if record.GetPestId() != 1 ||
		record.GetAction() != uint32(datav1.FriendInteractionAction_APPLY_PEST_TO_FRIEND) {
		t.Fatalf("durable interaction fields = %+v", record)
	}
	visitorStore.mu.Lock()
	defer visitorStore.mu.Unlock()
	if visitorStore.state.FriendActions.GetApplyPestChances() != 99 {
		t.Fatalf("remaining apply-pest chances = %d", visitorStore.state.FriendActions.GetApplyPestChances())
	}
	if len(visitorStore.state.FriendReceipts) != 1 ||
		visitorStore.state.FriendReceipts[0].Status != datav1.FriendReceiptStatus_FRIEND_RECEIPT_COMMITTED {
		t.Fatalf("visitor receipt = %+v", visitorStore.state.FriendReceipts)
	}
}

var _ OwnerFarmClient = (*recordingActionOwner)(nil)
