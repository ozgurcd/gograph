package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func runRenderGoReleaser(args []string) error {
	fs := flag.NewFlagSet("render-goreleaser", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	input := fs.String("input", ".goreleaser.yaml", "source GoReleaser configuration")
	repositoryRoot := fs.String("repository-root", ".", "repository root used as the GoReleaser working directory")
	output := fs.String("output", "", "temporary GoReleaser configuration output")
	mcpbOutput := fs.String("mcpb-output", "", "verified MCPB artifact directory")
	dist := fs.String("dist", "", "temporary GoReleaser distribution directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("render-goreleaser accepts no positional arguments")
	}
	if *output == "" || *mcpbOutput == "" || *dist == "" {
		return fmt.Errorf("--output, --mcpb-output, and --dist are required")
	}
	contents, err := os.ReadFile(*input)
	if err != nil {
		return fmt.Errorf("read %s: %w", *input, err)
	}
	rendered, err := renderGoReleaserConfig(contents, *repositoryRoot, *mcpbOutput, *dist)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(*output, rendered, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *output, err)
	}
	return nil
}

func renderGoReleaserConfig(contents []byte, repositoryRoot, mcpbOutput, dist string) ([]byte, error) {
	const header = "version: 2\n"
	if count := bytes.Count(contents, []byte(header)); count != 1 {
		return nil, fmt.Errorf("GoReleaser configuration has %d version 2 headers, want 1", count)
	}
	if bytes.Contains(contents, []byte("\ndist:")) {
		return nil, fmt.Errorf("GoReleaser configuration already declares dist")
	}
	absMCPB, err := filepath.Abs(mcpbOutput)
	if err != nil {
		return nil, fmt.Errorf("resolve MCPB output: %w", err)
	}
	absDist, err := filepath.Abs(dist)
	if err != nil {
		return nil, fmt.Errorf("resolve GoReleaser dist: %w", err)
	}
	distLine, err := yamlString(filepath.ToSlash(absDist))
	if err != nil {
		return nil, err
	}
	rendered := bytes.Replace(contents, []byte(header), []byte(header+"dist: "+distLine+"\n"), 1)
	const originalGlob = "    - glob: ./.release-mcpb/*.mcpb"
	if count := bytes.Count(rendered, []byte(originalGlob)); count != 2 {
		return nil, fmt.Errorf("GoReleaser configuration has %d canonical MCPB globs, want 2", count)
	}
	absRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	relativeMCPB, err := filepath.Rel(absRoot, absMCPB)
	if err != nil {
		return nil, fmt.Errorf("make MCPB output relative to repository: %w", err)
	}
	if relativeMCPB == ".." || strings.HasPrefix(relativeMCPB, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("MCPB output %s must be inside repository %s", absMCPB, absRoot)
	}
	relativeDist, err := filepath.Rel(absMCPB, absDist)
	if err != nil {
		return nil, fmt.Errorf("make GoReleaser dist relative to MCPB output: %w", err)
	}
	if relativeDist == "." || relativeDist == ".." || strings.HasPrefix(relativeDist, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("GoReleaser dist %s must be a child of MCPB output %s", absDist, absMCPB)
	}
	if !strings.HasPrefix(relativeMCPB, ".") {
		relativeMCPB = "." + string(filepath.Separator) + relativeMCPB
	}
	glob, err := yamlString(filepath.ToSlash(relativeMCPB) + "/*.mcpb")
	if err != nil {
		return nil, err
	}
	rendered = bytes.ReplaceAll(rendered, []byte(originalGlob), []byte("    - glob: "+glob))
	return rendered, nil
}

func yamlString(value string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode YAML string: %w", err)
	}
	return string(encoded), nil
}
