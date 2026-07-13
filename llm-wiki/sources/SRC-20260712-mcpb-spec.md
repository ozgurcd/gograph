# Official MCPB 2.1.2 manifest specification

## Metadata

- Source ID: SRC-20260712-mcpb-spec
- Original path: https://github.com/modelcontextprotocol/mcpb/tree/2a788100a60db19a6b1c018fb1cf84ae85de9537
- Source type: web page
- Received date: 2026-07-12
- Ingest date: 2026-07-12
- Trust level: external

## Summary

Pinned MCPB source and released validator defining genuine ZIP bundles, binary manifests, directory configuration, platform compatibility, and manifest schema 0.4.

## Key Claims

- A genuine MCPB is a ZIP with a root `manifest.json` and packaged server files.
- Binary manifests define an entry point plus command and argument arrays.
- A required `directory` user input can be substituted as one argument without shell interpolation.
- Compatibility standardizes `darwin`, `linux`, and `win32`, but does not standardize CPU architecture.
- Released tooling 2.1.2 validates manifest version 0.4.

## Entities and Concepts

- MCPB, binary manifest, `mcp_config`, `user_config`, platform compatibility.

## Contradictions or Updates

- The 2.1.2 runtime names 0.4 as its latest supported manifest, while a separately shipped latest-schema alias still points to 0.3. Gograph pins the explicit 0.4 schema.

## Derived Pages

- [Official MCP Registry Distribution](../mcp-registry.md).
