package memorylimit

import (
	"math"
	"runtime/debug"
	"testing"
)

func TestParseSize(t *testing.T) {
	tests := map[string]int64{
		"1GiB":   1 << 30,
		"1Gb":    1_000_000_000,
		"512MiB": 512 << 20,
		"64mb":   64_000_000,
		"4096":   4096,
	}
	for input, want := range tests {
		got, err := ParseSize(input)
		if err != nil {
			t.Fatalf("ParseSize(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", input, got, want)
		}
	}
	for _, input := range []string{"", "0", "1.5GiB", "GiB", "1XB", "999999999999999999999GiB"} {
		if _, err := ParseSize(input); err == nil {
			t.Errorf("ParseSize(%q) unexpectedly succeeded", input)
		}
	}
}

func TestPolicyValidation(t *testing.T) {
	if err := (Policy{Mode: ModeLow, MaxBytes: 1 << 30}).Validate(); err != nil {
		t.Fatalf("valid low-memory policy: %v", err)
	}
	if err := (Policy{Mode: ModeStandard, MaxBytes: 1 << 30}).Validate(); err == nil {
		t.Fatal("standard mode unexpectedly accepted --max-memory")
	}
	if mode, err := ParseMode("normal"); err != nil || mode != ModeStandard {
		t.Fatalf("ParseMode(normal) = %q, %v", mode, err)
	}
}

func TestApplyPreservesStricterExistingLimitAndRestores(t *testing.T) {
	const existing = int64(256 << 20)
	prior := debug.SetMemoryLimit(existing)
	t.Cleanup(func() { debug.SetMemoryLimit(prior) })

	controller, err := Apply(Policy{Mode: ModeLow, MaxBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if got := controller.EffectiveLimit(); got != existing {
		t.Fatalf("effective limit = %d, want preserved %d", got, existing)
	}
	controller.Restore()
	if got := debug.SetMemoryLimit(math.MaxInt64); got != existing {
		t.Fatalf("restored limit = %d, want %d", got, existing)
	}
	debug.SetMemoryLimit(existing)
}
