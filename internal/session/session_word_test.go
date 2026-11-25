package session

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A session created with an intention records it as the default, and an
// audit that names a creation word grades every session created with it.
func TestSessionDefaultIntentionAndWordAudit(t *testing.T) {
	root := t.TempDir()
	first, err := StartCallerSessionWithIntentionAt(root, "task", "", OwnerCLI, "fix the parser")
	if err != nil {
		t.Fatal(err)
	}
	if got := SessionIntentionAt(root, first); got != "fix the parser" {
		t.Fatalf("SessionIntentionAt = %q", got)
	}
	if err := LogCallerCommandAt(root, first, "plan", []string{"Parse"}, "fix the parser", time.Millisecond, "success"); err != nil {
		t.Fatal(err)
	}
	if _, err := EndCallerSessionAt(root, first); err != nil {
		t.Fatal(err)
	}
	second, err := StartCallerSessionAt(root, "task", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := SessionIntentionAt(root, second); got != "" {
		t.Fatalf("a session without -i has no default, got %q", got)
	}
	if err := LogCallerCommandAt(root, second, "review", []string{"--uncommitted"}, "review", time.Millisecond, "success"); err != nil {
		t.Fatal(err)
	}
	if _, err := EndCallerSessionAt(root, second); err != nil {
		t.Fatal(err)
	}
	other, err := StartCallerSessionAt(root, "task_other", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EndCallerSessionAt(root, other); err != nil {
		t.Fatal(err)
	}

	audit := func(selector string) AuditReport {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := RunAuditToAt(root, selector, true, &stdout, &stderr); code != 0 {
			t.Fatalf("audit %q: %s", selector, stderr.String())
		}
		var report AuditReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	one := audit(second)
	if one.PlanRun || !one.ReviewRun || len(one.SessionIDs) != 0 {
		t.Fatalf("single-session audit: %+v", one)
	}
	if !strings.Contains(strings.Join(one.Recommendations, "\n"), "session audit task") {
		t.Fatalf("single-session audit should point at the word: %v", one.Recommendations)
	}
	word := audit("task")
	if word.SessionID != "task" || len(word.SessionIDs) != 2 || !word.PlanRun || !word.ReviewRun || !strings.HasPrefix(word.Grade, "A") {
		t.Fatalf("word audit must grade both task sessions, not task_other: %+v", word)
	}
	var stdout, stderr bytes.Buffer
	if code := RunAuditToAt(root, "missing", true, &stdout, &stderr); code == 0 {
		t.Fatal("an unknown selector must fail")
	}
}
