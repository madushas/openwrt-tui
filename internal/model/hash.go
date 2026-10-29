package model

import (
	"crypto/sha256"
	"encoding/hex"
)

func fingerprint(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
