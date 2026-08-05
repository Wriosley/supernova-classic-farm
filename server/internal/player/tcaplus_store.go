package player

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
	tcaplusv1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/tcaplus"
	tcaplus "github.com/tencentyun/tcaplusdb-go-sdk/pb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/terror"
	"google.golang.org/protobuf/proto"
)

const DefaultTcaplusCheckpointTable = "PlayerCheckpoint"

// TcaplusConfig contains only connection coordinates. Signature must be
// injected through the process environment or a Kubernetes Secret.
type TcaplusConfig struct {
	AppID     uint64
	ZoneID    uint32
	DirURL    string
	Signature string
	TableName string
}

func LoadTcaplusConfigFromEnv() (TcaplusConfig, error) {
	appID, err := parseRequiredUintEnv("TCAPLUS_APP_ID", 64)
	if err != nil {
		return TcaplusConfig{}, err
	}
	zoneID, err := parseRequiredUintEnv("TCAPLUS_ZONE_ID", 32)
	if err != nil {
		return TcaplusConfig{}, err
	}
	config := TcaplusConfig{
		AppID:     appID,
		ZoneID:    uint32(zoneID),
		DirURL:    strings.TrimSpace(os.Getenv("TCAPLUS_DIR_URL")),
		Signature: strings.TrimSpace(os.Getenv("TCAPLUS_SIGNATURE")),
		TableName: strings.TrimSpace(os.Getenv("TCAPLUS_CHECKPOINT_TABLE")),
	}
	if config.DirURL == "" {
		return TcaplusConfig{}, errors.New("TCAPLUS_DIR_URL is required")
	}
	if config.Signature == "" {
		return TcaplusConfig{}, errors.New("TCAPLUS_SIGNATURE is required")
	}
	if config.TableName == "" {
		config.TableName = DefaultTcaplusCheckpointTable
	}
	if config.TableName != DefaultTcaplusCheckpointTable {
		return TcaplusConfig{}, fmt.Errorf(
			"TCAPLUS_CHECKPOINT_TABLE must match protobuf message name %q",
			DefaultTcaplusCheckpointTable,
		)
	}
	return config, nil
}

func parseRequiredUintEnv(name string, bitSize int) (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	value, err := strconv.ParseUint(raw, 10, bitSize)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s must be a positive uint%d", name, bitSize)
	}
	return value, nil
}

type tcaplusCheckpointClient interface {
	DoGet(proto.Message, *option.PBOpt, ...uint32) error
	DoInsert(proto.Message, *option.PBOpt, ...uint32) error
	DoUpdate(proto.Message, *option.PBOpt, ...uint32) error
}

// TcaplusCheckpointStore is the PlayerCheckpoint single-record POC adapter.
// It deliberately does not implement accounts, Sessions, ShardFence or Outbox
// records yet and is therefore not wired into Zone startup.
type TcaplusCheckpointStore struct {
	client tcaplusCheckpointClient
	zoneID uint32
	close  func()
}

func NewTcaplusCheckpointStore(config TcaplusConfig) (*TcaplusCheckpointStore, error) {
	if config.AppID == 0 || config.ZoneID == 0 || config.DirURL == "" ||
		config.Signature == "" || config.TableName != DefaultTcaplusCheckpointTable {
		return nil, errors.New("complete Tcaplus checkpoint configuration is required")
	}
	client := tcaplus.NewPBClient()
	if err := client.Dial(
		config.AppID,
		[]uint32{config.ZoneID},
		config.DirURL,
		config.Signature,
		30,
		map[uint32][]string{config.ZoneID: {config.TableName}},
	); err != nil {
		client.Close()
		return nil, fmt.Errorf("dial TcaplusDB: %w", err)
	}
	if err := client.SetDefaultZoneId(config.ZoneID); err != nil {
		client.Close()
		return nil, fmt.Errorf("set TcaplusDB default zone: %w", err)
	}
	return &TcaplusCheckpointStore{
		client: client,
		zoneID: config.ZoneID,
		close:  client.Close,
	}, nil
}

func NewTcaplusCheckpointStoreWithClient(
	client tcaplusCheckpointClient,
	zoneID uint32,
) (*TcaplusCheckpointStore, error) {
	if client == nil || zoneID == 0 {
		return nil, errors.New("Tcaplus checkpoint client and zone are required")
	}
	return &TcaplusCheckpointStore{client: client, zoneID: zoneID}, nil
}

func (s *TcaplusCheckpointStore) Close() {
	if s != nil && s.close != nil {
		s.close()
		s.close = nil
	}
}

