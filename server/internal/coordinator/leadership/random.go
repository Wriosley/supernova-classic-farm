package leadership

import "crypto/rand"

func randomRead(buf []byte) (int, error) {
	return rand.Read(buf)
}
