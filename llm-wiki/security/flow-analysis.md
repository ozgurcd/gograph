# Security Flow Analysis

Implemented 2026-07-11 as CLI `flow` and MCP `gograph_flow`. Builds persist source, transfer, call, return, and sink facts. Queries apply `.gograph/flow.json` return-value sanitizer policy without rebuilding.

Sources: HTTP contexts, decoded JSON/binders, environment. Sinks: SQL text, process arguments, filesystem paths, outbound HTTP. Parameterized SQL values are not query-text sinks. Indexed returns and up to 16 call-site frames prevent unrelated caller/result leakage. Precise builds add method/interface targets.

Analysis remains path-insensitive. Globals, reflection, arbitrary heap aliases, and some dynamic calls are not modeled. External propagation is low confidence. Findings require source review. Config paths and symlink targets stay inside the graph root.
