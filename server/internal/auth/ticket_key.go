package auth

import (
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

const (
	ticketHMACKeyEnv   = "LOGIN_TICKET_HMAC_KEY"
	internalGRPCKeyEnv = "INTERNAL_GRPC_HMAC_KEY"
)

// ApplyTicketHMACKeyFromEnv configures a shared ticket key.
// Preference: LOGIN_TICKET_HMAC_KEY, else INTERNAL_GRPC_HMAC_KEY.
// Values may be raw secret bytes or hex-encoded.
func ApplyTicketHMACKeyFromEnv(store *Store) error {
	if store == nil {
		return errors.New("auth store is required")
	}
	raw := strings.TrimSpace(os.Getenv(ticketHMACKeyEnv))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(internalGRPCKeyEnv))
	}
	if raw == "" {
		if strings.TrimSpace(os.Getenv("APP_ENV")) == "production" {
			return errors.New("LOGIN_TICKET_HMAC_KEY or INTERNAL_GRPC_HMAC_KEY is required in production")
		}
		// Development keeps the per-process random key from NewStore.
		return nil
	}
	key, err := decodeTicketKeyMaterial(raw)
	if err != nil {
		return err
	}
	return store.ConfigureTicketHMACKey(key)
}

func decodeTicketKeyMaterial(raw string) ([]byte, error) {
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) >= 32 {
		return decoded, nil
	}
	if len(raw) < 32 {
		return nil, errors.New("ticket HMAC key material must be at least 32 bytes (or 64 hex chars)")
	}
	return []byte(raw), nil
}
