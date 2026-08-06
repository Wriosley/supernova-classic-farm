package friend

import (
	"context"
	"testing"
	"time"

	friendv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/friend"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
)

func friendCreateShareCodeRequest(playerID uint64) *friendv1.CreateShareCodeRequest {
	return &friendv1.CreateShareCodeRequest{CallerPlayerId: playerID}
}

func friendRedeemRequest(playerID uint64, code string) *friendv1.RedeemShareCodeRequest {
	return &friendv1.RedeemShareCodeRequest{CallerPlayerId: playerID, Code: code}
}

func friendListRequest(playerID uint64) *friendv1.ListFriendsRequest {
	return &friendv1.ListFriendsRequest{CallerPlayerId: playerID}
}

func friendCheckMutualRequest(playerAID, playerBID uint64) *friendv1.CheckMutualFriendRequest {
	return &friendv1.CheckMutualFriendRequest{PlayerAId: playerAID, PlayerBId: playerBID}
}

func newTestService(t *testing.T) (*serviceHarness, *Service) {
	t.Helper()
	client, store, creditor, linker := newTestLinker(t)
	seedAccount(t, client, nil, 1, "alice")
	seedAccount(t, client, nil, 2, "bob")
	seedAccount(t, client, nil, 3, "carol")

	harness := &serviceHarness{client: client, store: store, creditor: creditor, now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	service, err := NewService(store, linker, func() time.Time { return harness.now })
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return harness, service
}

type serviceHarness struct {
	client   interface{}
	store    *TcaplusStore
	creditor *stubCreditor
	now      time.Time
}

func TestCreateShareCodeReturnsSameCodeUntilExpiry(t *testing.T) {
	ctx := context.Background()
	harness, service := newTestService(t)

	first, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil {
		t.Fatalf("CreateShareCode: %v", err)
	}
	if first.GetError() != nil {
		t.Fatalf("unexpected error: %v", first.GetError())
	}
	if first.Code == "" {
		t.Fatalf("expected a non-empty code")
	}

	harness.now = harness.now.Add(5 * time.Minute)
	second, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil {
		t.Fatalf("CreateShareCode (reuse): %v", err)
	}
	if second.Code != first.Code {
		t.Fatalf("expected same code before expiry, got %q vs %q", first.Code, second.Code)
	}

	harness.now = harness.now.Add(6 * time.Minute)
	third, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil {
		t.Fatalf("CreateShareCode (post-expiry): %v", err)
	}
	if third.Code == first.Code {
		t.Fatalf("expected a new code after expiry")
	}
}

func TestCreateShareCodeInvalidArgument(t *testing.T) {
	_, service := newTestService(t)
	response, err := service.CreateShareCode(context.Background(), friendCreateShareCodeRequest(0))
	if err != nil {
		t.Fatalf("CreateShareCode: %v", err)
	}
	if response.GetError().GetCode() != wsv1.ErrorCode_INVALID_ARGUMENT {
		t.Fatalf("expected INVALID_ARGUMENT, got %v", response.GetError())
	}
}

func TestRedeemShareCodeSuccess(t *testing.T) {
	ctx := context.Background()
	harness, service := newTestService(t)

	created, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil || created.GetError() != nil {
		t.Fatalf("CreateShareCode: %v %v", err, created.GetError())
	}

	redeemed, err := service.RedeemShareCode(ctx, friendRedeemRequest(2, created.Code))
	if err != nil {
		t.Fatalf("RedeemShareCode: %v", err)
	}
	if redeemed.GetError() != nil {
		t.Fatalf("unexpected error: %v", redeemed.GetError())
	}
	if !redeemed.NewlyCreated {
		t.Fatalf("expected NewlyCreated=true")
	}
	if redeemed.Friend == nil || redeemed.Friend.PlayerId != 1 || redeemed.Friend.AccountName != "alice" {
		t.Fatalf("unexpected friend view: %+v", redeemed.Friend)
	}
	if len(redeemed.RelationId) != 16 {
		t.Fatalf("expected 16-byte relation ID")
	}
	_ = harness
}

func TestRedeemShareCodeCannotFriendSelf(t *testing.T) {
	ctx := context.Background()
	_, service := newTestService(t)
	created, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil || created.GetError() != nil {
		t.Fatalf("CreateShareCode: %v %v", err, created.GetError())
	}
	response, err := service.RedeemShareCode(ctx, friendRedeemRequest(1, created.Code))
	if err != nil {
		t.Fatalf("RedeemShareCode: %v", err)
	}
	if response.GetError().GetCode() != wsv1.ErrorCode_CANNOT_FRIEND_SELF {
		t.Fatalf("expected CANNOT_FRIEND_SELF, got %v", response.GetError())
	}
}

func TestRedeemShareCodeNotFound(t *testing.T) {
	ctx := context.Background()
	_, service := newTestService(t)
	response, err := service.RedeemShareCode(ctx, friendRedeemRequest(2, "NOSUCHCD"))
	if err != nil {
		t.Fatalf("RedeemShareCode: %v", err)
	}
	if response.GetError().GetCode() != wsv1.ErrorCode_FRIEND_CODE_NOT_FOUND {
		t.Fatalf("expected FRIEND_CODE_NOT_FOUND, got %v", response.GetError())
	}
}

func TestRedeemShareCodeExpired(t *testing.T) {
	ctx := context.Background()
	harness, service := newTestService(t)
	created, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil || created.GetError() != nil {
		t.Fatalf("CreateShareCode: %v %v", err, created.GetError())
	}

	harness.now = harness.now.Add(11 * time.Minute)
	response, err := service.RedeemShareCode(ctx, friendRedeemRequest(2, created.Code))
	if err != nil {
		t.Fatalf("RedeemShareCode: %v", err)
	}
	if response.GetError().GetCode() != wsv1.ErrorCode_FRIEND_CODE_EXPIRED {
		t.Fatalf("expected FRIEND_CODE_EXPIRED, got %v", response.GetError())
	}

	// After expiry, a fresh code can be created and redeemed normally.
	fresh, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil || fresh.GetError() != nil {
		t.Fatalf("CreateShareCode (post-expiry): %v %v", err, fresh.GetError())
	}
	if fresh.Code == created.Code {
		t.Fatalf("expected a new code to be minted after expiry")
	}
	redeemed, err := service.RedeemShareCode(ctx, friendRedeemRequest(2, fresh.Code))
	if err != nil {
		t.Fatalf("RedeemShareCode (fresh): %v", err)
	}
	if redeemed.GetError() != nil {
		t.Fatalf("unexpected error redeeming fresh code: %v", redeemed.GetError())
	}
}

func TestRedeemShareCodeDuplicateRedeemReturnsExistingRelation(t *testing.T) {
	ctx := context.Background()
	harness, service := newTestService(t)
	created, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil || created.GetError() != nil {
		t.Fatalf("CreateShareCode: %v %v", err, created.GetError())
	}

	first, err := service.RedeemShareCode(ctx, friendRedeemRequest(2, created.Code))
	if err != nil || first.GetError() != nil {
		t.Fatalf("first RedeemShareCode: %v %v", err, first.GetError())
	}

	second, err := service.RedeemShareCode(ctx, friendRedeemRequest(2, created.Code))
	if err != nil {
		t.Fatalf("second RedeemShareCode: %v", err)
	}
	if second.GetError() != nil {
		t.Fatalf("unexpected error on duplicate redeem: %v", second.GetError())
	}
	if second.NewlyCreated {
		t.Fatalf("expected NewlyCreated=false on duplicate redeem")
	}
	if string(second.RelationId) != string(first.RelationId) {
		t.Fatalf("expected the same relation ID on duplicate redeem")
	}
	_ = harness
}

func TestListFriendsAndCheckMutualFriend(t *testing.T) {
	ctx := context.Background()
	_, service := newTestService(t)
	created, err := service.CreateShareCode(ctx, friendCreateShareCodeRequest(1))
	if err != nil || created.GetError() != nil {
		t.Fatalf("CreateShareCode: %v %v", err, created.GetError())
	}

	beforeMutual, err := service.CheckMutualFriend(ctx, friendCheckMutualRequest(1, 2))
	if err != nil {
		t.Fatalf("CheckMutualFriend (before): %v", err)
	}
	if beforeMutual.MutualFriend {
		t.Fatalf("expected not mutual friends before redemption")
	}

	redeemed, err := service.RedeemShareCode(ctx, friendRedeemRequest(2, created.Code))
	if err != nil || redeemed.GetError() != nil {
		t.Fatalf("RedeemShareCode: %v %v", err, redeemed.GetError())
	}

	list, err := service.ListFriends(ctx, friendListRequest(1))
	if err != nil {
		t.Fatalf("ListFriends: %v", err)
	}
	if len(list.Friends) != 1 || list.Friends[0].PlayerId != 2 {
		t.Fatalf("unexpected friend list: %+v", list.Friends)
	}

	afterMutual, err := service.CheckMutualFriend(ctx, friendCheckMutualRequest(2, 1))
	if err != nil {
		t.Fatalf("CheckMutualFriend (after): %v", err)
	}
	if !afterMutual.MutualFriend {
		t.Fatalf("expected mutual friends after redemption")
	}
	if string(afterMutual.RelationId) != string(redeemed.RelationId) {
		t.Fatalf("expected matching relation ID")
	}
}
