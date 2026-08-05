package player

import (
	"context"
	"errors"
	"testing"
	"time"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/terror"
	"google.golang.org/protobuf/proto"
)

type fakeTcaplusCheckpointClient struct {
	get    func(*tcaplusv1.PlayerCheckpoint, *option.PBOpt) error
	insert func(*tcaplusv1.PlayerCheckpoint, *option.PBOpt) error
	update func(*tcaplusv1.PlayerCheckpoint, *option.PBOpt) error
}

func (f *fakeTcaplusCheckpointClient) DoGet(
	message proto.Message,
	opt *option.PBOpt,
	_ ...uint32,
) error {
	return f.get(message.(*tcaplusv1.PlayerCheckpoint), opt)
}

func (f *fakeTcaplusCheckpointClient) DoInsert(
	message proto.Message,
	opt *option.PBOpt,
	_ ...uint32,
) error {
	return f.insert(message.(*tcaplusv1.PlayerCheckpoint), opt)
}

func (f *fakeTcaplusCheckpointClient) DoUpdate(
	message proto.Message,
	opt *option.PBOpt,
	_ ...uint32,
) error {
	return f.update(message.(*tcaplusv1.PlayerCheckpoint), opt)
}

func TestLoadTcaplusConfigFromEnv(t *testing.T) {
	t.Setenv("TCAPLUS_APP_ID", "2")
	t.Setenv("TCAPLUS_ZONE_ID", "3")
	t.Setenv("TCAPLUS_DIR_URL", "tcp://127.0.0.1:9999")
	t.Setenv("TCAPLUS_SIGNATURE", "secret-from-environment")
	t.Setenv("TCAPLUS_CHECKPOINT_TABLE", "")

	config, err := LoadTcaplusConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.AppID != 2 || config.ZoneID != 3 ||
		config.TableName != DefaultTcaplusCheckpointTable {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestTcaplusCheckpointStoreLoadsEnvelopeAndVersion(t *testing.T) {
	checkpoint := NewInitialCheckpoint(42, time.Now().UTC())
	record, err := tcaplusRecordFromCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeTcaplusCheckpointClient{
		get: func(output *tcaplusv1.PlayerCheckpoint, opt *option.PBOpt) error {
			proto.Merge(output, record)
			opt.Version = 7
			return nil
		},
	}
	store := &TcaplusCheckpointStore{client: client, zoneID: 3}

	loaded, err := store.Load(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	version, err := decodeTcaplusVersion(loaded.Token)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State.PlayerID != 42 ||
		loaded.PersistedRevision != checkpoint.CheckpointRevision ||
		version != 7 {
		t.Fatalf("unexpected loaded checkpoint: %+v version=%d", loaded, version)
	}
}

func TestTcaplusCheckpointStoreSaveCASUsesBothVersions(t *testing.T) {
	checkpoint := advancedCheckpoint(t, 42, 1, 2, 1)
	token, err := encodeTcaplusVersion(7)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeTcaplusCheckpointClient{
		update: func(record *tcaplusv1.PlayerCheckpoint, opt *option.PBOpt) error {
			if record.CheckpointRevision != 2 ||
				opt.Version != 7 ||
				opt.VersionPolicy != option.CheckDataVersionAutoIncrease ||
				opt.Condition != "checkpoint_revision = 1" {
				t.Fatalf("unexpected CAS request: record=%+v opt=%+v", record, opt)
			}
			opt.Version = 8
			return nil
		},
	}
	store := &TcaplusCheckpointStore{client: client, zoneID: 3}

	result, err := store.SaveCAS(context.Background(), CheckpointWrite{
		Checkpoint: checkpoint, ExpectedRevision: 1, ExpectedToken: token,
	})
	if err != nil || result.Status != CheckpointWriteApplied {
		t.Fatalf("SaveCAS() result=%+v error=%v", result, err)
	}
	newVersion, err := decodeTcaplusVersion(result.NewToken)
	if err != nil || newVersion != 8 {
		t.Fatalf("new token version=%d error=%v", newVersion, err)
	}
}

func TestTcaplusCheckpointStoreReconcilesDuplicateRetry(t *testing.T) {
	checkpoint := advancedCheckpoint(t, 42, 1, 2, 1)
	record, err := tcaplusRecordFromCheckpoint(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := encodeTcaplusVersion(7)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeTcaplusCheckpointClient{
		update: func(*tcaplusv1.PlayerCheckpoint, *option.PBOpt) error {
			return &terror.ErrorCode{Code: terror.SVR_ERR_FAIL_LOW_VERSION}
		},
		get: func(output *tcaplusv1.PlayerCheckpoint, opt *option.PBOpt) error {
			proto.Merge(output, record)
			opt.Version = 8
			return nil
		},
	}
	store := &TcaplusCheckpointStore{client: client, zoneID: 3}

	result, err := store.SaveCAS(context.Background(), CheckpointWrite{
		Checkpoint: checkpoint, ExpectedRevision: 1, ExpectedToken: oldToken,
	})
	if err != nil || result.Status != CheckpointWriteAlreadyApplied {
		t.Fatalf("SaveCAS() result=%+v error=%v", result, err)
	}
}

func TestTcaplusCheckpointStoreClassifiesFencedWriter(t *testing.T) {
	writeCheckpoint := advancedCheckpoint(t, 42, 1, 2, 1)
	storedCheckpoint := advancedCheckpoint(t, 42, 2, 3, 2)
	storedRecord, err := tcaplusRecordFromCheckpoint(storedCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := encodeTcaplusVersion(7)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeTcaplusCheckpointClient{
		update: func(*tcaplusv1.PlayerCheckpoint, *option.PBOpt) error {
			return errors.New("version conflict")
		},
		get: func(output *tcaplusv1.PlayerCheckpoint, opt *option.PBOpt) error {
			proto.Merge(output, storedRecord)
			opt.Version = 9
			return nil
		},
	}
	store := &TcaplusCheckpointStore{client: client, zoneID: 3}

	result, err := store.SaveCAS(context.Background(), CheckpointWrite{
		Checkpoint: writeCheckpoint, ExpectedRevision: 1, ExpectedToken: oldToken,
	})
	if err != nil || result.Status != CheckpointWriteFenced {
		t.Fatalf("SaveCAS() result=%+v error=%v", result, err)
	}
}

func TestTcaplusCheckpointStoreRejectsMalformedToken(t *testing.T) {
	store := &TcaplusCheckpointStore{
		client: &fakeTcaplusCheckpointClient{},
	}
	result, err := store.SaveCAS(context.Background(), CheckpointWrite{
		Checkpoint:       advancedCheckpoint(t, 42, 1, 2, 1),
		ExpectedRevision: 1,
		ExpectedToken:    StoreToken{1},
	})
	if err == nil || result.Status != CheckpointWriteStaleCopy {
		t.Fatalf("SaveCAS() result=%+v error=%v", result, err)
	}
}

func advancedCheckpoint(
	t *testing.T,
	playerID uint64,
	ownerEpoch uint64,
	revision uint64,
	playerSeq uint64,
) *datav1.PlayerCheckpointV1 {
	t.Helper()
	checkpoint := NewInitialCheckpoint(playerID, time.Now().UTC())
	checkpoint.OwnerEpoch = ownerEpoch
	checkpoint.CheckpointRevision = revision
	checkpoint.PlayerSeq = playerSeq
	checkpoint.UpdatedAtMs++
	return checkpoint
}
