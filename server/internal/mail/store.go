package mail

import (
	"context"
	"errors"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
)

var (
	ErrNotFound      = errors.New("mail record not found")
	ErrAlreadyExists = errors.New("mail record already exists")
	ErrConflict      = errors.New("mail record version conflict")
)

const (
	DefaultPageSize  = 20
	MaxPageSize      = 50
	MaxAttachments   = 8
	MaxAttachmentQty = 999
	MaxTitleRunes    = 64
	MaxContentRunes  = 2000
)

// Store is the durable dependency of MailSvr. Callers own CAS retries.
type Store interface {
	RegisteredAtMS(ctx context.Context, playerID uint64) (registeredAtMS int64, found bool, err error)

	InsertPublicMail(ctx context.Context, record *tcaplusv1.PublicMail) error
	GetPublicMail(ctx context.Context, mailID string) (*tcaplusv1.PublicMail, error)
	ListPublicMails(ctx context.Context) ([]*tcaplusv1.PublicMail, error)

	InsertPrivateMail(ctx context.Context, record *tcaplusv1.PrivateMail) error
	ListPrivateMails(ctx context.Context, recipientPlayerID uint64) ([]*tcaplusv1.PrivateMail, error)
	GetPrivateMail(ctx context.Context, recipientPlayerID uint64, mailID string) (*tcaplusv1.PrivateMail, error)

	GetCursor(ctx context.Context, playerID uint64) (*tcaplusv1.PlayerMailboxCursor, int32, error)
	InsertCursor(ctx context.Context, record *tcaplusv1.PlayerMailboxCursor) (int32, error)
	UpdateCursor(ctx context.Context, record *tcaplusv1.PlayerMailboxCursor, expectedVersion int32) (int32, error)

	GetMailState(ctx context.Context, playerID uint64, mailID string) (*tcaplusv1.PlayerMailState, int32, error)
	ListMailStates(ctx context.Context, playerID uint64) ([]*tcaplusv1.PlayerMailState, error)
	InsertMailState(ctx context.Context, record *tcaplusv1.PlayerMailState) (int32, error)
	UpdateMailState(ctx context.Context, record *tcaplusv1.PlayerMailState, expectedVersion int32) (int32, error)

	// TryInsertSourceDedup returns ErrAlreadyExists when source_event_id was used.
	TryInsertSourceDedup(ctx context.Context, record *tcaplusv1.MailSourceDedup) error
	GetSourceDedup(ctx context.Context, sourceEventID string) (*tcaplusv1.MailSourceDedup, error)

	GetClaimSaga(ctx context.Context, claimID []byte) (*tcaplusv1.MailClaimSaga, int32, error)
	InsertClaimSaga(ctx context.Context, record *tcaplusv1.MailClaimSaga) (int32, error)
	UpdateClaimSaga(ctx context.Context, record *tcaplusv1.MailClaimSaga, expectedVersion int32) (int32, error)
	ListClaimSagas(ctx context.Context) ([]*tcaplusv1.MailClaimSaga, error)
}
