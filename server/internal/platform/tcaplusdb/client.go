package tcaplusdb

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	tcaplus "github.com/tencentyun/tcaplusdb-go-sdk/pb"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/protocol/option"
	"github.com/tencentyun/tcaplusdb-go-sdk/pb/terror"
	"google.golang.org/protobuf/proto"
)

type Config struct {
	AppID     uint64
	ZoneID    uint32
	DirURL    string
	Signature string
}

func LoadConfigFromEnv() (Config, error) {
	appID, err := requiredUint("TCAPLUS_APP_ID", 64)
	if err != nil {
		return Config{}, err
	}
	zoneID, err := requiredUint("TCAPLUS_ZONE_ID", 32)
	if err != nil {
		return Config{}, err
	}
	config := Config{
		AppID:     appID,
		ZoneID:    uint32(zoneID),
		DirURL:    strings.TrimSpace(os.Getenv("TCAPLUS_DIR_URL")),
		Signature: strings.TrimSpace(os.Getenv("TCAPLUS_SIGNATURE")),
	}
	if config.DirURL == "" {
		return Config{}, errors.New("TCAPLUS_DIR_URL is required")
	}
	if config.Signature == "" {
		return Config{}, errors.New("TCAPLUS_SIGNATURE is required")
	}
	return config, nil
}

func TableName(envName, defaultName string) (string, error) {
	value := strings.TrimSpace(os.Getenv(envName))
	if value == "" {
		value = defaultName
	}
	if value != defaultName {
		return "", fmt.Errorf("%s must match protobuf message name %q", envName, defaultName)
	}
	return value, nil
}

func requiredUint(name string, bitSize int) (uint64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	value, err := strconv.ParseUint(raw, 10, bitSize)
	if raw == "" || err != nil || value == 0 {
		return 0, fmt.Errorf("%s must be a positive uint%d", name, bitSize)
	}
	return value, nil
}

type Client struct {
	raw    *tcaplus.PBClient
	zoneID uint32
}

func Open(config Config, tableNames ...string) (*Client, error) {
	if config.AppID == 0 || config.ZoneID == 0 ||
		config.DirURL == "" || config.Signature == "" {
		return nil, errors.New("complete Tcaplus configuration is required")
	}
	tables := append([]string(nil), tableNames...)
	sort.Strings(tables)
	tables = compactStrings(tables)
	if len(tables) == 0 || tables[0] == "" {
		return nil, errors.New("at least one Tcaplus table is required")
	}
	raw := tcaplus.NewPBClient()
	if err := raw.Dial(
		config.AppID,
		[]uint32{config.ZoneID},
		config.DirURL,
		config.Signature,
		30,
		map[uint32][]string{config.ZoneID: tables},
	); err != nil {
		raw.Close()
		return nil, fmt.Errorf("dial TcaplusDB: %w", err)
	}
	if err := raw.SetDefaultZoneId(config.ZoneID); err != nil {
		raw.Close()
		return nil, fmt.Errorf("set TcaplusDB default zone: %w", err)
	}
	return &Client{raw: raw, zoneID: config.ZoneID}, nil
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func (c *Client) Close() {
	if c != nil && c.raw != nil {
		c.raw.Close()
		c.raw = nil
	}
}

func (c *Client) DoGet(message proto.Message, opt *option.PBOpt, _ ...uint32) error {
	return c.raw.DoGet(message, opt, c.zoneID)
}

func (c *Client) DoInsert(message proto.Message, opt *option.PBOpt, _ ...uint32) error {
	return c.raw.DoInsert(message, opt, c.zoneID)
}

func (c *Client) DoUpdate(message proto.Message, opt *option.PBOpt, _ ...uint32) error {
	return c.raw.DoUpdate(message, opt, c.zoneID)
}

func (c *Client) DoDelete(message proto.Message, opt *option.PBOpt, _ ...uint32) error {
	return c.raw.DoDelete(message, opt, c.zoneID)
}

func (c *Client) Traverse(message proto.Message) ([]proto.Message, error) {
	return c.raw.TraverseWithZone(message, c.zoneID)
}

func (c *Client) Ping(ctx context.Context, message proto.Message) error {
	opt := &option.PBOpt{Ctx: ctx}
	err := c.DoGet(message, opt)
	if IsNotFound(err) {
		return nil
	}
	return err
}

func IsNotFound(err error) bool {
	return ErrorCode(err) == terror.TXHDB_ERR_RECORD_NOT_EXIST
}

func IsAlreadyExists(err error) bool {
	return ErrorCode(err) == terror.SVR_ERR_FAIL_RECORD_EXIST
}

func ErrorCode(err error) int {
	var code *terror.ErrorCode
	if errors.As(err, &code) && code != nil {
		return code.Code
	}
	return 0
}

func EncodeVersion(version int32) ([]byte, error) {
	if version <= 0 {
		return nil, errors.New("TcaplusDB returned a non-positive record version")
	}
	token := make([]byte, 4)
	binary.BigEndian.PutUint32(token, uint32(version))
	return token, nil
}

func DecodeVersion(token []byte) (int32, error) {
	if len(token) != 4 {
		return 0, errors.New("Tcaplus version token must contain one int32")
	}
	version := binary.BigEndian.Uint32(token)
	if version == 0 || version > math.MaxInt32 {
		return 0, errors.New("Tcaplus version token is invalid")
	}
	return int32(version), nil
}
