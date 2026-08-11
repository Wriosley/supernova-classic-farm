package mail

import (
	"context"
	"errors"
	"fmt"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

const tcaplusAccountStatusActive uint32 = 3

type tcaplusClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
	Traverse(proto.Message) ([]proto.Message, error)
}

// TcaplusStore implements Store against real or fake Tcaplus clients.
type TcaplusStore struct {
	client tcaplusClient
	zoneID uint32
}

func NewTcaplusStore(client tcaplusClient, zoneID uint32) (*TcaplusStore, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus mail client and zone are required")
	}
	return &TcaplusStore{client: client, zoneID: zoneID}, nil
}

func (s *TcaplusStore) RegisteredAtMS(ctx context.Context, playerID uint64) (int64, bool, error) {
	record := &tcaplusv1.AccountByPlayer{PlayerId: playerID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("get AccountByPlayer: %w", err)
	}
	if record.Status != tcaplusAccountStatusActive {
		return 0, false, nil
	}
	return record.CreatedAtMs, true, nil
}

func (s *TcaplusStore) InsertPublicMail(ctx context.Context, record *tcaplusv1.PublicMail) error {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("insert PublicMail: %w", err)
	}
	return nil
}

func (s *TcaplusStore) GetPublicMail(ctx context.Context, mailID string) (*tcaplusv1.PublicMail, error) {
	record := &tcaplusv1.PublicMail{MailId: mailID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get PublicMail: %w", err)
	}
	return record, nil
}

func (s *TcaplusStore) ListPublicMails(ctx context.Context) ([]*tcaplusv1.PublicMail, error) {
	_ = ctx
	rows, err := s.client.Traverse(&tcaplusv1.PublicMail{})
	if err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("traverse PublicMail: %w", err)
	}
	out := make([]*tcaplusv1.PublicMail, 0, len(rows))
	for _, row := range rows {
		record, ok := row.(*tcaplusv1.PublicMail)
		if !ok || record == nil {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *TcaplusStore) InsertPrivateMail(ctx context.Context, record *tcaplusv1.PrivateMail) error {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("insert PrivateMail: %w", err)
	}
	return nil
}

func (s *TcaplusStore) ListPrivateMails(ctx context.Context, recipientPlayerID uint64) ([]*tcaplusv1.PrivateMail, error) {
	_ = ctx
	rows, err := s.client.Traverse(&tcaplusv1.PrivateMail{})
	if err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("traverse PrivateMail: %w", err)
	}
	out := make([]*tcaplusv1.PrivateMail, 0)
	for _, row := range rows {
		record, ok := row.(*tcaplusv1.PrivateMail)
		if !ok || record == nil || record.RecipientPlayerId != recipientPlayerID {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *TcaplusStore) GetPrivateMail(
	ctx context.Context, recipientPlayerID uint64, mailID string,
) (*tcaplusv1.PrivateMail, error) {
	record := &tcaplusv1.PrivateMail{RecipientPlayerId: recipientPlayerID, MailId: mailID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get PrivateMail: %w", err)
	}
	return record, nil
}

func (s *TcaplusStore) GetCursor(ctx context.Context, playerID uint64) (*tcaplusv1.PlayerMailboxCursor, int32, error) {
	record := &tcaplusv1.PlayerMailboxCursor{PlayerId: playerID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get PlayerMailboxCursor: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) InsertCursor(ctx context.Context, record *tcaplusv1.PlayerMailboxCursor) (int32, error) {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert PlayerMailboxCursor: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) UpdateCursor(
	ctx context.Context, record *tcaplusv1.PlayerMailboxCursor, expectedVersion int32,
) (int32, error) {
	opt := updateOpt(ctx, expectedVersion)
	if err := s.client.DoUpdate(record, opt, s.zoneID); err != nil {
		return 0, fmt.Errorf("update PlayerMailboxCursor: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) GetMailState(
	ctx context.Context, playerID uint64, mailID string,
) (*tcaplusv1.PlayerMailState, int32, error) {
	record := &tcaplusv1.PlayerMailState{PlayerId: playerID, MailId: mailID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get PlayerMailState: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) InsertMailState(ctx context.Context, record *tcaplusv1.PlayerMailState) (int32, error) {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert PlayerMailState: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) UpdateMailState(
	ctx context.Context, record *tcaplusv1.PlayerMailState, expectedVersion int32,
) (int32, error) {
	opt := updateOpt(ctx, expectedVersion)
	if err := s.client.DoUpdate(record, opt, s.zoneID); err != nil {
		return 0, fmt.Errorf("update PlayerMailState: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) TryInsertSourceDedup(ctx context.Context, record *tcaplusv1.MailSourceDedup) error {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return ErrAlreadyExists
		}
		return fmt.Errorf("insert MailSourceDedup: %w", err)
	}
	return nil
}

func (s *TcaplusStore) GetSourceDedup(ctx context.Context, sourceEventID string) (*tcaplusv1.MailSourceDedup, error) {
	record := &tcaplusv1.MailSourceDedup{SourceEventId: sourceEventID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get MailSourceDedup: %w", err)
	}
	return record, nil
}

func (s *TcaplusStore) GetClaimSaga(ctx context.Context, claimID []byte) (*tcaplusv1.MailClaimSaga, int32, error) {
	record := &tcaplusv1.MailClaimSaga{ClaimId: append([]byte(nil), claimID...)}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("get MailClaimSaga: %w", err)
	}
	return record, opt.Version, nil
}

func (s *TcaplusStore) InsertClaimSaga(ctx context.Context, record *tcaplusv1.MailClaimSaga) (int32, error) {
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsAlreadyExists(err) {
			return 0, ErrAlreadyExists
		}
		return 0, fmt.Errorf("insert MailClaimSaga: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) UpdateClaimSaga(
	ctx context.Context, record *tcaplusv1.MailClaimSaga, expectedVersion int32,
) (int32, error) {
	opt := updateOpt(ctx, expectedVersion)
	if err := s.client.DoUpdate(record, opt, s.zoneID); err != nil {
		return 0, fmt.Errorf("update MailClaimSaga: %w", err)
	}
	return opt.Version, nil
}

func (s *TcaplusStore) ListClaimSagas(ctx context.Context) ([]*tcaplusv1.MailClaimSaga, error) {
	_ = ctx
	rows, err := s.client.Traverse(&tcaplusv1.MailClaimSaga{})
	if err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("traverse MailClaimSaga: %w", err)
	}
	out := make([]*tcaplusv1.MailClaimSaga, 0, len(rows))
	for _, row := range rows {
		record, ok := row.(*tcaplusv1.MailClaimSaga)
		if !ok || record == nil {
			continue
		}
		out = append(out, record)
	}
	return out, nil
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
