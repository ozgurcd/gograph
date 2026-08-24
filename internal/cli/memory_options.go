package cli

import (
	"fmt"
	"strings"

	"github.com/ozgurcd/gograph/internal/memorylimit"
)

func parseMemoryOption(args []string, index int, policy *memorylimit.Policy) (bool, int, error) {
	argument := args[index]
	switch {
	case argument == "--memory-mode":
		if index+1 >= len(args) {
			return true, index, fmt.Errorf("--memory-mode requires standard or low")
		}
		mode, err := memorylimit.ParseMode(args[index+1])
		if err != nil {
			return true, index, err
		}
		policy.Mode = mode
		return true, index + 1, nil
	case strings.HasPrefix(argument, "--memory-mode="):
		mode, err := memorylimit.ParseMode(strings.TrimPrefix(argument, "--memory-mode="))
		if err != nil {
			return true, index, err
		}
		policy.Mode = mode
		return true, index, nil
	case argument == "--max-memory":
		if index+1 >= len(args) {
			return true, index, fmt.Errorf("--max-memory requires a size such as 1GiB")
		}
		bytes, err := memorylimit.ParseSize(args[index+1])
		if err != nil {
			return true, index, fmt.Errorf("invalid --max-memory: %w", err)
		}
		policy.MaxBytes = bytes
		return true, index + 1, nil
	case strings.HasPrefix(argument, "--max-memory="):
		bytes, err := memorylimit.ParseSize(strings.TrimPrefix(argument, "--max-memory="))
		if err != nil {
			return true, index, fmt.Errorf("invalid --max-memory: %w", err)
		}
		policy.MaxBytes = bytes
		return true, index, nil
	default:
		return false, index, nil
	}
}

func memoryPolicySummary(policy memorylimit.Policy) string {
	if policy.Mode != memorylimit.ModeLow {
		return "standard"
	}
	if policy.MaxBytes == 0 {
		return "low (aggressive reclamation; existing Go runtime limit preserved)"
	}
	return fmt.Sprintf("low (soft Go runtime memory target %s; not an RSS or hard limit)", memorylimit.FormatSize(policy.MaxBytes))
}
