package player

import (
	"errors"
	"testing"
)

func TestCheckpointWriteErrorNormalizesStoreStatuses(t *testing.T) {
	tests := []struct {
		name   string
		status CheckpointWriteStatus
		want   error
	}{
		{name: "applied", status: CheckpointWriteApplied},
		{name: "already applied", status: CheckpointWriteAlreadyApplied},
		{name: "stale copy", status: CheckpointWriteStaleCopy, want: ErrCheckpointConflict},
		{name: "fenced", status: CheckpointWriteFenced, want: ErrCheckpointFenced},
		{name: "retryable", status: CheckpointWriteRetryableFailure, want: ErrCheckpointRetryable},
		{name: "corrupt conflict", status: CheckpointWriteCorruptConflict, want: ErrCheckpointCorruptConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := checkpointWriteError(
				CheckpointWriteResult{Status: test.status},
				nil,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("checkpointWriteError() = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCloneStoreTokenDoesNotAliasAdapterMemory(t *testing.T) {
	adapterToken := StoreToken("version-1")
	runtimeToken := cloneStoreToken(adapterToken)
	adapterToken[0] = 'X'
	if string(runtimeToken) != "version-1" {
		t.Fatalf("runtime token changed through adapter alias: %q", runtimeToken)
	}
}
