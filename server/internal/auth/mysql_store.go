package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
	driver "github.com/go-sql-driver/mysql"
)

const mysqlOperationTimeout = 5 * time.Second

type mysqlStore struct {
	db *sql.DB
}

func NewMySQLStore(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("MySQL database is required")
	}
	store, err := NewStore()
	if err != nil {
		return nil, err
	}
	store.mysql = &mysqlStore{db: db}
	return store, nil
}

func (m *mysqlStore) register(name string, hash passwordHash, now time.Time) (string, *Session, error) {
	raw, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256([]byte(raw))
	session := newDatabaseSession(digest, 0, name, 1, now)

	ctx, cancel := context.WithTimeout(context.Background(), mysqlOperationTimeout)
	defer cancel()
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", nil, fmt.Errorf("begin registration: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (
			account_name, status, password_salt, password_digest,
			password_memory_kib, password_iterations, password_threads, password_version,
			session_generation, created_at_ms, updated_at_ms
		) VALUES (?, 'ACTIVE', ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
		name, hash.Salt, hash.Digest, hash.MemoryKiB, hash.Iterations, hash.Threads,
		hash.Version, now.UnixMilli(), now.UnixMilli(),
	)
	if err != nil {
		if duplicateKey(err) {
			return "", nil, ErrAccountUnavailable
		}
		return "", nil, fmt.Errorf("insert account: %w", err)
	}
	playerID, err := result.LastInsertId()
	if err != nil || playerID <= 0 {
		return "", nil, errors.New("registration did not allocate player_id")
	}
	session.PlayerID = uint64(playerID)

	checkpoint := player.NewInitialCheckpoint(session.PlayerID, now)
	var fenceOwnerZoneID string
	var fenceOwnerEpoch uint64
	if err := tx.QueryRowContext(ctx, `
		SELECT owner_zone_id, owner_epoch
		FROM shard_fences
		WHERE logical_shard_id = ?
		FOR UPDATE`,
		checkpoint.LogicalShardId,
	).Scan(&fenceOwnerZoneID, &fenceOwnerEpoch); err != nil {
		return "", nil, fmt.Errorf("verify initial checkpoint fence: %w", err)
	}
	if fenceOwnerZoneID == "" || fenceOwnerEpoch == 0 {
		return "", nil, errors.New("initial checkpoint fence is invalid")
	}
	checkpoint.OwnerEpoch = fenceOwnerEpoch
	blob, checkpointSHA, err := player.MarshalCheckpoint(checkpoint)
	if err != nil {
		return "", nil, fmt.Errorf("create initial checkpoint: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO player_checkpoints (
			player_id, db_shard_id, logical_shard_id, owner_epoch, player_seq,
			checkpoint_revision, checkpoint_schema_version, checkpoint_blob,
			checkpoint_sha256, last_applied_config_version, created_at_ms, updated_at_ms
		) VALUES (?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		checkpoint.PlayerId, checkpoint.LogicalShardId, checkpoint.OwnerEpoch,
		checkpoint.PlayerSeq, checkpoint.CheckpointRevision, checkpoint.SchemaVersion,
		blob, checkpointSHA[:], checkpoint.LastAppliedConfigVersion,
		checkpoint.CreatedAtMs, checkpoint.UpdatedAtMs,
	)
	if err != nil {
		return "", nil, fmt.Errorf("insert initial checkpoint: %w", err)
	}
	if err := insertSession(ctx, tx, session); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, fmt.Errorf("commit registration: %w", err)
	}
	return raw, session, nil
}

func (m *mysqlStore) login(name, password string, now time.Time) (string, *Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mysqlOperationTimeout)
	defer cancel()

	var account Account
	err := m.db.QueryRowContext(ctx, `
		SELECT player_id, account_name, password_salt, password_digest,
		       password_memory_kib, password_iterations, password_threads,
		       password_version, session_generation
		FROM accounts
		WHERE account_name = ? AND status = 'ACTIVE'`,
		name,
	).Scan(
		&account.PlayerID, &account.Name, &account.Password.Salt, &account.Password.Digest,
		&account.Password.MemoryKiB, &account.Password.Iterations, &account.Password.Threads,
		&account.Password.Version, &account.Generation,
	)
	if errors.Is(err, sql.ErrNoRows) {
		_ = verifyPassword(password, dummyPasswordHash())
		return "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", nil, fmt.Errorf("load account: %w", err)
	}
	if !verifyPassword(password, account.Password) {
		return "", nil, ErrInvalidCredentials
	}

	raw, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256([]byte(raw))

	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", nil, fmt.Errorf("begin login: %w", err)
	}
	defer tx.Rollback()

	var currentGeneration uint64
	err = tx.QueryRowContext(ctx, `
		SELECT session_generation
		FROM accounts
		WHERE player_id = ? AND account_name = ? AND status = 'ACTIVE'
		FOR UPDATE`,
		account.PlayerID, name,
	).Scan(&currentGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrInvalidCredentials
	}
	if err != nil {
		return "", nil, fmt.Errorf("lock account: %w", err)
	}
	nextGeneration := currentGeneration + 1
	if nextGeneration == 0 {
		return "", nil, errors.New("session generation exhausted")
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE accounts
		SET session_generation = ?, updated_at_ms = ?
		WHERE player_id = ?`,
		nextGeneration, now.UnixMilli(), account.PlayerID,
	); err != nil {
		return "", nil, fmt.Errorf("advance Session generation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked = TRUE, updated_at_ms = ?
		WHERE player_id = ? AND revoked = FALSE`,
		now.UnixMilli(), account.PlayerID,
	); err != nil {
		return "", nil, fmt.Errorf("revoke old Sessions: %w", err)
	}
	session := newDatabaseSession(digest, account.PlayerID, name, nextGeneration, now)
	if err := insertSession(ctx, tx, session); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, fmt.Errorf("commit login: %w", err)
	}
	return raw, session, nil
}

