# Privacy Policy

**Effective date:** 2026-05-16

## Overview

Gograph is a local static-analysis tool. Gograph itself has no hosted service and does not collect or transmit repository code or personal data. It does write project artifacts and optional workflow metadata locally.

## Data Collection

Gograph collects **no data**. Specifically:

- **No code is uploaded.** All AST parsing and graph analysis runs locally against files on your filesystem.
- **No remote telemetry.** Optional audit sessions write command metadata to `.gograph/sessions/`; raw query results are not logged or transmitted.
- **Local MCP transport.** The MCP server communicates over stdio and opens no listening port. Default AST indexing makes no application-service calls. Precise mode and `gograph doc` invoke the installed Go toolchain, which follows the user's configured module cache/proxy/network policy.
- **No accounts or authentication.** Gograph requires no login, API key, or user account.

## Repository Data

When you run `gograph build .`, it reads selected Go source plus project metadata such as `go.mod`, `.gitignore`, and Git ignore state. Other commands may read graph/config JSON or YAML and Git state; `flow` reads `.gograph/flow.json` when present or a user-selected sanitizer policy inside the graph root. It writes `.gograph/graph.json`, Markdown reports, snapshots, boundary/check configuration, wiki pages, and optional session logs locally. `source`/`context` return requested Go source, inline route-handler bodies may be stored in the graph, and compact source/transfer/sink facts are stored for query-time security-flow analysis.

## Third-Party Services

Gograph has no analytics or hosted backend integration. Local integrations include Git, the Go toolchain, MCP clients, and optional Claude configuration files; those tools retain their own policies and permissions.

## Contact

For questions, open an issue at: https://github.com/ozgurcd/gograph/issues
