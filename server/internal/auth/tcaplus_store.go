package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"time"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

const (
	tcaplusAccountReserved uint32 = 1
	tcaplusAccountActive   uint32 = 3
	tcaplusCounterID       uint32 = 1
	tcaplusMaxCASAttempts         = 8
)

type tcaplusAuthClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
}

type tcaplusAuthStore struct {
	client tcaplusAuthClient
	zoneID uint32
}

// NewTcaplusStore 只负责账号身份（Account / PlayerID / Session）。
// 初始农田 Checkpoint 由 Owner Zone 在 Actor 首次激活时创建。
func NewTcaplusStore(
	client tcaplusAuthClient,
	zoneID uint32,
) (*Store, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus auth client and zone are required")
	}
	store, err := NewStore()
	if err != nil {
		return nil, err
	}
	store.durable = &tcaplusAuthStore{
		client: client, zoneID: zoneID,
	}
	return store, nil
}

func (s *tcaplusAuthStore) register(
	name string,
	password string,
	hash passwordHash,
	now time.Time,
) (string, *Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	account, accountVersion, err := s.loadAccountByName(ctx, name)
	switch {
	case err == nil:
		if account.Status != tcaplusAccountReserved ||
			!verifyPassword(password, passwordHashFromAccount(account)) {
			return "", nil, ErrAccountUnavailable
		}
	case errors.Is(err, ErrAccountUnavailable):
		playerID, allocErr := s.allocatePlayerID(ctx, now)
		if allocErr != nil {
			return "", nil, allocErr
		}
		provisioningID := make([]byte, 16)
		if _, allocErr = rand.Read(provisioningID); allocErr != nil {
			return "", nil, allocErr
		}
		account = &tcaplusv1.AccountByName{
			AccountName: name, Status: tcaplusAccountReserved,
			PlayerId: playerID, ProvisioningId: provisioningID,
			PasswordSalt:      append([]byte(nil), hash.Salt...),
			PasswordDigest:    append([]byte(nil), hash.Digest...),
			PasswordMemoryKib: hash.MemoryKiB, PasswordIterations: hash.Iterations,
			PasswordThreads: hash.Threads, PasswordVersion: hash.Version,
			SagaStep: 1, SagaStartedAtMs: now.UnixMilli(),
			CreatedAtMs: now.UnixMilli(), UpdatedAtMs: now.UnixMilli(),
		}
		opt := insertOpt(ctx)
		if insertErr := s.client.DoInsert(account, opt, s.zoneID); insertErr != nil {
			if tcaplusdb.IsAlreadyExists(insertErr) {
				return s.register(name, password, hash, now)
			}
			return "", nil, fmt.Errorf("reserve Tcaplus account: %w", insertErr)
		}
		accountVersion = opt.Version
	default:
		return "", nil, err
	}

	playerRecord, playerVersion, err := s.ensurePlayerAccount(ctx, account, now)
	if err != nil {
		return "", nil, err
	}

	raw, session, sessionErr := newTcaplusSession(account.PlayerId, name, 1, now)
	if sessionErr != nil {
		return "", nil, sessionErr
	}
	if err := s.insertSession(ctx, session); err != nil {
		return "", nil, err
	}
	playerRecord.Status = tcaplusAccountActive
	playerRecord.SessionGeneration = 1
	playerRecord.UpdatedAtMs = now.UnixMilli()
	if err := s.updatePlayerAccount(ctx, playerRecord, playerVersion); err != nil {
		return "", nil, err
	}
	account.Status = tcaplusAccountActive
	account.SessionGeneration = 1
	account.SagaStep = 7
	account.UpdatedAtMs = now.UnixMilli()
	if err := s.updateNameAccount(ctx, account, accountVersion); err != nil {
		return "", nil, err
	}
	return raw, session, nil
}

