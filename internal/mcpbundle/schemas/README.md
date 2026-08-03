# Pinned MCP distribution schemas

These schemas are vendored so release validation is reproducible and fails closed when an
upstream `latest` alias changes.

| File | Upstream source | SHA-256 |
| --- | --- | --- |
| `server-2025-12-11.schema.json` | MCP Registry commit [`2d3262c8`](https://github.com/modelcontextprotocol/registry/blob/2d3262c8aa34bae8e1a6060c958b207e26e72ff7/internal/validators/schemas/2025-12-11.json) | `578b5bb01866d060ff6a67734cf6b2f17a5da283a0877775c7913e4761a626e5` |
| `mcpb-manifest-v0.4.schema.json` | MCPB commit [`2a788100`](https://github.com/modelcontextprotocol/mcpb/blob/2a788100a60db19a6b1c018fb1cf84ae85de9537/schemas/mcpb-manifest-v0.4.schema.json) as shipped by `@anthropic-ai/mcpb@2.1.2` | `9e4fa3cdc4ae3872b3d76dd538a2517c4e9cf43a7ea2707819e11aedce09ee69` |

The corresponding upstream terms are retained in `LICENSE.registry` and
`LICENSE.mcpb`.

The MCPB 2.1.2 runtime exposes `0.4` as `LATEST_MANIFEST_VERSION` and validates it,
although its separately published `mcpb-manifest-latest.schema.json` alias still points to
`0.3`. Gograph pins the explicit 0.4 schema and never relies on that stale alias.

To update a schema, pin an immutable upstream commit and released validator version, replace
the file, update the digest above, and run the full MCPB release verification. Never update a
schema by following an unpinned `main` or `latest` URL in the publication workflow.
