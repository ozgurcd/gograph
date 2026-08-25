package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLinkedDoctorVersion(t *testing.T) {
	for _, test := range []struct {
		flags, want string
	}{
		{`-s -w -X main.version=1.6.1 -X main.releaseVersionMarker=x`, "1.6.1"},
		{`"-X main.version=1.6.1-local-dirty"`, "1.6.1-local-dirty"},
		{`-s -w`, ""},
	} {
		if got := linkedDoctorVersion(test.flags); got != test.want {
			t.Errorf("linkedDoctorVersion(%q) = %q, want %q", test.flags, got, test.want)
		}
	}
}

func TestDoctorVersionFindingsWarnWhenPATHShadowsNewer(t *testing.T) {
	document := doctorDocument{Candidates: []doctorExecutable{
		{Path: "/old/gograph", Version: "1.5.9", Running: true, PATHResolved: true},
		{Path: "/new/gograph", Version: "1.6.1"},
	}}
	appendDoctorVersionFindings(&document)
	if len(document.Findings) != 1 || document.Findings[0].Code != "running_older_than_available" {
		t.Fatalf("unexpected findings: %#v", document.Findings)
	}
}

func TestDoctorDoesNotOrderDirtyBuildAgainstStableRelease(t *testing.T) {
	document := doctorDocument{Candidates: []doctorExecutable{
		{Path: "/work/gograph", Version: "1.6.0-deadbee-dirty", Running: true},
		{Path: "/stable/gograph", Version: "1.6.0", PATHResolved: true},
	}}
	appendDoctorVersionFindings(&document)
	if len(document.Findings) != 0 {
		t.Fatalf("dirty development build received misleading version ordering: %#v", document.Findings)
	}
}

func TestDoctorJSONContract(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "gograph"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	pathDir := t.TempDir()
	pathBinary := filepath.Join(pathDir, name)
	if err := os.Symlink(bin, pathBinary); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink test binary: %v", err)
		}
		t.Fatal(err)
	}

	t.Setenv("PATH", pathDir)
	oldJSON := jsonMode
	jsonMode = true
	defer func() { jsonMode = oldJSON }()
	exit, stdout := captureMachineOutput(t, func() int { return runDoctor(nil) })
	if exit != 0 {
		t.Fatalf("doctor --json exit = %d\n%s", exit, stdout)
	}
	var document doctorDocument
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, stdout)
	}
	if document.SchemaVersion != doctorSchemaVersion || document.Running.Path == "" || document.PATHResolved == "" || len(document.Candidates) != 1 {
		t.Fatalf("unexpected doctor document: %#v", document)
	}
	if strings.Contains(stdout, "\n{") {
		t.Fatalf("doctor emitted more than one JSON document: %q", stdout)
	}
}

func TestDoctorReportsMissingRepositoryGraph(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/doctor\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repository, findings := inspectDoctorRepository(root, nil)
	if repository == nil || repository.Root != root || repository.GraphAvailable || repository.Freshness != "unavailable" {
		t.Fatalf("repository health = %+v", repository)
	}
	if repository.DiagnosticCode != "graph_missing" || repository.Diagnostic == "" {
		t.Fatalf("repository diagnostic = %+v", repository)
	}
	if !hasDoctorFinding(findings, "repository_graph_unavailable") {
		t.Fatalf("findings = %+v", findings)
	}
}
