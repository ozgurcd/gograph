package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/ozgurcd/gograph/internal/rootfind"
	"github.com/ozgurcd/gograph/internal/search"
)

type GateConfig struct {
	MaxComplexity       *int     `yaml:"max_complexity"`
	MaxInstability      *float64 `yaml:"max_instability"`
	MaxGodObjectMethods *int     `yaml:"max_god_object_methods"`
	AllowNewOrphans     *bool    `yaml:"allow_new_orphans"`
	MaxNewCouplingEdges *int     `yaml:"max_new_coupling_edges"`
}

// gateConfigTemplate is the scaffold written by `gograph gate init`. Each
// threshold is commented to explain what it gates and how to tune it,
// with conservative default values that won't trip on a typical codebase.
// Users can delete any line to disable that gate, or tighten the numbers
// to enforce stricter quality bars.
const gateConfigTemplate = `# .gograph.yml — quality gates enforced by 'gograph gate'.
# Each setting below is OPTIONAL: omit a line to disable that gate.
# Run 'gograph gate' from a working tree where 'gograph build .' has
# already produced .gograph/graph.json.
# The gate fails closed when source files are newer than graph.json.

# max_complexity: fail if any function's cyclomatic complexity exceeds N.
# Calibrate by running 'gograph complexity' once and reading the top score;
# pick a number a bit above that to allow tactical exceptions, then tighten
# over time. 30 is a common "danger zone" threshold.
max_complexity: 30

# max_instability: fail if any package's instability metric exceeds N
# (range 0.0-1.0; 1.0 = imports many, imported by none = leaf consumer).
# 0.9 catches packages that are wildly out of balance without rejecting
# normal top-level "main" or "cmd/..." packages.
max_instability: 0.95

# max_god_object_methods: fail if any struct has more than N methods
# (god-object smell). 20 catches obvious offenders without flagging
# legitimate large APIs.
max_god_object_methods: 20

# allow_new_orphans: when false, gate fails if the orphan count grows
# vs. the saved baseline (set up with 'gograph snapshot save baseline').
# Use to prevent adding dead code over time.
allow_new_orphans: false

# max_new_coupling_edges: fail if the number of new import edges (vs.
# baseline) exceeds N. Use to slow uncontrolled package coupling growth.
max_new_coupling_edges: 10
`

