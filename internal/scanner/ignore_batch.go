package scanner

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
)

// One native Git process belongs to one walk. Never reuse its rule/index cache
// across freshness checks. Streaming preserves the walk's pruning and the order
// of source-safety checks instead of gathering files through excluded subtrees.
func (g *gitIgnoreChecker) startBatch() {
	cmd := exec.Command("git", "-C", g.root, "check-ignore", "--stdin", "-z", "--verbose", "--non-matching")
	cmd.Env = append(os.Environ(), "GIT_FLUSH=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		return
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return
	}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return
	}
	g.cmd, g.input = cmd, input
	// Bound protocol memory even for an unusually large ignore pattern. Such a
	// response falls back to --quiet, which needs no captured pattern text.
	g.output = bufio.NewReaderSize(output, 64*1024)
}

func (g *gitIgnoreChecker) stopBatch(kill bool) {
	if g.cmd == nil {
		return
	}
	_ = g.input.Close()
	if kill {
		_ = g.cmd.Process.Kill()
	}
	_ = g.cmd.Wait()
	g.cmd, g.input, g.output = nil, nil, nil
}

func readIgnoreResponse(reader *bufio.Reader, path string) (bool, error) {
	ignored := false
	for field := range 4 {
		value, err := reader.ReadSlice(0)
		if err != nil {
			return false, err
		}
		value = value[:len(value)-1]
		if field == 2 {
			ignored = len(value) > 0 && value[0] != '!'
		}
		if field == 3 && string(value) != path {
			return false, fmt.Errorf("git ignore response path does not match request")
		}
	}
	return ignored, nil
}
