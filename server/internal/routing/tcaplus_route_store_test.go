package routing_test

import (
	"testing"

	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/Wriosley/supernova-classic-farm/server/internal/testtcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
)

func TestFakeTcaplusStoresRouteControlRecordsByPrimaryKey(t *testing.T) {
	client := testtcaplus.New()
	meta := &tcaplusv1.ShardMapMeta{
		MapId: 1, ShardCount: 4096, HashAlgorithmVersion: 1,
		AssignmentAlgorithmVersion: 1, MapVersion: 7,
	}
	route := &tcaplusv1.ShardRoute{
		LogicalShardId: 42, OwnerZoneId: "zone-a",
		OwnerEndpoint: "http://zone-a:8082", OwnerEpoch: 2,
		RouteVersion: 3, CommittedMapVersion: 7, State: "ACTIVE",
	}
	if err := client.DoInsert(meta, &option.PBOpt{}); err != nil {
		t.Fatal(err)
	}
	if err := client.DoInsert(route, &option.PBOpt{}); err != nil {
		t.Fatal(err)
	}

	loadedMeta := &tcaplusv1.ShardMapMeta{MapId: 1}
	if err := client.DoGet(loadedMeta, &option.PBOpt{}); err != nil {
		t.Fatal(err)
	}
	loadedRoute := &tcaplusv1.ShardRoute{LogicalShardId: 42}
	if err := client.DoGet(loadedRoute, &option.PBOpt{}); err != nil {
		t.Fatal(err)
	}
	if loadedMeta.MapVersion != 7 || loadedRoute.OwnerZoneId != "zone-a" {
		t.Fatalf("loaded meta=%+v route=%+v", loadedMeta, loadedRoute)
	}
}
