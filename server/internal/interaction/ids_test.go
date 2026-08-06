package interaction

import (
	"bytes"
	"testing"

	datav1 "github.com/Wriosley/supernova-classic-farm/server/gen/classicfarm/v1/data"
)

func TestParseInteractionIDAcceptsCanonicalUUID(t *testing.T) {
	id, err := ParseInteractionID("00112233-4455-6677-8899-aabbccddeeff")
	if err != nil {
		t.Fatalf("ParseInteractionID: %v", err)
	}
	if len(id) != 16 {
		t.Fatalf("expected 16 bytes, got %d", len(id))
	}
	expected := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	if !bytes.Equal(id, expected) {
		t.Fatalf("unexpected bytes: %x", id)
	}
}

func TestParseInteractionIDRejectsMalformed(t *testing.T) {
	for _, requestID := range []string{"", "not-a-uuid", "00112233445566778899aabbccddee", "00112233-4455-6677-8899"} {
		if _, err := ParseInteractionID(requestID); err == nil {
			t.Fatalf("expected an error for %q", requestID)
		}
	}
}

func TestRequestDigestIsDeterministicAndSensitiveToEveryField(t *testing.T) {
	base := RequestDigest(datav1.FriendInteractionAction_STEAL_FRIEND_CROP, 1, 2, fixedVisitID(0xAA), 3, 0)
	again := RequestDigest(datav1.FriendInteractionAction_STEAL_FRIEND_CROP, 1, 2, fixedVisitID(0xAA), 3, 0)
	if !bytes.Equal(base, again) {
		t.Fatalf("expected identical inputs to produce identical digests")
	}

	variants := [][]byte{
		RequestDigest(datav1.FriendInteractionAction_STEAL_FRIEND_CROP, 9, 2, fixedVisitID(0xAA), 3, 0),
		RequestDigest(datav1.FriendInteractionAction_STEAL_FRIEND_CROP, 1, 9, fixedVisitID(0xAA), 3, 0),
		RequestDigest(datav1.FriendInteractionAction_STEAL_FRIEND_CROP, 1, 2, fixedVisitID(0xBB), 3, 0),
		RequestDigest(datav1.FriendInteractionAction_STEAL_FRIEND_CROP, 1, 2, fixedVisitID(0xAA), 9, 0),
		RequestDigest(datav1.FriendInteractionAction_STEAL_FRIEND_CROP, 1, 2, fixedVisitID(0xAA), 3, 9),
	}
	for i, variant := range variants {
		if bytes.Equal(base, variant) {
			t.Fatalf("variant %d unexpectedly matched the base digest", i)
		}
	}
}
