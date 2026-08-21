package precise

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnrichSupportsGo127GenericMethods(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/genericmethod\n\ngo 1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const source = `package genericmethod

type Box struct{}

func (Box) Transform[T ~int | ~string](value T) T { return value }

func Use() int { return Box{}.Transform(42) }
`
	if err := os.WriteFile(filepath.Join(root, "generic_method.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	requirePreciseEnrich(t, Enrich(root, emptyGraph()))
}
