// Package gograph provides repository-level assets embedded into the gograph
// CLI binary. The main command lives in package main under cmd/gograph.
//
// gograph is a local Go static analysis, call-graph, blast-radius, and CI
// architecture-gate CLI, with a built-in stdio Model Context Protocol (MCP)
// server for AI coding agents. It builds a local AST and optional type-checked
// (CHA/SSA) graph of a Go repository without executing target code or sending
// source to external services.
//
// Install the CLI:
//
//	go install github.com/ozgurcd/gograph/cmd/gograph@latest
//
// Documentation and references:
//
//   - Home & overview: https://gograph.identuum.ai/
//   - Getting started guide: https://gograph.identuum.ai/docs/getting-started/
//   - Complete CLI & MCP command reference: https://gograph.identuum.ai/docs/command-reference/
package gograph
