package interaction

import (
	"context"
	"errors"
	"fmt"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

// tcaplusClient is the narrow subset of the Tcaplus SDK this package needs,
// matching friend.tcaplusClient and internal/auth's pattern so the same
// concrete client (real or testtcaplus.Client) satisfies every package.
type tcaplusClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
}

// TcaplusStore implements Store against the FriendInteraction table using
// Tcaplus's record version for optimistic CAS, exactly like
// friend.TcaplusStore does for FriendLinkSaga.
type TcaplusStore struct {
	client tcaplusClient
	zoneID uint32
}

func NewTcaplusStore(client tcaplusClient, zoneID uint32) (*TcaplusStore, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus interaction client and zone are required")
	}
	return &TcaplusStore{client: client, zoneID: zoneID}, nil
}

func (s *TcaplusStore) Get(
	ctx context.Context, interactionID []byte,
) (*tcaplusv1.FriendInteraction, int32, error) {
	record := &tcaplusv1.FriendInteraction{InteractionId: interactionID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get FriendInteraction: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) Insert(
	ctx context.Context, record *tcaplusv1.FriendInteraction,
) (int32, error) {
	opt := &option.PBOpt{Ctx: ctx, ResultFlag: option.TcaplusResultFlagAllNewValue}
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert FriendInteraction: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) Update(
	ctx context.Context, record *tcaplusv1.FriendInteraction, expectedVersion int32,
) (int32, error) {
	opt := &option.PBOpt{
		Ctx: ctx, Version: expectedVersion,
		VersionPolicy: option.CheckDataVersionAutoIncrease,
		ResultFlag:    option.TcaplusResultFlagAllNewValue,
	}
	if err := s.client.DoUpdate(record, opt, s.zoneID); err != nil {
		return 0, fmt.Errorf("update FriendInteraction: %w", err)
	}
	return opt.Version, nil
}
