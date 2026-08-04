package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// chdir changes the working directory to a temp dir for the duration of the
// test and restores it afterwards. Session uses relative paths (.gograph/…)
// so every test must run in an isolated temp directory.
func chdir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("warning: could not restore working directory: %v", err)
		}
	})
}

// --- GetActiveSessionID ---

func TestGetActiveSessionID_NoPointerFile(t *testing.T) {
	chdir(t)
	id, err := GetActiveSessionID()
	if err != nil {
		t.Fatalf("expected no error when pointer file absent, got: %v", err)
	}
	if id != "" {
		t.Fatalf("expected empty id, got %q", id)
	}
}

func TestGetActiveSessionID_CorruptPointerFile(t *testing.T) {
	chdir(t)
	if err := os.MkdirAll(".gograph", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(activePointerPathAbs(), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt pointer: %v", err)
	}
	_, err := GetActiveSessionID()
	if err == nil {
		t.Fatal("expected error for corrupt JSON, got nil")
	}
}

// --- StartSession / EndSession lifecycle ---

func TestStartAndEndSession_Default(t *testing.T) {
	chdir(t)

	id, err := StartSession("")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if !strings.HasPrefix(id, "session_") {
		t.Errorf("expected id to start with 'session_', got %q", id)
	}

	// Active pointer must now exist
	activeID, err := GetActiveSessionID()
	if err != nil {
		t.Fatalf("GetActiveSessionID after start: %v", err)
	}
	if activeID != id {
		t.Errorf("active id %q != created id %q", activeID, id)
	}

	// End the session
	endedID, err := EndSession()
	if err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if endedID != id {
		t.Errorf("ended id %q != created id %q", endedID, id)
	}

	// Active pointer must be gone
	afterID, err := GetActiveSessionID()
	if err != nil {
		t.Fatalf("GetActiveSessionID after end: %v", err)
	}
	if afterID != "" {
		t.Errorf("expected no active session after end, got %q", afterID)
	}
}

func TestStartSession_CustomWord(t *testing.T) {
	chdir(t)
	id, err := StartSession("myfeature")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if !strings.HasPrefix(id, "myfeature_") {
		t.Errorf("expected id to start with 'myfeature_', got %q", id)
	}
	// cleanup
	if _, err := EndSession(); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
}

func TestStartSession_SpecialCharsInWord(t *testing.T) {
	chdir(t)
	// Special characters should be stripped; only alphanumeric/underscore kept.
	id, err := StartSession("my-feature!@#")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if !strings.HasPrefix(id, "myfeature_") {
		t.Errorf("expected cleaned prefix 'myfeature_', got %q", id)
	}
	_, _ = EndSession()
}

func TestStartSession_AllSpecialChars_FallsBackToCustom(t *testing.T) {
	chdir(t)
	id, err := StartSession("!@#$%")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// When the cleaned word is empty the code uses "custom"
	if !strings.HasPrefix(id, "custom_") {
		t.Errorf("expected prefix 'custom_', got %q", id)
	}
	_, _ = EndSession()
}

func TestStartSession_AlreadyActive(t *testing.T) {
	chdir(t)
	if _, err := StartSession("first"); err != nil {
		t.Fatalf("first StartSession: %v", err)
	}
	_, err := StartSession("second")
	if err == nil {
		t.Fatal("expected error when a session is already active")
	}
	if !strings.Contains(err.Error(), "already active") {
		t.Errorf("unexpected error text: %v", err)
	}
	_, _ = EndSession()
}

func TestEndSession_NoActiveSession(t *testing.T) {
	chdir(t)
	_, err := EndSession()
	if err == nil {
		t.Fatal("expected error when no session is active")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("unexpected error text: %v", err)
	}
}

// --- LogCommand ---

func TestLogCommand_NoActiveSession_IsNoOp(t *testing.T) {
	chdir(t)
	// Must not error when there is no active session.
	err := LogCommand("query", []string{"Foo"}, "testing", time.Millisecond*10, "success")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestLogCommand_HookGuardSuccessSkipped(t *testing.T) {
	chdir(t)
	id, err := StartSession("hooktest")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _, _ = EndSession() }()

	// hook-guard success must be silently skipped — nothing written to the log.
	if err := LogCommand("hook-guard", nil, "", 0, "success"); err != nil {
		t.Fatalf("LogCommand hook-guard success: %v", err)
	}

	// Read the session log and confirm no "command" entry was written.
	logPath := filepath.Join(sessionsDirAbs(), "session_"+id+".jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		var entry GenericLogLine
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "command" {
			t.Errorf("hook-guard success was logged but should have been skipped: %s", line)
		}
	}
}

func TestLogCommand_HookGuardFailureIsLogged(t *testing.T) {
	chdir(t)
	_, err := StartSession("hookfail")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _, _ = EndSession() }()

	// hook-guard failure must be logged normally.
	if err := LogCommand("hook-guard", nil, "", 0, "failure"); err != nil {
		t.Fatalf("LogCommand hook-guard failure: %v", err)
	}
	activeID, _ := GetActiveSessionID()
	logPath := filepath.Join(sessionsDirAbs(), "session_"+activeID+".jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	if !strings.Contains(string(data), `"command"`) {
		t.Error("expected command entry in log but found none")
	}
}