func passwordHashFromAccount(account *tcaplusv1.AccountByName) passwordHash {
	if account == nil {
		return passwordHash{}
	}
	return passwordHash{
		Salt: account.PasswordSalt, Digest: account.PasswordDigest,
		MemoryKiB:  account.PasswordMemoryKib,
		Iterations: account.PasswordIterations,
		Threads:    account.PasswordThreads, Version: account.PasswordVersion,
	}
}

func (s *tcaplusAuthStore) allocatePlayerID(ctx context.Context, now time.Time) (uint64, error) {
	for attempt := 0; attempt < tcaplusMaxCASAttempts; attempt++ {
		record := &tcaplusv1.PlayerIdCounter{CounterId: tcaplusCounterID}
		getOpt := &option.PBOpt{Ctx: ctx}
		err := s.client.DoGet(record, getOpt, s.zoneID)
		if tcaplusdb.IsNotFound(err) {
			seed := &tcaplusv1.PlayerIdCounter{
				CounterId: tcaplusCounterID, NextPlayerId: 2,
				UpdatedAtMs: now.UnixMilli(),
			}
			if insertErr := s.client.DoInsert(seed, insertOpt(ctx), s.zoneID); insertErr == nil {
				return 1, nil
			} else if !tcaplusdb.IsAlreadyExists(insertErr) {
				return 0, fmt.Errorf("seed Tcaplus player counter: %w", insertErr)
			}
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("load Tcaplus player counter: %w", err)
		}
		if record.NextPlayerId == 0 || record.NextPlayerId == math.MaxUint64 {
			return 0, errors.New("Tcaplus player ID counter is exhausted")
		}
		allocated := record.NextPlayerId
		record.NextPlayerId++
		record.UpdatedAtMs = now.UnixMilli()
		if updateErr := s.client.DoUpdate(
			record, updateOpt(ctx, getOpt.Version), s.zoneID,
		); updateErr == nil {
			return allocated, nil
		}
	}
	return 0, errors.New("Tcaplus player ID allocation conflicted too many times")
}

func (s *tcaplusAuthStore) ensurePlayerAccount(
	ctx context.Context,
	account *tcaplusv1.AccountByName,
	now time.Time,
) (*tcaplusv1.AccountByPlayer, int32, error) {
	record := &tcaplusv1.AccountByPlayer{
		PlayerId: account.PlayerId, AccountName: account.AccountName,
		Status:         tcaplusAccountReserved,
		ProvisioningId: append([]byte(nil), account.ProvisioningId...),
		CreatedAtMs:    account.CreatedAtMs, UpdatedAtMs: now.UnixMilli(),
	}
	opt := insertOpt(ctx)
	if err := s.client.DoInsert(record, opt, s.zoneID); err == nil {
		return record, opt.Version, nil
	} else if !tcaplusdb.IsAlreadyExists(err) {
		return nil, 0, fmt.Errorf("insert Tcaplus player account: %w", err)
	}
	loaded, version, err := s.loadAccountByPlayer(ctx, account.PlayerId)
	if err != nil {
		return nil, 0, err
	}
	if loaded.AccountName != account.AccountName ||
		!bytes.Equal(loaded.ProvisioningId, account.ProvisioningId) {
		return nil, 0, errors.New("Tcaplus player account conflicts with provisioning")
	}
	return loaded, version, nil
}

func (s *tcaplusAuthStore) login(
	name string,
	password string,
	now time.Time,
) (string, *Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	account, _, err := s.loadAccountByName(ctx, name)
	if err != nil || account.Status != tcaplusAccountActive {
		_ = verifyPassword(password, dummyPasswordHash())
		return "", nil, ErrInvalidCredentials
	}
	if !verifyPassword(password, passwordHashFromAccount(account)) {
		return "", nil, ErrInvalidCredentials
	}
	generation, err := s.advanceSessionGeneration(ctx, account.PlayerId, name, now)
	if err != nil {
		return "", nil, err
	}
	raw, session, err := newTcaplusSession(account.PlayerId, name, generation, now)
	if err != nil {
		return "", nil, err
	}
	if err := s.insertSession(ctx, session); err != nil {
		return "", nil, err
	}
	return raw, session, nil
}

