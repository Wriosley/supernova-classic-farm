package interaction

import (
	"context"
	"errors"
	"testing"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
)

func newTestStore(t *testing.T) (*testtcaplus.Client, *TcaplusStore) {
	t.Helper()
	client := testtcaplus.New()
	store, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatalf("NewTcaplusStore: %v", err)
	}
	return client, store
}

func TestTcaplusStoreInsertGetUpdateRoundTrip(t *testing.T) {
	_, store := newTestStore(t)
	ctx := context.Background()
	id := fixedVisitID(0x01)

	if _, _, err := store.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound before insert, got %v", err)
	}

	record := &tcaplusv1.FriendInteraction{
		InteractionId: id, VisitorPlayerId: 1, OwnerPlayerId: 2,
		Status: tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_INIT,
	}
	version, err := store.Insert(ctx, record)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if version == 0 {
		t.Fatalf("expected a non-zero version after insert")
	}

	if _, err := store.Insert(ctx, record); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected ErrAlreadyExists on duplicate insert, got %v", err)
	}

	loaded, loadedVersion, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if loaded.VisitorPlayerId != 1 || loaded.OwnerPlayerId != 2 {
		t.Fatalf("unexpected loaded record: %+v", loaded)
	}

	loaded.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED
	if _, err := store.Update(ctx, loaded, loadedVersion); err != nil {
		t.Fatalf("Update: %v", err)
	}

	final, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if final.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED {
		t.Fatalf("expected updated status to persist, got %v", final.Status)
	}
}

// TestTcaplusStoreUpdateRejectsStaleVersion guards the CAS conflict
// requirement: a write against a version that is no longer current must
// fail, never silently "succeed" over the newer value.
func TestTcaplusStoreUpdateRejectsStaleVersion(t *testing.T) {
	_, store := newTestStore(t)
	ctx := context.Background()
	id := fixedVisitID(0x02)

	record := &tcaplusv1.FriendInteraction{
		InteractionId: id, VisitorPlayerId: 1, OwnerPlayerId: 2,
		Status: tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_INIT,
	}
	staleVersion, err := store.Insert(ctx, record)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Advance the record once from underneath, bumping its live version.
	loaded, liveVersion, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	loaded.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED
	if _, err := store.Update(ctx, loaded, liveVersion); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	// A second writer using the now-stale version must be rejected, not
	// silently accepted as if it were still current.
	loaded.Status = tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_OWNER_APPLIED
	if _, err := store.Update(ctx, loaded, staleVersion); err == nil {
		t.Fatalf("expected stale-version Update to fail")
	}

	final, _, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after conflict: %v", err)
	}
	if final.Status != tcaplusv1.FriendInteractionStatus_FRIEND_INTERACTION_STATUS_VISITOR_RESERVED {
		t.Fatalf("expected the rejected write to leave status untouched, got %v", final.Status)
	}
}
