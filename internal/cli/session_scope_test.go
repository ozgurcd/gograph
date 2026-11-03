package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallerSessionIsolationCLI(t *testing.T) {
	t.Setenv("GOGRAPH_SESSION", "")
	root, bin := setupGraphFixture(t)
	run := func(wantOK bool, envID string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOGRAPH_SESSION="+envID)
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("%v: success=%t, want %t: %s", args, err == nil, wantOK, out)
		}
		return out
	}
	create := func() string {
		t.Helper()
		out := run(true, "", "session", "create", "same_word")
		parts := strings.Split(string(out), "\"")
		if len(parts) < 3 {
			t.Fatal("session create must return a selector")
		}
		return parts[1]
	}
	// Two owners are active, so an unselected caller cannot adopt either one
	// and remains an independent, foreign caller.
	id := create()
	id2 := create()
	if id2 == id {
		t.Fatal("concurrent session IDs collided")
	}
	namesActive := func(out []byte) bool {
		return strings.Contains(string(out), id) || strings.Contains(string(out), id2)
	}
	external := exec.Command(bin, "build", "--precise", root)
	external.Dir = t.TempDir()
	if out, err := external.CombinedOutput(); err == nil || !namesActive(out) {
		t.Fatal("explicit build target must guard its own sessions")
	}
	logPath := filepath.Join(root, ".gograph", "sessions", "session_"+id+".jsonl")
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"query", "source", "callers", "usages", "explain"} {
		run(true, "", command, "RunAudit")
	}
	for _, args := range [][]string{{"session", "end"}, {"session", "cleanup"}, {"build", "."}, {"wiki"}, {"snapshot", "save", "foreign"}} {
		out := run(false, "", args...)
		if !namesActive(out) {
			t.Fatal("refusal must name active session")
		}
	}
	after, _ := os.ReadFile(logPath)
	if !bytes.Equal(before, after) {
		t.Fatal("non-owner changed owner's audit bytes")
	}
	if out := run(false, id, "query", "RunAudit"); !strings.Contains(string(out), "requires an intention") {
		t.Fatal("owner must supply intention")
	}
	run(true, id, "plan", "RunAudit", "-i", "fixture plan")
	run(true, "", "review", "RunAudit", "--session-id", id, "-i", "fixture review")
	run(true, id2, "plan", "RunAudit", "-i", "second caller")
	run(false, id, "session", "cleanup")
	run(true, id, "session", "end")
	run(true, id2, "session", "end")
	var report struct {
		TotalCommands   int     `json:"total_commands"`
		PlanRun         bool    `json:"plan_run"`
		ReviewRun       bool    `json:"review_run"`
		ComplianceScore float64 `json:"compliance_score"`
		Grade           string  `json:"grade"`
	}
	if err := json.Unmarshal(run(true, "", "session", "audit", id, "--json"), &report); err != nil {
		t.Fatal(err)
	}
	if report.TotalCommands != 3 || !report.PlanRun || !report.ReviewRun || report.ComplianceScore != 100 || report.Grade != "A (Highly Compliant)" {
		t.Fatalf("owner fixture grade changed: %+v", report)
	}
}

// Each agent shell command is a new process without GOGRAPH_SESSION. The
// repository's only active session, created by this host's CLI, is adopted so
// build, plan, review and end work and are recorded without a selector.
func TestCLIAdoptsSoleLocalSessionAcrossShellCalls(t *testing.T) {
	t.Setenv("GOGRAPH_SESSION", "")
	root, bin := setupGraphFixture(t)
	run := func(wantOK bool, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOGRAPH_SESSION=")
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("%v: success=%t, want %t: %s", args, err == nil, wantOK, out)
		}
		return out
	}
	parts := strings.Split(string(run(true, "session", "create", "solo")), "\"")
	if len(parts) < 3 {
		t.Fatal("session create must return a selector")
	}
	id := parts[1]

	run(true, "build", ".")
	if out := run(false, "query", "RunAudit"); !strings.Contains(string(out), "requires an intention") {
		t.Fatalf("an adopted session must enforce intentions: %s", out)
	}
	run(true, "plan", "RunAudit", "-i", "plan from a fresh shell")
	run(true, "review", "RunAudit", "-i", "review from a fresh shell")

	var report struct {
		PlanRun   bool `json:"plan_run"`
		ReviewRun bool `json:"review_run"`
	}
	if err := json.Unmarshal(run(true, "session", "audit", "--json"), &report); err != nil {
		t.Fatal(err)
	}
	if !report.PlanRun || !report.ReviewRun {
		t.Fatalf("plan and review from fresh shells must be audited: %+v", report)
	}
	if out := run(true, "session", "end"); !strings.Contains(string(out), id) {
		t.Fatalf("session end without a selector must end the adopted session: %s", out)
	}

	// A positional ID ends a session even when adoption is ambiguous.
	first := strings.Split(string(run(true, "session", "create", "one")), "\"")[1]
	second := strings.Split(string(run(true, "session", "create", "two")), "\"")[1]
	if out := run(false, "build", "."); !strings.Contains(string(out), "--session-id") {
		t.Fatalf("an ambiguous refusal must say how to select a session: %s", out)
	}
	run(true, "session", "end", first)
	run(true, "session", "end", second)
}