func (s *TcaplusCheckpointStore) Load(
	ctx context.Context,
	playerID uint64,
) (LoadedCheckpoint, error) {
	if s == nil || s.client == nil {
		return LoadedCheckpoint{}, errors.New("Tcaplus checkpoint store is not configured")
	}
	record := &tcaplusv1.PlayerCheckpoint{PlayerId: playerID}
	opt := &option.PBOpt{Ctx: ctx}
	if err := s.client.DoGet(record, opt, s.zoneID); err != nil {
		if tcaplusErrorCode(err) == terror.TXHDB_ERR_RECORD_NOT_EXIST {
			return LoadedCheckpoint{}, ErrCheckpointNotFound
		}
		return LoadedCheckpoint{}, fmt.Errorf("get Tcaplus checkpoint: %w", err)
	}
	state, err := stateFromTcaplusRecord(record)
	if err != nil {
		return LoadedCheckpoint{}, err
	}
	token, err := encodeTcaplusVersion(opt.Version)
	if err != nil {
		return LoadedCheckpoint{}, err
	}
	return LoadedCheckpoint{
		State:             state,
		PersistedRevision: record.CheckpointRevision,
		Token:             token,
	}, nil
}

// Create inserts the initial checkpoint only when the player key is absent.
// It is intentionally concrete-store API because activation never creates a
// missing player implicitly.
func (s *TcaplusCheckpointStore) Create(
	ctx context.Context,
	checkpoint *datav1.PlayerCheckpointV1,
) (CheckpointWriteResult, error) {
	if s == nil || s.client == nil {
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
			errors.New("Tcaplus checkpoint store is not configured")
	}
	record, err := tcaplusRecordFromCheckpoint(checkpoint)
	if err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict}, err
	}
	opt := &option.PBOpt{
		Ctx:        ctx,
		ResultFlag: option.TcaplusResultFlagAllNewValue,
	}
	if err := s.client.DoInsert(record, opt, s.zoneID); err != nil {
		loaded, loadErr := s.Load(ctx, checkpoint.PlayerId)
		if loadErr == nil {
			same, compareErr := loadedMatchesCheckpoint(loaded, checkpoint)
			if compareErr != nil {
				return CheckpointWriteResult{
					Status: CheckpointWriteCorruptConflict,
				}, compareErr
			}
			if same {
				return CheckpointWriteResult{
					Status:   CheckpointWriteAlreadyApplied,
					NewToken: cloneStoreToken(loaded.Token),
				}, nil
			}
		}
		if tcaplusErrorCode(err) == terror.SVR_ERR_FAIL_RECORD_EXIST {
			return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict}, nil
		}
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
			fmt.Errorf("insert Tcaplus checkpoint: %w", err)
	}
	token, err := encodeTcaplusVersion(opt.Version)
	if err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure}, err
	}
	return CheckpointWriteResult{
		Status: CheckpointWriteApplied, NewToken: token,
	}, nil
}

func (s *TcaplusCheckpointStore) SaveCAS(
	ctx context.Context,
	write CheckpointWrite,
) (CheckpointWriteResult, error) {
	if s == nil || s.client == nil {
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
			errors.New("Tcaplus checkpoint store is not configured")
	}
	version, err := decodeTcaplusVersion(write.ExpectedToken)
	if err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteStaleCopy}, err
	}
	if write.Checkpoint == nil ||
		write.Checkpoint.CheckpointRevision <= write.ExpectedRevision {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict},
			errors.New("checkpoint revision must advance")
	}
	record, err := tcaplusRecordFromCheckpoint(write.Checkpoint)
	if err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict}, err
	}
	opt := &option.PBOpt{
		Ctx:           ctx,
		VersionPolicy: option.CheckDataVersionAutoIncrease,
		Version:       version,
		Condition: fmt.Sprintf(
			"checkpoint_revision = %d",
			write.ExpectedRevision,
		),
		ResultFlag: option.TcaplusResultFlagAllNewValue,
	}
	if updateErr := s.client.DoUpdate(record, opt, s.zoneID); updateErr != nil {
		return s.reconcileFailedCAS(ctx, write, updateErr)
	}
	token, err := encodeTcaplusVersion(opt.Version)
	if err != nil {
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure}, err
	}
	return CheckpointWriteResult{
		Status: CheckpointWriteApplied, NewToken: token,
	}, nil
}

