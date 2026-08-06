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
// deterministic SHA-256 over the action, visitor/owner player IDs, visit ID
// and plot ID (docs/contracts/data-model.md §18). pestID is always 0 for
// every action Phase 5 implements (STEAL_FRIEND_CROP carries no pest);
// Phase 6's pest-carrying actions will pass their real pest_id. A retry
// whose digest differs from the stored one is a REQUEST_ID_CONFLICT.
func RequestDigest(
	action datav1.FriendInteractionAction,
	visitorPlayerID, ownerPlayerID uint64,
	visitID []byte,
	plotID uint32,
	pestID uint32,
) []byte {
	body := make([]byte, 0, 4+8+8+len(visitID)+4+4)
	body = binary.BigEndian.AppendUint32(body, uint32(action))
	body = binary.BigEndian.AppendUint64(body, visitorPlayerID)
	body = binary.BigEndian.AppendUint64(body, ownerPlayerID)
	body = append(body, visitID...)
	body = binary.BigEndian.AppendUint32(body, plotID)
	body = binary.BigEndian.AppendUint32(body, pestID)
	digest := sha256.Sum256(body)
	return digest[:]
}
