// Package auth implements LoginSvr account, Session, CSRF, and WS ticket paths.
//
// Account/Session may be durable (Tcaplus/MySQL). CSRF remains process-local.
// WS tickets are HMAC-signed and verifiable by any Login replica that shares
// the ticket key; one-time consumption is tracked in process memory (best-effort
// across replicas within the short ticket TTL).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	SessionCookieName = "cf_session_dev"
	CSRFCookieName    = "cf_csrf_dev"
)

var (
	ErrAccountUnavailable = errors.New("account name unavailable")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrTicketConflict     = errors.New("ticket request conflict")
	ErrTicketReplay       = errors.New("ticket replay expired")
	ErrGatewayNotFound    = errors.New("gateway not found")
)

type Account struct {
	PlayerID   uint64
	Name       string
	Password   passwordHash
	Generation uint64
}

type Session struct {
	Digest            [32]byte
	PlayerID          uint64
	AccountName       string
	Generation        uint64
	CreatedAt         time.Time
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	Revoked           bool
}

type csrfRecord struct {
	ExpiresAt         time.Time
	SessionDigest     [32]byte
	SessionGeneration uint64
	Authenticated     bool
}

type ticketRecord struct {
	Raw           string
	SessionDigest [32]byte
	PlayerID      uint64
	Generation    uint64
	IssueID       string
	GatewayID     string
	ExpiresAt     time.Time
	Consumed      bool
}

type Store struct {
	mu         sync.Mutex
	now        func() time.Time
	durable    durableStore
	accounts   map[string]*Account
	sessions   map[[32]byte]*Session
	csrf       map[string]csrfRecord
	issues     map[[32]byte]map[string]*ticketRecord
	consumed   map[string]time.Time // issueID -> expires, one-time guard
	ticketKey  [32]byte
	nextPlayer uint64
}

type durableStore interface {
	register(string, string, passwordHash, time.Time) (string, *Session, error)
	login(string, string, time.Time) (string, *Session, error)
	session(string, time.Time) (*Session, error)
	logout([32]byte, time.Time) error
	sessionActive([32]byte, uint64, time.Time) (bool, error)
}

func NewStore() (*Store, error) {
	store := &Store{
		now:        time.Now,
		accounts:   make(map[string]*Account),
		sessions:   make(map[[32]byte]*Session),
		csrf:       make(map[string]csrfRecord),
		issues:     make(map[[32]byte]map[string]*ticketRecord),
		consumed:   make(map[string]time.Time),
		nextPlayer: 1,
	}
	if _, err := rand.Read(store.ticketKey[:]); err != nil {
		return nil, err
	}
	return store, nil
}

// ConfigureTicketHMACKey installs a shared ticket key so every Login replica
// can verify tickets issued by any other replica.
func (s *Store) ConfigureTicketHMACKey(raw []byte) error {
	key, err := normalizeTicketHMACKey(raw)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ticketKey = key
	s.mu.Unlock()
	return nil
}

func ValidateCredentials(name, password string) bool {
	if len(name) < 3 || len(name) > 32 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	if !utf8.ValidString(password) || len(password) == 0 {
		return false
	}
	count := utf8.RuneCountInString(password)
	return count >= 6 && count <= 128 && !containsNUL(password)
}

func containsNUL(value string) bool {
	for _, r := range value {
		if r == 0 {
			return true
		}
	}
	return false
}

func (s *Store) NewCSRF(session *Session) (string, time.Time, error) {
	token, err := randomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	record := csrfRecord{ExpiresAt: now.Add(2 * time.Hour)}
	if session != nil {
		record.Authenticated = true
		record.SessionDigest = session.Digest
		record.SessionGeneration = session.Generation
	}
	s.mu.Lock()
	s.csrf[token] = record
	s.mu.Unlock()
	return token, record.ExpiresAt, nil
}

func (s *Store) ValidateCSRF(token string, session *Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.csrf[token]
	if !ok || !s.now().Before(record.ExpiresAt) {
		delete(s.csrf, token)
		return false
	}
	if !record.Authenticated {
		return session == nil
	}
	return session != nil && !session.Revoked &&
		subtle.ConstantTimeCompare(record.SessionDigest[:], session.Digest[:]) == 1 &&
		record.SessionGeneration == session.Generation
}

