package interaction

import (
	"context"
	"errors"
	"testing"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
)

func TestMemoryStoreInsertGetUpdateAndConflict(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	id := fixedVisitID(0x01)

	if _, _, err := store.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	record := &tcaplusv1.FriendInteraction{
		InteractionId: id, VisitorPlayerId: 1, OwnerPlayerId: 2,
		Status: tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_INIT,
	}
	version, err := store.Insert(ctx, record)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := store.Insert(ctx, record); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists, got %v", err)
	}

	loaded, loadedVersion, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loadedVersion != version {
		t.Fatalf("expected loaded version to match inserted version")
	}
	loaded.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED
	newVersion, err := store.Update(ctx, loaded, loadedVersion)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// A stale-version Update must fail, not silently succeed.
	loaded.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_OWNER_APPLIED
	if _, err := store.Update(ctx, loaded, loadedVersion); err == nil {
		t.Fatalf("expected a stale-version Update to fail")
	}

	final, finalVersion, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after conflict: %v", err)
	}
	if finalVersion != newVersion {
		t.Fatalf("expected version to reflect only the successful update")
	}
	if final.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED {
		t.Fatalf("expected the rejected write to leave status untouched, got %v", final.Status)
	}
}
