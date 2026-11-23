package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMakeVulnerabilityChecksUseFreshExplicitInputs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release Makefile uses a POSIX shell")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is unavailable")
	}
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	native := dryRunMake(t, makePath, repositoryRoot, "vulnerability-check", "GRYPE=grype-contract")
	assertOrderedText(t, native,
		"go build ",
		"grype-contract file:go.mod --fail-on high",
		"grype-contract file:bin/gograph --fail-on high",
	)
	if strings.Contains(native, "grype-contract dir:.") {
		t.Fatalf("native vulnerability gate scans ambient repository artifacts:\n%s", native)
	}

	const output = ".release-work/makefile-contract"
	release := dryRunMake(t, makePath, repositoryRoot,
		"release-artifact-vulnerability-check",
		"MCPB_OUTPUT="+output,
		"GRYPE=grype-contract",
	)
	// The snapshot is the release artifact gate: its build hook scans each
	// binary, after one database refresh.
	assertOrderedText(t, release,
		"render-goreleaser",
		`--dist "`+output+`/goreleaser-dist"`,
		"grype-contract db update",
		`GRYPE="grype-contract" go run github.com/goreleaser/goreleaser/v2@v2.17.0 release --snapshot --clean --skip=publish`,
	)
	if strings.Contains(release, "grype-contract dir:.") || strings.Contains(release, "file:dist/") || strings.Contains(release, "scan-release-artifacts") {
		t.Fatalf("release vulnerability gate scans ambient repository artifacts:\n%s", release)
	}
}