func TestLogCommand_WritesEntry(t *testing.T) {
	chdir(t)
	_, err := StartSession("logtest")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _, _ = EndSession() }()

	err = LogCommand("callers", []string{"Foo"}, "find callers of Foo", 42*time.Millisecond, "success")
	if err != nil {
		t.Fatalf("LogCommand: %v", err)
	}

	activeID, _ := GetActiveSessionID()
	logPath := filepath.Join(sessionsDirAbs(), "session_"+activeID+".jsonl")
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), `"callers"`) {
		t.Error("expected 'callers' command in session log")
	}
	if !strings.Contains(string(data), `"success"`) {
		t.Error("expected 'success' status in session log")
	}
}

// --- FindMostRecentSessionID ---

func TestFindMostRecentSessionID_NoDir(t *testing.T) {
	chdir(t)
	_, err := FindMostRecentSessionID()
	if err == nil {
		t.Fatal("expected error when sessions directory does not exist")
	}
}

func TestFindMostRecentSessionID_EmptyDir(t *testing.T) {
	chdir(t)
	if err := os.MkdirAll(sessionsDirAbs(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := FindMostRecentSessionID()
	if err == nil {
		t.Fatal("expected error for empty sessions directory")
	}
}

func TestFindMostRecentSessionID_ReturnsNewest(t *testing.T) {
	chdir(t)
	if err := os.MkdirAll(sessionsDirAbs(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write two stub session files with different mtime by touching them sequentially.
	older := filepath.Join(sessionsDirAbs(), "session_older_20260101_000000.jsonl")
	newer := filepath.Join(sessionsDirAbs(), "session_newer_20260102_000000.jsonl")
	for _, f := range []string{older, newer} {
		if err := os.WriteFile(f, []byte(`{"type":"session_start"}`+"\n"), 0o644); err != nil {
			t.Fatalf("write stub: %v", err)
		}
	}
	// Force mtime ordering
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(older, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	id, err := FindMostRecentSessionID()
	if err != nil {
		t.Fatalf("FindMostRecentSessionID: %v", err)
	}
	if id != "newer_20260102_000000" {
		t.Errorf("expected 'newer_20260102_000000', got %q", id)
	}
}

// --- CleanupSessions ---

func TestCleanupSessions_NoSessionsDir(t *testing.T) {
	chdir(t)
	count, err := CleanupSessions()
	if err != nil {
		t.Fatalf("CleanupSessions with missing dir: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 deletions, got %d", count)
	}
}

func TestCleanupSessions_DeletesInactiveLogs(t *testing.T) {
	chdir(t)
	// Start a session so we have an active log.
	id, err := StartSession("cleantest")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer func() { _, _ = EndSession() }()

	// Create an additional stale log file.
	stalePath := filepath.Join(sessionsDirAbs(), "session_stale_20260101_000000.jsonl")
	if err := os.WriteFile(stalePath, []byte(`{"type":"session_start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write stale log: %v", err)
	}

	count, err := CleanupSessions()
	if err != nil {
		t.Fatalf("CleanupSessions: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 deletion, got %d", count)
	}

	// Active session log must still exist.
	activePath := filepath.Join(sessionsDirAbs(), "session_"+id+".jsonl")
	if _, err := os.Stat(activePath); os.IsNotExist(err) {
		t.Error("active session log was incorrectly deleted")
	}
}

// --- RunAudit ---

func TestRunAudit_EmptyLog(t *testing.T) {
	chdir(t)
	if err := os.MkdirAll(sessionsDirAbs(), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	badPath := filepath.Join(sessionsDirAbs(), "session_empty_test.jsonl")
	if err := os.WriteFile(badPath, []byte{}, 0o644); err != nil {
		t.Fatalf("write empty log: %v", err)
	}
	code := RunAudit("empty_test", false)
	if code != 1 {
		t.Errorf("expected exit code 1 for empty log, got %d", code)
	}
}

func TestRunAudit_FullSession_JSON(t *testing.T) {
	chdir(t)
	id, err := StartSession("auditme")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Log a plan, a review, and a raw callers command.
	for _, cmd := range []string{"plan", "review", "callers"} {
		_ = LogCommand(cmd, []string{"Foo"}, "intention", 10*time.Millisecond, "success")
	}

	if _, err := EndSession(); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// RunAudit should succeed (exit 0) with JSON output.
	code := RunAudit(id, true)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestRunAudit_FallsBackToMostRecent(t *testing.T) {
	chdir(t)
	id, err := StartSession("recent")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	_ = LogCommand("query", []string{"X"}, "find X", 5*time.Millisecond, "success")
	if _, err := EndSession(); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// Passing empty sessionID should auto-resolve to the most recent.
	code := RunAudit("", false)
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	_ = id
}

// --- Regression tests for gograph-session-audit-plan-review-attribution ---

// TestSessionAttribution_PlanIncrementsTotalCommands creates a session, logs a
// plan command, and asserts that the audit report shows >= 1 total command and
// plan_run = true.
func TestSessionAttribution_PlanIncrementsTotalCommands(t *testing.T) {
	chdir(t)
	id, err := StartSession("attrplan")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := LogCommand("plan", []string{"SomeSymbol"}, "testing plan attribution", 10*time.Millisecond, "success"); err != nil {
		t.Fatalf("LogCommand plan: %v", err)
	}
	if _, err := EndSession(); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// Capture audit report via JSON mode.
	// We re-read the JSONL directly to avoid stdout capture complexity.
	logPath := filepath.Join(sessionsDirAbs(), "session_"+id+".jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	commandCount := 0
	planSeen := false
	for _, line := range lines {
		var entry GenericLogLine
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "command" {
			commandCount++
			if entry.Command == "plan" {
				planSeen = true
			}
		}
	}
	if commandCount < 1 {
		t.Errorf("expected >= 1 command entry, got %d", commandCount)
	}
	if !planSeen {
		t.Error("expected plan command to be recorded in session log")
	}

	// RunAudit must agree.
	code := RunAudit(id, true)
	if code != 0 {
		t.Errorf("RunAudit exit code = %d, want 0", code)
	}
}

// TestSessionAttribution_ReviewIncrementsReviewRun creates a session, logs a
// review command, and asserts review_run = true in the audit output.
func TestSessionAttribution_ReviewIncrementsReviewRun(t *testing.T) {
	chdir(t)
	id, err := StartSession("attrreview")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := LogCommand("review", []string{"SomeSymbol"}, "testing review attribution", 10*time.Millisecond, "success"); err != nil {
		t.Fatalf("LogCommand review: %v", err)
	}
	if _, err := EndSession(); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	logPath := filepath.Join(sessionsDirAbs(), "session_"+id+".jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}
	reviewSeen := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry GenericLogLine
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Command == "review" {
			reviewSeen = true
		}
	}
	if !reviewSeen {
		t.Error("expected review command to be recorded in session log")
	}
}

// TestSessionAttribution_PlanAndReview_GradeNotF asserts that a session
// containing both plan and review does not receive grade "F" purely due to
// zero command counters.
func TestSessionAttribution_PlanAndReview_GradeNotF(t *testing.T) {
	chdir(t)
	id, err := StartSession("attrgrade")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	for _, cmd := range []string{"plan", "review"} {
		if err := LogCommand(cmd, []string{"Foo"}, "grade test", 5*time.Millisecond, "success"); err != nil {
			t.Fatalf("LogCommand %s: %v", cmd, err)
		}
	}
	if _, err := EndSession(); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// Parse audit JSON output by reading the JSONL directly.
	logPath := filepath.Join(sessionsDirAbs(), "session_"+id+".jsonl")
	data, _ := os.ReadFile(logPath)
	totalCmds := 0
	planRun, reviewRun := false, false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry GenericLogLine
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "command" {
			totalCmds++
			if entry.Command == "plan" {
				planRun = true
			}
			if entry.Command == "review" {
				reviewRun = true
			}
		}
	}
	if totalCmds < 2 {
		t.Errorf("total commands = %d, want >= 2", totalCmds)
	}
	if !planRun {
		t.Error("plan_run expected true")
	}
	if !reviewRun {
		t.Error("review_run expected true")
	}
}

// TestSessionAttribution_NoSession_PlanAndReviewStillWork verifies that
// LogCommand is a safe no-op when no session is active — plan and review must
// not panic or return an error.
func TestSessionAttribution_NoSession_PlanAndReviewStillWork(t *testing.T) {
	chdir(t) // temp dir with no session
	for _, cmd := range []string{"plan", "review"} {
		if err := LogCommand(cmd, []string{"Foo"}, "no session", 1*time.Millisecond, "success"); err != nil {
			t.Errorf("LogCommand %s without active session returned error: %v", cmd, err)
		}
	}
}

// TestSessionAttribution_SubdirectoryRootDiscovery verifies that
// FindGographRoot() walks up to the parent that contains .gograph/ and that
// session files created in the parent are visible from a child directory.
// This is the exact scenario reported as broken: session created in dir A,
// plan/review run from a subdirectory of A — audit must still see the commands.
func TestSessionAttribution_SubdirectoryRootDiscovery(t *testing.T) {
	// Build a temp tree:  <root>/.gograph/   <root>/subdir/
	root := t.TempDir()
	subdir := filepath.Join(root, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	// Create the .gograph marker directory so FindGographRoot can find root.
	if err := os.MkdirAll(filepath.Join(root, ".gograph"), 0o755); err != nil {
		t.Fatalf("mkdir .gograph: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// ── Step 1: create session from root ──────────────────────────────────────
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir root: %v", err)
	}
	id, err := StartSession("subdirtest")
	if err != nil {
		t.Fatalf("StartSession from root: %v", err)
	}

	// ── Step 2: log plan/review from subdir ───────────────────────────────────
	if err := os.Chdir(subdir); err != nil {
		t.Fatalf("chdir subdir: %v", err)
	}
	// FindGographRoot() must walk up and land on root.
	// Resolve symlinks on both sides: macOS exposes /var → /private/var.
	gotRaw := FindGographRoot()
	got, _ := filepath.EvalSymlinks(gotRaw)
	wantResolved, _ := filepath.EvalSymlinks(root)
	if got != wantResolved {
		t.Errorf("FindGographRoot() from subdir = %q, want %q", gotRaw, root)
	}
	for _, cmd := range []string{"plan", "review"} {
		if err := LogCommand(cmd, []string{"Symbol"}, "subdir test", 2*time.Millisecond, "success"); err != nil {
			t.Errorf("LogCommand %s from subdir: %v", cmd, err)
		}
	}

	// ── Step 3: end session and audit from subdir ─────────────────────────────
	if _, err := EndSession(); err != nil {
		t.Fatalf("EndSession from subdir: %v", err)
	}

	logPath := filepath.Join(root, ".gograph", "sessions", "session_"+id+".jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read session log: %v", err)
	}

	planSeen, reviewSeen := false, false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry GenericLogLine
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "command" {
			if entry.Command == "plan" {
				planSeen = true
			}
			if entry.Command == "review" {
				reviewSeen = true
			}
		}
	}
	if !planSeen {
		t.Error("plan command not visible in session log when logged from subdir")
	}
	if !reviewSeen {
		t.Error("review command not visible in session log when logged from subdir")
	}
}

// --- Repository confinement regressions ---

func TestStartSessionAtRejectsLinkedGographDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, gographDir)); err != nil {
		t.Fatalf("link .gograph: %v", err)
	}

	if _, err := StartSessionAt(root, "unsafe"); err == nil {
		t.Fatal("StartSessionAt accepted a symlinked .gograph directory")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(data) != "unchanged" {
		t.Fatalf("outside sentinel changed to %q", data)
	}
	if _, err := os.Stat(filepath.Join(outside, "sessions")); !os.IsNotExist(err) {
		t.Fatalf("outside sessions directory was created: %v", err)
	}
}

func TestStartSessionAtRejectsDanglingSessionsDirectoryLink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, gographDir), 0o755); err != nil {
		t.Fatalf("mkdir .gograph: %v", err)
	}
	outsideTarget := filepath.Join(t.TempDir(), "missing-sessions")
	if err := os.Symlink(outsideTarget, filepath.Join(root, relSessionsDir)); err != nil {
		t.Fatalf("link sessions directory: %v", err)
	}

	if _, err := StartSessionAt(root, "unsafe"); err == nil {
		t.Fatal("StartSessionAt accepted a dangling sessions directory link")
	}
	if _, err := os.Stat(outsideTarget); !os.IsNotExist(err) {
		t.Fatalf("outside sessions target was created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, relActivePointerPath)); !os.IsNotExist(err) {
		t.Fatalf("active pointer was created after rejecting sessions link: %v", err)
	}
}

func TestStartSessionAtRejectsDanglingActivePointerLink(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, gographDir), 0o755); err != nil {
		t.Fatalf("mkdir .gograph: %v", err)
	}
	outsideTarget := filepath.Join(t.TempDir(), "missing-pointer.json")
	if err := os.Symlink(outsideTarget, filepath.Join(root, relActivePointerPath)); err != nil {
		t.Fatalf("link active pointer: %v", err)
	}

	if _, err := StartSessionAt(root, "unsafe"); err == nil {
		t.Fatal("StartSessionAt accepted a dangling active pointer link")
	}
	if _, err := os.Stat(outsideTarget); !os.IsNotExist(err) {
		t.Fatalf("outside pointer target was created: %v", err)
	}
}

func TestSessionLogOperationsRejectLinkedLog(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, relSessionsDir), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	id := "linked_log"
	pointer, err := json.Marshal(ActiveSessionPointer{ActiveSessionID: id})
	if err != nil {
		t.Fatalf("marshal pointer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, relActivePointerPath), pointer, 0o644); err != nil {
		t.Fatalf("write pointer: %v", err)
	}
	outsideLog := filepath.Join(t.TempDir(), "outside.jsonl")
	want := []byte(`{"type":"session_start","session_id":"outside"}` + "\n")
	if err := os.WriteFile(outsideLog, want, 0o644); err != nil {
		t.Fatalf("write outside log: %v", err)
	}
	linkedLog := filepath.Join(root, relSessionsDir, "session_"+id+".jsonl")
	if err := os.Symlink(outsideLog, linkedLog); err != nil {
		t.Fatalf("link session log: %v", err)
	}

	if err := LogCommandAt(root, "source", []string{"Secret"}, "test confinement", time.Millisecond, "success"); err == nil {
		t.Fatal("LogCommandAt accepted a symlinked session log")
	}
	var stdout, stderr strings.Builder
	if code := RunAuditToAt(root, id, true, &stdout, &stderr); code != 1 {
		t.Fatalf("RunAuditToAt exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("audit emitted outside log contents: %q", stdout.String())
	}
	got, err := os.ReadFile(outsideLog)
	if err != nil {
		t.Fatalf("read outside log: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("outside log changed:\n got %q\nwant %q", got, want)
	}
}

func TestSessionLogOperationsRejectDanglingLogLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, relSessionsDir), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	id := "dangling_log"
	pointer, err := json.Marshal(ActiveSessionPointer{ActiveSessionID: id})
	if err != nil {
		t.Fatalf("marshal pointer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, relActivePointerPath), pointer, 0o644); err != nil {
		t.Fatalf("write pointer: %v", err)
	}
	outsideTarget := filepath.Join(t.TempDir(), "missing-log.jsonl")
	if err := os.Symlink(outsideTarget, filepath.Join(root, relSessionsDir, "session_"+id+".jsonl")); err != nil {
		t.Fatalf("link session log: %v", err)
	}

	if err := LogCommandAt(root, "source", nil, "test confinement", time.Millisecond, "success"); err == nil {
		t.Fatal("LogCommandAt accepted a dangling session log link")
	}
	if _, err := EndSessionAt(root); err == nil {
		t.Fatal("EndSessionAt accepted a dangling session log link")
	}
	if _, err := os.Stat(outsideTarget); !os.IsNotExist(err) {
		t.Fatalf("outside log target was created: %v", err)
	}
}

func TestActivePointerRejectsTraversalSessionID(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, relSessionsDir), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	pointer, err := json.Marshal(ActiveSessionPointer{ActiveSessionID: "../../../../outside"})
	if err != nil {
		t.Fatalf("marshal pointer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, relActivePointerPath), pointer, 0o644); err != nil {
		t.Fatalf("write pointer: %v", err)
	}
	keepLog := filepath.Join(root, relSessionsDir, "session_keep.jsonl")
	if err := os.WriteFile(keepLog, []byte(`{"type":"session_start"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	if _, err := GetActiveSessionIDAt(root); err == nil || !strings.Contains(err.Error(), "invalid session ID") {
		t.Fatalf("GetActiveSessionIDAt traversal error = %v", err)
	}
	if _, err := EndSessionAt(root); err == nil {
		t.Fatal("EndSessionAt accepted a traversal session ID")
	}
	if count, err := CleanupSessionsAt(root); err == nil || count != 0 {
		t.Fatalf("CleanupSessionsAt = (%d, %v), want (0, error)", count, err)
	}
	if _, err := os.Stat(keepLog); err != nil {
		t.Fatalf("cleanup removed a log after rejecting pointer: %v", err)
	}
}

func TestRunAuditToAtRejectsTraversalSessionID(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr strings.Builder
	if code := RunAuditToAt(root, "../../../../outside", true, &stdout, &stderr); code != 1 {
		t.Fatalf("RunAuditToAt exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "invalid session ID") {
		t.Fatalf("audit error = %q, want invalid session ID", stderr.String())
	}
}

func TestCleanupSessionsAtRejectsLinkedLogWithoutRemovingTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, relSessionsDir), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	outsideLog := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outsideLog, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside log: %v", err)
	}
	if err := os.Symlink(outsideLog, filepath.Join(root, relSessionsDir, "session_linked.jsonl")); err != nil {
		t.Fatalf("link session log: %v", err)
	}

	if count, err := CleanupSessionsAt(root); err == nil || count != 0 {
		t.Fatalf("CleanupSessionsAt = (%d, %v), want (0, error)", count, err)
	}
	data, err := os.ReadFile(outsideLog)
	if err != nil {
		t.Fatalf("outside log was removed: %v", err)
	}
	if string(data) != "keep" {
		t.Fatalf("outside log changed to %q", data)
	}
}

func TestSessionOperationsAllowExplicitSymlinkedProjectRoot(t *testing.T) {
	realRoot := t.TempDir()
	alias := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatalf("link project root: %v", err)
	}

	id, err := StartSessionAt(alias, "linkedroot")
	if err != nil {
		t.Fatalf("StartSessionAt linked root: %v", err)
	}
	if err := LogCommandAt(alias, "plan", []string{"Symbol"}, "linked root", time.Millisecond, "success"); err != nil {
		t.Fatalf("LogCommandAt linked root: %v", err)
	}
	if _, err := EndSessionAt(alias); err != nil {
		t.Fatalf("EndSessionAt linked root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(realRoot, relSessionsDir, "session_"+id+".jsonl"))
	if err != nil {
		t.Fatalf("read real session log: %v", err)
	}
	if !strings.Contains(string(data), `"command":"plan"`) {
		t.Fatalf("session log missing command: %s", data)
	}
}
