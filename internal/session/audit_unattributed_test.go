package session

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// GG-07 / F-R1-1: with several active sessions an unselected plan/review could
// belong to any of them. No session is credited; every session whose lifetime
// holds the call reports it as unattributed.
func TestAuditDoesNotCreditAnotherLanesUnselectedCall(t *testing.T) {
	mine, other := startTwoSessions(t)
	if err := LogCallerCommandAt("", "", "plan", []string{"ParseGrant"}, "edit", time.Millisecond, "success"); err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "review", []string{"--uncommitted"}, "verify", time.Millisecond, "success"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{mine, other} {
		report := auditJSON(t, id)
		if report.PlanRun || report.ReviewRun {
			t.Fatalf("%s credited with an ambiguous call: plan_run=%v review_run=%v", id, report.PlanRun, report.ReviewRun)
		}
		if report.UnselectedPlan != 0 || report.UnselectedReview != 0 {
			t.Fatalf("%s: exact credit plan=%d review=%d", id, report.UnselectedPlan, report.UnselectedReview)
		}
		if report.UnattributedPlan != 1 || report.UnattributedReview != 1 {
			t.Fatalf("%s: unattributed plan=%d review=%d", id, report.UnattributedPlan, report.UnattributedReview)
		}
		if len(report.PlanSymbols) != 0 || len(report.ReviewTargets) != 0 {
			t.Fatalf("%s: coverage from uncredited calls plan=%v review=%v", id, report.PlanSymbols, report.ReviewTargets)
		}
	}
}