// The release vulnerability gate is a GoReleaser build hook, so every binary
// GoReleaser builds is scanned before it is archived or published, and the
// scanned bytes are the published ones. No glob over a dist directory decides
// what is scanned, so a missing, stale, or extra archive cannot escape it.
func TestGoReleaserScansEveryBuiltBinaryBeforePublishing(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(repositoryRoot, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	config := read(".goreleaser.yaml")
	// One build entry: a second entry would not carry the hook.
	if got := strings.Count(config, "\n    main: "); got != 1 {
		t.Fatalf(".goreleaser.yaml has %d build entries, want 1", got)
	}
	builds := strings.Index(config, "\nbuilds:\n")
	archives := strings.Index(config, "\narchives:\n")
	if builds < 0 || archives < builds {
		t.Fatal(".goreleaser.yaml must declare builds before archives")
	}
	build := config[builds : archives+1]
	for _, required := range []string{
		"\n    hooks:\n      post:\n",
		`        - cmd: '{{ envOrDefault "GRYPE" "grype" }} file:{{ .Path }} --fail-on high'` + "\n",
		// The hooks run in parallel; they only read a database refreshed once beforehand.
		"            - GRYPE_DB_AUTO_UPDATE=false\n",
	} {
		if !strings.Contains(build, required) {
			t.Errorf(".goreleaser.yaml build is missing %q", required)
		}
	}
	if got := strings.Count(build, "- cmd:"); got != 1 {
		t.Errorf(".goreleaser.yaml build has %d hooks, want only the scan", got)
	}

	workflow := read(".github/workflows/release.yml")
	// Every GoReleaser run in the release job builds, so each one gets Grype,
	// and the database is refreshed once before the first one.
	runs := strings.Count(workflow, "uses: goreleaser/goreleaser-action@")
	if runs == 0 || strings.Count(workflow, "          GRYPE: ${{ steps.grype.outputs.cmd }}\n") != runs+1 {
		t.Errorf("release.yml must pass Grype to the database update and to each of its %d GoReleaser runs", runs)
	}
	assertOrderedText(t, workflow,
		"uses: anchore/scan-action/download-grype@e1165082ffb1fe366ebaf02d8526e7c4989ea9d2",
		"grype-version: v0.116.1",
		`run: '"$GRYPE" db update'`,
		"uses: goreleaser/goreleaser-action@",
		"args: release --clean\n",
	)
}

// Every verification gate is defined once, in checks.yml. CI and Release call
// that file and must not carry a second copy of any gate.
func TestGitHubWorkflowsUseTheCurrentInputVulnerabilityGates(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	const (
		toolchain  = "uses: ozgurcd/lictor/.github/actions/go-toolchain@fda0c2afaa765f190b7e331113a71570db802199"
		callChecks = "uses: ./.github/workflows/checks.yml"
		// Gates that a full CI run on the identical tree already passed.
		reusable = "if: ${{ !inputs.vulnerability-only && steps.head.outputs.commit != inputs.tested-commit }}"
	)
	gateCommands := []string{"go test", "go vet", "go build", "staticcheck", "golangci-lint", "govulncheck", "grype", "--snapshot", "hugo "}
	// The release job installs Grype only for the GoReleaser build hook.
	releaseForbidden := []string{"go test", "go vet", "go build", "staticcheck", "golangci-lint", "govulncheck", "grype file:", "grype dir:", "--snapshot", "hugo "}
	tests := []struct {
		path       string
		toolchains int
		required   []string
		forbidden  []string
	}{
		{
			path:       ".github/workflows/checks.yml",
			toolchains: 1,
			required: []string{
				"GOLANGCI_LINT_VERSION: 'v2.14.0'",
				"GOVULNCHECK_VERSION: 'v1.3.0'",
				"go mod verify",
				"go mod tidy -diff",
				"gofmt -l .",
				// Every package is tested without -race; race tests run locally.
				"      - name: Run Tests\n        " + reusable + "\n        run: go test -count=1 ./...\n",
				"go vet ./...",
				"staticcheck ./...",
				"go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION} run ./...",
				"go run golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION} ./...",
				"anchore/scan-action/download-grype@e1165082ffb1fe366ebaf02d8526e7c4989ea9d2",
				"grype-version: v0.116.1",
				"make vulnerability-check",
				"cmp server.json \"$server\"",
				"hugo --source docs-site --renderToMemory --minify",
				// test-proof.yml finds the suite by these names.
				"\n  verify:\n",
				"      - name: Verify dependencies and formatting\n        " + reusable + "\n",
				"      - name: Vet\n        " + reusable + "\n",
				"      - name: Run static analysis\n        " + reusable + "\n",
				"      - name: Build, validate, and smoke-test MCP bundles\n        " + reusable + "\n",
				"      - name: Build documentation\n        " + reusable + "\n",
			},
			forbidden: []string{
				"go test -count=1 -v ./...", "-race", "full-race", "grype dir:.", "go install honnef.co/go/tools/cmd/staticcheck", "go install github.com/golangci/golangci-lint", "go install golang.org/x/vuln/cmd/govulncheck",
				// Tests, vet, `make vulnerability-check`, and the MCPB steps already compile and link every package.
				"go build",
				// The release job scans the build it publishes; a snapshot here would be a second, different build.
				"--snapshot", "scan-release-artifacts", "goreleaser/goreleaser-action@", "goreleaser/v2@",
			},
		},
		{
			path:       ".github/workflows/ci.yml",
			toolchains: 0,
			required: []string{
				callChecks,
				"uses: ./.github/workflows/test-proof.yml",
				"\n  checks:\n",
				// Branch protection on main requires a check with this exact name.
				"\n  build-and-test:\n",
				"if: ${{ always() }}",
				"name: tested-commit-${{ github.sha }}",
				// A main push whose identical tree a CI run already verified runs no checks;
				// the nightly run refreshes the vulnerability scans.
				"if: ${{ !cancelled() && needs.release-tag.outputs.tagged != 'true' && needs.test-proof.outputs.verified != 'true' }}",
				"needs: [release-tag, test-proof, checks]\n    if: ${{ always() }}",
				"VERIFIED: ${{ needs.test-proof.outputs.verified }}",
				`if [[ "$RESULT" == skipped && "$VERIFIED" == true ]]; then`,
			},
			forbidden: append([]string{"actions/checkout@", "full-race"}, gateCommands...),
		},
		{
			// Compiles to warm main's caches; it must never run a gate.
			path:       ".github/workflows/warm-caches.yml",
			toolchains: 1,
			required: []string{
				`mapfile -t tested < <(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' ./...)`,
				`go test -c -o "$RUNNER_TEMP/plain-test-binaries/" "${tested[@]}"`,
				"      - go.mod\n      - go.sum\n",
			},
			forbidden: []string{"go test -count", "go test -race", "go vet", "staticcheck ./...", "golangci-lint", "govulncheck", "grype", "make vulnerability-check", "--snapshot", "hugo ", "uses: ./.github/workflows/checks.yml"},
		},
		{
			path:       ".github/workflows/release.yml",
			toolchains: 3,
			required: []string{
				callChecks,
				"uses: ozgurcd/gograph/.github/workflows/test-proof.yml@main",
				"release-tag: ${{ inputs.tag || github.ref_name }}",
			},
			forbidden: append([]string{"require-full-race"}, releaseForbidden...),
		},
	}
	for _, test := range tests {
		data, err := os.ReadFile(filepath.Join(repositoryRoot, test.path))
		if err != nil {
			t.Fatal(err)
		}
		contents := string(data)
		if got := strings.Count(contents, toolchain); got != test.toolchains {
			t.Errorf("%s has %d shared toolchain steps, want %d", test.path, got, test.toolchains)
		}
		for _, duplicate := range []string{"actions/setup-go@", "\n  GO_VERSION:", "STATICCHECK_VERSION:", "STATICCHECK_TAG:"} {
			if strings.Contains(contents, duplicate) {
				t.Errorf("%s contains duplicate toolchain setup %q", test.path, duplicate)
			}
		}
		for _, required := range test.required {
			if !strings.Contains(contents, required) {
				t.Errorf("%s is missing %q", test.path, required)
			}
		}
		for _, forbidden := range test.forbidden {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s contains %q, which belongs only in checks.yml", test.path, forbidden)
			}
		}
	}
}

