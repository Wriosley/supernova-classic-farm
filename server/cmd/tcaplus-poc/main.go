package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wriosley/supernova-classic-farm/server/internal/player"
)

func main() {
	config, err := player.LoadTcaplusConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	playerID, err := requiredPlayerID()
	if err != nil {
		log.Fatal(err)
	}
	store, err := player.NewTcaplusCheckpointStore(config)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	loaded, err := store.Load(ctx, playerID)
	if errors.Is(err, player.ErrCheckpointNotFound) {
		initial := player.NewInitialCheckpoint(playerID, time.Now().UTC())
		result, createErr := store.Create(ctx, initial)
		if createErr != nil {
			log.Fatal(createErr)
		}
		if result.Status != player.CheckpointWriteApplied &&
			result.Status != player.CheckpointWriteAlreadyApplied {
			log.Fatalf("create status=%d", result.Status)
		}
		loaded, err = store.Load(ctx, playerID)
	}
	if err != nil {
		log.Fatal(err)
	}

	checkpoint, err := loaded.State.Checkpoint()
	if err != nil {
		log.Fatal(err)
	}
	checkpoint.PlayerSeq++
	checkpoint.CheckpointRevision++
	checkpoint.UpdatedAtMs = time.Now().UTC().UnixMilli()

	write := player.CheckpointWrite{
		Checkpoint:       checkpoint,
		ExpectedRevision: loaded.PersistedRevision,
		ExpectedToken:    loaded.Token,
	}
	applied, err := store.SaveCAS(ctx, write)
	if err != nil || applied.Status != player.CheckpointWriteApplied {
		log.Fatalf("CAS apply status=%d error=%v", applied.Status, err)
	}
	duplicate, err := store.SaveCAS(ctx, write)
	if err != nil || duplicate.Status != player.CheckpointWriteAlreadyApplied {
		log.Fatalf("duplicate CAS status=%d error=%v", duplicate.Status, err)
	}

	staleCheckpoint, err := loaded.State.Checkpoint()
	if err != nil {
		log.Fatal(err)
	}
	staleCheckpoint.CheckpointRevision = checkpoint.CheckpointRevision + 1
	staleCheckpoint.UpdatedAtMs = checkpoint.UpdatedAtMs + 1
	stale, err := store.SaveCAS(ctx, player.CheckpointWrite{
		Checkpoint:       staleCheckpoint,
		ExpectedRevision: loaded.PersistedRevision,
		ExpectedToken:    loaded.Token,
	})
	if err != nil || stale.Status != player.CheckpointWriteStaleCopy {
		log.Fatalf("stale CAS status=%d error=%v", stale.Status, err)
	}

	reloaded, err := store.Load(ctx, playerID)
	if err != nil {
		log.Fatal(err)
	}
	if reloaded.PersistedRevision != checkpoint.CheckpointRevision {
		log.Fatalf(
			"reload revision=%d want=%d",
			reloaded.PersistedRevision,
			checkpoint.CheckpointRevision,
		)
	}
	fmt.Printf(
		"TCAPLUS_POC PASS player_id=%d checkpoint_revision=%d "+
			"create_load=true cas=true duplicate=true stale_rejected=true reload=true\n",
		playerID,
		reloaded.PersistedRevision,
	)
}

func requiredPlayerID() (uint64, error) {
	raw := strings.TrimSpace(os.Getenv("TCAPLUS_POC_PLAYER_ID"))
	value, err := strconv.ParseUint(raw, 10, 64)
	if raw == "" || err != nil || value == 0 {
		return 0, errors.New("TCAPLUS_POC_PLAYER_ID must be a positive uint64")
	}
	return value, nil
}
