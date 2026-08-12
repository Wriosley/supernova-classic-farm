package friend

import (
	"context"
	"errors"
	"time"

	friendv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/friend"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	wsv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/ws"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// shareCodeTTL is the fixed validity window for a freshly minted share code.
const shareCodeTTL = 10 * time.Minute

// Service implements classicfarm.friend.v1.FriendService.
type Service struct {
	friendv1.UnimplementedFriendServiceServer

	store             Store
	linker            *FriendLinker
	now               func() time.Time
	publicWebBaseURL  string
}

func NewService(store Store, linker *FriendLinker, now func() time.Time) (*Service, error) {
	baseURL, err := LoadPublicWebBaseURL()
	if err != nil {
		return nil, err
	}
	return NewServiceWithBaseURL(store, linker, now, baseURL)
}

func NewServiceWithBaseURL(
	store Store, linker *FriendLinker, now func() time.Time, publicWebBaseURL string,
) (*Service, error) {
	if store == nil || linker == nil {
		return nil, errors.New("friend store and linker are required")
	}
	if now == nil {
		now = time.Now
	}
	normalized, err := normalizePublicWebBaseURL(publicWebBaseURL)
	if err != nil {
		return nil, err
	}
	return &Service{
		store: store, linker: linker, now: now, publicWebBaseURL: normalized,
	}, nil
}

func (s *Service) CreateShareCode(
	ctx context.Context, request *friendv1.CreateShareCodeRequest,
) (*friendv1.CreateShareCodeResponse, error) {
	if request == nil || request.CallerPlayerId == 0 {
		return &friendv1.CreateShareCodeResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT},
		}, nil
	}
	now := s.now().UTC()
	current, err := s.createOrReuseCode(ctx, request.CallerPlayerId, now)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	shareURL, err := FriendShareURL(s.publicWebBaseURL, current.Code)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &friendv1.CreateShareCodeResponse{
		Code: current.Code, CreatedAtMs: current.CreatedAtMs, ExpiresAtMs: current.ExpiresAtMs,
		ShareUrl: shareURL,
	}, nil
}

// createOrReuseCode returns the caller's still-valid code, or mints and
// persists a new one when there is none or the previous one has expired.
func (s *Service) createOrReuseCode(
	ctx context.Context, playerID uint64, now time.Time,
) (*tcaplusv1.FriendCodeCurrent, error) {
	for attempt := 0; attempt < tcaplusMaxCASAttempts; attempt++ {
		current, version, err := s.store.GetCodeCurrent(ctx, playerID)
		notFound := errors.Is(err, ErrNotFound)
		if err != nil && !notFound {
			return nil, err
		}
		if !notFound && current.Status == tcaplusv1.FriendCodeStatus_FRIEND_CODE_STATUS_ACTIVE &&
			now.UnixMilli() < current.ExpiresAtMs {
			return current, nil
		}
		if !notFound && current.Code != "" {
			if lookup, lookupVersion, lookupErr := s.store.GetCodeLookup(ctx, current.Code); lookupErr == nil {
				lookup.Status = tcaplusv1.FriendCodeStatus_FRIEND_CODE_STATUS_EXPIRED
				lookup.UpdatedAtMs = now.UnixMilli()
				if _, updateErr := s.store.UpdateCodeLookup(ctx, lookup, lookupVersion); updateErr != nil {
					return nil, updateErr
				}
			} else if !errors.Is(lookupErr, ErrNotFound) {
				return nil, lookupErr
			}
		}
		code, err := newShareCode()
		if err != nil {
			return nil, err
		}
		lookup := &tcaplusv1.FriendCodeLookup{
			Code: code, OwnerPlayerId: playerID,
			Status:      tcaplusv1.FriendCodeStatus_FRIEND_CODE_STATUS_ACTIVE,
			ExpiresAtMs: now.Add(shareCodeTTL).UnixMilli(), UpdatedAtMs: now.UnixMilli(),
		}
		if _, err := s.store.InsertCodeLookup(ctx, lookup); err != nil {
			if errors.Is(err, ErrAlreadyExists) {
				continue
			}
			return nil, err
		}
		next := &tcaplusv1.FriendCodeCurrent{
			OwnerPlayerId: playerID, Code: code,
			Status:      tcaplusv1.FriendCodeStatus_FRIEND_CODE_STATUS_ACTIVE,
			CreatedAtMs: now.UnixMilli(), ExpiresAtMs: now.Add(shareCodeTTL).UnixMilli(),
			UpdatedAtMs: now.UnixMilli(),
		}
		if notFound {
			if _, err := s.store.InsertCodeCurrent(ctx, next); err != nil {
				if errors.Is(err, ErrAlreadyExists) {
					continue
				}
				return nil, err
			}
			return next, nil
		}
		if _, err := s.store.UpdateCodeCurrent(ctx, next, version); err != nil {
			continue
		}
		return next, nil
	}
	return nil, errors.New("create share code conflicted too many times")
}

