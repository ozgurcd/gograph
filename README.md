# gograph

[![Go Report Card](https://goreportcard.com/badge/github.com/ozgurcd/gograph)](https://goreportcard.com/report/github.com/ozgurcd/gograph)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

`gograph` is a local AST/type-aware Go repository context indexer for AI coding agents.

![Gograph Demo](gograph-demo.gif)

It builds a compact graph of packages, symbols, calls, routes, config reads, tests, and code-quality signals so agents can navigate Go repositories with fewer raw file reads.

> **Note on Language Support:** I originally built `gograph` specifically for **Golang** because that is what I needed for my own workflows. It currently only parses and maps Go codebases. However, the architecture is extensible! If you want to add support for other languages (Python, TypeScript, Rust, etc.), **contributions are more than welcome.** Please see the [Contributing Guide](CONTRIBUTING.md) to get started.

## Features
- **Local Only:** Graph building performs no network calls and sends no source code to external APIs. MCP integration is local stdio-based.
- **Go Focused:** Maps Go project structures, packages, and dependencies using the standard AST.
- **Targeted Focus:** Extract incredibly targeted context for a single package using `focus` to save LLM tokens.
- **Token-Saving Context Bundle:** `context <symbol>` replaces 4–5 separate tool calls — returns node, source, callers, callees, and tests in one response.
- **Hotspot Ranking:** `hotspot` ranks functions by incoming call count so agents know which functions to study first.
- **Code Quality Analysis:** Cyclomatic complexity (`complexity`), god-object detection (`godobj`), and package coupling/instability (`coupling`).
- **Change Detection:** `changes` surfaces new/modified/deleted symbols since the last build without re-reading source files.
- **Dependency Trees:** `deps <pkg> [--transitive]` shows direct or full transitive import closures for any package.
- **Tech Stack Extraction:** Automatically parses `go.mod` to summarize your external dependencies (like `gin` or `pgx`) so agents instantly understand your stack.
- **Concurrency Mapping:** Detects goroutine spawns, channel sends, mutex locks, WaitGroup usage, and `sync.Once.Do` calls across the entire codebase.
- **Interface Satisfaction:** Best-effort duck-typing analysis that tells you which interfaces any struct satisfies — without running the compiler.
- **Test Coverage Map:** Best-effort mapping that links `Test*` functions to the production symbols they likely exercise.
- **Environment Config:** Surfaces every `os.Getenv` / `viper.Get*` read with file, line, and enclosing function.
- **Pathfinding:** `path <from> <to>` finds the shortest call chain between any two symbols via BFS.
- **Dead Code Detection:** `orphans` uses full reachability analysis from entry points — stricter than simple 0-call-count checks.
- **Clean Graph (No Generated Files):** Uses strict line-based detection to automatically exclude generated files like mocks or protobufs.
- **Fast:** Written in Go for high performance.

## Non-goals
- No multi-language parsing.
- No AI/model API calls.
- No embeddings.
- No SaaS backend.
- No telemetry.
- No replacement for compiler/type-checker correctness.
- No guarantee that heuristic extractors find every route, SQL query, test relation, or dynamic call.

## Correctness model
- **Default mode** uses Go AST parsing and best-effort heuristics. It tolerates incomplete or non-compiling repositories.
- **Precise mode** uses type-checked enrichment and requires compilable packages.
- Heuristic extractors such as routes, SQL, tests, and error mapping are navigation aids, not authoritative program analysis.

## Installation

```bash
# MacOS / Linux (via Homebrew)
brew install ozgurcd/tap/gograph

# Or using Go:
go install github.com/ozgurcd/gograph/cmd/gograph@latest
```

## Usage

**1. Generate the Graph (Run this after every major code change):**
```bash
gograph build .
# OR for more precise type-checked analysis (slower, but provides exact dynamic dispatch & interface satisfaction proofs):
gograph build . --precise
```
*This instantly generates `.gograph/graph.json` and `.gograph/GRAPH_REPORT.md`.*

**2. Query the Graph (Lightning fast, no re-parsing):**
```bash
gograph query "Auth"              # Search for symbols, files, or packages
gograph focus "internal/auth"     # Generate a highly targeted context for one package
gograph callers "ValidateToken" [--no-tests] # See what functions call ValidateToken (with exact source snippet)
gograph callees "InitServer" [--no-tests]    # See what InitServer calls (with exact source snippet)
gograph implementers "AuthService" # See which structs implement an interface
gograph interfaces "UserService"  # See which interfaces a struct satisfies (type-checked if --precise was used)
gograph fields "User"             # Extract all fields and types of a struct
gograph source "ValidateToken"    # Extract the source code for a specific symbol (function, interface, const)
gograph impact "ValidateToken"    # View the full blast radius (all downstream callers)
gograph impact --uncommitted      # Calculate the blast radius of all your uncommitted code changes
gograph orphans                   # List functions and methods with 0 explicit incoming calls
gograph node "UserStruct"         # Get detailed AST info about a specific node
gograph routes                    # Extract all HTTP REST API routes (e.g. GET /api)
gograph imports "redis"           # Find all files that import a specific external package
gograph sql                       # Extract database SQL queries from the AST
gograph errors                    # Map every custom error and panic to its function
gograph embeds "Mutex"            # See which structs embed a target struct
gograph public "internal/auth"    # Filter graph to only show exported public symbols
gograph envs                      # List every environment variable read in the codebase
gograph concurrency               # Map all goroutines, channels, mutexes, and sync primitives
gograph tests "ValidateToken"     # Find which test functions exercise a named symbol
gograph path "CreateUser" "sql"   # Shortest call chain between two symbols
gograph stale                     # Check if graph.json is out of date vs source files
gograph orphans                   # Symbols truly unreachable from any entry point
gograph godobj                    # Find god-object struct candidates
gograph godobj --methods 10 --fields 12 --calls 30 --top 5  # Custom thresholds
gograph complexity                # Cyclomatic complexity for all functions (highest first)
gograph complexity "Run"          # Complexity for a specific function
gograph coupling                  # Package fan-in, fan-out, instability table
gograph coupling "internal/auth" # Filter to a specific package
# --- TOKEN SAVERS ---
gograph context "ValidateToken"   # Node + source + callers + callees + tests in ONE call
gograph hotspot                   # Top 10 most-called functions (where to focus first)
gograph hotspot --top 20          # Expand to top 20
gograph deps "internal/auth"      # Direct import dependencies of a package
gograph deps "internal/auth" --transitive  # Full transitive closure
gograph changes                   # New/modified/deleted symbols since last build
gograph trace "parse failed"      # Trace an error string backwards to entry points
gograph mutate "User.Status"      # Find functions that mutate a specific struct field
gograph arity --min 5             # Find functions with many arguments (long parameter list smell)
gograph skeleton                  # Output the whole repository's API signatures (bodies stripped)
gograph constructors "User"       # Find factory functions returning the named struct
gograph schema "users"            # Find structs mapped to a database table/schema via tags
gograph globals "internal/auth"   # Find pkg-level vars, consts, and functions mutating them
gograph mocks "AuthService"       # Find structs implementing an interface in test/mock files
gograph fixtures "internal/auth"  # Find test helper structs and functions in test files
```

**3. Agent JSON Integration:**
All search and query commands support the `--json` flag to emit strictly formatted, machine-parseable JSON envelopes.

For specific instructions on how to configure agents to use `gograph`, read the [Claude Code Integration Guide](docs/claude-code-integration.md).

```bash
gograph callers "ValidateToken" --json
```
*Returns: `{"schema_version": "1", "command": "callers", "status": "ok", "count": 2, "results": [...]}`*


**4. Run as an MCP Server (For AI Agents):**
If you want to give your AI agent native tool execution capabilities, `gograph` has a built-in [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server.
```bash
gograph mcp .
```
You can add this to your AI client's configuration (like Claude Desktop or VS Code extensions like Cline) so the AI can run these graph queries autonomously!

## 🤖 Integrating with AI Agents (Cursor, Claude Code, Copilot)

To get the absolute best results from your AI coding assistant, you no longer need to copy-paste giant instruction files. Just add this one-liner to your `.cursorrules`, `CLAUDE.md`, or AI system instructions:

> **System Prompt:**
> Before answering architecture or repository questions, run `gograph capabilities` and follow the instructions it prints.

## Example Output

When you run `gograph build .`, the generated `GRAPH_REPORT.md` gives your AI a condensed, highly-dense context map that looks like this:

**External Dependencies (Tech Stack)**
| Module | Version |
|--------|---------|
| `github.com/gin-gonic/gin` | `v1.9.1` |
| `github.com/jackc/pgx/v5` | `v5.5.5` |

**Important Symbols (Top by outgoing calls)**
| Symbol | Kind | File | Line | Calls out |
|--------|------|------|------|-----------|
| `(Server).Start` | method | `server.go` | 42 | 18 |
| `ValidateAuth` | function | `auth.go` | 12 | 14 |

## Contributing

We love pull requests! See the [CONTRIBUTING.md](CONTRIBUTING.md) file for guidelines on how to build, test, and contribute to the project. If you are adding support for a new language, please open an issue first to discuss the design.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