// A verifier version is pinned in the Makefile for local runs and in the
// workflows for hosted runs. The copies must name the same version.
func TestWorkflowVerifierPinsMatchTheMakefile(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(repositoryRoot, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	pin := func(path, contents, prefix string) string {
		t.Helper()
		var found []string
		for _, line := range strings.Split(contents, "\n") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
				found = append(found, strings.Trim(strings.TrimSpace(value), `'"`))
			}
		}
		if len(found) != 1 || found[0] == "" {
			t.Fatalf("%s must pin %q exactly once, found %q", path, prefix, found)
		}
		return found[0]
	}
	makefile := read("Makefile")
	checks := read(".github/workflows/checks.yml")
	for _, name := range []string{"GOLANGCI_LINT_VERSION", "GOVULNCHECK_VERSION"} {
		want := pin("Makefile", makefile, "override "+name+" :=")
		if got := pin("checks.yml", checks, name+":"); got != want {
			t.Errorf("checks.yml pins %s %s, Makefile pins %s", name, got, want)
		}
	}
	// GoReleaser runs only in the release job and the local snapshot.
	release := read(".github/workflows/release.yml")
	if got, want := pin("release.yml", release, "GORELEASER_VERSION:"), pin("Makefile", makefile, "override GORELEASER_VERSION :="); got != want {
		t.Errorf("release.yml pins GoReleaser %s, Makefile pins %s", got, want)
	}
	if got, want := pin("release.yml", release, "grype-version:"), pin("checks.yml", checks, "grype-version:"); got != want {
		t.Errorf("release.yml pins Grype %s, checks.yml pins %s", got, want)
	}
	if got, want := pin("hugo.yml", read(".github/workflows/hugo.yml"), "HUGO_VERSION:"), pin("checks.yml", checks, "HUGO_VERSION:"); got != want {
		t.Errorf("hugo.yml pins Hugo %s, checks.yml pins %s", got, want)
	}
}

