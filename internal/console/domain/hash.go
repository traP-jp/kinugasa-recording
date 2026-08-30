package domain

import (
	"encoding/base64"
	"encoding/hex"
)

// ContentHash stores the raw bytes of a SHA-256 digest. API and lock-file
// encodings are derived at their respective boundaries.
type ContentHash [32]byte

func ContentHashFromBytes(value []byte) (ContentHash, error) {
	if len(value) != 32 {
		return ContentHash{}, invalid("hash", "must contain exactly 32 bytes")
	}
	var hash ContentHash
	copy(hash[:], value)
	return hash, nil
}

func (h ContentHash) Base64() string {
	return base64.StdEncoding.EncodeToString(h[:])
}

func (h ContentHash) Hex() string {
	return hex.EncodeToString(h[:])
}
