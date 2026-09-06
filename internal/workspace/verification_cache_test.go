package workspace

import (
	"fmt"
	"testing"
)

func TestOverlayVerificationCacheIsBoundedAndBindsAllInputs(t *testing.T) {
	var cache overlayVerificationCache
	key := overlayVerificationKey("root", "input", "artifact")
	cache.remember(key)
	if !cache.contains(key) {
		t.Fatal("receipt was not retained")
	}
	for _, changed := range [][3]string{{"other", "input", "artifact"}, {"root", "other", "artifact"}, {"root", "input", "other"}} {
		if cache.contains(overlayVerificationKey(changed[0], changed[1], changed[2])) {
			t.Fatal("receipt ignored an identity dimension")
		}
	}
	for i := 0; i < len(cache.keys); i++ {
		cache.remember(overlayVerificationKey(fmt.Sprint(i), "input", "artifact"))
	}
	if cache.count != len(cache.keys) || cache.contains(key) {
		t.Fatal("receipt cache did not evict within its fixed bound")
	}
}