func (s *Service) RedeemShareCode(
	ctx context.Context, request *friendv1.RedeemShareCodeRequest,
) (*friendv1.RedeemShareCodeResponse, error) {
	if request == nil || request.CallerPlayerId == 0 {
		return &friendv1.RedeemShareCodeResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT},
		}, nil
	}
	code := normalizeCode(request.Code)
	if code == "" {
		return &friendv1.RedeemShareCodeResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT},
		}, nil
	}
	now := s.now().UTC()
	lookup, _, err := s.store.GetCodeLookup(ctx, code)
	if errors.Is(err, ErrNotFound) {
		return &friendv1.RedeemShareCodeResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_FRIEND_CODE_NOT_FOUND},
		}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if lookup.OwnerPlayerId == request.CallerPlayerId {
		return &friendv1.RedeemShareCodeResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_CANNOT_FRIEND_SELF},
		}, nil
	}
	if lookup.Status != tcaplusv1.FriendCodeStatus_FRIEND_CODE_STATUS_ACTIVE ||
		now.UnixMilli() >= lookup.ExpiresAtMs {
		return &friendv1.RedeemShareCodeResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_FRIEND_CODE_EXPIRED},
		}, nil
	}

	relationID, newlyCreated, err := s.linker.EstablishFriendship(
		ctx, lookup.OwnerPlayerId, request.CallerPlayerId, code, now,
	)
	switch {
	case errors.Is(err, ErrCannotFriendSelf):
		return &friendv1.RedeemShareCodeResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_CANNOT_FRIEND_SELF},
		}, nil
	case errors.Is(err, ErrFriendLimitReached):
		return &friendv1.RedeemShareCodeResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_FRIEND_LIMIT_REACHED},
		}, nil
	case err != nil:
		return nil, status.Error(codes.Internal, err.Error())
	}

	name, found, err := s.store.AccountName(ctx, lookup.OwnerPlayerId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !found || name == "" {
		return nil, status.Error(codes.Internal, "code owner account is unavailable")
	}
	return &friendv1.RedeemShareCodeResponse{
		Friend: &friendv1.FriendView{
			PlayerId: lookup.OwnerPlayerId, AccountName: name, CreatedAtMs: now.UnixMilli(),
		},
		RelationId: relationID, NewlyCreated: newlyCreated,
	}, nil
}

func (s *Service) ListFriends(
	ctx context.Context, request *friendv1.ListFriendsRequest,
) (*friendv1.ListFriendsResponse, error) {
	if request == nil || request.CallerPlayerId == 0 {
		return &friendv1.ListFriendsResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT},
		}, nil
	}
	list, _, err := s.store.GetFriendList(ctx, request.CallerPlayerId)
	if errors.Is(err, ErrNotFound) {
		return &friendv1.ListFriendsResponse{}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	views := make([]*friendv1.FriendView, 0, len(list.Entries))
	for _, entry := range list.Entries {
		views = append(views, &friendv1.FriendView{
			PlayerId: entry.FriendPlayerId, AccountName: entry.AccountName,
			CreatedAtMs: entry.CreatedAtMs,
		})
	}
	return &friendv1.ListFriendsResponse{Friends: views}, nil
}

func (s *Service) CheckMutualFriend(
	ctx context.Context, request *friendv1.CheckMutualFriendRequest,
) (*friendv1.CheckMutualFriendResponse, error) {
	if request == nil || request.PlayerAId == 0 || request.PlayerBId == 0 ||
		request.PlayerAId == request.PlayerBId {
		return &friendv1.CheckMutualFriendResponse{
			Error: &wsv1.Error{Code: wsv1.ErrorCode_INVALID_ARGUMENT},
		}, nil
	}
	low, high := sortedPlayerIDs(request.PlayerAId, request.PlayerBId)
	relation, _, err := s.store.GetRelation(ctx, low, high)
	if errors.Is(err, ErrNotFound) {
		return &friendv1.CheckMutualFriendResponse{MutualFriend: false}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if relation.Status != tcaplusv1.FriendRelationStatus_FRIEND_RELATION_STATUS_ACTIVE {
		return &friendv1.CheckMutualFriendResponse{MutualFriend: false}, nil
	}
	return &friendv1.CheckMutualFriendResponse{
		MutualFriend: true, RelationId: relation.RelationId,
	}, nil
}
