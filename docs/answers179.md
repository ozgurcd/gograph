# Gograph 1.7.9 answer regressions

Baseline: 0566c12d1d6208eeaf89dea943067f21136e4851, installed v1.7.8,
clean and equal to origin/main. Reproductions use committed snapshots exported
inside this repository's ignored scratch directory; consumer files are unchanged.
OSS snapshot: 6d994a79361f7ddb2a4e27586dcf049533db4413.
Lictor snapshot: 8d2c222b8275f7b6fcb4785d63db4bb70a208332.

## Measured answers

With a precise OSS graph, `endpoint "DELETE /api/v1/api-resources/:id"`
printed uuid.Parse, c.Param, apiResourceActor, deps.APIResourceService.Delete,
errors.Is and deps.Audit.Record twice in the returned-handler chain. The source
at internal/handlers/api_resources.go:429-461 contains each once. The breadth-first
traversal appended a discovered node both when enqueuing and when visiting it.
The correction applies to factory-returned handlers; other endpoint shapes retain
their existing behavior in this scoped release.

With an AST OSS graph, `routes HandleDeleteAPIResource --json` said
"dynamic handler — cannot be statically resolved; factory call recorded, returned
handler not resolved", without precise-build advice. AST factory rows now add:
"A precise build resolves statically known factory handlers; run gograph build . --precise".
Precise route answers are unchanged.

Lictor's `plan executor.DeclareEnvironment` said "Reads env: no", despite
os.LookupEnv(name) in internal/executor/environment.go:23-57. Plan and review
looked only at indexed literal environment keys through a name-based downstream
traversal; risk also missed dynamic keys. Direct body evidence now supplements
those existing results, preserving known keys and labelling unknown ones, such
as "os.LookupEnv (key not statically known)". No runtime values are read.

## Regression evidence

The self-contained testdata/answers179 module uses a small vendored Gin API.
internal/cli/answers179_test.go exercises CLI and MCP endpoint rows, AST/precise
route text and JSON, and plan/review/risk environment evidence with a no-read
control. Existing tests are unchanged. Initial fixture mistakes (unsupported
local router and incorrect MCP argument/response field names) were corrected.

The final fixture was replayed against byte-identical baseline endpoint, route,
plan, review and risk implementations, confirmed by git diff --exit-code. The
new unused environment helper had no effect on that run. All three tests failed:
"CLI deleteRecord appears 2 times, want 1", the same MCP duplication, missing
AST route guidance, and zero environment counts in both transports.
See ignored .gograph/answers179-baseline-red.log. Restoring the fixes made all
three top-level tests and their four AST/precise subtests pass; see
.gograph/answers179-green.log. These are deterministic tests with no clock or
network dependency.

The contributing archive count was corrected through Scrinium, whose session
finished with no missing reads or maintenance requirements.