func TestReleaseUsesCurrentHomebrewCaskPublishing(t *testing.T) {
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path      string
		required  []string
		forbidden []string
	}{
		{
			path: ".goreleaser.yaml",
			required: []string{
				"homebrew_casks:",
				"binaries:",
				"com.apple.quarantine",
				"      - linux",
				"      - darwin",
				"Windows binaries are no longer built or published",
			},
			forbidden: []string{"\nbrews:", "directory: Formula", "- windows", "goos: windows"},
		},
		{
			path: ".github/workflows/release.yml",
			required: []string{
				"dist/homebrew/Casks/gograph.rb",
				"Casks/gograph.rb",
				"rewrite-homebrew-cask --input \"$cask_path\" --output \"$cask_path\"",
				"gh auth setup-git",
				`grep -Fq "/releases/download/v#{version}/$asset" "$cask_path"`,
				`sed -i "s/sha256 \"$local_hash\"/sha256 \"$published_hash\"/" "$cask_path"`,
				`{"gograph":"gograph"}`,
				"rm -f \"$tap_dir/Formula/gograph.rb\"",
			},
			forbidden: []string{
				`formula_path="dist/homebrew/Formula/gograph.rb"`,
				"contents/Formula/gograph.rb > \"$remote_path\"",
				`test "$local_hash" = "$published_hash"`,
			},
		},
	}
	for _, test := range tests {
		data, err := os.ReadFile(filepath.Join(repositoryRoot, test.path))
		if err != nil {
			t.Fatal(err)
		}
		contents := string(data)
		for _, required := range test.required {
			if !strings.Contains(contents, required) {
				t.Errorf("%s is missing %q", test.path, required)
			}
		}
		for _, forbidden := range test.forbidden {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s retains obsolete Homebrew configuration %q", test.path, forbidden)
			}
		}
	}
}

func TestMakeAnalysisToolsUsePinnedVersions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release Makefile uses a POSIX shell")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is unavailable")
	}
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		target  string
		command string
	}{
		{"staticcheck", "staticcheck --version | grep -F \"(0.8.2)\""},
		{"staticcheck", "staticcheck ./..."},
		{"lint", "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./..."},
		{"govulncheck", "go run golang.org/x/vuln/cmd/govulncheck@v1.3.0 ./..."},
	}
	for _, test := range tests {
		output := dryRunMake(t, makePath, repositoryRoot, test.target)
		if !strings.Contains(output, test.command) {
			t.Errorf("make %s is missing pinned command %q:\n%s", test.target, test.command, output)
		}
	}
}

func TestMakeReleaseTestsDisableGoTestCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release Makefile uses a POSIX shell")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is unavailable")
	}
	repositoryRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	output := dryRunMake(t, makePath, repositoryRoot, "test", "GRYPE=grype-contract")
	for _, command := range []string{
		"go test -count=1 -v ./...",
		"go test -count=1 -race ./...",
	} {
		if !strings.Contains(output, command) {
			t.Fatalf("make test is missing %q:\n%s", command, output)
		}
	}
}

func dryRunMake(t *testing.T, makePath, repositoryRoot string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"--dry-run", "--no-print-directory"}, args...)
	command := exec.Command(makePath, commandArgs...)
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("make %s failed: %v\n%s", strings.Join(commandArgs, " "), err, output)
	}
	return string(output)
}

func assertOrderedText(t *testing.T, output string, fragments ...string) {
	t.Helper()
	position := 0
	for _, fragment := range fragments {
		index := strings.Index(output[position:], fragment)
		if index < 0 {
			t.Fatalf("output is missing %q after byte %d:\n%s", fragment, position, output)
		}
		position += index + len(fragment)
	}
}
