# Gograph answer corrections, v1.7.7

Measured against Gograph v1.7.6 (`826435f`) and Identuum OSS `2f3917a`.
The original OSS checkout is read-only. CLI rebuilds use a `git archive` copy
under Gograph's ignored scratch directory, with Git discovery confined there.

## Measurements and regression proofs

- `tests service.OIDCLoginService.InitiateLogin` and the corresponding callback
  query answered “No test functions found”. Removing the package qualifier found
  typed calls through fields. The defect was selector matching, not missing type
  information. The new field-call fixture covers both methods through CLI and MCP.
- The environment queries answered “No environment variable reads found”. OSS
  `internal/runtime/single_replica_config.go:30` invokes a getter parameter with
  an `os.Getenv` fallback; `upstream_oidc_config.go:24` passes a constant key to
  `resolveEnvBool`. Precise analysis now records those conditional reads. The
  fixture also excludes an unrelated function parameter merely named `getenv`.
- `usages ErrLoginRateLimited` answered “No usage sites found for type”. Precise
  object references now include its uses, including `login_risk_service.go:161`.
  The fixture excludes a local variable that shadows the same name.
- `source domain.ValidatePassword` answered “ambiguous” and named both that
  function and `ValidatePasswordPolicy`. Exact matches now take precedence;
  multiple exact matches remain ambiguous. The source fixture tests CLI and MCP.
- OSS `internal/api/router.go:943` registers `healthHandler(resolved)` as the
  handler argument. The old result said “dynamic handler — cannot be statically
  resolved”. The answer and capabilities now state the exact remaining limit:
  the factory call is recorded, but its returned handler is not resolved to a
  named function or closure. This release does not claim to solve return-value
  data flow for factories.
- Each `session <verb> --help` returned the parent session help, rather than
  action-specific descriptions and options. The fixture exercises create, end,
  audit and cleanup. Help is CLI-only; MCP exposes typed lifecycle tools instead.
- v1.7.6 MCP capabilities reported its own version and a generic restart note,
  but no installed-version comparison. The installed executable's Go build
  metadata carries its module version and `main.version` linker setting.
  The drift fixture places a locally built newer executable on PATH and proves
  both CLI and MCP report restart required without executing that binary.
  Other running processes, wrappers and unversioned binaries remain outside this
  comparison, as capabilities state.

The first full verification exposed an existing exact-output assertion for a
synthetic development version. A deterministic `0.0.0-test.fixed` regression
reproduced the extra warning. Development and prerelease versions now report an
unknown comparison rather than claiming an upgrade; the new regression and the
unchanged executable-version test both pass. Released versions still report
when the installed release is newer.

`internal/cli/answers177_test.go` contains the seven regression tests. Each was
observed failing before its implementation; all seven then passed with
`go test ./internal/cli -run '^TestAnswers177' -count=1 -v`. Fixtures are in
`testdata/answers177` and `testdata/versionprobe`. Existing tests are unchanged.
The OSS snapshot was separately checked through CLI and the MCP stdio transport.

Getter facts carry precise provenance and are excluded from incremental AST
reuse. Variable references also remain precise-only. The analysis-cache version
is incremented; rebuild precise indexes after upgrading.

The owner-authorized publication sequence is a fast-forward main push followed
by one annotated version tag, after verification. This differs from the normal
automatic helper's atomic main-and-tag push; it does not weaken release checks.
