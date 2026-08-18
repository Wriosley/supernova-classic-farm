package auth

import (
	"encoding/hex"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBlake2bEmptyVector(t *testing.T) {
	t.Parallel()
	const want = "786a02f742015903c6c6fd852552d272912f4740e15847618a86e217f71f5419d25e1031afee585313896444934eb04b903a685b1448b755d56f701afe9be2ce"
	if got := hex.EncodeToString(blake2bSum(nil, 64)); got != want {
		t.Fatalf("BLAKE2b mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestRegisterLoginAndDuplicateLoginRevocation(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	oldRaw, oldSession, err := store.Register("farmer_one", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session(oldRaw); err != nil {
		t.Fatalf("new Session unavailable: %v", err)
	}

	newRaw, newSession, err := store.Login("farmer_one", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if newSession.Generation <= oldSession.Generation {
		t.Fatal("login did not advance Session generation")
	}
	if _, err := store.Session(oldRaw); err == nil {
		t.Fatal("older Session remained active")
	}
	if _, err := store.Session(newRaw); err != nil {
		t.Fatalf("new Session unavailable: %v", err)
	}
}

func TestTicketReplayAndAtomicConsumption(t *testing.T) {
	store, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := store.Register("farmer_two", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	const issueID = "57e0f2d4-a597-4c34-a7ec-15d823890a76"
	first, firstExpiry, err := store.IssueTicket(session, issueID, "local-gateway")
	if err != nil {
		t.Fatal(err)
	}
	replay, replayExpiry, err := store.IssueTicket(session, issueID, "local-gateway")
	if err != nil {
		t.Fatal(err)
	}
	if replay != first || !replayExpiry.Equal(firstExpiry) {
		t.Fatal("same issue ID did not replay the same live result")
	}
	if !strings.HasPrefix(first, "cfwt1.") {
		t.Fatalf("ticket is not versioned HMAC form: %q", first)
	}

	var successes atomic.Int32
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			if playerID, err := store.ConsumeTicket(first, "local-gateway"); err == nil && playerID == session.PlayerID {
				successes.Add(1)
			}
		}()
	}
	group.Wait()
	if successes.Load() != 1 {
		t.Fatalf("atomic consumption successes = %d, want 1", successes.Load())
	}
	if _, _, err := store.IssueTicket(session, issueID, "local-gateway"); err != ErrTicketReplay {
		t.Fatalf("consumed issue replay error = %v, want %v", err, ErrTicketReplay)
	}
}

func TestStatelessTicketCrossReplicaConsume(t *testing.T) {
	const shared = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	issuer, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := issuer.ConfigureTicketHMACKey([]byte(shared)); err != nil {
		t.Fatal(err)
	}
	if err := consumer.ConfigureTicketHMACKey([]byte(shared)); err != nil {
		t.Fatal(err)
	}
	_, session, err := issuer.Register("farmer_cross", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	// Consumer replica has the durable-less memory map empty; only HMAC +
	// claims matter for verification. Seed the same session into consumer so
	// the in-memory sessionActive check passes (Tcaplus mode uses durable).
	consumer.mu.Lock()
	copySession := *session
	consumer.sessions[session.Digest] = &copySession
	consumer.mu.Unlock()

	ticket, _, err := issuer.IssueTicket(session, "11111111-1111-4111-8111-111111111111", "local-gateway")
	if err != nil {
		t.Fatal(err)
	}
	playerID, err := consumer.ConsumeTicket(ticket, "local-gateway")
	if err != nil {
		t.Fatalf("cross-replica consume failed: %v", err)
	}
	if playerID != session.PlayerID {
		t.Fatalf("playerID=%d want %d", playerID, session.PlayerID)
	}
	if _, err := consumer.ConsumeTicket(ticket, "local-gateway"); err == nil {
		t.Fatal("second consume on consumer replica succeeded")
	}
}
