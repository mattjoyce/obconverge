package hashing

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// ContentHash is the hex-encoded SHA-256 of some bytes. A value, not a place.
type ContentHash string

// OfFile streams the file's bytes through SHA-256.
func OfFile(path string) (ContentHash, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return ContentHash(hex.EncodeToString(h.Sum(nil))), nil
}

// OfBytes hashes a byte slice.
func OfBytes(b []byte) ContentHash {
	sum := sha256.Sum256(b)
	return ContentHash(hex.EncodeToString(sum[:]))
}