func (s *tcaplusAuthStore) advanceSessionGeneration(
	ctx context.Context,
	playerID uint64,
	name string,
	now time.Time,
) (uint64, error) {
	for attempt := 0; attempt < tcaplusMaxCASAttempts; attempt++ {
		account, accountVersion, err := s.loadAccountByName(ctx, name)
		if err != nil || account.Status != tcaplusAccountActive ||
			account.PlayerId != playerID {
			return 0, ErrInvalidCredentials
		}
		playerRecord, playerVersion, err := s.loadAccountByPlayer(ctx, playerID)
		if err != nil || playerRecord.Status != tcaplusAccountActive ||
			playerRecord.AccountName != name {
			return 0, ErrInvalidCredentials
		}
		current := account.SessionGeneration
		if playerRecord.SessionGeneration > current {
			current = playerRecord.SessionGeneration
		}
		if current == math.MaxUint64 {
			return 0, errors.New("session generation exhausted")
		}
		next := current + 1
		playerRecord.SessionGeneration = next
		playerRecord.UpdatedAtMs = now.UnixMilli()
		if err := s.updatePlayerAccount(ctx, playerRecord, playerVersion); err != nil {
			continue
		}
		account.SessionGeneration = next
		account.UpdatedAtMs = now.UnixMilli()
		if err := s.updateNameAccount(ctx, account, accountVersion); err != nil {
			continue
		}
		return next, nil
	}
	return 0, errors.New("advance Tcaplus Session generation conflicted too many times")
}

func (s *tcaplusAuthStore) session(raw string, now time.Time) (*Session, error) {
	digest := sha256.Sum256([]byte(raw))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record, version, err := s.loadSession(ctx, digest)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	account, _, err := s.loadAccountByPlayer(ctx, record.PlayerId)
	if err != nil || account.Status != tcaplusAccountActive ||
		record.Revoked || record.Generation != account.SessionGeneration ||
		now.UnixMilli() >= record.IdleExpiresAtMs ||
		now.UnixMilli() >= record.AbsoluteExpiresAtMs {
		_ = s.logout(digest, now)
		return nil, ErrUnauthenticated
	}
	nextIdle := now.Add(12 * time.Hour).UnixMilli()
	if nextIdle > record.AbsoluteExpiresAtMs {
		nextIdle = record.AbsoluteExpiresAtMs
	}
	record.IdleExpiresAtMs = nextIdle
	record.UpdatedAtMs = now.UnixMilli()
	if err := s.client.DoUpdate(record, updateOpt(ctx, version), s.zoneID); err != nil {
		return nil, fmt.Errorf("refresh Tcaplus Session: %w", err)
	}
	return sessionFromTcaplus(record, account.AccountName), nil
}

func (s *tcaplusAuthStore) logout(digest [32]byte, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record, version, err := s.loadSession(ctx, digest)
	if err != nil {
		return nil
	}
	record.Revoked = true
	record.UpdatedAtMs = now.UnixMilli()
	if err := s.client.DoUpdate(record, updateOpt(ctx, version), s.zoneID); err != nil {
		return fmt.Errorf("revoke Tcaplus Session: %w", err)
	}
	return nil
}

func (s *tcaplusAuthStore) sessionActive(
	digest [32]byte,
	generation uint64,
	now time.Time,
) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record, _, err := s.loadSession(ctx, digest)
	if tcaplusdb.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	account, _, err := s.loadAccountByPlayer(ctx, record.PlayerId)
	if err != nil {
		return false, err
	}
	return account.Status == tcaplusAccountActive &&
		account.SessionGeneration == generation &&
		record.Generation == generation && !record.Revoked &&
		now.UnixMilli() < record.IdleExpiresAtMs &&
		now.UnixMilli() < record.AbsoluteExpiresAtMs, nil
}

