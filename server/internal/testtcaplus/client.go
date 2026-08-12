package testtcaplus

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/terror"
	"google.golang.org/protobuf/proto"
)

type storedRecord struct {
	message proto.Message
	version int32
}

type Client struct {
	mu      sync.Mutex
	records map[string]storedRecord
}

func New() *Client {
	return &Client{records: make(map[string]storedRecord)}
}

func (c *Client) DoGet(message proto.Message, opt *option.PBOpt, _ ...uint32) error {
	if err := contextError(opt); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	record, found := c.records[recordKey(message)]
	if !found {
		return &terror.ErrorCode{Code: terror.TXHDB_ERR_RECORD_NOT_EXIST}
	}
	proto.Reset(message)
	proto.Merge(message, record.message)
	if opt != nil {
		opt.Version = record.version
	}
	return nil
}

func (c *Client) DoInsert(message proto.Message, opt *option.PBOpt, _ ...uint32) error {
	if err := contextError(opt); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := recordKey(message)
	if _, found := c.records[key]; found {
		return &terror.ErrorCode{Code: terror.SVR_ERR_FAIL_RECORD_EXIST}
	}
	c.records[key] = storedRecord{message: proto.Clone(message), version: 1}
	if opt != nil {
		opt.Version = 1
	}
	return nil
}

func (c *Client) DoUpdate(message proto.Message, opt *option.PBOpt, _ ...uint32) error {
	if err := contextError(opt); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := recordKey(message)
	current, found := c.records[key]
	if !found {
		return &terror.ErrorCode{Code: terror.TXHDB_ERR_RECORD_NOT_EXIST}
	}
	if opt != nil && opt.Version > 0 && current.version != opt.Version {
		return errors.New("fake Tcaplus record version conflict")
	}
	if err := checkCondition(current.message, opt); err != nil {
		return err
	}
	current.version++
	current.message = proto.Clone(message)
	c.records[key] = current
	if opt != nil {
		opt.Version = current.version
	}
	return nil
}

func (c *Client) DoDelete(message proto.Message, opt *option.PBOpt, _ ...uint32) error {
	if err := contextError(opt); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := recordKey(message)
	current, found := c.records[key]
	if !found {
		return &terror.ErrorCode{Code: terror.TXHDB_ERR_RECORD_NOT_EXIST}
	}
	if opt != nil && opt.Version > 0 && current.version != opt.Version {
		return errors.New("fake Tcaplus record version conflict")
	}
	delete(c.records, key)
	return nil
}

func (c *Client) Traverse(message proto.Message) ([]proto.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	table := string(message.ProtoReflect().Descriptor().Name())
	result := make([]proto.Message, 0)
	for _, record := range c.records {
		if string(record.message.ProtoReflect().Descriptor().Name()) == table {
			result = append(result, proto.Clone(record.message))
		}
	}
	if len(result) == 0 {
		return nil, &terror.ErrorCode{Code: terror.TXHDB_ERR_RECORD_NOT_EXIST}
	}
	return result, nil
}

func recordKey(message proto.Message) string {
	table := string(message.ProtoReflect().Descriptor().Name())
	var key string
	switch record := message.(type) {
	case *tcaplusv1.PlayerCheckpoint:
		key = strconv.FormatUint(record.PlayerId, 10)
	case *tcaplusv1.PlayerIdCounter:
		key = strconv.FormatUint(uint64(record.CounterId), 10)
	case *tcaplusv1.AccountByName:
		key = record.AccountName
	case *tcaplusv1.AccountByPlayer:
		key = strconv.FormatUint(record.PlayerId, 10)
	case *tcaplusv1.Session:
		key = fmt.Sprintf("%x", record.SessionDigest)
	case *tcaplusv1.ShardFence:
		key = strconv.FormatUint(uint64(record.LogicalShardId), 10)
	case *tcaplusv1.ShardMapMeta:
		key = strconv.FormatUint(uint64(record.MapId), 10)
	case *tcaplusv1.ShardRoute:
		key = strconv.FormatUint(uint64(record.LogicalShardId), 10)
	case *tcaplusv1.MigrationProgress:
		key = strconv.FormatUint(uint64(record.LogicalShardId), 10)
	case *tcaplusv1.PlayerOutbox:
		key = fmt.Sprintf("%x", record.EventId)
	case *tcaplusv1.FriendCodeCurrent:
		key = strconv.FormatUint(record.OwnerPlayerId, 10)
	case *tcaplusv1.FriendCodeLookup:
		key = record.Code
	case *tcaplusv1.FriendRelation:
		key = strconv.FormatUint(record.PlayerLowId, 10) + ":" +
			strconv.FormatUint(record.PlayerHighId, 10)
	case *tcaplusv1.FriendList:
		key = strconv.FormatUint(record.PlayerId, 10)
	case *tcaplusv1.FriendLinkSaga:
		key = fmt.Sprintf("%x", record.LinkId)
	case *tcaplusv1.FriendInteraction:
		key = fmt.Sprintf("%x", record.InteractionId)
	case *tcaplusv1.FirstFriendReward:
		key = strconv.FormatUint(record.InviteePlayerId, 10)
	case *tcaplusv1.PublicMail:
		key = record.MailId
	case *tcaplusv1.PrivateMail:
		key = strconv.FormatUint(record.RecipientPlayerId, 10) + ":" + record.MailId
	case *tcaplusv1.PlayerMailboxCursor:
		key = strconv.FormatUint(record.PlayerId, 10)
	case *tcaplusv1.PlayerMailState:
		key = strconv.FormatUint(record.PlayerId, 10) + ":" + record.MailId
	case *tcaplusv1.MailSourceDedup:
		key = record.SourceEventId
	case *tcaplusv1.MailClaimSaga:
		key = fmt.Sprintf("%x", record.ClaimId)
	default:
		panic(fmt.Sprintf("unsupported fake Tcaplus message %T", message))
	}
	return table + "/" + key
}

func checkCondition(current proto.Message, opt *option.PBOpt) error {
	if opt == nil || strings.TrimSpace(opt.Condition) == "" {
		return nil
	}
	checkpoint, ok := current.(*tcaplusv1.PlayerCheckpoint)
	if !ok {
		return errors.New("fake Tcaplus condition is unsupported")
	}
	parts := strings.Fields(opt.Condition)
	if len(parts) != 3 || parts[0] != "checkpoint_revision" || parts[1] != "=" {
		return errors.New("fake Tcaplus checkpoint condition is invalid")
	}
	expected, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil || checkpoint.CheckpointRevision != expected {
		return errors.New("fake Tcaplus checkpoint condition did not match")
	}
	return nil
}

func contextError(opt *option.PBOpt) error {
	if opt != nil && opt.Ctx != nil {
		return opt.Ctx.Err()
	}
	return nil
}
