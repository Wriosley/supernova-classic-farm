package zoneidentity

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Wriosley/supernova-classic-farm/server/internal/platform/internalnet"
)

const identityDomain = "classic-farm/zone"

var dnsNamespace = [16]byte{0x6b, 0xa7, 0xb8, 0x10, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}

type Config struct {
	ClusterID       string
	Namespace       string
	StatefulSetName string
	PodName         string
	LogicalOverride string
	Endpoint        string
}

type Identity struct {
	LogicalZoneID string `json:"logical_zone_id"`
	IncarnationID string `json:"incarnation_id"`
	Endpoint      string `json:"endpoint"`
}

func New(cfg Config) (Identity, error) {
	endpoint, err := normalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return Identity{}, err
	}
	logicalID := strings.TrimSpace(cfg.LogicalOverride)
	if logicalID == "" {
		ordinal, parseErr := ParseOrdinal(cfg.PodName, cfg.StatefulSetName)
		if parseErr != nil {
			return Identity{}, parseErr
		}
		logicalID, err = DeriveLogicalID(cfg.ClusterID, cfg.Namespace, cfg.StatefulSetName, ordinal)
		if err != nil {
			return Identity{}, err
		}
	} else if logicalID != "zone-a" && logicalID != "zone-b" && logicalID != "zone-local" {
		return Identity{}, errors.New("logical override is not a compatibility Zone")
	}
	incarnation, err := randomUUID()
	if err != nil {
		return Identity{}, fmt.Errorf("create Zone incarnation: %w", err)
	}
	return Identity{LogicalZoneID: logicalID, IncarnationID: incarnation, Endpoint: endpoint}, nil
}

func DeriveLogicalID(clusterID, namespace, statefulSetName string, ordinal int) (string, error) {
	clusterID = strings.TrimSpace(clusterID)
	namespace = strings.TrimSpace(namespace)
	statefulSetName = strings.TrimSpace(statefulSetName)
	if clusterID == "" || namespace == "" || statefulSetName == "" || ordinal < 0 {
		return "", errors.New("complete Zone topology and non-negative ordinal are required")
	}
	name := strings.Join([]string{identityDomain, clusterID, namespace, statefulSetName, strconv.Itoa(ordinal)}, "/")
	hash := sha1.New()
	_, _ = hash.Write(dnsNamespace[:])
	_, _ = hash.Write([]byte(name))
	var id [16]byte
	copy(id[:], hash.Sum(nil))
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return formatUUID(id), nil
}

func ParseOrdinal(podName, statefulSetName string) (int, error) {
	podName = strings.TrimSpace(podName)
	statefulSetName = strings.TrimSpace(statefulSetName)
	prefix := statefulSetName + "-"
	if statefulSetName == "" || !strings.HasPrefix(podName, prefix) {
		return 0, errors.New("Pod name does not match StatefulSet")
	}
	suffix := strings.TrimPrefix(podName, prefix)
	if suffix == "" || strings.HasPrefix(suffix, "+") || strings.HasPrefix(suffix, "-") {
		return 0, errors.New("Pod ordinal is invalid")
	}
	ordinal, err := strconv.Atoi(suffix)
	if err != nil || ordinal < 0 || strconv.Itoa(ordinal) != suffix {
		return 0, errors.New("Pod ordinal is invalid")
	}
	return ordinal, nil
}

func normalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if err := internalnet.ValidateHTTPURL(raw); err != nil {
		return "", fmt.Errorf("Zone endpoint: %w", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("Zone endpoint must contain only scheme and authority")
	}
	parsed.Path = ""
	return parsed.String(), nil
}

func randomUUID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return formatUUID(id), nil
}

func formatUUID(id [16]byte) string {
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], id[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], id[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], id[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], id[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], id[10:16])
	return string(encoded)
}
