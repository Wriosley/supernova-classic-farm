package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mailv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/mail"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
)

type recordingNotifier struct {
	calls  [][2]any
	counts []uint32
	err    error
}

func (n *recordingNotifier) SetMailRedDot(_ context.Context, playerID uint64, notificationID string, count uint32) error {
	n.calls = append(n.calls, [2]any{playerID, notificationID})
	n.counts = append(n.counts, count)
	return n.err
}

type fakeMailboxQuickCache struct {
	known         bool
	count         uint32
	publicRefresh bool
	set           chan uint32
}

func (f *fakeMailboxQuickCache) ApplyMailEvent(context.Context, uint64, string, int64) (bool, uint32, error) {
	f.count++
	return f.known, f.count, nil
}
func (f *fakeMailboxQuickCache) SetMailbox(_ context.Context, _ uint64, count uint32, _ int64, _ int64) error {
	f.known = true
	f.count = count
	if f.set != nil {
		f.set <- count
	}
	return nil
}
func (f *fakeMailboxQuickCache) GetMailbox(context.Context, uint64) (bool, uint32, bool, error) {
	return f.known, f.count, f.publicRefresh, nil
}
func (f *fakeMailboxQuickCache) AdvancePublicWatermark(context.Context, int64) error { return nil }

func TestPublicMailRegistrationFilter(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(1, 1000)
	store.SeedAccount(2, 5000)
	svc, err := NewService(store, nil, fixedNow(6000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePublicMail(context.Background(), CreatePublicMailInput{
		Title: "公告", Content: "老", PublishedAtMS: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}

	oldPlayer, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 1, RegisteredAtMs: 1000,
	})
	if err != nil || oldPlayer.GetError() != nil {
		t.Fatalf("old player open: %v %v", err, oldPlayer.GetError())
	}
	if len(oldPlayer.Mails) != 1 || oldPlayer.Mails[0].MailId != mailID {
		t.Fatalf("old player mails=%v", oldPlayer.Mails)
	}

	newPlayer, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 2, RegisteredAtMs: 5000,
	})
	if err != nil || newPlayer.GetError() != nil {
		t.Fatalf("new player open: %v %v", err, newPlayer.GetError())
	}
	if len(newPlayer.Mails) != 0 {
		t.Fatalf("new player should not see pre-reg public mail: %v", newPlayer.Mails)
	}
}

func TestOpenMailboxAutoMarksPublicMailReadAndRefreshesCount(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(7, 1)
	clock := &clock{at: time.UnixMilli(1000)}
	svc, err := NewService(store, nil, clock.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeMailboxQuickCache{known: true, count: 9, set: make(chan uint32, 1)}
	svc.SetMailboxQuickCache(cache)

	clock.at = time.UnixMilli(2000)
	if _, err := svc.CreatePublicMail(context.Background(), CreatePublicMailInput{
		Title: "公告", Content: "body", PublishedAtMS: 1500,
	}); err != nil {
		t.Fatal(err)
	}

	clock.at = time.UnixMilli(3000)
	opened, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 7, RegisteredAtMs: 1,
	})
	if err != nil || len(opened.Mails) != 1 {
		t.Fatalf("open mailbox = %+v err=%v", opened, err)
	}
	if !opened.Mails[0].GetRead() {
		t.Fatal("public mail should be marked read in the response")
	}

	select {
	case count := <-cache.set:
		if count != 0 {
			t.Fatalf("quick count after public open = %d, want 0", count)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for quick mailbox refresh")
	}

	deadline := time.Now().Add(time.Second)
	for {
		state, _, getErr := store.GetMailState(context.Background(), 7, opened.Mails[0].MailId)
		if getErr == nil && state.GetRead() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("public mail state not written back: state=%+v err=%v", state, getErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	indicator, err := svc.CheckMailboxIndicator(context.Background(), &mailv1.CheckMailboxIndicatorRequest{
		PlayerId: 7, RegisteredAtMs: 1,
	})
	if err != nil || indicator.GetNewMailCount() != 0 {
		t.Fatalf("indicator after public open = %+v err=%v", indicator, err)
	}
}

func TestPrivateMailIsolationAndRedDotFailOpen(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(10, 1)
	store.SeedAccount(11, 1)
	notifier := &recordingNotifier{err: errors.New("info down")}
	svc, err := NewService(store, notifier, fixedNow(100), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 10,
		Title:             "私信",
		Content:           "你好",
		Attachments:       []*tcaplusv1.MailAttachment{{ItemId: 1, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("expected red-dot attempt, got %v", notifier.calls)
	}
	if notifier.calls[0][0] != uint64(10) || notifier.calls[0][1] != mailID {
		t.Fatalf("unexpected notify payload %v", notifier.calls[0])
	}

	owner, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 10, RegisteredAtMs: 1,
	})
	if err != nil || len(owner.Mails) != 1 {
		t.Fatalf("owner open: %v %v", err, owner)
	}
	other, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 11, RegisteredAtMs: 1,
	})
	if err != nil || len(other.Mails) != 0 {
		t.Fatalf("other open: %v %v", err, other)
	}
}

