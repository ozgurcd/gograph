# Gograph 1.7.8 answer regressions

The implementation uses Go syntax, import provenance and type information. It
contains no application-specific symbol names, paths or exceptions. The isolated
`testdata/answers178` module covers factory closures, named and qualified returned
functions, unknown returns, non-router calls, field calls, getter parameters and
package variables. Minimal vendored framework declarations keep tests offline.

## Measurement

The read-only reproduction source was identuum-idp-oss at `2f3917a`, exported into
an ignored directory in this repository. With v1.7.7, `gograph build . --precise`
followed by every page of `routes --json` produced 290 production registrations;
256 carried `dynamic handler — cannot be statically resolved; factory call
recorded, returned handler not resolved`.

The revised analysis retained 262 registrations and removed 28 non-router calls:
six `zap.Any` calls and 22 URL-value or HTTP-header `Get` calls. Among the retained
registrations, 255 formerly dynamic factory handlers resolved. The `/metrics`
registration's external handler result remains unresolved. This is a count of
source registrations, including conditional fallback registrations, not unique
runtime endpoints.

The original brief's endpoint premise was too broad: querying the real delete
factory already showed service calls in v1.7.7. The fallback registrations showed
only their JSON response because that is their body. The correction adds returned
handler identity and confines traversal to that body, excluding factory setup.

Without `--precise`, the baseline printed:

```
No test functions found exercising 'service.OIDCLoginService.InitiateLogin'.
No environment variable reads found.
No usage sites found for type "ErrLoginRateLimited".
```

These empty AST-only answers now also say:

```
This AST-only answer may be incomplete; run gograph build . --precise and repeat the query.
```

## Regression proof

`internal/cli/answers178_test.go` exercises CLI text/JSON and MCP. Each requested
behavior failed before its implementation and passed afterward. The recorded
red runs are in ignored `.gograph/answers178-ast-red.log`,
`answers178-factory-red.log` and `answers178-routes-red.log`. An additional
qualified-factory fixture exposed an unsafe printer invocation; its red run is
`answers178-qualified-red.log`.

A stronger endpoint assertion subsequently exposed missing environment evidence
and call-site coordinates: parser calls use the opening parenthesis position.
`answers178-handler-env-red.log` records that failure. The test now inspects the
actual endpoint call-chain and environment fields, not just the quoted source.
Existing test files and assertions were not changed.

## Limits and compatibility

Factory resolution requires precise information and one statically known return.
It follows at most eight factory calls, rejects cycles, and leaves unknown or
multiple returns explicit. Endpoint traversal retains its existing heuristic
call-graph limits. SQL and environment facts retain their existing line-based
source coordinates.

Known non-router receivers are excluded. AST-only source fragments can contain
local types whose definitions are unavailable, as an existing grouped-route
parity test demonstrates. Those remain explicitly labelled route candidates;
they are not claimed to be verified routers. A precise build checks the receiver
type, including local adapters with a string path and function handler signature.
This bounded compatibility exception is stated in CLI and MCP capabilities.

The analysis cache version changes so an upgraded build cannot reuse old route
facts as current analysis. Rebuild indexes and restart existing MCP servers after
installing the release.
