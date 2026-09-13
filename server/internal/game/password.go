package game

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
)

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)
var passwordPattern = regexp.MustCompile(`^[A-Za-z0-9]{6,20}$`)
var requestPattern = regexp.MustCompile(`^[a-zA-Z0-9:_-]{8,64}$`)

func validateCredentials(username, password string) error {
	if !usernamePattern.MatchString(username) || !passwordPattern.MatchString(password) {
		return ErrInvalid
	}
	return nil
}

// encodePassword 是可逆的课堂演示编码，不用于真实系统。
func encodePassword(password string) string {
	// 字母和数字分别循环右移 3 位，其他字符不会通过注册校验。
	b := []byte(password)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			b[i] = 'a' + (c-'a'+3)%26
		case c >= 'A' && c <= 'Z':
			b[i] = 'A' + (c-'A'+3)%26
		case c >= '0' && c <= '9':
			b[i] = '0' + (c-'0'+3)%10
		}
	}
	return string(b)
}

func randomHex(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
