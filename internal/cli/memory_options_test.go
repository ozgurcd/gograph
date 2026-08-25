package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/memorylimit"
)

func TestParseBuildMemoryOptions(t *testing.T) {
	options, err := parseBuildArgs([]string{".", "--precise", "--strict", "--memory-mode=low", "--max-memory=1GiB"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Root != "." || !options.Precise || !options.Strict || options.Memory.Mode != memorylimit.ModeLow || options.Memory.MaxBytes != 1<<30 {
		t.Fatalf("parsed build options = %+v", options)
	}

	for _, args := range [][]string{
		{".", "--strict"},
		{".", "--max-memory=1GiB"},
		{".", "--memory-mode=fast"},
		{".", "--memory-mode=low", "--max-memory=0"},
		{".", "another"},
		{".", "--unknown"},
	} {
		if _, err := parseBuildArgs(args); err == nil {
			t.Errorf("parseBuildArgs(%q) unexpectedly succeeded", args)
		}
	}
}

func TestParseMCPMemoryOptions(t *testing.T) {
	options, err := parseMCPArgs([]string{"--persist-refresh", "--memory-mode", "low", "--max-memory", "1GB", "."})
	if err != nil {
		t.Fatal(err)
	}
	if !options.PersistRefresh || options.Memory.Mode != memorylimit.ModeLow || options.Memory.MaxBytes != 1_000_000_000 {
		t.Fatalf("parsed MCP options = %+v", options)
	}
	if _, err := parseMCPArgs([]string{"--max-memory=1GiB"}); err == nil {
		t.Fatal("MCP max-memory without low mode unexpectedly succeeded")
	}
}

func TestRunBuildPreciseLowMemory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lowbuild\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const source = `package lowbuild

type Runner interface { Run() }
type Service struct{}
func (*Service) Run() {}
func Dispatch(r Runner) { r.Run() }
`
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runBuild([]string{root, "--precise", "--memory-mode=low", "--max-memory=1GiB"}); code != 0 {
		t.Fatalf("runBuild exit code = %d, want 0", code)
	}
	built, err := loadGraph(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := built.Build.EffectivePrecision(); got != graph.PrecisionPrecise {
		t.Fatalf("precision = %q, want %q; warnings=%v", got, graph.PrecisionPrecise, built.Build.Warnings)
	}
	if len(built.Implements) != 1 || built.Implements[0].Concrete != "Service" {
		t.Fatalf("precise implementations = %+v", built.Implements)
	}
}
