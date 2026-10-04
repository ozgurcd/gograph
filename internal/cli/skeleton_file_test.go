package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ozgurcd/gograph/internal/graph"
)

func TestSkeletonFileSelection(t *testing.T) {
	g := &graph.Graph{
		Files: []graph.FileNode{
			{ID: "internal/api/api.go", Path: "internal/api/api.go", PackageName: "api"},
			{ID: "tools/devseed/main.go", Path: "tools/devseed/main.go", PackageName: "main"},
			{ID: "cmd/other/main.go", Path: "cmd/other/main.go", PackageName: "main"},
			{ID: "tools/devseed/empty.go", Path: "tools/devseed/empty.go", PackageName: "main"},
		},
		Symbols: []graph.SymbolNode{
			{ID: "example.com/api::Serve", Name: "Serve", Kind: graph.KindFunction, PackageName: "api", File: "internal/api/api.go", Signature: "func Serve()"},
			{ID: "example.com/devseed::main", Name: "main", Kind: graph.KindFunction, PackageName: "main", File: "tools/devseed/main.go", Signature: "func main()"},
			{ID: "example.com/other::Other", Name: "Other", Kind: graph.KindFunction, PackageName: "main", File: "cmd/other/main.go", Signature: "func Other()"},
		},
	}
	root := writeCLIParityGraph(t, g)
	for _, jsonOutput := range []bool{false, true} {
		for _, file := range []string{"tools/devseed/main.go", "./tools/devseed/main.go", "tools/devseed/empty.go", "missing.go", "main.go"} {
			t.Run(file+"/json="+map[bool]string{false: "false", true: "true"}[jsonOutput], func(t *testing.T) {
				args := []string{"skeleton", file}
				if jsonOutput {
					args = append(args, "--json")
				}
				stdout, stderr, code := runCLIParityInDir(t, root, func() int { return Run(args) })
				if file == "missing.go" || file == "main.go" {
					if code == 0 {
						t.Fatalf("unmatched file returned success: %s", stdout)
					}
					return
				}
				if code != 0 {
					t.Fatalf("skeleton failed: %d %s %s", code, stdout, stderr)
				}
				output := stdout
				if jsonOutput {
					var envelope struct {
						Results string `json:"results"`
					}
					if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
						t.Fatal(err)
					}
					output = envelope.Results
				}
				if !strings.HasPrefix(output, "package main\n") || strings.Contains(output, "Serve") || strings.Contains(output, "Other") {
					t.Fatalf("skeleton selected unrelated declarations: %s", output)
				}
				if strings.Contains(output, "func main()") != (file != "tools/devseed/empty.go") {
					t.Fatalf("skeleton lost file identity: %s", output)
				}
			})
		}
	}
}
