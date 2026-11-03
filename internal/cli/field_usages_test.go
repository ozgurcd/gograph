package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fieldUsagesSource = `package main

type Base struct{ ID string }

type Config struct {
	Base
	Limit int
	Name  string
}

type Other struct{ Limit int }

func newConfig() *Config { return &Config{Limit: 3} }

func use(c *Config) int {
	c.Name = "x"
	pointer := &c.Limit
	_ = pointer
	return len(c.ID) + c.Limit
}

func other(o Other) int { return o.Limit }

func main() {
	_ = use(newConfig())
	_ = other(Other{})
}
`

func writeFieldUsagesRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":  "module example.com/fields\n\ngo 1.22\n",
		"main.go": fieldUsagesSource,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestUsagesFindsTypedStructFieldUses(t *testing.T) {
	root := writeFieldUsagesRepository(t)
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", ".", "--precise"}) }); code != 0 {
		t.Fatalf("precise build failed: %s", stderr)
	}
	out, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"usages", "Config.Limit"}) })
	if code != 0 {
		t.Fatalf("usages failed: %s", stderr)
	}
	for _, want := range []string{
		"[field] Config.Limit — declared field of type int",
		"[field_literal] Config.Limit — literal in newConfig  (main.go:13)",
		"[field_address] Config.Limit — address in use  (main.go:17)",
		"[field_read] Config.Limit — read in use  (main.go:19)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("usages Config.Limit lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "in other") {
		t.Fatalf("typed field uses must not include another type's same-named field:\n%s", out)
	}

	out, _, _ = runCLIParityInDir(t, root, func() int { return Run([]string{"usages", "Config.Name"}) })
	if !strings.Contains(out, "[field_write] Config.Name — write in use  (main.go:16)") {
		t.Fatalf("an assignment must be a write:\n%s", out)
	}
	out, _, _ = runCLIParityInDir(t, root, func() int { return Run([]string{"usages", "Base.ID"}) })
	if !strings.Contains(out, "[field_read] Base.ID — read in use  (main.go:19)") {
		t.Fatalf("a promoted field must be attributed to its embedded struct:\n%s", out)
	}
	out, _, _ = runCLIParityInDir(t, root, func() int { return Run([]string{"usages", "Limit"}) })
	if !strings.Contains(out, "Config.Limit — read in use") || !strings.Contains(out, "Other.Limit — read in other") {
		t.Fatalf("a bare field name must cover every declaring struct:\n%s", out)
	}
	out, _, _ = runCLIParityInDir(t, root, func() int { return Run([]string{"usages", "Config"}) })
	if strings.Contains(out, "[field_read]") {
		t.Fatalf("type usages must keep their type-reference answer:\n%s", out)
	}
}

func TestUsagesFieldFallbackOnASTGraphIsLabelled(t *testing.T) {
	root := writeFieldUsagesRepository(t)
	if _, stderr, code := runCLIParityInDir(t, root, func() int { return Run([]string{"build", "."}) }); code != 0 {
		t.Fatalf("build failed: %s", stderr)
	}
	out, _, code := runCLIParityInDir(t, root, func() int { return Run([]string{"usages", "Config.Limit"}) })
	if code != 0 || !strings.Contains(out, "[limit]") || !strings.Contains(out, "--precise") || !strings.Contains(out, "[possible_field_use] Limit — possible selector in other") {
		t.Fatalf("an AST graph must return labelled name-only rows with the precise remedy:\n%s", out)
	}
}
