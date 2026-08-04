---
title: "Documentation Portal"
description: "Explore installation, CLI command references, AI agent MCP integration, and structural analysis workflows."
---

Welcome to the `gograph` documentation portal! 

gograph is a developer and AI agent tool built to index, trace, and query Go codebases using AST-derived call graphs and dependency maps.

---

## 🗺️ Documentation Map

Explore the documentation across five primary categories:

### 🚀 [Getting Started](/docs/getting-started/)
Set up gograph on your system, initialize your first repository graph index, and run basic queries.
- **Topics**: Homebrew install, Go build, compilation requirements, `stats` verification, stale checks.

### 📝 [Command Reference](/docs/command-reference/)
An exhaustive command manual detailing all gograph capabilities.
- **Topics**: Indexing commands, Search & Navigation, Call Graph traversal, Interface resolution, Packages & Imports, security flow analysis, Concurrency mapping, custom error flows, and composite literals mapping.

### 🤖 [AI Agent Integration](/docs/agent-integration/)
Connect gograph with AI coding assistants (like Claude Code, Cursor, and custom LLM workflows) to reduce broad file reads and unsupported structural guesses.
- **Topics**: MCP stdio setup, default in-memory refreshes, opt-in artifact publication, CLI/MCP Mermaid output, Claude Desktop/plugin configuration, and the Claude Code PreToolUse hook guard.

### ⚙️ [Agent Workflows](/docs/workflows/)
Discover safe development workflows and optimization protocols.
- **Topics**: Onboarding to fresh codebases, the plan-to-review edit lifecycle, and package refactoring dependencies extraction.

### 📦 [Official MCP Registry](/docs/mcp-registry/)
Install a platform-specific MCP Bundle from the preview Registry and select the
Go project directory it analyzes.
- **Topics**: MCPB versus Homebrew/`go install`, six release targets, local data handling, and the current CPU-selection limitation.
