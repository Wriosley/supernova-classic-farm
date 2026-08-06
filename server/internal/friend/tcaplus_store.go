package friend

import (
	"context"
	"errors"
	"fmt"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

const (
	tcaplusMaxCASAttempts             = 8
	tcaplusAccountStatusActive uint32 = 3
)

// tcaplusClient is the narrow subset of the Tcaplus SDK the friend package
// needs, matching the pattern in internal/auth/tcaplus_store.go.
type tcaplusClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
}

// TcaplusStore implements Store against real or fake Tcaplus clients. It also
// opens AccountByPlayer read-only to resolve friend display names.
type TcaplusStore struct {
	client tcaplusClient
	zoneID uint32
}

func NewTcaplusStore(client tcaplusClient, zoneID uint32) (*TcaplusStore, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus friend client and zone are required")
	}
	return &TcaplusStore{client: client, zoneID: zoneID}, nil
}

func (s *TcaplusStore) AccountName(ctx context.Context, playerID uint64) (string, bool, error) {
	record := &tcaplusv1.AccountByPlayer{PlayerId: playerID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get Tcaplus account: %w", err)
	}
	if record.Status != tcaplusAccountStatusActive {
		return "", false, nil
	}
	return record.AccountName, true, nil
}

func (s *TcaplusStore) GetCodeCurrent(
	ctx context.Context, ownerPlayerID uint64,
) (*tcaplusv1.FriendCodeCurrent, int32, error) {
	record := &tcaplusv1.FriendCodeCurrent{OwnerPlayerId: ownerPlayerID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get FriendCodeCurrent: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) InsertCodeCurrent(
	ctx context.Context, record *tcaplusv1.FriendCodeCurrent,
) (int32, error) {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert FriendCodeCurrent: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) UpdateCodeCurrent(
	ctx context.Context, record *tcaplusv1.FriendCodeCurrent, expectedVersion int32,
) (int32, error) {
	opt := updateOpt(ctx, expectedVersion)
	if err := s.client.DoUpdate(record, opt, s.zoneID); err != nil {
		return 0, fmt.Errorf("update FriendCodeCurrent: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) GetCodeLookup(
	ctx context.Context, code string,
) (*tcaplusv1.FriendCodeLookup, int32, error) {
	record := &tcaplusv1.FriendCodeLookup{Code: code}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get FriendCodeLookup: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) InsertCodeLookup(
	ctx context.Context, record *tcaplusv1.FriendCodeLookup,
) (int32, error) {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert FriendCodeLookup: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) UpdateCodeLookup(
	ctx context.Context, record *tcaplusv1.FriendCodeLookup, expectedVersion int32,
) (int32, error) {
	opt := updateOpt(ctx, expectedVersion)
	if err := s.client.DoUpdate(record, opt, s.zoneID); err != nil {
		return 0, fmt.Errorf("update FriendCodeLookup: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) GetRelation(
	ctx context.Context, playerLowID, playerHighID uint64,
) (*tcaplusv1.FriendRelation, int32, error) {
	record := &tcaplusv1.FriendRelation{PlayerLowId: playerLowID, PlayerHighId: playerHighID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get FriendRelation: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) InsertRelation(
	ctx context.Context, record *tcaplusv1.FriendRelation,
) (int32, error) {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert FriendRelation: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) GetFriendList(
	ctx context.Context, playerID uint64,
) (*tcaplusv1.FriendList, int32, error) {
	record := &tcaplusv1.FriendList{PlayerId: playerID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get FriendList: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) InsertFriendList(
	ctx context.Context, record *tcaplusv1.FriendList,
) (int32, error) {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert FriendList: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) UpdateFriendList(
	ctx context.Context, record *tcaplusv1.FriendList, expectedVersion int32,
) (int32, error) {
	opt := updateOpt(ctx, expectedVersion)
	if err := s.client.DoUpdate(record, opt, s.zoneID); err != nil {
		return 0, fmt.Errorf("update FriendList: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) GetSaga(
	ctx context.Context, linkID []byte,
) (*tcaplusv1.FriendLinkSaga, int32, error) {
	record := &tcaplusv1.FriendLinkSaga{LinkId: linkID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get FriendLinkSaga: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) InsertSaga(
	ctx context.Context, record *tcaplusv1.FriendLinkSaga,
) (int32, error) {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert FriendLinkSaga: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) UpdateSaga(
	ctx context.Context, record *tcaplusv1.FriendLinkSaga, expectedVersion int32,
) (int32, error) {
	opt := updateOpt(ctx, expectedVersion)
	if err := s.client.DoUpdate(record, opt, s.zoneID); err != nil {
		return 0, fmt.Errorf("update FriendLinkSaga: %w", err)
	}
	return opt.Version, nil
}

func insertOpt(ctx context.Context) *option.PBOpt {
	return &option.PBOpt{Ctx: ctx, ResultFlag: option.TcaplusResultFlagAllNewValue}
}

func updateOpt(ctx context.Context, version int32) *option.PBOpt {
	return &option.PBOpt{
		Ctx: ctx, Version: version,
		VersionPolicy: option.CheckDataVersionAutoIncrease,
		ResultFlag:    option.TcaplusResultFlagAllNewValue,
	}
}