func (s *tcaplusAuthStore) loadAccountByName(
	ctx context.Context,
	name string,
) (*tcaplusv1.AccountByName, int32, error) {
	record := &tcaplusv1.AccountByName{AccountName: name}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return nil, 0, ErrAccountUnavailable
		}
		return nil, 0, fmt.Errorf("load Tcaplus account: %w", err)
	}
	return record, opt.Version, nil
}

func (s *tcaplusAuthStore) loadAccountByPlayer(
	ctx context.Context,
	playerID uint64,
) (*tcaplusv1.AccountByPlayer, int32, error) {
	record := &tcaplusv1.AccountByPlayer{PlayerId: playerID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		return nil, 0, fmt.Errorf("load Tcaplus player account: %w", err)
	}
	return record, opt.Version, nil
}

func (s *tcaplusAuthStore) loadSession(
	ctx context.Context,
	digest [32]byte,
) (*tcaplusv1.Session, int32, error) {
	record := &tcaplusv1.Session{SessionDigest: append([]byte(nil), digest[:]...)}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		return nil, 0, err
	}
	return record, opt.Version, nil
}

func (s *tcaplusAuthStore) insertSession(ctx context.Context, session *Session) error {
	record := &tcaplusv1.Session{
		SessionDigest: append([]byte(nil), session.Digest[:]...),
		PlayerId:      session.PlayerID, Generation: session.Generation,
		CreatedAtMs:         session.CreatedAt.UnixMilli(),
		IdleExpiresAtMs:     session.IdleExpiresAt.UnixMilli(),
		AbsoluteExpiresAtMs: session.AbsoluteExpiresAt.UnixMilli(),
		UpdatedAtMs:         session.CreatedAt.UnixMilli(),
	}
	if err := s.client.DoInsert(record, insertOpt(ctx), s.zoneID); err != nil {
		return fmt.Errorf("insert Tcaplus Session: %w", err)
	}
	return nil
}

func (s *tcaplusAuthStore) updateNameAccount(
	ctx context.Context,
	record *tcaplusv1.AccountByName,
	version int32,
) error {
	if err := s.client.DoUpdate(record, updateOpt(ctx, version), s.zoneID); err != nil {
		return fmt.Errorf("update Tcaplus account: %w", err)
	}
	return nil
}

func (s *tcaplusAuthStore) updatePlayerAccount(
	ctx context.Context,
	record *tcaplusv1.AccountByPlayer,
	version int32,
) error {
	if err := s.client.DoUpdate(record, updateOpt(ctx, version), s.zoneID); err != nil {
		return fmt.Errorf("update Tcaplus player account: %w", err)
	}
	return nil
}

func insertOpt(ctx context.Context) *option.PBOpt {
	return &option.PBOpt{
		Ctx: ctx, ResultFlag: option.TcaplusResultFlagAllNewValue,
	}
}

func updateOpt(ctx context.Context, version int32) *option.PBOpt {
	return &option.PBOpt{
		Ctx: ctx, Version: version,
		VersionPolicy: option.CheckDataVersionAutoIncrease,
		ResultFlag:    option.TcaplusResultFlagAllNewValue,
	}
}

func newTcaplusSession(
	playerID uint64,
	name string,
	generation uint64,
	now time.Time,
) (string, *Session, error) {
	raw, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256([]byte(raw))
	return raw, newDatabaseSession(digest, playerID, name, generation, now), nil
}

func sessionFromTcaplus(record *tcaplusv1.Session, name string) *Session {
	var digest [32]byte
	copy(digest[:], record.SessionDigest)
	return &Session{
		Digest: digest, PlayerID: record.PlayerId, AccountName: name,
		Generation: record.Generation, Revoked: record.Revoked,
		CreatedAt:         time.UnixMilli(record.CreatedAtMs),
		IdleExpiresAt:     time.UnixMilli(record.IdleExpiresAtMs),
		AbsoluteExpiresAt: time.UnixMilli(record.AbsoluteExpiresAtMs),
	}
}
