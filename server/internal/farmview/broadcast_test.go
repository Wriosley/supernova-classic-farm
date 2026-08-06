package farmview

import (
	"context"
	"errors"
	"sort"
	"testing"

	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"github.com/Wriosley/supernova-classic-farm/server/internal/visit"
)

type recordedPublish struct {
	gateID       string
	recipientIDs []uint64
	patch        *wsv1.FarmViewPatch
}

type recordingPatchPublisher struct {
	calls []recordedPublish
	err   error
}

func (p *recordingPatchPublisher) PublishFarmViewPatch(
	_ context.Context, gateID string, recipientIDs []uint64, patch *wsv1.FarmViewPatch,
) error {
	sorted := append([]uint64(nil), recipientIDs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p.calls = append(p.calls, recordedPublish{gateID: gateID, recipientIDs: sorted, patch: patch})
	return p.err
}

type staticVisitorLister struct {
	visitors []visit.VisitRecord
}

func (l *staticVisitorLister) ListVisitors(uint64) []visit.VisitRecord {
	return l.visitors
}

func testPatch() *wsv1.FarmViewPatch {
	return &wsv1.FarmViewPatch{
		OwnerPlayerId: 7,
		Version:       &wsv1.FarmViewVersion{FarmViewEpoch: []byte("0123456789abcdef"), FarmViewSeq: 1},
	}
}

func TestBroadcastAlwaysIncludesOwnerOnOwnerGate(t *testing.T) {
	publisher := &recordingPatchPublisher{}
	lister := &staticVisitorLister{}
	broadcaster, err := NewBroadcaster(publisher, lister, "owner-gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := broadcaster.Broadcast(context.Background(), 7, testPatch()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 1 ||
		publisher.calls[0].gateID != "owner-gate" ||
		len(publisher.calls[0].recipientIDs) != 1 ||
		publisher.calls[0].recipientIDs[0] != 7 {
		t.Fatalf("calls = %+v", publisher.calls)
	}
}

func TestBroadcastGroupsVisitorsByGateAndIncludesOwner(t *testing.T) {
	publisher := &recordingPatchPublisher{}
	lister := &staticVisitorLister{visitors: []visit.VisitRecord{
		{VisitorPlayerID: 101, GateID: "gate-a"},
		{VisitorPlayerID: 102, GateID: "gate-a"},
		{VisitorPlayerID: 201, GateID: "gate-b"},
	}}
	broadcaster, err := NewBroadcaster(publisher, lister, "owner-gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := broadcaster.Broadcast(context.Background(), 7, testPatch()); err != nil {
		t.Fatal(err)
	}
	byGate := make(map[string][]uint64, len(publisher.calls))
	for _, call := range publisher.calls {
		byGate[call.gateID] = call.recipientIDs
	}
	if len(byGate) != 3 {
		t.Fatalf("expected 3 distinct gate calls, got %+v", byGate)
	}
	if len(byGate["owner-gate"]) != 1 || byGate["owner-gate"][0] != 7 {
		t.Fatalf("owner-gate recipients = %+v", byGate["owner-gate"])
	}
	if len(byGate["gate-a"]) != 2 || byGate["gate-a"][0] != 101 || byGate["gate-a"][1] != 102 {
		t.Fatalf("gate-a recipients = %+v", byGate["gate-a"])
	}
	if len(byGate["gate-b"]) != 1 || byGate["gate-b"][0] != 201 {
		t.Fatalf("gate-b recipients = %+v", byGate["gate-b"])
	}
}

func TestBroadcastCoalescesVisitorOnOwnerGate(t *testing.T) {
	publisher := &recordingPatchPublisher{}
	lister := &staticVisitorLister{visitors: []visit.VisitRecord{
		{VisitorPlayerID: 101, GateID: "owner-gate"},
	}}
	broadcaster, err := NewBroadcaster(publisher, lister, "owner-gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := broadcaster.Broadcast(context.Background(), 7, testPatch()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 1 ||
		publisher.calls[0].gateID != "owner-gate" ||
		len(publisher.calls[0].recipientIDs) != 2 ||
		publisher.calls[0].recipientIDs[0] != 7 ||
		publisher.calls[0].recipientIDs[1] != 101 {
		t.Fatalf("calls = %+v", publisher.calls)
	}
}

func TestBroadcastSkipsVisitorsWithEmptyGateID(t *testing.T) {
	publisher := &recordingPatchPublisher{}
	lister := &staticVisitorLister{visitors: []visit.VisitRecord{
		{VisitorPlayerID: 101, GateID: ""},
	}}
	broadcaster, err := NewBroadcaster(publisher, lister, "owner-gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := broadcaster.Broadcast(context.Background(), 7, testPatch()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 1 || len(publisher.calls[0].recipientIDs) != 1 {
		t.Fatalf("calls = %+v", publisher.calls)
	}
}

func TestBroadcastReturnsJoinedErrorAndStillCallsEveryGate(t *testing.T) {
	publisher := &recordingPatchPublisher{err: errors.New("boom")}
	lister := &staticVisitorLister{visitors: []visit.VisitRecord{
		{VisitorPlayerID: 101, GateID: "gate-a"},
	}}
	broadcaster, err := NewBroadcaster(publisher, lister, "owner-gate")
	if err != nil {
		t.Fatal(err)
	}
	err = broadcaster.Broadcast(context.Background(), 7, testPatch())
	if err == nil {
		t.Fatal("expected error")
	}
	if len(publisher.calls) != 2 {
		t.Fatalf("calls = %+v, want both gates attempted", publisher.calls)
	}
}

func TestNewBroadcasterRejectsMissingDependencies(t *testing.T) {
	if _, err := NewBroadcaster(nil, &staticVisitorLister{}, "owner-gate"); err == nil {
		t.Fatal("expected error for nil publisher")
	}
	if _, err := NewBroadcaster(&recordingPatchPublisher{}, nil, "owner-gate"); err == nil {
		t.Fatal("expected error for nil visitor lister")
	}
	if _, err := NewBroadcaster(&recordingPatchPublisher{}, &staticVisitorLister{}, ""); err == nil {
		t.Fatal("expected error for empty gate id")
	}
}

func TestBroadcastRejectsMissingArguments(t *testing.T) {
	broadcaster, err := NewBroadcaster(&recordingPatchPublisher{}, &staticVisitorLister{}, "owner-gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := broadcaster.Broadcast(context.Background(), 0, testPatch()); err == nil {
		t.Fatal("expected error for zero owner player id")
	}
	if err := broadcaster.Broadcast(context.Background(), 7, nil); err == nil {
		t.Fatal("expected error for nil patch")
	}
}
