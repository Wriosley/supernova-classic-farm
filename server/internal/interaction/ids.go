package interaction

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
)

// ParseInteractionID parses a canonical lowercase UUID WebSocket request_id
// into its raw 16 bytes: interaction_id is defined as that same request_id
// (see docs/plans/friend_design_plan/03-好友互动Saga详细设计.md §4). This
// duplicates player.parseRequestID's logic rather than importing
// internal/player, keeping this package's only coupling to player-owned
// code the narrow VisitorSteps interface in saga.go.
func ParseInteractionID(requestID string) ([]byte, error) {
	compact := strings.ReplaceAll(requestID, "-", "")
	if len(compact) != 32 {
		return nil, errors.New("request_id must be a UUID")
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil {
		return nil, errors.New("request_id must be a UUID")
	}
	return decoded, nil
}

// RequestDigest computes FriendInteraction.request_digest_sha256: a
// deterministic SHA-256 over the action identity fields. STEAL_FRIEND_CROP
// includes expected crop and farm-view version so a conflict retry with a
// different crop/view is REQUEST_ID_CONFLICT. Pest actions pass crop=0 and
// empty farm_view_epoch.
func RequestDigest(
	action datav1.FriendInteractionAction,
	visitorPlayerID, ownerPlayerID uint64,
	visitID []byte,
	plotID uint32,
	pestID uint32,
	cropItemID uint32,
	farmViewEpoch []byte,
	farmViewSeq uint64,
) []byte {
	body := make([]byte, 0, 4+8+8+len(visitID)+4+4+4+len(farmViewEpoch)+8)
	body = binary.BigEndian.AppendUint32(body, uint32(action))
	body = binary.BigEndian.AppendUint64(body, visitorPlayerID)
	body = binary.BigEndian.AppendUint64(body, ownerPlayerID)
	body = append(body, visitID...)
	body = binary.BigEndian.AppendUint32(body, plotID)
	body = binary.BigEndian.AppendUint32(body, pestID)
	body = binary.BigEndian.AppendUint32(body, cropItemID)
	body = append(body, farmViewEpoch...)
	body = binary.BigEndian.AppendUint64(body, farmViewSeq)
	digest := sha256.Sum256(body)
	return digest[:]
}
