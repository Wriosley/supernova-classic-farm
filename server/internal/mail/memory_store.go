package mail

import (
	"context"
	"sort"
	"strconv"
	"sync"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"google.golang.org/protobuf/proto"
)

// MemoryStore is the development/test Store. It never persists.
type MemoryStore struct {
	mu          sync.Mutex
	accounts    map[uint64]int64
	public      map[string]*tcaplusv1.PublicMail
	private     map[string]*tcaplusv1.PrivateMail // recipient:mail_id
	cursors     map[uint64]*versionedCursor
	states      map[string]*versionedState
	sourceDedup map[string]*tcaplusv1.MailSourceDedup
	claims      map[string]*versionedClaim
}

type versionedCursor struct {
	record  *tcaplusv1.PlayerMailboxCursor
	version int32
}

type versionedState struct {
	record  *tcaplusv1.PlayerMailState
	version int32
}

type versionedClaim struct {
	record  *tcaplusv1.MailClaimSaga
	version int32
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		accounts:    make(map[uint64]int64),
		public:      make(map[string]*tcaplusv1.PublicMail),
		private:     make(map[string]*tcaplusv1.PrivateMail),
		cursors:     make(map[uint64]*versionedCursor),
		states:      make(map[string]*versionedState),
		sourceDedup: make(map[string]*tcaplusv1.MailSourceDedup),
		claims:      make(map[string]*versionedClaim),
	}
}

func (s *MemoryStore) SeedAccount(playerID uint64, registeredAtMS int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[playerID] = registeredAtMS
}

func (s *MemoryStore) RegisteredAtMS(_ context.Context, playerID uint64) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.accounts[playerID]
	return at, ok, nil
}

func (s *MemoryStore) InsertPublicMail(_ context.Context, record *tcaplusv1.PublicMail) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record == nil || record.MailId == "" {
		return ErrNotFound
	}
	if _, exists := s.public[record.MailId]; exists {
		return ErrAlreadyExists
	}
	s.public[record.MailId] = proto.Clone(record).(*tcaplusv1.PublicMail)
	return nil
}

func (s *MemoryStore) GetPublicMail(_ context.Context, mailID string) (*tcaplusv1.PublicMail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.public[mailID]
	if !ok {
		return nil, ErrNotFound
	}
	return proto.Clone(record).(*tcaplusv1.PublicMail), nil
}

func (s *MemoryStore) ListPublicMails(_ context.Context) ([]*tcaplusv1.PublicMail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*tcaplusv1.PublicMail, 0, len(s.public))
	for _, record := range s.public {
		out = append(out, proto.Clone(record).(*tcaplusv1.PublicMail))
	}
	sort.Slice(out, func(i, j int) bool {
		return mailLessDesc(out[i].CreatedAtMs, out[i].MailId, out[j].CreatedAtMs, out[j].MailId)
	})
	return out, nil
}

func (s *MemoryStore) InsertPrivateMail(_ context.Context, record *tcaplusv1.PrivateMail) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record == nil || record.MailId == "" || record.RecipientPlayerId == 0 {
		return ErrNotFound
	}
	key := privateKey(record.RecipientPlayerId, record.MailId)
	if _, exists := s.private[key]; exists {
		return ErrAlreadyExists
	}
	s.private[key] = proto.Clone(record).(*tcaplusv1.PrivateMail)
	return nil
}

func (s *MemoryStore) ListPrivateMails(_ context.Context, recipientPlayerID uint64) ([]*tcaplusv1.PrivateMail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*tcaplusv1.PrivateMail, 0)
	for _, record := range s.private {
		if record.RecipientPlayerId == recipientPlayerID {
			out = append(out, proto.Clone(record).(*tcaplusv1.PrivateMail))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return mailLessDesc(out[i].CreatedAtMs, out[i].MailId, out[j].CreatedAtMs, out[j].MailId)
	})
	return out, nil
}

func (s *MemoryStore) GetPrivateMail(_ context.Context, recipientPlayerID uint64, mailID string) (*tcaplusv1.PrivateMail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.private[privateKey(recipientPlayerID, mailID)]
	if !ok {
		return nil, ErrNotFound
	}
	return proto.Clone(record).(*tcaplusv1.PrivateMail), nil
}

func (s *MemoryStore) GetCursor(_ context.Context, playerID uint64) (*tcaplusv1.PlayerMailboxCursor, int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cursors[playerID]
	if !ok {
		return nil, 0, ErrNotFound
	}
	return proto.Clone(entry.record).(*tcaplusv1.PlayerMailboxCursor), entry.version, nil
}

