package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/rootfind"
	"github.com/ozgurcd/gograph/internal/search"
)

func runCheck(args []string) int {
	var configPath string
	uncommitted := false
	var sinceRef string

	// parse args
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config":
			if i+1 < len(args) {
				configPath = args[i+1]
				i++
			} else {
				if !jsonMode {
					fmt.Fprintln(os.Stderr, "missing value for --config")
				}
				return 1
			}
		case strings.HasPrefix(a, "--config="):
			configPath = strings.TrimPrefix(a, "--config=")
		case a == "--uncommitted":
			uncommitted = true
		case a == "--since":
			if i+1 < len(args) {
				sinceRef = args[i+1]
				i++
			} else {
				if !jsonMode {
					fmt.Fprintln(os.Stderr, "missing value for --since")
				}
				return 1
			}
		case strings.HasPrefix(a, "--since="):
			sinceRef = strings.TrimPrefix(a, "--since=")
		default:
			if !strings.HasPrefix(a, "-") {
				if !jsonMode {
					fmt.Fprintf(os.Stderr, "unknown argument: %s\n", a)
				}
				return 1
			}
		}
	}

	root := rootfind.FindRoot()
	// load config
	if configPath == "" {
		defaultPath := filepath.Join(root, ".gograph", "checks.json")
		if _, err := os.Stat(defaultPath); err == nil {
			configPath = defaultPath
		}
	} else if !filepath.IsAbs(configPath) {
		configPath = filepath.Join(root, configPath)
	}

	config := &search.CheckConfig{
		Checks: map[string]any{
			"boundaries":     "warn",
			"max_arity":      map[string]any{"level": "warn", "value": 6.0},
			"max_complexity": map[string]any{"level": "warn", "value": 20.0},
		},
		BoundariesConfig: filepath.Join(root, ".gograph", "boundaries.json"),
	}

	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			if !jsonMode {
				fmt.Fprintf(os.Stderr, "failed to read config: %v\n", err)
			}
			return 1
		}
		if err := json.Unmarshal(data, config); err != nil {
			if !jsonMode {
				fmt.Fprintf(os.Stderr, "failed to parse config: %v\n", err)
			}
			return 1
		}
	}
	if config.BoundariesConfig != "" && !filepath.IsAbs(config.BoundariesConfig) {
		config.BoundariesConfig = filepath.Join(root, config.BoundariesConfig)
	}

	// CLI flags override config
	if sinceRef != "" {
		config.Baseline = sinceRef
	}

	g, err := loadGraph(".")
	if err != nil {
		if !jsonMode {
			fmt.Fprintf(os.Stderr, "failed to load graph: %v\n", err)
		}
		return 1
	}

	var baselineGraph *graph.Graph
	if config.Baseline != "" {
		baselineGraph, err = BuildBaselineGraphFromGitRefAtRoot(graphRoot(g), config.Baseline, BuildGraph)
		if err != nil {
			if !jsonMode {
				fmt.Fprintf(os.Stderr, "failed to build baseline graph: %v\n", err)
			}
			return 1
		}
	}

	p := &search.CheckParams{
		CurrentGraph:  g,
		BaselineGraph: baselineGraph,
		Config:        config,
		SinceRef:      config.Baseline,
		Uncommitted:   uncommitted,
		RootDir:       graphRoot(g),
	}

	report, err := search.RunChecks(p)
	if err != nil {
		if !jsonMode {
			fmt.Fprintf(os.Stderr, "check failed: %v\n", err)
		}
		return 1
	}

	if jsonMode {
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Println("Status:", report.Status)
		if len(report.Findings) > 0 {
			var errs []search.CheckFinding
			var warns []search.CheckFinding
			for _, f := range report.Findings {
				if f.Level == "error" {
					errs = append(errs, f)
				} else {
					warns = append(warns, f)
				}
			}
			if len(errs) > 0 {
				fmt.Println("\nErrors:")
				limit := 10
				if len(errs) < limit {
					limit = len(errs)
				}
				for i := 0; i < limit; i++ {
					fmt.Printf("  - %s\n", errs[i].Message)
				}
				if len(errs) > limit {
					fmt.Printf("  ... and %d more errors. Use --json for full output.\n", len(errs)-limit)
				}
			}
			if len(warns) > 0 {
				fmt.Println("\nWarnings:")
				limit := 10
				if len(warns) < limit {
					limit = len(warns)
				}
				for i := 0; i < limit; i++ {
					fmt.Printf("  - %s\n", warns[i].Message)
				}
				if len(warns) > limit {
					fmt.Printf("  ... and %d more warnings. Use --json for full output.\n", len(warns)-limit)
				}
			}
		}

		if len(report.Skipped) > 0 {
			fmt.Println("\nSkipped:")
			for _, s := range report.Skipped {
				fmt.Printf("  - %s skipped: %s\n", s.Check, s.Reason)
			}
		}

		fmt.Println("\nSummary:")
		fmt.Printf("  errors: %d\n", report.Summary.Errors)
		fmt.Printf("  warnings: %d\n", report.Summary.Warnings)
		fmt.Printf("  skipped: %d\n", report.Summary.Skipped)
	}

	if report.Status == string(search.CheckFailed) {
		return 1
	}
	return 0
}
