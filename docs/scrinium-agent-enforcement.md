# Scrinium Agent Enforcement

<!-- BEGIN SCRINIUM ENFORCEMENT -->
# Scrinium Agent Enforcement

Generated agent targets: antigravity, claudecode, codex, opencode.

Use this command to refresh the repository instruction files:

```bash
scrinium enforce-agents
```

## MCP Configuration Snippet

Use the same Scrinium MCP server configuration for Codex, Claude Code, OpenCode, and Antigravity where MCP server configuration is supported:

```json
{
  "mcpServers": {
    "scrinium": {
      "command": "scrinium",
      "args": ["/Users/odemir/Development/identuum/gograph/scrinium.json"]
    }
  }
}
```

## Instruction Files

- `AGENTS.md` carries the shared enforcement block for Codex, OpenCode, Antigravity-compatible agents, and other tools that honor AGENTS-style repository instructions.
- `CLAUDE.md` carries the same enforcement block for Claude Code.
- `llm-wiki/agent-rules.md` is protected by `scrinium.json`; propose changes
  through Scrinium's `create_draft` workflow instead of editing it directly.

Tool-specific config file names can change. Prefer this shared instruction layer plus the MCP snippet unless a tool's current documentation defines a stable project-local config path.
<!-- END SCRINIUM ENFORCEMENT -->
