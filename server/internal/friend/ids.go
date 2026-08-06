// Package friend implements FriendSvr: share codes, the bidirectional friend
// link Saga, the friend list projection and mutual-friend checks.
package friend

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// shareCodeAlphabet excludes visually ambiguous characters (0/O, 1/I).
const shareCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

const shareCodeLength = 8

// linkIDDomain is a fixed, NUL-terminated domain separator so a share code and
// a redeemer player ID can never collide with another purpose's SHA-256 input.
const linkIDDomain = "cf-friend-link\x00"

// sortedPlayerIDs returns the pair in ascending order, matching the
// FriendRelation primary key convention (player_low_id, player_high_id).
func sortedPlayerIDs(a, b uint64) (low, high uint64) {
	if a < b {
		return a, b
	}
	return b, a
}

// normalizeCode trims surrounding whitespace and upper-cases a share code so
// redemption is forgiving of client casing or copy/paste whitespace.
func normalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// linkID derives the deterministic Saga key for one (code, redeemer) pair:
// SHA-256("cf-friend-link\0" + code + "\0" + little-endian redeemer uint64).
func linkID(code string, redeemer uint64) []byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(linkIDDomain))
	_, _ = hash.Write([]byte(code))
	_, _ = hash.Write([]byte{0})
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], redeemer)
	_, _ = hash.Write(encoded[:])
	return hash.Sum(nil)
}

// newShareCode generates one random 8-character code from shareCodeAlphabet.
func newShareCode() (string, error) {
	raw := make([]byte, shareCodeLength)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := make([]byte, shareCodeLength)
	for i, b := range raw {
		code[i] = shareCodeAlphabet[int(b)%len(shareCodeAlphabet)]
	}
	return string(code), nil
}

// newRelationID mints one random 16-byte relation identity, generated once
// when a FriendLinkSaga first reserves both sides and stored on the Saga so
// every later step is idempotent.
func newRelationID() ([]byte, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	return id, nil
}
