package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func auditJSON(t *testing.T, id string) AuditReport {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := RunAuditToAt("", id, true, &out, &errOut); code != 0 {
		t.Fatalf("audit exit %d: %s", code, errOut.String())
	}
	var report AuditReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("audit JSON: %v: %s", err, out.String())
	}
	return report
}

func startTwoSessions(t *testing.T) (mine, other string) {
	t.Helper()
	chdir(t)
	var err error
	if mine, err = StartCallerSessionAt("", "mine", ""); err != nil {
		t.Fatal(err)
	}
	if other, err = StartCallerSessionAt("", "other", ""); err != nil {
		t.Fatal(err)
	}
	return mine, other
}

// GG-07: with several active sessions an unselected plan/review adopts nothing.
// The audit must still credit them, say they were unattributed, and name the
// symbols covered.
func TestAuditCreditsUnselectedPlanAndReviewWhenSessionsAreAmbiguous(t *testing.T) {
	mine, _ := startTwoSessions(t)
	if err := LogCallerCommandAt("", "", "plan", []string{"ParseGrant"}, "edit", time.Millisecond, "success"); err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "review", []string{"--uncommitted"}, "verify", time.Millisecond, "success"); err != nil {
		t.Fatal(err)
	}
	report := auditJSON(t, mine)
	if !report.PlanRun || !report.ReviewRun {
		t.Fatalf("plan_run=%v review_run=%v; recommendations=%v", report.PlanRun, report.ReviewRun, report.Recommendations)
	}
	if report.UnattributedPlan != 1 || report.UnattributedReview != 1 {
		t.Fatalf("unattributed plan=%d review=%d", report.UnattributedPlan, report.UnattributedReview)
	}
	if !reflect.DeepEqual(report.PlanSymbols, []string{"ParseGrant"}) || !reflect.DeepEqual(report.ReviewTargets, []string{"--uncommitted"}) {
		t.Fatalf("coverage plan=%v review=%v", report.PlanSymbols, report.ReviewTargets)
	}
	if len(report.Recommendations) != 0 {
		t.Fatalf("recommendations after credited plan/review: %v", report.Recommendations)
	}
}

func TestAuditStatesSymbolCoverageOfSelectedCalls(t *testing.T) {
	chdir(t)
	id, err := StartCallerSessionAt("", "cov", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"First"}, {"Last"}, {"First"}} {
		if err := LogCallerCommandAt("", id, "plan", args, "edit", time.Millisecond, "success"); err != nil {
			t.Fatal(err)
		}
	}
	report := auditJSON(t, id)
	if !reflect.DeepEqual(report.PlanSymbols, []string{"First", "Last"}) {
		t.Fatalf("plan symbols = %v", report.PlanSymbols)
	}
	if report.UnattributedPlan != 0 {
		t.Fatalf("selected calls counted as unattributed: %d", report.UnattributedPlan)
	}
}

func TestUnattributedCallsAreJournaledOnlyForPlanReviewWithAnActiveSession(t *testing.T) {
	chdir(t)
	journal := filepath.Join(sessionsDirAbs(), "unattributed.jsonl")
	if err := LogCallerCommandAt("", "", "plan", []string{"X"}, "", 0, "success"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("journal written without any active session: %v", err)
	}
	if _, err := StartCallerSessionAt("", "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "callers", []string{"X"}, "", 0, "success"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("raw query journaled: %v", err)
	}
}

func TestAuditIgnoresUnattributedCallsOutsideWindowOrNotSuccessful(t *testing.T) {
	mine, _ := startTwoSessions(t)
	journal := filepath.Join(sessionsDirAbs(), "unattributed.jsonl")
	old := time.Now().Add(-time.Hour).Format(time.RFC3339)
	now := time.Now().Format(time.RFC3339)
	lines := []string{
		`{"type":"command","timestamp":"` + old + `","command":"plan","args":["Old"],"status":"success"}`,
		`{"type":"command","timestamp":"` + now + `","command":"review","args":["--uncommitted"],"status":"invocation_error"}`,
		`{"type":"command","timestamp":"` + now + `","command":"plan","args":["Bad"],"status":"failure"}`,
		`not json`,
	}
	if err := os.WriteFile(journal, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := auditJSON(t, mine)
	if report.PlanRun || report.ReviewRun || report.UnattributedPlan != 0 || report.UnattributedReview != 0 {
		t.Fatalf("stale or failed entries credited: %+v", report)
	}
}

func TestUnattributedJournalIsBounded(t *testing.T) {
	startTwoSessions(t)
	journal := filepath.Join(sessionsDirAbs(), "unattributed.jsonl")
	if err := os.WriteFile(journal, bytes.Repeat([]byte("x"), maxUnattributedJournalBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "plan", []string{"X"}, "", 0, "success"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(journal)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(maxUnattributedJournalBytes) {
		t.Fatalf("journal grew past its cap: %d", info.Size())
	}
}
