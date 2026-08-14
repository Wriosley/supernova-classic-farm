package main

import (
	"context"
	"testing"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"google.golang.org/protobuf/proto"
)

type recordingOwnerFarmClient struct {
	calls    int
	lastReq  *rpcv1.ApplyVisitorActionRequest
	response *rpcv1.ApplyVisitorActionResponse
	err      error
}

func (c *recordingOwnerFarmClient) ApplyVisitorAction(
	_ context.Context, request *rpcv1.ApplyVisitorActionRequest,
) (*rpcv1.ApplyVisitorActionResponse, error) {
	c.calls++
	c.lastReq = request
	if c.err != nil {
		return nil, c.err
	}
	if c.response != nil {
		return c.response, nil
	}
	return &rpcv1.ApplyVisitorActionResponse{}, nil
}

func TestExecuteFriendActionStealCallsOwnerDirectly(t *testing.T) {
	payload, err := proto.Marshal(&wsv1.FriendActionResponse{InteractionId: make([]byte, 16)})
	if err != nil {
		t.Fatal(err)
	}
	owner := &recordingOwnerFarmClient{
		response: &rpcv1.ApplyVisitorActionResponse{
			ResultPayload: payload,
			FarmPatch:     &wsv1.FarmViewPatch{},
		},
	}
	server := newVisitorZoneRPCServer(mustVisitService(t), owner, localAuthorization{}, routing.DefaultZoneID)
	visitorRuntime, err := player.NewRuntimeWithStore(&stealTestCheckpointStore{state: player.NewDevelopmentState(1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(visitorRuntime.Close)
	server.withRuntime(visitorRuntime)

	resp, err := server.ExecuteFriendAction(context.Background(), &rpcv1.ExecuteFriendActionRequest{
		CallerPlayerId:     1,
		OwnerPlayerId:      2,
		VisitId:            make([]byte, 16),
		GateId:             "local-gateway",
		RequestId:          "00112233-4455-6677-8899-aabbccddeeff",
		Action:             datav1.FriendInteractionAction_STEAL_FRIEND_CROP,
		PlotId:             1,
		ExpectedCropItemId: 1002,
		FarmViewEpoch:      make([]byte, 16),
		FarmViewSeq:        7,
	})
	if err != nil || resp.GetResult() == nil || owner.calls != 1 {
		t.Fatalf("resp=%+v err=%v owner_calls=%d", resp, err, owner.calls)
	}
	if owner.lastReq.GetOwnerPlayerId() != 2 ||
		owner.lastReq.GetVisitorPlayerId() != 1 ||
		owner.lastReq.GetPlotId() != 1 ||
		owner.lastReq.GetExpectedCropItemId() != 1002 ||
		owner.lastReq.GetFarmViewSeq() != 7 {
		t.Fatalf("unexpected owner request: %+v", owner.lastReq)
	}
	if resp.GetResult().GetFarmPatch() == nil {
		t.Fatal("expected farm_patch forwarded from owner response")
	}
	if resp.GetResult().GetVisitorPatch() == nil || resp.GetResult().GetVisitorPatch().CoinBalance == nil {
		t.Fatal("expected visitor coin patch from side effect")
	}
}

func TestVisitorStealFastPathWiresOwnerClient(t *testing.T) {
	owner := &recordingOwnerFarmClient{}
	server := newVisitorZoneRPCServer(mustVisitService(t), owner, localAuthorization{}, routing.DefaultZoneID)
	if server.owner == nil {
		t.Fatal("owner client must be wired for direct steal")
	}
}
