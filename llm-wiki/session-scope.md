---
title: Caller-owned audit sessions
type: decision
status: current
updated: 2026-10-09
sources:
  - SRC-20260614-gograph-legacy-agent-contract
---

# Caller-owned audit sessions

GOGRAPH-SESSION-SCOPE, owner-approved 2026-10-09: an active audit enforces its owner's workflow without blocking independent readers or absorbing their commands. CLI selects the ID returned by create using `--session-id ID` or `GOGRAPH_SESSION=ID`; the flag takes precedence. MCP accepts `session_id` and `intention` per call and never inherits the server environment. IDs provide cooperative attribution between callers sharing an OS user, not authentication or credentials.

Only selected analytical calls require a rationale and enter that audit. Independent readers need no intention and leave audit bytes unchanged. CLI non-analytical exemptions remain; MCP exempts capabilities, stats, stale, wiki and doc. MCP records command, duration, status and rationale, omitting arguments and results. The compliance formula, weights, thresholds and grades are unchanged.

Each new session has a collision-resistant ID, an exclusively created `session_<ID>.jsonl` log and its own `session_<ID>.active` marker beneath `.gograph/sessions`. Creation never replaces a shared pointer. Independent callers can create concurrently, including with equal labels. End appends the unchanged end record and removes only the selected marker. Existing pointer-based sessions remain selectable explicitly by their old ID; ending one removes its legacy pointer. Historical JSONL records need no migration. Rooted regular-file confinement and strict ID character checks remain binding.

Writes refuse whenever a different active caller is present, naming its session. This covers build, wiki, boundary creation, snapshot save/drop, gate init, installation, workspace overlay/member refresh, and opt-in MCP persisted refresh. Default MCP refresh stays in memory. An owner can end its own session while other owners remain active. Cleanup preserves selected active logs, refuses other active owners, and removes only ended regular logs. Unmarked incomplete logs are retained so cleanup cannot delete a creator's not-yet-marked audit. Audit is read-only and may inspect an explicit historical ID; audit does not end a session. Unset `GOGRAPH_SESSION` after CLI end.

The external wiki hook detects open JSONL logs by their final entry type; the unchanged start/command/end layout preserves compatibility. Achta's generic gate runner inherits its caller environment: independent gates omit the selector, while owners deliberately passing it retain intention enforcement. No hook, integration configuration or achta source change is required.

Regression evidence: CLI/MCP caller-isolation tests prove byte-identical foreign reads/refusals, intention enforcement, independent creation/ending and unchanged owner fixture grades. Session tests cover concurrent equal-label creation, independent telemetry, cleanup, linked markers and traversal rejection.
