package identity

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// New returns a lexicographically time-ordered 128-bit identifier.
func New() (string, error) {
	var value [16]byte
	millis := uint64(time.Now().UTC().UnixMilli())
	value[0] = byte(millis >> 40)
	value[1] = byte(millis >> 32)
	value[2] = byte(millis >> 24)
	value[3] = byte(millis >> 16)
	value[4] = byte(millis >> 8)
	value[5] = byte(millis)
	if _, err := rand.Read(value[6:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
