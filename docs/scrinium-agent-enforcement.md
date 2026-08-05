# Scrinium Agent Usage

Scrinium is the governed write interface for durable project memory under
`llm-wiki/`. It is intentionally conditional: source, tests, generated
contracts, release artifacts, and live services remain authoritative for
behavior.

## When to use it

Do not start Scrinium for read-only questions, status checks, trivial edits, or
ordinary changes that create no durable cross-session knowledge.

Use it when maintained wiki content must change or when a material architecture,
security, release, governance, or externally sourced decision should persist.
For those tasks:

1. Start `scrinium ./scrinium.json` once for the working session.
2. Call `capabilities` once for that connection and call `begin_session`.
3. Read `index.md`, `agent-rules.md`, and only directly relevant pages.
4. Write durable knowledge through Scrinium. Avoid routine implementation and
   formatting-only log entries.
5. Call `session_status`, satisfy required maintenance, and call
   `finish_session`.

Generated gograph wiki pages are ignored caches. Check graph freshness before
using them and rebuild them only when structural context is needed.

## MCP configuration

Run the server from the repository root so the configuration remains portable:

```json
{
  "mcpServers": {
    "scrinium": {
      "command": "scrinium",
      "args": ["./scrinium.json"]
    }
  }
}
```

`AGENTS.md` carries the shared repository policy. Local `CLAUDE.md` and
`.agents/rules/llm-wiki.md` files, when present, should carry the same
conditional policy for their respective agent harnesses.

Scrinium 0.1.3's `enforce-agents` template generates an unconditional workflow
and an absolute local configuration path. Do not regenerate these project
instructions with that command until the upstream template can express the
conditional, repository-relative policy.

## Governance limits

Protected pages must use Scrinium's draft workflow. Scrinium can govern writes
made through its own tools, but it cannot prevent arbitrary direct filesystem
edits and must not be treated as a security boundary.
