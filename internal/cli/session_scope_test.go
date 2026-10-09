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
	id := create()
	external := exec.Command(bin, "build", "--precise", root)
	external.Dir = t.TempDir()
	if out, err := external.CombinedOutput(); err == nil || !strings.Contains(string(out), id) {
		t.Fatal("explicit build target must guard its own sessions")
	}
	logPath := filepath.Join(root, ".gograph", "sessions", "session_"+id+".jsonl")
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	run(true, "", "query", "RunAudit")
	for _, args := range [][]string{{"session", "end"}, {"session", "cleanup"}, {"build", "."}, {"wiki"}, {"snapshot", "save", "foreign"}} {
		out := run(false, "", args...)
		if !strings.Contains(string(out), id) {
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
	id2 := create()
	if id2 == id {
		t.Fatal("concurrent session IDs collided")
	}
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
