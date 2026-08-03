# Official MCP Registry publishing and package specifications

## Metadata

- Source ID: SRC-20260712-mcp-registry-spec
- Original path: https://github.com/modelcontextprotocol/registry/tree/2d3262c8aa34bae8e1a6060c958b207e26e72ff7/docs/modelcontextprotocol-io
- Source type: web page
- Received date: 2026-07-12
- Ingest date: 2026-07-12
- Trust level: external

## Summary

Pinned official Registry documentation and 2025-12-11 server schema governing MCPB metadata, immutable versions, GitHub OIDC, and publication.

## Key Claims

- The Registry stores metadata while package artifacts remain on an external supported host.
- MCPB identifiers must be public HTTPS GitHub/GitLab release URLs containing `mcp`; lowercase SHA-256 is required.
- Server versions are immutable and must not be reused.
- GitHub Actions OIDC supports `io.github.<owner>/*` with `contents: read` and `id-token: write`.
- The 2025-12-11 schema limits descriptions to 100 characters and supports immutable repository IDs.

## Entities and Concepts

- MCP Registry, `server.json`, MCPB package, GitHub OIDC, `mcp-publisher`.

## Contradictions or Updates

- Publisher v1.7.9 advertises a validate command that is not reliably callable; gograph validates independently against the pinned schema.

## Derived Pages

- [Official MCP Registry Distribution](../mcp-registry.md).