func TestMailboxIndicatorRepairsStaleQuickCountFromUnreadState(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(12, 1)
	svc, err := NewService(store, nil, fixedNow(100), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 12, Title: "unread", Content: "body",
	}); err != nil {
		t.Fatal(err)
	}
	cache := &fakeMailboxQuickCache{known: true, count: 0}
	svc.SetMailboxQuickCache(cache)
	response, err := svc.CheckMailboxIndicator(context.Background(), &mailv1.CheckMailboxIndicatorRequest{PlayerId: 12, RegisteredAtMs: 1})
	if err != nil || !response.GetHasNewMail() || response.GetNewMailCount() != 1 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if cache.count != 1 {
		t.Fatalf("repaired quick count = %d, want 1", cache.count)
	}
}

func TestMailboxCursorAndIndicator(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(7, 1)
	clock := &clock{at: time.UnixMilli(1000)}
	svc, err := NewService(store, nil, clock.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	indicator, err := svc.CheckMailboxIndicator(context.Background(), &mailv1.CheckMailboxIndicatorRequest{
		PlayerId: 7, RegisteredAtMs: 1,
	})
	if err != nil || indicator.HasNewMail {
		t.Fatalf("empty mailbox indicator: %v %+v", err, indicator)
	}

	clock.at = time.UnixMilli(2000)
	if _, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 7, Title: "t", Content: "c",
	}); err != nil {
		t.Fatal(err)
	}
	indicator, err = svc.CheckMailboxIndicator(context.Background(), &mailv1.CheckMailboxIndicatorRequest{
		PlayerId: 7, RegisteredAtMs: 1,
	})
	if err != nil || !indicator.HasNewMail {
		t.Fatalf("expected new mail: %v %+v", err, indicator)
	}

	clock.at = time.UnixMilli(3000)
	opened, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 7, RegisteredAtMs: 1,
	})
	if err != nil || opened.LastMailboxOpenedAtMs != 3000 {
		t.Fatalf("open: %v %+v", err, opened)
	}
	if _, _, err := store.GetCursor(context.Background(), 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("opening mailbox must not persist a cursor, err=%v", err)
	}
	indicator, err = svc.CheckMailboxIndicator(context.Background(), &mailv1.CheckMailboxIndicatorRequest{
		PlayerId: 7, RegisteredAtMs: 1,
	})
	if err != nil || !indicator.HasNewMail || indicator.GetNewMailCount() != 1 {
		t.Fatalf("opening mailbox must preserve unread count: %v %+v", err, indicator)
	}
	if _, err := svc.MarkMailRead(context.Background(), &mailv1.MarkMailReadRequest{
		PlayerId: 7, MailId: opened.Mails[0].MailId, RegisteredAtMs: 1,
	}); err != nil {
		t.Fatal(err)
	}
	indicator, err = svc.CheckMailboxIndicator(context.Background(), &mailv1.CheckMailboxIndicatorRequest{
		PlayerId: 7, RegisteredAtMs: 1,
	})
	if err != nil || indicator.HasNewMail || indicator.GetNewMailCount() != 0 {
		t.Fatalf("mark read should clear the only unread mail: %v %+v", err, indicator)
	}
}

