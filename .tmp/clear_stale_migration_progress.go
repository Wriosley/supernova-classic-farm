package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/tcaplusdb"
	"github.com/Wriosley/supernova-classic-farm/server/internal/routing"
)

func main() {
	shardID := mustUint32("SHARD_ID")
	transitionID := os.Getenv("TRANSITION_ID")
	if transitionID == "" {
		fatalf("TRANSITION_ID is required")
	}
	cfg, err := tcaplusdb.LoadConfigFromEnv()
	if err != nil {
		fatalf("load config: %v", err)
	}
	table, err := tcaplusdb.TableName("TCAPLUS_MIGRATION_TABLE", "MigrationProgress")
	if err != nil {
		fatalf("table name: %v", err)
	}
	client, err := tcaplusdb.Open(cfg, table)
	if err != nil {
		fatalf("open tcaplus: %v", err)
	}
	defer client.Close()

	store, err := routing.NewTcaplusControlStore(client, cfg.ZoneID)
	if err != nil {
		fatalf("new control store: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := store.DeleteOpenProgress(ctx, shardID, transitionID); err != nil {
		fatalf("delete open progress: %v", err)
	}
	fmt.Printf("deleted open progress shard=%d transition=%s\n", shardID, transitionID)
}

func mustUint32(name string) uint32 {
	raw := os.Getenv(name)
	if raw == "" {
		fatalf("%s is required", name)
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		fatalf("%s: %v", name, err)
	}
	return uint32(v)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
