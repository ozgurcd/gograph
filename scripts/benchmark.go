package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func main() {
	sym := flag.String("sym", "Run", "Symbol name to benchmark")
	goplsTarget := flag.String("gopls-target", "", "Target for gopls references (e.g., file:line:col)")
	gographBin := flag.String("gograph-bin", "bin/gograph", "Path to the gograph binary")
	flag.Parse()

	fmt.Printf("Benchmarking %s...\n", *sym)
	fmt.Println(strings.Repeat("-", 60))

	// 1. Benchmark gograph context
	startGograph := time.Now()
	gographOut, err := exec.Command(*gographBin, "context", *sym).CombinedOutput()
	durationGograph := time.Since(startGograph)
	if err != nil {
		fmt.Printf("gograph error: %v\n", err)
	}
	tokensGograph := len(gographOut) / 4 // rough estimate

	// 2. Benchmark gopls
	var goplsOut []byte
	var durationGopls time.Duration
	if *goplsTarget == "" {
		// Default to workspace_symbol if no specific target is provided
		startGopls := time.Now()
		goplsOut, err = exec.Command("gopls", "workspace_symbol", *sym).CombinedOutput()
		durationGopls = time.Since(startGopls)
	} else {
		startGopls := time.Now()
		goplsOut, err = exec.Command("gopls", "references", *goplsTarget).CombinedOutput()
		durationGopls = time.Since(startGopls)
	}

	if err != nil {
		fmt.Printf("gopls error: %v\n", err)
	}

	// Compare only bytes actually emitted by each command. Follow-up reads and
	// model tokenization depend on the client and task, so the harness must not
	// invent a fixed penalty for either workflow.
	tokensGopls := len(goplsOut) / 4

	fmt.Println("LATENCY:")
	fmt.Printf("%-6s 🔷 %s %dms\n", *sym, bar(int(durationGograph.Milliseconds()), 50), durationGograph.Milliseconds())
	fmt.Printf("%-6s 🔶 %s %dms\n", "", bar(int(durationGopls.Milliseconds()), 50), durationGopls.Milliseconds())
	fmt.Println()

	fmt.Println("RAW OUTPUT TOKEN ESTIMATE (bytes / 4):")
	fmt.Printf("%-6s 🔷 %s %d\n", *sym, bar(tokensGograph, 500), tokensGograph)
	fmt.Printf("%-6s 🔶 %s %d\n", "", bar(tokensGopls, 500), tokensGopls)
	fmt.Println()
	fmt.Println("Note: the commands return different evidence; this is a payload and latency sample, not a correctness or end-to-end agent benchmark.")
}

func bar(val int, scale int) string {
	count := val / scale
	if count < 1 {
		count = 1 // min bar size
	}
	if count > 80 {
		count = 80 // max bar size
	}
	return strings.Repeat("█", count)
}