func TestClaimMailRefreshesUnreadQuickCount(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(13, 1)
	creator, err := NewService(store, nil, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	mailID, err := creator.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
		RecipientPlayerID: 13, Title: "reward", Content: "body",
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1002, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := NewClaimOrchestrator(store, &fakeZoneApplier{}, fixedNow(2000))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewServiceWithOrchestrator(store, nil, orchestrator, fixedNow(2000), nil)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeMailboxQuickCache{known: true, count: 1, set: make(chan uint32, 1)}
	svc.SetMailboxQuickCache(cache)
	response, err := svc.ClaimMail(context.Background(), &mailv1.ClaimMailRequest{
		PlayerId: 13, MailId: mailID, ClaimId: bytes.Repeat([]byte{13}, 16), RegisteredAtMs: 1,
	})
	if err != nil || response.GetError() != nil {
		t.Fatalf("claim response=%+v err=%v", response, err)
	}
	select {
	case count := <-cache.set:
		if count != 0 {
			t.Fatalf("quick unread count after claim = %d, want 0", count)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async unread refresh")
	}
}

func TestPaginationAndMarkRead(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(3, 1)
	svc, err := NewService(store, nil, fixedNow(10_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := svc.CreatePrivateMail(context.Background(), CreatePrivateMailInput{
			RecipientPlayerID: 3,
			Title:             "t",
			Content:           "c",
			SourceEventID:     "evt-" + string(rune('a'+i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 3, RegisteredAtMs: 1, PageSize: 2,
	})
	if err != nil || len(page1.Mails) != 2 || page1.NextPageToken == "" {
		t.Fatalf("page1: %v %+v", err, page1)
	}
	page2, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 3, RegisteredAtMs: 1, PageSize: 2, PageToken: page1.NextPageToken,
	})
	if err != nil || len(page2.Mails) != 2 {
		t.Fatalf("page2: %v %+v", err, page2)
	}
	if page1.Mails[0].MailId == page2.Mails[0].MailId {
		t.Fatal("pages should not overlap")
	}
	mailID := page1.Mails[0].MailId
	if _, err := svc.MarkMailRead(context.Background(), &mailv1.MarkMailReadRequest{
		PlayerId: 3, MailId: mailID, RegisteredAtMs: 1,
	}); err != nil {
		t.Fatal(err)
	}
	again, err := svc.OpenMailbox(context.Background(), &mailv1.OpenMailboxRequest{
		PlayerId: 3, RegisteredAtMs: 1, PageSize: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, view := range again.Mails {
		if view.MailId == mailID {
			found = true
			if !view.Read {
				t.Fatal("expected read")
			}
		}
	}
	if !found {
		t.Fatal("mail missing after mark read")
	}
}

func TestCreateGiftMailDedup(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(2, 1)
	notifier := &recordingNotifier{}
	svc, err := NewService(store, notifier, fixedNow(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	eventID := make([]byte, 16)
	for i := range eventID {
		eventID[i] = byte(i + 1)
	}
	first, err := svc.CreateGiftMail(context.Background(), &mailv1.CreateGiftMailRequest{
		SourceEventId: eventID, SenderPlayerId: 1, SenderDisplayName: "A",
		RecipientPlayerId: 2, CropItemId: 1002, Quantity: 3, CreatedAtMs: 1000,
	})
	if err != nil || first.GetError() != nil || first.MailId == "" || first.AlreadyApplied {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := svc.CreateGiftMail(context.Background(), &mailv1.CreateGiftMailRequest{
		SourceEventId: eventID, SenderPlayerId: 1, SenderDisplayName: "A",
		RecipientPlayerId: 2, CropItemId: 1002, Quantity: 3, CreatedAtMs: 1000,
	})
	if err != nil || !second.AlreadyApplied || second.MailId != first.MailId {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	mails, err := store.ListPrivateMails(context.Background(), 2)
	if err != nil || len(mails) != 1 || mails[0].MailType != tcaplusv1.MailType_MAIL_TYPE_GIFT {
		t.Fatalf("mails=%v err=%v", mails, err)
	}
	if len(notifier.counts) != 1 || notifier.counts[0] != 1 {
		t.Fatalf("gift red-dot counts=%v, want [1]", notifier.counts)
	}
}

func TestAdminAPIAuthAndCreate(t *testing.T) {
	store := NewMemoryStore()
	store.SeedAccount(42, 1)
	svc, err := NewService(store, &recordingNotifier{}, fixedNow(50), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewAdminHandler(svc, "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/internal/v1/admin/mails/private", strings.NewReader(`{
		"recipient_player_id":42,"title":"hi","content":"body"
	}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created createMailResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil || created.MailID == "" {
		t.Fatalf("decode: %v %v", err, created)
	}

	bad := httptest.NewRequest(http.MethodPost, "/internal/v1/admin/mails/public", strings.NewReader(`{
		"title":"x","content":"y"
	}`))
	bad.RemoteAddr = "127.0.0.1:1234"
	bad.Header.Set("Authorization", "Bearer wrong")
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, bad)
	if badRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", badRec.Code)
	}
	if strings.Contains(badRec.Body.String(), "secret-token") {
		t.Fatal("admin token leaked")
	}
	_, _ = io.Copy(io.Discard, badRec.Body)
}

type clock struct{ at time.Time }

func (c *clock) Now() time.Time { return c.at }

func fixedNow(ms int64) func() time.Time {
	return func() time.Time { return time.UnixMilli(ms) }
}