func (s *MemoryStore) InsertCursor(_ context.Context, record *tcaplusv1.PlayerMailboxCursor) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record == nil || record.PlayerId == 0 {
		return 0, ErrNotFound
	}
	if _, exists := s.cursors[record.PlayerId]; exists {
		return 0, ErrAlreadyExists
	}
	s.cursors[record.PlayerId] = &versionedCursor{
		record:  proto.Clone(record).(*tcaplusv1.PlayerMailboxCursor),
		version: 1,
	}
	return 1, nil
}

func (s *MemoryStore) UpdateCursor(_ context.Context, record *tcaplusv1.PlayerMailboxCursor, expectedVersion int32) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cursors[record.PlayerId]
	if !ok {
		return 0, ErrNotFound
	}
	if entry.version != expectedVersion {
		return 0, ErrConflict
	}
	entry.record = proto.Clone(record).(*tcaplusv1.PlayerMailboxCursor)
	entry.version++
	return entry.version, nil
}

func (s *MemoryStore) GetMailState(_ context.Context, playerID uint64, mailID string) (*tcaplusv1.PlayerMailState, int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.states[stateKey(playerID, mailID)]
	if !ok {
		return nil, 0, ErrNotFound
	}
	return proto.Clone(entry.record).(*tcaplusv1.PlayerMailState), entry.version, nil
}

func (s *MemoryStore) InsertMailState(_ context.Context, record *tcaplusv1.PlayerMailState) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stateKey(record.PlayerId, record.MailId)
	if _, exists := s.states[key]; exists {
		return 0, ErrAlreadyExists
	}
	s.states[key] = &versionedState{
		record:  proto.Clone(record).(*tcaplusv1.PlayerMailState),
		version: 1,
	}
	return 1, nil
}

func (s *MemoryStore) UpdateMailState(_ context.Context, record *tcaplusv1.PlayerMailState, expectedVersion int32) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stateKey(record.PlayerId, record.MailId)
	entry, ok := s.states[key]
	if !ok {
		return 0, ErrNotFound
	}
	if entry.version != expectedVersion {
		return 0, ErrConflict
	}
	entry.record = proto.Clone(record).(*tcaplusv1.PlayerMailState)
	entry.version++
	return entry.version, nil
}

func (s *MemoryStore) TryInsertSourceDedup(_ context.Context, record *tcaplusv1.MailSourceDedup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record == nil || record.SourceEventId == "" {
		return ErrNotFound
	}
	if _, exists := s.sourceDedup[record.SourceEventId]; exists {
		return ErrAlreadyExists
	}
	s.sourceDedup[record.SourceEventId] = proto.Clone(record).(*tcaplusv1.MailSourceDedup)
	return nil
}

func (s *MemoryStore) GetSourceDedup(_ context.Context, sourceEventID string) (*tcaplusv1.MailSourceDedup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.sourceDedup[sourceEventID]
	if !ok {
		return nil, ErrNotFound
	}
	return proto.Clone(record).(*tcaplusv1.MailSourceDedup), nil
}

func (s *MemoryStore) GetClaimSaga(_ context.Context, claimID []byte) (*tcaplusv1.MailClaimSaga, int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.claims[string(claimID)]
	if !ok {
		return nil, 0, ErrNotFound
	}
	return proto.Clone(entry.record).(*tcaplusv1.MailClaimSaga), entry.version, nil
}

func (s *MemoryStore) InsertClaimSaga(_ context.Context, record *tcaplusv1.MailClaimSaga) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(record.ClaimId)
	if _, exists := s.claims[key]; exists {
		return 0, ErrAlreadyExists
	}
	s.claims[key] = &versionedClaim{
		record: proto.Clone(record).(*tcaplusv1.MailClaimSaga), version: 1,
	}
	return 1, nil
}

func (s *MemoryStore) UpdateClaimSaga(_ context.Context, record *tcaplusv1.MailClaimSaga, expectedVersion int32) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.claims[string(record.ClaimId)]
	if !ok {
		return 0, ErrNotFound
	}
	if entry.version != expectedVersion {
		return 0, ErrConflict
	}
	entry.record = proto.Clone(record).(*tcaplusv1.MailClaimSaga)
	entry.version++
	return entry.version, nil
}

func (s *MemoryStore) ListClaimSagas(_ context.Context) ([]*tcaplusv1.MailClaimSaga, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*tcaplusv1.MailClaimSaga, 0, len(s.claims))
	for _, entry := range s.claims {
		out = append(out, proto.Clone(entry.record).(*tcaplusv1.MailClaimSaga))
	}
	return out, nil
}

func privateKey(recipient uint64, mailID string) string {
	return strconv.FormatUint(recipient, 10) + ":" + mailID
}

func stateKey(playerID uint64, mailID string) string {
	return strconv.FormatUint(playerID, 10) + ":" + mailID
}

func mailLessDesc(createdI int64, idI string, createdJ int64, idJ string) bool {
	if createdI != createdJ {
		return createdI > createdJ
	}
	return idI > idJ
}
