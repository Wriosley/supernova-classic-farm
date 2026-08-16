package mail

import (
	"context"
	"errors"
	"testing"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/terror"
	"google.golang.org/protobuf/proto"
)

type rejectTraverseClient struct {
	*testtcaplus.Client
	traverseCalls int
}

type missingIndexClient struct {
	*testtcaplus.Client
	partKeyCalls int
}

func (c *missingIndexClient) DoGetByPartKey(
	proto.Message, []string, *option.PBOpt, ...uint32,
) ([]proto.Message, error) {
	c.partKeyCalls++
	return nil, &terror.ErrorCode{Code: terror.TXHDB_ERR_INDEX_NO_EXIST}
}

func (c *rejectTraverseClient) Traverse(proto.Message) ([]proto.Message, error) {
	c.traverseCalls++
	return nil, errors.New("full-table Traverse is forbidden")
}

func TestTcaplusMailboxFallsBackWhenLiveTableHasNoIndex(t *testing.T) {
	client := &missingIndexClient{Client: testtcaplus.New()}
	store, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, record := range []*tcaplusv1.PrivateMail{
		{RecipientPlayerId: 7, MailId: "m-7"},
		{RecipientPlayerId: 8, MailId: "m-8"},
	} {
		if err := store.InsertPrivateMail(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []*tcaplusv1.PlayerMailState{
		{PlayerId: 7, MailId: "m-7", Read: true},
		{PlayerId: 8, MailId: "m-8", Read: true},
	} {
		if _, err := store.InsertMailState(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	mails, err := store.ListPrivateMails(ctx, 7)
	if err != nil || len(mails) != 1 || mails[0].GetRecipientPlayerId() != 7 {
		t.Fatalf("fallback mails = %+v err=%v", mails, err)
	}
	states, err := store.ListMailStates(ctx, 7)
	if err != nil || len(states) != 1 || states[0].GetPlayerId() != 7 {
		t.Fatalf("fallback states = %+v err=%v", states, err)
	}
	if client.partKeyCalls != 2 {
		t.Fatalf("part-key calls = %d, want 2", client.partKeyCalls)
	}
}

func TestTcaplusListPrivateMailsUsesRecipientPartKey(t *testing.T) {
	client := &rejectTraverseClient{Client: testtcaplus.New()}
	store, err := NewTcaplusStore(client, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, record := range []*tcaplusv1.PrivateMail{
		{RecipientPlayerId: 7, MailId: "m-7-a", Title: "a"},
		{RecipientPlayerId: 8, MailId: "m-8", Title: "other"},
		{RecipientPlayerId: 7, MailId: "m-7-b", Title: "b"},
	} {
		if err := store.InsertPrivateMail(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ListPrivateMails(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].GetRecipientPlayerId() != 7 || rows[1].GetRecipientPlayerId() != 7 {
		t.Fatalf("recipient rows = %+v, want two rows for player 7", rows)
	}
	if client.traverseCalls != 0 {
		t.Fatalf("private mailbox used %d full-table traverses", client.traverseCalls)
	}
	if _, err := store.InsertMailState(ctx, &tcaplusv1.PlayerMailState{
		PlayerId: 7, MailId: "m-7-a", Read: true,
	}); err != nil {
		t.Fatal(err)
	}
	states, err := store.ListMailStates(ctx, 7)
	if err != nil || len(states) != 1 || states[0].GetMailId() != "m-7-a" {
		t.Fatalf("player states = %+v err=%v", states, err)
	}
}
