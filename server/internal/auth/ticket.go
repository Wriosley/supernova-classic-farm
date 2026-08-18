package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ticketVersionByte      byte = 1
	ticketPrefix                = "cfwt1."
	ticketLifetimeDuration      = 30 * time.Second
	maxTicketIssueIDLen         = 64
	maxTicketGatewayLen         = 64
)

var errMalformedTicket = errors.New("malformed ticket")

type ticketClaims struct {
	PlayerID      uint64
	SessionDigest [32]byte
	Generation    uint64
	ExpiresAt     time.Time
	GatewayID     string
	IssueID       string
}

func encodeTicket(key []byte, claims ticketClaims) (string, error) {
	if len(key) < 32 {
		return "", errors.New("ticket HMAC key must be at least 32 bytes")
	}
	if claims.PlayerID == 0 || claims.Generation == 0 || claims.ExpiresAt.IsZero() {
		return "", errors.New("ticket claims are incomplete")
	}
	if claims.GatewayID == "" || claims.IssueID == "" {
		return "", errors.New("ticket claims are incomplete")
	}
	if len(claims.GatewayID) > maxTicketGatewayLen || len(claims.IssueID) > maxTicketIssueIDLen {
		return "", errors.New("ticket claim field too long")
	}
	payload := make([]byte, 0, 1+8+32+8+8+2+len(claims.GatewayID)+2+len(claims.IssueID))
	payload = append(payload, ticketVersionByte)
	payload = binary.BigEndian.AppendUint64(payload, claims.PlayerID)
	payload = append(payload, claims.SessionDigest[:]...)
	payload = binary.BigEndian.AppendUint64(payload, claims.Generation)
	payload = binary.BigEndian.AppendUint64(payload, uint64(claims.ExpiresAt.UTC().Unix()))
	payload = appendU16String(payload, claims.GatewayID)
	payload = appendU16String(payload, claims.IssueID)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return ticketPrefix +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeAndVerifyTicket(key []byte, raw string, now time.Time) (ticketClaims, error) {
	if len(key) < 32 {
		return ticketClaims{}, errors.New("ticket HMAC key must be at least 32 bytes")
	}
	if !strings.HasPrefix(raw, ticketPrefix) {
		return ticketClaims{}, errMalformedTicket
	}
	rest := strings.TrimPrefix(raw, ticketPrefix)
	payloadB64, macB64, ok := strings.Cut(rest, ".")
	if !ok || payloadB64 == "" || macB64 == "" || strings.Contains(macB64, ".") {
		return ticketClaims{}, errMalformedTicket
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return ticketClaims{}, errMalformedTicket
	}
	macSum, err := base64.RawURLEncoding.DecodeString(macB64)
	if err != nil || len(macSum) != sha256.Size {
		return ticketClaims{}, errMalformedTicket
	}
	expected := hmac.New(sha256.New, key)
	_, _ = expected.Write(payload)
	if subtle.ConstantTimeCompare(expected.Sum(nil), macSum) != 1 {
		return ticketClaims{}, ErrUnauthenticated
	}
	claims, err := parseTicketPayload(payload)
	if err != nil {
		return ticketClaims{}, err
	}
	if !now.Before(claims.ExpiresAt) {
		return ticketClaims{}, ErrUnauthenticated
	}
	return claims, nil
}

func parseTicketPayload(payload []byte) (ticketClaims, error) {
	if len(payload) < 1+8+32+8+8+2+2 {
		return ticketClaims{}, errMalformedTicket
	}
	if payload[0] != ticketVersionByte {
		return ticketClaims{}, errMalformedTicket
	}
	offset := 1
	var claims ticketClaims
	claims.PlayerID = binary.BigEndian.Uint64(payload[offset : offset+8])
	offset += 8
	copy(claims.SessionDigest[:], payload[offset:offset+32])
	offset += 32
	claims.Generation = binary.BigEndian.Uint64(payload[offset : offset+8])
	offset += 8
	claims.ExpiresAt = time.Unix(int64(binary.BigEndian.Uint64(payload[offset:offset+8])), 0).UTC()
	offset += 8
	gateway, offset, err := readU16String(payload, offset)
	if err != nil {
		return ticketClaims{}, err
	}
	issueID, offset, err := readU16String(payload, offset)
	if err != nil {
		return ticketClaims{}, err
	}
	if offset != len(payload) || claims.PlayerID == 0 || claims.Generation == 0 || gateway == "" || issueID == "" {
		return ticketClaims{}, errMalformedTicket
	}
	claims.GatewayID = gateway
	claims.IssueID = issueID
	return claims, nil
}

func appendU16String(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(value)))
	return append(dst, value...)
}

func readU16String(src []byte, offset int) (string, int, error) {
	if offset+2 > len(src) {
		return "", 0, errMalformedTicket
	}
	n := int(binary.BigEndian.Uint16(src[offset : offset+2]))
	offset += 2
	if n < 0 || offset+n > len(src) {
		return "", 0, errMalformedTicket
	}
	return string(src[offset : offset+n]), offset + n, nil
}

func normalizeTicketHMACKey(raw []byte) ([32]byte, error) {
	var key [32]byte
	if len(raw) < 32 {
		return key, fmt.Errorf("ticket HMAC key must be at least 32 bytes, got %d", len(raw))
	}
	// Prefer SHA-256 of the configured secret so hex/ascii keys of any length
	// collapse to a fixed 32-byte HMAC key shared by every Login replica.
	sum := sha256.Sum256(raw)
	copy(key[:], sum[:])
	return key, nil
}
