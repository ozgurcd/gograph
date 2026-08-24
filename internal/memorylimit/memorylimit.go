// Package memorylimit configures the Go runtime for memory-conscious analysis.
package memorylimit

import (
	"fmt"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
)

// Mode selects the runtime memory/GC policy used while building a graph.
type Mode string

const (
	ModeStandard Mode = "standard"
	ModeLow      Mode = "low"
)

const lowMemoryGCPercent = 25

// Policy is an operational resource policy. MaxBytes is a soft Go-runtime
// memory target, not an RSS limit or a hard allocation cap.
type Policy struct {
	Mode     Mode
	MaxBytes int64
}

// Standard returns the default policy used when no memory flags are supplied.
func Standard() Policy {
	return Policy{Mode: ModeStandard}
}

// Validate checks the relationship between the selected mode and limit.
func (p Policy) Validate() error {
	switch p.Mode {
	case ModeStandard, ModeLow:
	default:
		return fmt.Errorf("memory mode must be standard or low, got %q", p.Mode)
	}
	if p.MaxBytes < 0 {
		return fmt.Errorf("maximum memory must be positive")
	}
	if p.MaxBytes > 0 && p.Mode != ModeLow {
		return fmt.Errorf("--max-memory requires --memory-mode=low")
	}
	return nil
}

// ParseMode accepts the documented modes plus "normal" as a compatibility
// spelling for the default standard mode.
func ParseMode(value string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "standard", "normal":
		return ModeStandard, nil
	case "low":
		return ModeLow, nil
	default:
		return "", fmt.Errorf("memory mode must be standard or low, got %q", value)
	}
}

// ParseSize parses an integer byte count with decimal (KB/MB/GB/TB) or binary
// (KiB/MiB/GiB/TiB) units. Unit matching is case-insensitive.
func ParseSize(value string) (int64, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return 0, fmt.Errorf("memory size is empty")
	}
	digitEnd := 0
	for digitEnd < len(raw) && raw[digitEnd] >= '0' && raw[digitEnd] <= '9' {
		digitEnd++
	}
	if digitEnd == 0 {
		return 0, fmt.Errorf("invalid memory size %q", value)
	}
	number, err := strconv.ParseUint(raw[:digitEnd], 10, 64)
	if err != nil || number == 0 {
		return 0, fmt.Errorf("memory size must be a positive integer, got %q", value)
	}
	unit := strings.ToLower(strings.TrimSpace(raw[digitEnd:]))
	multipliers := map[string]uint64{
		"": 1, "b": 1,
		"k": 1_000, "kb": 1_000, "ki": 1 << 10, "kib": 1 << 10,
		"m": 1_000_000, "mb": 1_000_000, "mi": 1 << 20, "mib": 1 << 20,
		"g": 1_000_000_000, "gb": 1_000_000_000, "gi": 1 << 30, "gib": 1 << 30,
		"t": 1_000_000_000_000, "tb": 1_000_000_000_000, "ti": 1 << 40, "tib": 1 << 40,
	}
	multiplier, ok := multipliers[unit]
	if !ok {
		return 0, fmt.Errorf("unsupported memory unit in %q", value)
	}
	if number > math.MaxInt64/multiplier {
		return 0, fmt.Errorf("memory size %q exceeds the supported range", value)
	}
	return int64(number * multiplier), nil
}

// FormatSize renders a byte count with an exact binary unit when possible.
func FormatSize(bytes int64) string {
	for _, unit := range []struct {
		name  string
		bytes int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if bytes >= unit.bytes && bytes%unit.bytes == 0 {
			return fmt.Sprintf("%d%s", bytes/unit.bytes, unit.name)
		}
	}
	return fmt.Sprintf("%dB", bytes)
}

// Controller owns a temporary process-wide runtime policy.
type Controller struct {
	policy         Policy
	effectiveLimit int64
	previousLimit  int64
	previousGC     int
	restoreOnce    sync.Once
	active         bool
}

// Apply activates a validated policy. An existing stricter GOMEMLIMIT is
// preserved rather than silently relaxed.
func Apply(policy Policy) (*Controller, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	controller := &Controller{policy: policy, effectiveLimit: math.MaxInt64}
	if policy.Mode != ModeLow {
		return controller, nil
	}

	controller.active = true
	controller.previousLimit = debug.SetMemoryLimit(math.MaxInt64)
	controller.effectiveLimit = controller.previousLimit
	if policy.MaxBytes > 0 && policy.MaxBytes < controller.effectiveLimit {
		controller.effectiveLimit = policy.MaxBytes
	}
	debug.SetMemoryLimit(controller.effectiveLimit)

	controller.previousGC = debug.SetGCPercent(lowMemoryGCPercent)
	if controller.previousGC >= 0 && controller.previousGC < lowMemoryGCPercent {
		debug.SetGCPercent(controller.previousGC)
	}
	return controller, nil
}

// Policy returns the requested policy.
func (c *Controller) Policy() Policy {
	if c == nil {
		return Standard()
	}
	return c.policy
}

// EffectiveLimit returns the applied Go-runtime memory target. MaxInt64 means
// that neither the command nor the existing environment supplied a limit.
func (c *Controller) EffectiveLimit() int64 {
	if c == nil {
		return math.MaxInt64
	}
	return c.effectiveLimit
}

// BoundedEffectiveLimit returns zero when no finite runtime limit is active.
func (c *Controller) BoundedEffectiveLimit() int64 {
	limit := c.EffectiveLimit()
	if limit == math.MaxInt64 {
		return 0
	}
	return limit
}

// Reclaim forces a GC and returns unused pages to the operating system in low
// mode. It is intended for phase boundaries where large analysis structures
// are no longer reachable.
func (c *Controller) Reclaim() {
	if c != nil && c.active {
		debug.FreeOSMemory()
	}
}

// Restore returns the process runtime settings to their prior values.
func (c *Controller) Restore() {
	if c == nil || !c.active {
		return
	}
	c.restoreOnce.Do(func() {
		debug.SetGCPercent(c.previousGC)
		debug.SetMemoryLimit(c.previousLimit)
	})
}