func runGate(args []string) int {
	// Subcommand: 'gate init' writes the template config file and exits.
	if len(args) > 0 && args[0] == "init" {
		return runGateInit()
	}

	root := rootfind.FindRoot()
	configPath := filepath.Join(root, ".gograph.yml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "error: .gograph.yml not found in current directory.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Run 'gograph gate init' to scaffold a template config with")
			fmt.Fprintln(os.Stderr, "conservative defaults and a comment explaining each threshold.")
			return 1
		}
		fmt.Fprintf(os.Stderr, "error reading .gograph.yml: %v\n", err)
		return 1
	}

	var cfg GateConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing .gograph.yml: %v\n", err)
		return 1
	}

	g, err := loadGraph(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading graph (run gograph build . first): %v\n", err)
		return 1
	}

	root = graphRoot(g)
	sr := search.Stale(g, root)
	if sr.IsStale {
		fmt.Fprintf(os.Stderr, "error: graph is stale compared to %d source file(s); refusing to evaluate outdated data.\n", len(sr.ChangedFiles))
		fmt.Fprintln(os.Stderr, "Run 'gograph build .' and retry.")
		return 1
	}

	violations := 0

	// 1. max_complexity
	if cfg.MaxComplexity != nil {
		worstScore := 0
		worstName := ""
		for _, r := range search.Complexity(g, "") {
			if r.Score > worstScore {
				worstScore = r.Score
				worstName = r.Symbol
			}
		}
		if worstScore > *cfg.MaxComplexity {
			fmt.Printf("cross      complexity     max=%d    worst=%d  %s  VIOLATION\n", *cfg.MaxComplexity, worstScore, worstName)
			violations++
		} else {
			fmt.Printf("checkmark  complexity     max=%d    worst=%d  %s\n", *cfg.MaxComplexity, worstScore, worstName)
		}
	}

	// 2. max_instability
	if cfg.MaxInstability != nil {
		worstScore := 0.0
		worstName := ""
		coupling := search.Coupling(g, "", search.CouplingOptions{IncludeStdlib: true})
		for _, c := range coupling {
			if c.Instability > worstScore {
				worstScore = c.Instability
				worstName = c.Package
			}
		}
		if worstScore > *cfg.MaxInstability {
			fmt.Printf("cross      instability    max=%.2f  worst=%.2f  %s   VIOLATION\n", *cfg.MaxInstability, worstScore, worstName)
			violations++
		} else {
			fmt.Printf("checkmark  instability    max=%.2f  worst=%.2f  %s\n", *cfg.MaxInstability, worstScore, worstName)
		}
	}

	// 3. max_god_object_methods
	if cfg.MaxGodObjectMethods != nil {
		worstMethods := 0
		worstName := ""
		// Count methods per struct manually, matching the prompt's definition
		methodsByReceiver := make(map[string]int)
		for _, s := range g.Symbols {
			if s.Kind == "method" && s.Receiver != "" {
				recv := s.Receiver
				if len(recv) > 0 && recv[0] == '*' {
					recv = recv[1:]
				}
				methodsByReceiver[recv]++
			}
		}
		for name, count := range methodsByReceiver {
			if count > worstMethods {
				worstMethods = count
				worstName = name
			}
		}

		if worstMethods > *cfg.MaxGodObjectMethods {
			fmt.Printf("cross      god objects    %s has %d methods             VIOLATION\n", worstName, worstMethods)
			violations++
		} else {
			fmt.Printf("checkmark  god objects    0 structs exceed threshold\n")
		}
	}

	// 4. allow_new_orphans
	if cfg.AllowNewOrphans != nil && !*cfg.AllowNewOrphans {
		if g.Baseline == nil {
			fmt.Println("checkmark  orphans        0 new (skipped: no baseline exists yet)")
		} else {
			currOrphans := len(search.ReachableOrphans(g))
			if currOrphans > g.Baseline.OrphanCount {
				fmt.Printf("cross      orphans        %d new unreachable symbols (baseline %d)  VIOLATION\n", currOrphans-g.Baseline.OrphanCount, g.Baseline.OrphanCount)
				violations++
			} else {
				fmt.Printf("checkmark  orphans        0 new unreachable symbols\n")
			}
		}
	}

	// 5. max_new_coupling_edges
	if cfg.MaxNewCouplingEdges != nil {
		if g.Baseline == nil {
			fmt.Println("checkmark  coupling       0 new edges (skipped: no baseline exists yet)")
		} else {
			currEdges := len(g.Imports)
			newEdges := currEdges - g.Baseline.CouplingEdges
			if newEdges > *cfg.MaxNewCouplingEdges {
				fmt.Printf("cross      coupling       %d new edges (max %d)          VIOLATION\n", newEdges, *cfg.MaxNewCouplingEdges)
				violations++
			} else {
				if newEdges < 0 {
					newEdges = 0
				}
				fmt.Printf("checkmark  coupling       %d new edges\n", newEdges)
			}
		}
	}

	if violations == 0 {
		fmt.Println("GATE PASSED")
		return 0
	}
	fmt.Printf("GATE FAILED — %d violations\n", violations)
	return 1
}

// runGateInit scaffolds a .gograph.yml template in the project root.
// Refuses to overwrite an existing file — the user should `--force` or
// edit by hand. Conservative defaults; the template documents each
// threshold so users can tune without consulting external docs.
func runGateInit() int {
	configPath := filepath.Join(rootfind.FindRoot(), ".gograph.yml")
	if _, err := os.Stat(configPath); err == nil {
		fmt.Fprintf(os.Stderr, "error: %s already exists in the current directory.\n", configPath)
		fmt.Fprintln(os.Stderr, "Refusing to overwrite. Edit the existing file or remove it first.")
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error checking for %s: %v\n", configPath, err)
		return 1
	}
	// Permissions 0644: readable by user/group/other, writable only by owner.
	// This is a project-level config file checked into git; world-readable
	// is appropriate.
	if err := os.WriteFile(configPath, []byte(gateConfigTemplate), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", configPath, err)
		return 1
	}
	fmt.Printf("Wrote %s with conservative quality-gate defaults.\n", configPath)
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Println("  1. Review the template — each threshold is commented.")
	fmt.Println("  2. Tune values to your codebase (run 'gograph complexity',")
	fmt.Println("     'gograph coupling', 'gograph godobj' to see current numbers).")
	fmt.Println("  3. (Optional) Save a baseline for orphan/coupling-edge gating:")
	fmt.Println("     gograph snapshot save baseline")
	fmt.Println("  4. Run the gate:")
	fmt.Println("     gograph gate")
	return 0
}
