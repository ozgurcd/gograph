package workspace

import (
	"crypto/sha256"
	"sync"
)

// Cache only positive deterministic-verification receipts, never member graphs
// or freshness decisions. LoadWithBuildTags must validate current source paths,
// module ownership, build context, and exact artifact bytes before consulting it.
// A fixed-size ring bounds retained memory independently of repository count.
type overlayVerificationCache struct {
	mu    sync.Mutex
	keys  [16][sha256.Size]byte
	count int
	next  int
}

var verifiedOverlays overlayVerificationCache

func overlayVerificationKey(root, input, artifact string) [sha256.Size]byte {
	return sha256.Sum256([]byte(root + "\x00" + input + "\x00" + artifact))
}

func (c *overlayVerificationCache) contains(key [sha256.Size]byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := 0; i < c.count; i++ {
		if c.keys[i] == key {
			return true
		}
	}
	return false
}

func (c *overlayVerificationCache) remember(key [sha256.Size]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := 0; i < c.count; i++ {
		if c.keys[i] == key {
			return
		}
	}
	c.keys[c.next] = key
	c.next = (c.next + 1) % len(c.keys)
	if c.count < len(c.keys) {
		c.count++
	}
}