// An unselected call made while one session was the only active one can only be
// that session's; it stays credited to it after a second session appears and is
// never credited to the second one.
func TestAuditCreditsUnselectedCallToTheOnlyActiveSession(t *testing.T) {
	chdir(t)
	first, err := StartCallerSessionAt("", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "plan", []string{"ParseGrant"}, "edit", time.Millisecond, "success"); err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "review", []string{"--uncommitted"}, "verify", time.Millisecond, "success"); err != nil {
		t.Fatal(err)
	}
	second, err := StartCallerSessionAt("", "second", "")
	if err != nil {
		t.Fatal(err)
	}
	report := auditJSON(t, first)
	if !report.PlanRun || !report.ReviewRun || report.UnselectedPlan != 1 || report.UnselectedReview != 1 {
		t.Fatalf("sole-active credit missing: %+v", report)
	}
	if !reflect.DeepEqual(report.PlanSymbols, []string{"ParseGrant"}) || !reflect.DeepEqual(report.ReviewTargets, []string{"--uncommitted"}) {
		t.Fatalf("coverage plan=%v review=%v", report.PlanSymbols, report.ReviewTargets)
	}
	if report.UnattributedPlan != 0 || report.UnattributedReview != 0 {
		t.Fatalf("exact calls also shown as unattributed: %+v", report)
	}
	later := auditJSON(t, second)
	if later.PlanRun || later.ReviewRun || later.UnattributedPlan != 0 || later.UnattributedReview != 0 {
		t.Fatalf("later session touched by an earlier session's call: %+v", later)
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

func TestAuditIgnoresJournalEntriesThatAreNotExactSuccesses(t *testing.T) {
	mine, other := startTwoSessions(t)
	journal := filepath.Join(sessionsDirAbs(), "unattributed.jsonl")
	now := time.Now().Format(time.RFC3339)
	lines := []string{
		// no recorded active sessions (v1.7.16+ba9e51f shape): cannot be matched
		`{"type":"command","timestamp":"` + now + `","command":"plan","args":["NoIDs"],"status":"success"}`,
		// recorded for a different session only
		`{"type":"command","timestamp":"` + now + `","command":"plan","args":["Elsewhere"],"status":"success","active_sessions":["` + other + `"]}`,
		`{"type":"command","timestamp":"` + now + `","command":"review","args":["--uncommitted"],"status":"invocation_error","active_sessions":["` + mine + `"]}`,
		`{"type":"command","timestamp":"` + now + `","command":"plan","args":["Bad"],"status":"failure","active_sessions":["` + mine + `"]}`,
		`not json`,
	}
	if err := os.WriteFile(journal, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report := auditJSON(t, mine)
	if report.PlanRun || report.ReviewRun || report.UnselectedPlan+report.UnselectedReview != 0 || report.UnattributedPlan+report.UnattributedReview != 0 {
		t.Fatalf("non-exact entries credited or shown: %+v", report)
	}
}

// F-R1-2: a call made before a session existed is never credited to it, even
// when both fall in the same wall-clock second.
func TestAuditDoesNotCreditACallMadeBeforeTheSessionStarted(t *testing.T) {
	chdir(t)
	first, err := StartCallerSessionAt("", "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "plan", []string{"Early"}, "edit", 0, "success"); err != nil {
		t.Fatal(err)
	}
	if _, err := EndCallerSessionAt("", first); err != nil {
		t.Fatal(err)
	}
	second, err := StartCallerSessionAt("", "second", "")
	if err != nil {
		t.Fatal(err)
	}
	if report := auditJSON(t, second); report.PlanRun || report.UnselectedPlan != 0 || report.UnattributedPlan != 0 {
		t.Fatalf("earlier call credited to a later session: %+v", report)
	}
}

// F-R1-3: only successful plan/review calls count toward the cap; failures are
// not journaled at all.
func TestUnattributedJournalSkipsFailures(t *testing.T) {
	startTwoSessions(t)
	journal := filepath.Join(sessionsDirAbs(), "unattributed.jsonl")
	for _, status := range []string{"failure", "invocation_error"} {
		if err := LogCallerCommandAt("", "", "plan", []string{"X"}, "", 0, status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatalf("failed calls journaled: %v", err)
	}
}

// A full journal is compacted, not pruned: identical calls of sessions that are
// gone merge into one counted line, so live calls fit and nothing is lost.
func TestUnattributedJournalCompactsEndedSessionsWhenFull(t *testing.T) {
	mine, _ := startTwoSessions(t)
	journal := filepath.Join(sessionsDirAbs(), "unattributed.jsonl")
	dead := []byte(`{"type":"command","timestamp":"2020-01-01T00:00:00Z","command":"plan","args":["Dead"],"status":"success","active_sessions":["gone"]}` + "\n")
	n := maxUnattributedJournalBytes/len(dead) + 1
	if err := os.WriteFile(journal, bytes.Repeat(dead, n), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "plan", []string{"Live"}, "", 0, "success"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxUnattributedJournalBytes {
		t.Fatalf("journal over its cap after compaction: %d", len(data))
	}
	if !strings.Contains(string(data), fmt.Sprintf(`"count":%d`, n)) {
		t.Fatalf("ended session's %d calls not kept as a count: %s", n, data)
	}
	if report := auditJSON(t, mine); report.UnattributedPlan != 1 || report.JournalDropped != 0 {
		t.Fatalf("live entry lost after compaction: %+v", report)
	}
}

// R2-1: credit follows the eligibility rule of implicit adoption. A sole active
// session that adoption refuses (MCP-owned) is not credited an unselected call.
func TestAuditCreditFollowsImplicitAdoptionEligibility(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner string
	}{{"cli", OwnerCLI}, {"mcp", OwnerMCP}} {
		t.Run(tc.name, func(t *testing.T) {
			chdir(t)
			id, err := StartCallerSessionOwnedAt("", "sole", "", tc.owner)
			if err != nil {
				t.Fatal(err)
			}
			adopted, err := ImplicitCallerSessionAt("")
			if err != nil {
				t.Fatal(err)
			}
			if err := LogCallerCommandAt("", "", "plan", []string{"X"}, "", 0, "success"); err != nil {
				t.Fatal(err)
			}
			report := auditJSON(t, id)
			if report.PlanRun != (adopted == id) {
				t.Fatalf("adoption=%q but credited=%v: credit and adoption disagree", adopted, report.PlanRun)
			}
			if !report.PlanRun && report.UnattributedPlan != 1 {
				t.Fatalf("refused call not shown as unattributed: %+v", report)
			}
		})
	}
}

// R2-2: the audit of an ended session keeps its credit after the journal fills
// with later calls, and every call is either counted or reported as dropped.
func TestAuditKeepsEndedSessionCreditWhenJournalFills(t *testing.T) {
	for _, distinct := range []bool{false, true} {
		name := "same-symbol"
		if distinct {
			name = "distinct-symbols"
		}
		t.Run(name, func(t *testing.T) {
			chdir(t)
			a, err := StartCallerSessionAt("", "a", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := LogCallerCommandAt("", "", "plan", []string{"AEvidence"}, "", 0, "success"); err != nil {
				t.Fatal(err)
			}
			if _, err := EndCallerSessionAt("", a); err != nil {
				t.Fatal(err)
			}
			b, err := StartCallerSessionAt("", "b", "")
			if err != nil {
				t.Fatal(err)
			}
			const calls = 600
			for i := 0; i < calls; i++ {
				sym := "BSym"
				if distinct {
					sym = fmt.Sprintf("BSym%04d", i)
				}
				if err := LogCallerCommandAt("", "", "plan", []string{sym}, "", 0, "success"); err != nil {
					t.Fatal(err)
				}
			}
			ra := auditJSON(t, a)
			if !ra.PlanRun || ra.UnselectedPlan != 1 || !reflect.DeepEqual(ra.PlanSymbols, []string{"AEvidence"}) {
				t.Fatalf("ended session lost its credit: %+v", ra)
			}
			rb := auditJSON(t, b)
			if got := rb.UnselectedPlan + rb.JournalDropped; got != calls {
				t.Fatalf("b: credited %d + dropped %d != %d calls (silent loss)", rb.UnselectedPlan, rb.JournalDropped, calls)
			}
			if !distinct && rb.JournalDropped != 0 {
				t.Fatalf("repeated calls should compact, not drop: %+v", rb)
			}
			if distinct && rb.JournalDropped == 0 {
				t.Fatalf("overflow of distinct calls not surfaced: %+v", rb)
			}
			info, err := os.Stat(filepath.Join(sessionsDirAbs(), "unattributed.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() > int64(maxUnattributedJournalBytes) {
				t.Fatalf("journal over cap: %d", info.Size())
			}
		})
	}
}

// Distinct entries that fill the budget are kept; the new call is counted as
// dropped, shown by the audit, and the journal stays within its cap.
func TestUnattributedJournalIsBounded(t *testing.T) {
	mine, other := startTwoSessions(t)
	journal := filepath.Join(sessionsDirAbs(), "unattributed.jsonl")
	var seed []byte
	for i := 0; len(seed) < maxUnattributedJournalBytes-unattributedDroppedReserve; i++ {
		line, err := json.Marshal(unattributedLogEntry{
			CommandLogEntry: CommandLogEntry{Type: "command", Timestamp: "2020-01-01T00:00:00Z", Command: "plan", Args: []string{fmt.Sprintf("Live%05d", i)}, Status: "success"},
			ActiveSessions:  []string{mine, other},
		})
		if err != nil {
			t.Fatal(err)
		}
		seed = append(append(seed, line...), '\n')
	}
	if err := os.WriteFile(journal, seed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LogCallerCommandAt("", "", "plan", []string{"X"}, "", 0, "success"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(journal)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > int64(maxUnattributedJournalBytes) {
		t.Fatalf("journal grew past its cap: %d", info.Size())
	}
	report := auditJSON(t, mine)
	if report.JournalDropped != 1 {
		t.Fatalf("dropped call not reported: %+v", report)
	}
}
