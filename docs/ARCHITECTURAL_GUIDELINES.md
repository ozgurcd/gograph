# Architectural Guidelines

This document outlines the core architectural philosophy, constraints, and development standards for `gograph`. Any new features, commands, or optimizations must adhere to these principles.

## 1. Core Philosophy & Technical Constraints

`gograph` is designed as a local, AST-aware repository navigation tool tailored specifically for AI coding agents.

- **Local Service Boundary:** Source and telemetry must never be sent to a gograph service or external analytics API. Default AST analysis is local. Precise mode and `doc` may invoke the installed Go toolchain, which follows the user's module/cache/network policy.
- **No Target-Code Execution:** The tool statically analyzes code and does not run target tests, binaries, or application entry points.
- **Explicit Freshness:** CLI analysis uses a persisted `.gograph/graph.json` snapshot, except commands whose contract explicitly reads source/Git state (`source`, `stale`, `changes`, complexity, etc.). MCP source-analysis tools refresh an in-memory AST graph; MCP persisted-index tools preserve CLI snapshot semantics. Keep both paths bounded and deterministic.
- **Token Efficiency:** The output of CLI commands must be concise and targeted to save LLM context window tokens.

## 2. Correctness Model

- **Default Mode (Heuristic):** The default `gograph build .` uses raw Go AST parsing (`go/ast`, `go/parser`). It uses duck-typing and structural heuristics. It **must** tolerate incomplete, uncompilable, or messy codebases.
- **Precise Mode (Type-Checked):** The `gograph build . --precise` command uses `go/types` for Class Hierarchy Analysis (CHA) and exact interface satisfaction. It is allowed to fail or drop precision if the target codebase does not compile.
- **Navigation Aids, Not Proofs:** Heuristic extractors (such as REST route mappers, SQL query extractors, or test edge mappers) are strictly navigation aids for AI agents. They are not guaranteed to find every dynamic invocation. Do not use hyperbolic language (e.g., "cryptographic proof") to describe AST analysis.

## 3. Package Architecture

The codebase is organized into strict domains:
- **`internal/graph`**: Defines the core data models (`SymbolNode`, `MutationEdge`, `Dependency`, etc.). Keep this lightweight and easily serializable to JSON.
- **`internal/parser`**: Handles AST inspection, scope resolution, and metadata extraction. All logic for extracting structural data (functions, globals, concurrency primitives) belongs here.
- **`internal/search`**: Contains query processing, graph traversal, duck-typing, and filtering. Most functions are graph-pure; filesystem/Git-backed functions must accept an explicit graph root and use shared scanner/baseline rules.
- **`internal/cli`**: Orchestrates the user-facing commands, argument parsing, and CLI formatting.
- **`internal/mcp`**: Handles the Model Context Protocol stdio server wrapper around the search functions.

## 4. Development Standards

- **Go Version:** The project requires **Go 1.26.5 or newer**. Never default to or generate code for older versions; 1.26.5 is the minimum security patch level used by CI and release builds.
- **Build Pipeline:** Always compile the binary using `make build`. Never use raw `go build`, as the Makefile handles version injection (`ldflags`) via `bump2version`.
- **Documentation Discipline:** Every new feature, command, or flag must be immediately documented across all relevant targets:
  1. `README.md`
  2. `docs/coding-agent-usage.md`
  3. `gograph capabilities` (`internal/cli/cli.go`)
  4. `gograph --help` (`internal/cli/cli.go`)
  5. `llm-wiki/README.md` — update the generated pages table if a new page type is added
  6. `llm-wiki/agent-contract.md` — update tool selection rules if a new command changes workflow
