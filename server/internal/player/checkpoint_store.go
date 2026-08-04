package player

import (
	"context"
	"errors"
	"fmt"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
)

var (
	ErrCheckpointNotFound        = errors.New("player checkpoint not found")
	ErrCheckpointConflict        = errors.New("player checkpoint compare-and-set conflict")
	ErrCheckpointFenced          = errors.New("player checkpoint owner epoch is fenced")
	ErrCheckpointRetryable       = errors.New("player checkpoint write is retryable")
	ErrCheckpointCorruptConflict = errors.New("player checkpoint content conflicts at the same revision")
)

// StoreToken is an opaque physical record version. Runtime preserves it but
// never interprets it; each CheckpointStore implementation owns its encoding.
type StoreToken []byte

// LoadedCheckpoint is the durable state and CAS position loaded at activation.
type LoadedCheckpoint struct {
	State             *State
	PersistedRevision uint64
	Token             StoreToken
}

// CheckpointWrite is one immutable Dirty snapshot copied from an Actor mailbox.
type CheckpointWrite struct {
	Checkpoint       *datav1.PlayerCheckpointV1
	ExpectedRevision uint64
	ExpectedToken    StoreToken
}

// CheckpointWriteStatus normalizes persistence outcomes across adapters.
type CheckpointWriteStatus uint8

const (
	CheckpointWriteApplied CheckpointWriteStatus = iota + 1
	CheckpointWriteAlreadyApplied
	CheckpointWriteStaleCopy
	CheckpointWriteFenced
	CheckpointWriteRetryableFailure
	CheckpointWriteCorruptConflict
)

// CheckpointWriteResult reports one normalized CAS outcome.
type CheckpointWriteResult struct {
	Status   CheckpointWriteStatus
	NewToken StoreToken
}

// CheckpointStore is the only persistence dependency of Player Runtime.
// Ordinary player commands do not call it synchronously; activation, Dirty
// flushing and controlled migration do.
type CheckpointStore interface {
	Load(context.Context, uint64) (LoadedCheckpoint, error)
	SaveCAS(context.Context, CheckpointWrite) (CheckpointWriteResult, error)
}

func cloneStoreToken(token StoreToken) StoreToken {
	return append(StoreToken(nil), token...)
}

func checkpointWriteError(result CheckpointWriteResult, err error) error {
	if err != nil {
		return err
	}
	switch result.Status {
	case CheckpointWriteApplied, CheckpointWriteAlreadyApplied:
		return nil
	case CheckpointWriteStaleCopy:
		return ErrCheckpointConflict
	case CheckpointWriteFenced:
		return ErrCheckpointFenced
	case CheckpointWriteRetryableFailure:
		return ErrCheckpointRetryable
	case CheckpointWriteCorruptConflict:
		return ErrCheckpointCorruptConflict
	default:
		return fmt.Errorf("unsupported checkpoint write status %d", result.Status)
	}
}
