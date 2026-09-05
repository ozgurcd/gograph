package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
)

var (
	legacyHomebrewPostflight = []byte(`  postflight do
    if OS.mac?
      system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/gograph"]
    end
  end`)
	structuredHomebrewPostflight = []byte(`  postflight_steps do
    on_macos do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{staged_path}}/gograph"]
    end
  end`)
)

func runRewriteHomebrewCask(args []string) error {
	fs := flag.NewFlagSet("rewrite-homebrew-cask", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	input := fs.String("input", "", "generated Homebrew cask input")
	output := fs.String("output", "", "rewritten Homebrew cask output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("rewrite-homebrew-cask accepts no positional arguments")
	}
	if *input == "" || *output == "" {
		return fmt.Errorf("--input and --output are required")
	}
	cask, err := os.ReadFile(*input)
	if err != nil {
		return fmt.Errorf("read %s: %w", *input, err)
	}
	rewritten, err := rewriteHomebrewCaskPostflight(cask)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(*output, rewritten, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *output, err)
	}
	return nil
}

func rewriteHomebrewCaskPostflight(cask []byte) ([]byte, error) {
	legacyCount := bytes.Count(cask, legacyHomebrewPostflight)
	structuredCount := bytes.Count(cask, structuredHomebrewPostflight)
	switch {
	case legacyCount == 1 && structuredCount == 0:
		return bytes.Replace(cask, legacyHomebrewPostflight, structuredHomebrewPostflight, 1), nil
	case legacyCount == 0 && structuredCount == 1:
		return bytes.Clone(cask), nil
	default:
		return nil, fmt.Errorf("generated cask contains legacy postflight %d times and structured postflight_steps %d times; want exactly one supported form", legacyCount, structuredCount)
	}
}