func (s *TcaplusCheckpointStore) reconcileFailedCAS(
	ctx context.Context,
	write CheckpointWrite,
	updateErr error,
) (CheckpointWriteResult, error) {
	loaded, loadErr := s.Load(ctx, write.Checkpoint.PlayerId)
	if loadErr != nil {
		return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
			fmt.Errorf("update Tcaplus checkpoint: %w", updateErr)
	}
	same, compareErr := loadedMatchesCheckpoint(loaded, write.Checkpoint)
	if compareErr != nil {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict},
			compareErr
	}
	if same {
		return CheckpointWriteResult{
			Status:   CheckpointWriteAlreadyApplied,
			NewToken: cloneStoreToken(loaded.Token),
		}, nil
	}
	if loaded.State.OwnerEpoch > write.Checkpoint.OwnerEpoch {
		return CheckpointWriteResult{Status: CheckpointWriteFenced}, nil
	}
	if loaded.PersistedRevision == write.Checkpoint.CheckpointRevision {
		return CheckpointWriteResult{Status: CheckpointWriteCorruptConflict}, nil
	}
	if loaded.PersistedRevision != write.ExpectedRevision ||
		!bytes.Equal(loaded.Token, write.ExpectedToken) {
		return CheckpointWriteResult{Status: CheckpointWriteStaleCopy}, nil
	}
	return CheckpointWriteResult{Status: CheckpointWriteRetryableFailure},
		fmt.Errorf("update Tcaplus checkpoint: %w", updateErr)
}

func tcaplusRecordFromCheckpoint(
	checkpoint *datav1.PlayerCheckpointV1,
) (*tcaplusv1.PlayerCheckpoint, error) {
	if checkpoint == nil {
		return nil, errors.New("checkpoint is required")
	}
	blob, digest, err := MarshalCheckpoint(checkpoint)
	if err != nil {
		return nil, err
	}
	return &tcaplusv1.PlayerCheckpoint{
		PlayerId:                 checkpoint.PlayerId,
		LogicalShardId:           checkpoint.LogicalShardId,
		OwnerEpoch:               checkpoint.OwnerEpoch,
		PlayerSeq:                checkpoint.PlayerSeq,
		CheckpointRevision:       checkpoint.CheckpointRevision,
		CheckpointSchemaVersion:  checkpoint.SchemaVersion,
		CheckpointBlob:           blob,
		CheckpointSha256:         append([]byte(nil), digest[:]...),
		LastAppliedConfigVersion: checkpoint.LastAppliedConfigVersion,
		CreatedAtMs:              checkpoint.CreatedAtMs,
		UpdatedAtMs:              checkpoint.UpdatedAtMs,
	}, nil
}

func stateFromTcaplusRecord(record *tcaplusv1.PlayerCheckpoint) (*State, error) {
	if record == nil {
		return nil, errors.New("Tcaplus checkpoint record is required")
	}
	checkpoint, err := UnmarshalCheckpoint(
		record.CheckpointBlob,
		record.CheckpointSha256,
	)
	if err != nil {
		return nil, err
	}
	if checkpoint.PlayerId != record.PlayerId ||
		checkpoint.LogicalShardId != record.LogicalShardId ||
		checkpoint.OwnerEpoch != record.OwnerEpoch ||
		checkpoint.PlayerSeq != record.PlayerSeq ||
		checkpoint.CheckpointRevision != record.CheckpointRevision ||
		checkpoint.SchemaVersion != record.CheckpointSchemaVersion ||
		checkpoint.LastAppliedConfigVersion != record.LastAppliedConfigVersion ||
		checkpoint.CreatedAtMs != record.CreatedAtMs ||
		checkpoint.UpdatedAtMs != record.UpdatedAtMs {
		return nil, errors.New("Tcaplus checkpoint envelope does not match blob")
	}
	return StateFromCheckpoint(checkpoint)
}

func loadedMatchesCheckpoint(
	loaded LoadedCheckpoint,
	expected *datav1.PlayerCheckpointV1,
) (bool, error) {
	if loaded.State == nil || expected == nil {
		return false, nil
	}
	actual, err := loaded.State.Checkpoint()
	if err != nil {
		return false, err
	}
	actualBlob, actualDigest, err := MarshalCheckpoint(actual)
	if err != nil {
		return false, err
	}
	expectedBlob, expectedDigest, err := MarshalCheckpoint(expected)
	if err != nil {
		return false, err
	}
	return bytes.Equal(actualBlob, expectedBlob) &&
		bytes.Equal(actualDigest[:], expectedDigest[:]), nil
}

func encodeTcaplusVersion(version int32) (StoreToken, error) {
	if version <= 0 {
		return nil, errors.New("TcaplusDB returned a non-positive record version")
	}
	token := make(StoreToken, 4)
	binary.BigEndian.PutUint32(token, uint32(version))
	return token, nil
}

func decodeTcaplusVersion(token StoreToken) (int32, error) {
	if len(token) != 4 {
		return 0, errors.New("Tcaplus StoreToken must contain one int32 version")
	}
	version := binary.BigEndian.Uint32(token)
	if version == 0 || version > math.MaxInt32 {
		return 0, errors.New("Tcaplus StoreToken contains an invalid version")
	}
	return int32(version), nil
}

func tcaplusErrorCode(err error) int {
	var code *terror.ErrorCode
	if errors.As(err, &code) && code != nil {
		return code.Code
	}
	return 0
}
