# AGENTS.md

<!-- BEGIN SCRINIUM ENFORCEMENT -->
# Scrinium Enforcement

Audience: Codex, OpenCode, Antigravity-compatible agents.
Generated for agents: antigravity, claudecode, codex, opencode.

Scrinium is the project memory and governance server. Treat `llm-wiki/` as the source of truth for durable project context.

## Required Loop

1. Start Scrinium MCP with command `scrinium` and args `/Users/odemir/Development/identuum/gograph/scrinium.json`.
2. After any harness or plugin bootstrap instructions are loaded, call Scrinium `capabilities` before project work or wiki writes.
3. Call `begin_session` before project changes.
4. Read `index.md` and `agent-rules.md` with `read_wiki_page`.
5. Read any relevant workflow pages before specialized wiki work.
6. Make project changes.
7. Update `llm-wiki` through Scrinium tools so durable context stays current.
8. Update `log.md`, `index.md`, and `source-registry.md` when Scrinium reports they are required.
9. Call `session_status`.
10. Call `finish_session` before reporting completion.

Do not report completion while `finish_session` fails. Satisfy its pending maintenance checklist first.

## Boundaries

Scrinium can enforce wiki writes made through its MCP tools. It cannot see arbitrary direct filesystem edits unless the agent records them back into the wiki before finishing.
<!-- END SCRINIUM ENFORCEMENT -->
