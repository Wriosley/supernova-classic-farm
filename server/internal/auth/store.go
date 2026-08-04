// Package auth implements the explicitly development-only in-memory account,
// Session, CSRF, and one-time WebSocket ticket path used by LoginSvr.
//
// Nothing in this package is durable: process restart loses registrations and
// invalidates every Session and ticket. It must not be used as registration
// durability evidence.
package auth

import (
	"crypto/hmac"
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
	Digest        [32]byte
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
	mysql      *mysqlStore
	accounts   map[string]*Account
	sessions   map[[32]byte]*Session
	csrf       map[string]csrfRecord
	tickets    map[[32]byte]*ticketRecord
	issues     map[[32]byte]map[string]*ticketRecord
	ticketKey  [32]byte
	nextPlayer uint64
}

func NewStore() (*Store, error) {
	store := &Store{
		now:        time.Now,
		accounts:   make(map[string]*Account),
		sessions:   make(map[[32]byte]*Session),
		csrf:       make(map[string]csrfRecord),
		tickets:    make(map[[32]byte]*ticketRecord),
		issues:     make(map[[32]byte]map[string]*ticketRecord),
		nextPlayer: 1,
	}
	if _, err := rand.Read(store.ticketKey[:]); err != nil {
		return nil, err
	}
	return store, nil
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
	return count >= 12 && count <= 128 && !containsNUL(password)
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
	if s.mysql != nil {
		return s.mysql.register(name, hash, s.now())
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
	if s.mysql != nil {
		raw, session, err := s.mysql.login(name, password, s.now())
		if err == nil {
			s.mu.Lock()
			for _, ticket := range s.tickets {
				if ticket.PlayerID == session.PlayerID && !ticket.Consumed {
					ticket.Consumed = true
				}
			}
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
	if s.mysql != nil {
		return s.mysql.session(raw, s.now())
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
	if s.mysql != nil {
		_ = s.mysql.logout(digest, s.now())
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
	if s.mysql != nil {
		active, err := s.mysql.sessionActive(session.Digest, session.Generation, s.now())
		if err != nil {
			return "", time.Time{}, err
		}
		if !active {
			return "", time.Time{}, ErrUnauthenticated
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mysql == nil {
		live := s.sessions[session.Digest]
		if !s.sessionActiveLocked(live) || live.Generation != session.Generation {
			return "", time.Time{}, ErrUnauthenticated
		}
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
		return s.deriveTicket(session.Digest, issueID, gatewayID), prior.ExpiresAt, nil
	}
	for _, old := range byIssue {
		if !old.Consumed {
			old.Consumed = true
		}
	}
	raw := s.deriveTicket(session.Digest, issueID, gatewayID)
	digest := sha256.Sum256([]byte(raw))
	record := &ticketRecord{
		Digest: digest, SessionDigest: session.Digest, PlayerID: session.PlayerID,
		Generation: session.Generation, IssueID: issueID, GatewayID: gatewayID,
		ExpiresAt: s.now().Add(30 * time.Second),
	}
	s.tickets[digest] = record
	byIssue[issueID] = record
	return raw, record.ExpiresAt, nil
}

func (s *Store) ConsumeTicket(raw, gatewayID string) (uint64, error) {
	digest := sha256.Sum256([]byte(raw))
	s.mu.Lock()
	ticket := s.tickets[digest]
	if ticket == nil || ticket.Consumed || !s.now().Before(ticket.ExpiresAt) ||
		ticket.GatewayID != gatewayID {
		s.mu.Unlock()
		return 0, ErrUnauthenticated
	}
	sessionDigest := ticket.SessionDigest
	generation := ticket.Generation
	s.mu.Unlock()
	if s.mysql != nil {
		active, err := s.mysql.sessionActive(sessionDigest, generation, s.now())
		if err != nil {
			return 0, err
		}
		if !active {
			return 0, ErrUnauthenticated
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket = s.tickets[digest]
	if ticket == nil || ticket.Consumed || !s.now().Before(ticket.ExpiresAt) ||
		ticket.GatewayID != gatewayID {
		return 0, ErrUnauthenticated
	}
	if s.mysql == nil {
		session := s.sessions[ticket.SessionDigest]
		if !s.sessionActiveLocked(session) || session.Generation != ticket.Generation {
			return 0, ErrUnauthenticated
		}
	}
	ticket.Consumed = true
	return ticket.PlayerID, nil
}

func (s *Store) sessionActiveLocked(session *Session) bool {
	if session == nil || session.Revoked {
		return false
	}
	now := s.now()
	return now.Before(session.IdleExpiresAt) && now.Before(session.AbsoluteExpiresAt)
}

func (s *Store) invalidateTicketsLocked(sessionDigest [32]byte) {
	for _, ticket := range s.tickets {
		if ticket.SessionDigest == sessionDigest && !ticket.Consumed {
			ticket.Consumed = true
		}
	}
}

func (s *Store) deriveTicket(session [32]byte, issueID, gatewayID string) string {
	mac := hmac.New(sha256.New, s.ticketKey[:])
	_, _ = mac.Write(session[:])
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(issueID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(gatewayID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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
