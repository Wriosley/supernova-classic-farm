package main

import (
	"context"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"google.golang.org/protobuf/proto"
)

type accountNameLookup struct {
	client interface {
		DoGet(proto.Message, *option.PBOpt, ...uint32) error
	}
	zoneID uint32
}

func (l accountNameLookup) AccountName(ctx context.Context, playerID uint64) (string, bool, error) {
	if l.client == nil || playerID == 0 {
		return "", false, nil
	}
	record := &tcaplusv1.AccountByPlayer{PlayerId: playerID}
	if err := l.client.DoGet(record, &option.PBOpt{Ctx: ctx}, l.zoneID); err != nil {
		if tcaplusdb.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if record.Status != 3 || record.AccountName == "" {
		return "", false, nil
	}
	return record.AccountName, true, nil
}

type authorizationShardOwner struct {
	auth   ownerAuthorization
	zoneID string
}

func (o authorizationShardOwner) OwnsLogicalShard(logicalShardID uint32) bool {
	if o.auth == nil {
		return true
	}
	entry, ok := o.auth.Entry(logicalShardID)
	if !ok {
		return false
	}
	return entry.OwnerZoneID == o.zoneID && entry.State == routing.RouteStateActive
}
