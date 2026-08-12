package mail

import (
	"bytes"
	"context"
	"testing"
	"time"

	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	rpcv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/rpc"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

type fakeZoneApplier struct {
	calls int
	fail  *wsv1.Error
}

func (f *fakeZoneApplier) ApplyMailReward(
	_ context.Context, playerID uint64, claimID []byte, mailID string,
	attachments []*tcaplusv1.MailClaimAttachment, coinAmount int64,
) (*rpcv1.ApplyMailRewardResponse, error) {
	f.calls++
	if f.fail != nil {
		return &rpcv1.ApplyMailRewardResponse{Error: f.fail}, nil
	}
	items := claimAttachmentsToViews(attachments)
	return &rpcv1.ApplyMailRewardResponse{
		NewlyApplied: f.calls == 1,
		ItemsAdded:   items,
		CoinsAdded:   coinAmount,
		Patch:        &wsv1.PlayerStatePatch{InventoryUpserts: items},
		OwnerEpoch:   fakeOwnerEpoch,
		PlayerSeq:    fakePlayerSeq,
	}, nil
}

const (
	fakeOwnerEpoch = uint64(41)
	fakePlayerSeq  = uint64(77)
)

// TestClaimMailCarriesStateVersion pins the version hand-off: without it H5
// cannot sequence patch onto its snapshot and rejects an otherwise successful
// claim.
func TestClaimMailCarriesStateVersion(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(21, 1)
	svc, err := NewService(store, nil, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 21, Title: "礼物", Content: "附件",
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1002, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	orch, err := NewClaimOrchestrator(store, &fakeZoneApplier{}, fixedNow(2000))
	if err != nil {
		t.Fatal(err)
	}
	response, err := orch.ClaimMail(context.Background(), &mailv1.ClaimMailRequest{
		PlayerId: 21, MailId: mailID, ClaimId: bytes.Repeat([]byte{9}, 16), RegisteredAtMs: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetError() != nil {
		t.Fatalf("claim error=%v", response.GetError())
	}
	version := response.GetStateVersion()
	if version.GetOwnerEpoch() != fakeOwnerEpoch || version.GetPlayerSeq() != fakePlayerSeq {
		t.Fatalf("state_version=%+v, want owner_epoch=%d player_seq=%d",
			version, fakeOwnerEpoch, fakePlayerSeq)
	}
	if response.GetPatch() == nil {
		t.Fatal("claim response must carry patch")
	}
}

// TestClaimMailOmitsStateVersionOnReplay documents the one case H5 must handle
// by reloading a snapshot: the reward was applied by an earlier attempt, so no
// version is replayable.
func TestClaimMailOmitsStateVersionOnReplay(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(22, 1)
	svc, err := NewService(store, nil, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 22, Title: "礼物", Content: "附件",
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1002, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	orch, err := NewClaimOrchestrator(store, &fakeZoneApplier{}, fixedNow(2000))
	if err != nil {
		t.Fatal(err)
	}
	request := &mailv1.ClaimMailRequest{
		PlayerId: 22, MailId: mailID, ClaimId: bytes.Repeat([]byte{10}, 16), RegisteredAtMs: 1,
	}
	if _, err := orch.ClaimMail(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	replay, err := orch.ClaimMail(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.GetError() != nil {
		t.Fatalf("replay error=%v", replay.GetError())
	}
	if replay.GetStateVersion() != nil {
		t.Fatalf("replay must not invent a version, got %+v", replay.GetStateVersion())
	}
}

func TestClaimMailHappyPathAndCrashWindows(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(7, 1)
	svc, err := NewService(store, nil, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 7, Title: "礼物", Content: "附件",
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1002, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	zone := &fakeZoneApplier{}
	orch, err := NewClaimOrchestrator(store, zone, fixedNow(2000))
	if err != nil {
		t.Fatal(err)
	}
	claimID := bytes.Repeat([]byte{1}, 16)

	// Window 1: Begin only, then Advance recovers.
	req := &mailv1.ClaimMailRequest{PlayerId: 7, MailId: mailID, ClaimId: claimID, RegisteredAtMs: 1}
	saga, _, err := orch.BeginClaim(context.Background(), req)
	if err != nil || saga.GetState() != tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_CLAIMING {
		t.Fatalf("begin=%+v err=%v", saga, err)
	}
	if _, err := orch.Advance(context.Background(), claimID); err != nil {
		t.Fatal(err)
	}
	final, _, err := store.GetClaimSaga(context.Background(), claimID)
	if err != nil || final.GetState() != tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED {
		t.Fatalf("final=%+v err=%v", final, err)
	}
	state, _, err := store.GetMailState(context.Background(), 7, mailID)
	if err != nil || !state.GetClaimed() {
		t.Fatalf("claimed state=%+v err=%v", state, err)
	}
	if zone.calls != 1 {
		t.Fatalf("zone calls=%d", zone.calls)
	}

	// Window 3 style: COMPLETED re-advance is no-op.
	if _, err := orch.Advance(context.Background(), claimID); err != nil {
		t.Fatal(err)
	}
	if zone.calls != 1 {
		t.Fatal("completed saga must not re-apply")
	}
}

func TestClaimMailInventoryCancel(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(8, 1)
	svc, err := NewService(store, nil, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 8, Title: "礼物", Content: "附件",
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1002, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	zone := &fakeZoneApplier{fail: &wsv1.Error{Code: wsv1.ErrorCode_INVENTORY_CAPACITY_EXCEEDED}}
	orch, err := NewClaimOrchestrator(store, zone, fixedNow(2000))
	if err != nil {
		t.Fatal(err)
	}
	claimID := bytes.Repeat([]byte{2}, 16)
	resp, err := orch.ClaimMail(context.Background(), &mailv1.ClaimMailRequest{
		PlayerId: 8, MailId: mailID, ClaimId: claimID, RegisteredAtMs: 1,
	})
	if err != nil || resp.GetError().GetCode() != wsv1.ErrorCode_INVENTORY_CAPACITY_EXCEEDED {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	saga, _, err := store.GetClaimSaga(context.Background(), claimID)
	if err != nil || saga.GetState() != tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_AVAILABLE {
		t.Fatalf("saga=%+v err=%v", saga, err)
	}
	_, _, err = store.GetMailState(context.Background(), 8, mailID)
	if err != ErrNotFound {
		t.Fatalf("mail must stay unclaimed, err=%v", err)
	}
}

func TestClaimMailPlayerAppliedRecovery(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(9, 1)
	svc, err := NewService(store, nil, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 9, Title: "礼物", Content: "附件",
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1002, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	zone := &fakeZoneApplier{}
	orch, err := NewClaimOrchestrator(store, zone, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	claimID := bytes.Repeat([]byte{3}, 16)
	req := &mailv1.ClaimMailRequest{PlayerId: 9, MailId: mailID, ClaimId: claimID, RegisteredAtMs: 1}
	if _, _, err := orch.BeginClaim(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// Simulate crash after Apply but before Complete: force PLAYER_APPLIED.
	saga, version, err := store.GetClaimSaga(context.Background(), claimID)
	if err != nil {
		t.Fatal(err)
	}
	saga.State = tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_PLAYER_APPLIED
	if _, err := store.UpdateClaimSaga(context.Background(), saga, version); err != nil {
		t.Fatal(err)
	}
	if _, err := orch.Advance(context.Background(), claimID); err != nil {
		t.Fatal(err)
	}
	final, _, err := store.GetClaimSaga(context.Background(), claimID)
	if err != nil || final.GetState() != tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED {
		t.Fatalf("final=%+v err=%v", final, err)
	}
	if zone.calls != 0 {
		t.Fatalf("PLAYER_APPLIED recovery must not re-apply, calls=%d", zone.calls)
	}
}

func TestClaimMailCrashAfterApplyBeforePlayerApplied(t *testing.T) {
	// Window 2: Zone applied, MailSvr still CLAIMING. Recovery re-applies
	// (Zone idempotent) then completes.
	store := NewMemoryStore()
	store.SeedAccount(10, 1)
	svc, err := NewService(store, nil, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 10, Title: "礼物", Content: "附件",
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1002, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	zone := &fakeZoneApplier{}
	orch, err := NewClaimOrchestrator(store, zone, fixedNow(2000))
	if err != nil {
		t.Fatal(err)
	}
	claimID := bytes.Repeat([]byte{4}, 16)
	req := &mailv1.ClaimMailRequest{PlayerId: 10, MailId: mailID, ClaimId: claimID, RegisteredAtMs: 1}
	if _, _, err := orch.BeginClaim(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// Pretend Zone already applied once while saga stayed CLAIMING.
	if _, err := zone.ApplyMailReward(context.Background(), 10, claimID, mailID, nil, 0); err != nil {
		t.Fatal(err)
	}
	if zone.calls != 1 {
		t.Fatalf("seed apply calls=%d", zone.calls)
	}
	if _, err := orch.Advance(context.Background(), claimID); err != nil {
		t.Fatal(err)
	}
	final, _, err := store.GetClaimSaga(context.Background(), claimID)
	if err != nil || final.GetState() != tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED {
		t.Fatalf("final=%+v err=%v", final, err)
	}
	if zone.calls != 2 {
		t.Fatalf("recovery must re-call Zone once, calls=%d", zone.calls)
	}
	state, _, err := store.GetMailState(context.Background(), 10, mailID)
	if err != nil || !state.GetClaimed() {
		t.Fatalf("claimed state=%+v err=%v", state, err)
	}
}

func TestClaimReconcilerResumesDueSaga(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(11, 1)
	svc, err := NewService(store, nil, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 11, Title: "礼物", Content: "附件",
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1002, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	zone := &fakeZoneApplier{}
	orch, err := NewClaimOrchestrator(store, zone, fixedNow(3000))
	if err != nil {
		t.Fatal(err)
	}
	claimID := bytes.Repeat([]byte{5}, 16)
	if _, _, err := orch.BeginClaim(context.Background(), &mailv1.ClaimMailRequest{
		PlayerId: 11, MailId: mailID, ClaimId: claimID, RegisteredAtMs: 1,
	}); err != nil {
		t.Fatal(err)
	}
	rec := NewClaimReconciler(orch, fixedNow(3000), nil)
	if err := rec.ReconcileDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	final, _, err := store.GetClaimSaga(context.Background(), claimID)
	if err != nil || final.GetState() != tcaplusv1.MailClaimSagaStatus_MAIL_CLAIM_SAGA_COMPLETED {
		t.Fatalf("final=%+v err=%v", final, err)
	}
}