func (m *mysqlStore) session(raw string, now time.Time) (*Session, error) {
	digest := sha256.Sum256([]byte(raw))
	ctx, cancel := context.WithTimeout(context.Background(), mysqlOperationTimeout)
	defer cancel()

	var session Session
	var accountGeneration uint64
	err := m.db.QueryRowContext(ctx, `
		SELECT s.player_id, a.account_name, s.generation, s.created_at_ms,
		       s.idle_expires_at_ms, s.absolute_expires_at_ms, s.revoked,
		       a.session_generation
		FROM auth_sessions s
		JOIN accounts a ON a.player_id = s.player_id
		WHERE s.session_digest = ? AND a.status = 'ACTIVE'`,
		digest[:],
	).Scan(
		&session.PlayerID, &session.AccountName, &session.Generation,
		timeScanner{target: &session.CreatedAt}, timeScanner{target: &session.IdleExpiresAt},
		timeScanner{target: &session.AbsoluteExpiresAt}, &session.Revoked, &accountGeneration,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, fmt.Errorf("load Session: %w", err)
	}
	session.Digest = digest
	if session.Revoked || session.Generation != accountGeneration ||
		!now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		_ = m.logout(digest, now)
		return nil, ErrUnauthenticated
	}
	nextIdle := now.Add(12 * time.Hour)
	if nextIdle.After(session.AbsoluteExpiresAt) {
		nextIdle = session.AbsoluteExpiresAt
	}
	if _, err := m.db.ExecContext(ctx, `
		UPDATE auth_sessions
		SET idle_expires_at_ms = ?, updated_at_ms = ?
		WHERE session_digest = ? AND revoked = FALSE`,
		nextIdle.UnixMilli(), now.UnixMilli(), digest[:],
	); err != nil {
		return nil, fmt.Errorf("refresh Session: %w", err)
	}
	session.IdleExpiresAt = nextIdle
	return &session, nil
}

func (m *mysqlStore) logout(digest [32]byte, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), mysqlOperationTimeout)
	defer cancel()
	_, err := m.db.ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked = TRUE, updated_at_ms = ?
		WHERE session_digest = ?`,
		now.UnixMilli(), digest[:],
	)
	return err
}

func (m *mysqlStore) sessionActive(digest [32]byte, generation uint64, now time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mysqlOperationTimeout)
	defer cancel()
	var active int
	err := m.db.QueryRowContext(ctx, `
		SELECT 1
		FROM auth_sessions s
		JOIN accounts a ON a.player_id = s.player_id
		WHERE s.session_digest = ?
		  AND s.generation = ?
		  AND s.revoked = FALSE
		  AND s.idle_expires_at_ms > ?
		  AND s.absolute_expires_at_ms > ?
		  AND a.status = 'ACTIVE'
		  AND a.session_generation = s.generation`,
		digest[:], generation, now.UnixMilli(), now.UnixMilli(),
	).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("validate Session: %w", err)
	}
	return active == 1, nil
}

func newDatabaseSession(digest [32]byte, playerID uint64, name string, generation uint64, now time.Time) *Session {
	return &Session{
		Digest: digest, PlayerID: playerID, AccountName: name, Generation: generation,
		CreatedAt: now, IdleExpiresAt: now.Add(12 * time.Hour),
		AbsoluteExpiresAt: now.Add(7 * 24 * time.Hour),
	}
}

func insertSession(ctx context.Context, tx *sql.Tx, session *Session) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO auth_sessions (
			session_digest, player_id, generation, created_at_ms,
			idle_expires_at_ms, absolute_expires_at_ms, revoked, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, FALSE, ?)`,
		session.Digest[:], session.PlayerID, session.Generation,
		session.CreatedAt.UnixMilli(), session.IdleExpiresAt.UnixMilli(),
		session.AbsoluteExpiresAt.UnixMilli(), session.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("insert Session: %w", err)
	}
	return nil
}

func duplicateKey(err error) bool {
	var mysqlError *driver.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}

type timeScanner struct {
	target *time.Time
}

func (s timeScanner) Scan(value any) error {
	milliseconds, ok := value.(int64)
	if !ok {
		if raw, valid := value.([]byte); valid {
			var err error
			milliseconds, err = parseInt64(raw)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("cannot scan %T as Unix milliseconds", value)
		}
	}
	*s.target = time.UnixMilli(milliseconds)
	return nil
}

func parseInt64(raw []byte) (int64, error) {
	var value int64
	if len(raw) == 0 {
		return 0, errors.New("empty integer")
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return 0, errors.New("invalid integer")
		}
		value = value*10 + int64(digit-'0')
	}
	return value, nil
}
