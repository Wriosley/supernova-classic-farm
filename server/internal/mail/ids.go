package mail

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func newMailID(now time.Time) (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("m%d%s", now.UnixMilli(), hex.EncodeToString(raw[:])), nil
}

func encodePageToken(createdAtMS int64, mailID string) string {
	return strconv.FormatInt(createdAtMS, 10) + "|" + mailID
}

func decodePageToken(token string) (createdAtMS int64, mailID string, ok bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, "", true
	}
	createdRaw, mailID, found := strings.Cut(token, "|")
	if !found || mailID == "" {
		return 0, "", false
	}
	createdAtMS, err := strconv.ParseInt(createdRaw, 10, 64)
	if err != nil {
		return 0, "", false
	}
	return createdAtMS, mailID, true
}

func afterPageToken(createdAtMS int64, mailID string, tokenCreated int64, tokenMailID string) bool {
	if tokenMailID == "" {
		return true
	}
	if createdAtMS != tokenCreated {
		return createdAtMS < tokenCreated
	}
	return mailID < tokenMailID
}
