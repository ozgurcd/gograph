package graph

import (
	"crypto/sha256"
	"encoding/hex"
)

// SourceDigest returns the stable lowercase SHA-256 digest used for source
// freshness and incremental parser-result reuse.
func SourceDigest(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}
