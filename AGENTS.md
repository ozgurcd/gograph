# AGENTS.md

# Project authority

Current source, tests, generated CLI/MCP contracts, release artifacts, and live
services are the source of truth for behavior. `llm-wiki/` stores durable
decisions, rationale, security constraints, provenance, and cross-session
context; it is not a substitute for verifying the code or current external
state. Generated gograph wiki pages are caches and must be freshness-checked
before use.

# Conditional Scrinium use

Do not start Scrinium, call `capabilities`, or create a Scrinium session for a
read-only question, repository status check, trivial edit, or ordinary change
that creates no durable cross-session knowledge. A code or documentation change
by itself does not require a Scrinium session.

Use Scrinium when the task will create or update maintained `llm-wiki/`
content, or when a material architecture, security, release, governance, or
externally sourced decision should persist for future agents.

When Scrinium is required:

1. Start one server for the working session with `scrinium ./scrinium.json`.
2. Call `capabilities` once for that server connection, then call
   `begin_session`.
3. Read `index.md` and `agent-rules.md`, which Scrinium requires before wiki
   writes. Read only the additional pages directly relevant to the task; do not
   preload the whole wiki.
4. Make project changes and write only durable knowledge through Scrinium.
   Do not log routine implementation details, formatting cleanup, or facts that
   are already obvious from source control.
5. Update `log.md`, `index.md`, and `source-registry.md` only when
   `session_status` requires them. Use one concise log entry per material
   outcome.
6. Call `session_status` and `finish_session` before reporting completion of a
   session that was started. Do not call them when no Scrinium session exists.

Protected pages must use Scrinium's draft workflow. Scrinium governs writes
made through its tools; it is workflow assistance, not a security boundary for
arbitrary filesystem edits.