func (s *Store) Register(name, password string) (string, *Session, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return "", nil, err
	}
	if s.durable != nil {
		return s.durable.register(name, password, hash, s.now())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.accounts[name]; exists {
		return "", nil, ErrAccountUnavailable
	}
	account := &Account{PlayerID: s.nextPlayer, Name: name, Password: hash, Generation: 1}
	s.nextPlayer++
	raw, session, err := s.newSessionLocked(account)
	if err != nil {
		return "", nil, err
	}
	s.accounts[name] = account
	return raw, session, nil
}

func (s *Store) Login(name, password string) (string, *Session, error) {
	if s.durable != nil {
		raw, session, err := s.durable.login(name, password, s.now())
		if err == nil {
			s.mu.Lock()
			s.invalidateTicketsForPlayerLocked(session.PlayerID)
			s.mu.Unlock()
		}
		return raw, session, err
	}
	s.mu.Lock()
	account := s.accounts[name]
	var encoded passwordHash
	if account != nil {
		encoded = account.Password
	}
	s.mu.Unlock()

	if account == nil {
		// The fixed dummy hash keeps unknown-account work in the same bounded
		// expensive class without revealing account existence.
		_ = verifyPassword(password, dummyPasswordHash())
		return "", nil, ErrInvalidCredentials
	}
	if !verifyPassword(password, encoded) {
		return "", nil, ErrInvalidCredentials
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.accounts[name]
	if current != account {
		return "", nil, ErrInvalidCredentials
	}
	current.Generation++
	for _, session := range s.sessions {
		if session.PlayerID == current.PlayerID {
			session.Revoked = true
			s.invalidateTicketsLocked(session.Digest)
		}
	}
	return s.newSessionLocked(current)
}

func (s *Store) newSessionLocked(account *Account) (string, *Session, error) {
	raw, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256([]byte(raw))
	now := s.now()
	session := &Session{
		Digest: digest, PlayerID: account.PlayerID, AccountName: account.Name,
		Generation: account.Generation, CreatedAt: now,
		IdleExpiresAt: now.Add(12 * time.Hour), AbsoluteExpiresAt: now.Add(7 * 24 * time.Hour),
	}
	s.sessions[digest] = session
	return raw, session, nil
}

func (s *Store) Session(raw string) (*Session, error) {
	if raw == "" {
		return nil, ErrUnauthenticated
	}
	if s.durable != nil {
		return s.durable.session(raw, s.now())
	}
	digest := sha256.Sum256([]byte(raw))
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[digest]
	if !s.sessionActiveLocked(session) {
		if session != nil {
			session.Revoked = true
			s.invalidateTicketsLocked(digest)
		}
		return nil, ErrUnauthenticated
	}
	now := s.now()
	nextIdle := now.Add(12 * time.Hour)
	if nextIdle.After(session.AbsoluteExpiresAt) {
		nextIdle = session.AbsoluteExpiresAt
	}
	session.IdleExpiresAt = nextIdle
	copy := *session
	return &copy, nil
}

func (s *Store) Logout(raw string) {
	if raw == "" {
		return
	}
	digest := sha256.Sum256([]byte(raw))
	if s.durable != nil {
		_ = s.durable.logout(digest, s.now())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[digest]; session != nil {
		session.Revoked = true
	}
	s.invalidateTicketsLocked(digest)
}

func (s *Store) IssueTicket(session *Session, issueID, gatewayID string) (string, time.Time, error) {
	if gatewayID != "local-gateway" {
		return "", time.Time{}, ErrGatewayNotFound
	}
	if s.durable != nil {
		active, err := s.durable.sessionActive(session.Digest, session.Generation, s.now())
		if err != nil {
			return "", time.Time{}, err
		}
		if !active {
			return "", time.Time{}, ErrUnauthenticated
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.durable == nil {
		live := s.sessions[session.Digest]
		if !s.sessionActiveLocked(live) || live.Generation != session.Generation {
			return "", time.Time{}, ErrUnauthenticated
		}
	}
	s.purgeExpiredTicketStateLocked()
	if expiresAt, consumed := s.consumed[issueID]; consumed {
		if s.now().Before(expiresAt) {
			return "", time.Time{}, ErrTicketReplay
		}
		delete(s.consumed, issueID)
	}
	byIssue := s.issues[session.Digest]
	if byIssue == nil {
		byIssue = make(map[string]*ticketRecord)
		s.issues[session.Digest] = byIssue
	}
	if prior := byIssue[issueID]; prior != nil {
		if prior.GatewayID != gatewayID {
			return "", time.Time{}, ErrTicketConflict
		}
		if prior.Consumed || !s.now().Before(prior.ExpiresAt) {
			return "", time.Time{}, ErrTicketReplay
		}
		return prior.Raw, prior.ExpiresAt, nil
	}
	for _, old := range byIssue {
		if !old.Consumed {
			old.Consumed = true
			s.consumed[old.IssueID] = old.ExpiresAt
		}
	}
	expiresAt := s.now().UTC().Add(ticketLifetimeDuration)
	raw, err := encodeTicket(s.ticketKey[:], ticketClaims{
		PlayerID: session.PlayerID, SessionDigest: session.Digest,
		Generation: session.Generation, ExpiresAt: expiresAt,
		GatewayID: gatewayID, IssueID: issueID,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	byIssue[issueID] = &ticketRecord{
		Raw: raw, SessionDigest: session.Digest, PlayerID: session.PlayerID,
		Generation: session.Generation, IssueID: issueID, GatewayID: gatewayID,
		ExpiresAt: expiresAt,
	}
	return raw, expiresAt, nil
}

func (s *Store) ConsumeTicket(raw, gatewayID string) (uint64, error) {
	s.mu.Lock()
	key := s.ticketKey
	now := s.now()
	s.mu.Unlock()
	claims, err := decodeAndVerifyTicket(key[:], raw, now)
	if err != nil {
		if errors.Is(err, errMalformedTicket) {
			return 0, ErrUnauthenticated
		}
		return 0, err
	}
	// Stateless tickets: any gate can consume a ticket issued by any gate.
	// The gatewayID parameter is retained for API compatibility but not enforced.
	if s.durable != nil {
		active, activeErr := s.durable.sessionActive(claims.SessionDigest, claims.Generation, now)
		if activeErr != nil {
			return 0, activeErr
		}
		if !active {
			return 0, ErrUnauthenticated
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeExpiredTicketStateLocked()
	if expiresAt, ok := s.consumed[claims.IssueID]; ok && s.now().Before(expiresAt) {
		return 0, ErrUnauthenticated
	}
	if s.durable == nil {
		session := s.sessions[claims.SessionDigest]
		if !s.sessionActiveLocked(session) || session.Generation != claims.Generation {
			return 0, ErrUnauthenticated
		}
	}
	s.consumed[claims.IssueID] = claims.ExpiresAt
	if byIssue := s.issues[claims.SessionDigest]; byIssue != nil {
		if record := byIssue[claims.IssueID]; record != nil {
			record.Consumed = true
		}
	}
	return claims.PlayerID, nil
}

func (s *Store) purgeExpiredTicketStateLocked() {
	now := s.now()
	for issueID, expiresAt := range s.consumed {
		if !now.Before(expiresAt) {
			delete(s.consumed, issueID)
		}
	}
}

func (s *Store) sessionActiveLocked(session *Session) bool {
	if session == nil || session.Revoked {
		return false
	}
	now := s.now()
	return now.Before(session.IdleExpiresAt) && now.Before(session.AbsoluteExpiresAt)
}

func (s *Store) invalidateTicketsLocked(sessionDigest [32]byte) {
	if byIssue := s.issues[sessionDigest]; byIssue != nil {
		for issueID, ticket := range byIssue {
			if ticket != nil && !ticket.Consumed {
				ticket.Consumed = true
				s.consumed[issueID] = ticket.ExpiresAt
			}
		}
	}
}

func (s *Store) invalidateTicketsForPlayerLocked(playerID uint64) {
	for _, byIssue := range s.issues {
		for issueID, ticket := range byIssue {
			if ticket != nil && ticket.PlayerID == playerID && !ticket.Consumed {
				ticket.Consumed = true
				s.consumed[issueID] = ticket.ExpiresAt
			}
		}
	}
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

var dummyOnce sync.Once
var dummyHash passwordHash

func dummyPasswordHash() passwordHash {
	dummyOnce.Do(func() {
		salt := []byte("classic-farm-dev")
		dummyHash = passwordHash{
			Salt: salt, Digest: argon2idKey([]byte("dummy-password-value"), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen),
			MemoryKiB: argonMemoryKiB, Iterations: argonTime, Threads: argonThreads, Version: 0x13,
		}
	})
	return dummyHash
}
