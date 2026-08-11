package mail

import (
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
	calls [][2]any
	err   error
}

func (n *recordingNotifier) SetMailRedDot(_ context.Context, playerID uint64, notificationID string) error {
	n.calls = append(n.calls, [2]any{playerID, notificationID})
	return n.err
}

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
		Attachments: []*tcaplusv1.MailAttachment{{ItemId: 1, Quantity: 2}},
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
	indicator, err = svc.CheckMailboxIndicator(context.Background(), &mailv1.CheckMailboxIndicatorRequest{
		PlayerId: 7, RegisteredAtMs: 1,
	})
	if err != nil || indicator.HasNewMail {
		t.Fatalf("after open should clear: %v %+v", err, indicator)
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
	svc, err := NewService(store, &recordingNotifier{}, fixedNow(1000), nil)
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
