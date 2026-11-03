package session

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// GG-11: a session its job forgot must not keep blocking or absorbing later
// callers. Abandoned sessions expire; live and selected ones never do.
func TestExpireAbandonedSessionsEndsOnlyProvenAbandonment(t *testing.T) {
	host, _ := os.Hostname()
	root := t.TempDir()
	start := func(word, owner string) string {
		t.Helper()
		id, err := StartCallerSessionOwnedAt(root, word, "", owner)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	setMeta := func(id string, meta CallerMarker) {
		t.Helper()
		meta.ID = id
		data, _ := json.Marshal(meta)
		if err := os.WriteFile(sessionFile(root, id, ".meta.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	idleCLI := start("idle", OwnerCLI)
	ageCallerLog(t, root, idleCLI)
	recentCLI := start("recent", OwnerCLI)
	selectedIdle := start("selected", OwnerCLI)
	ageCallerLog(t, root, selectedIdle)
	goneMCP := start("gonemcp", OwnerMCP)
	setMeta(goneMCP, CallerMarker{Owner: OwnerMCP, PID: deadPID(t), Host: host})
	liveMCP := start("livemcp", OwnerMCP)
	ageCallerLog(t, root, liveMCP)
	unknownOwner := start("unknown", OwnerCLI)
	setMeta(unknownOwner, CallerMarker{Owner: "", Host: host})
	ageCallerLog(t, root, unknownOwner)

	expired, err := ExpireAbandonedSessionsAt(root, selectedIdle)
	if err != nil {
		t.Fatal(err)
	}
	ended := map[string]string{}
	for _, session := range expired {
		ended[session.ID] = session.Reason
	}
	if len(ended) != 2 || !strings.Contains(ended[idleCLI], "no activity for") || !strings.Contains(ended[goneMCP], "MCP server process has exited") {
		t.Fatalf("expired = %+v, want only the idle CLI and the gone MCP sessions", expired)
	}
	for _, id := range []string{recentCLI, selectedIdle, liveMCP, unknownOwner} {
		if _, err := ActiveCallerSessionAt(root, id); err != nil {
			t.Fatalf("session %s must stay active: %v", id, err)
		}
	}
	for _, id := range []string{idleCLI, goneMCP} {
		if lastLogType(t, sessionFile(root, id, ".jsonl")) != "session_end/stale" {
			t.Fatalf("expired session %s needs a stale end record", id)
		}
		if _, err := os.Stat(sessionFile(root, id, ".meta.json")); err != nil {
			t.Fatalf("expired session %s keeps its sidecar for audit and cleanup: %v", id, err)
		}
	}
	_, err = ActiveCallerSessionAt(root, idleCLI)
	if err == nil || !strings.Contains(err.Error(), "ended as abandoned") || !strings.Contains(err.Error(), IdleLimitEnv) {
		t.Fatalf("its returning owner must learn why the session ended: %v", err)
	}
	if again, err := ExpireAbandonedSessionsAt(root, ""); err != nil || len(again) != 1 || again[0].ID != selectedIdle {
		t.Fatalf("an unselected idle session expires on the next pass only: %+v, %v", again, err)
	}
}

func TestExpireAbandonedSessionsRemovesMarkerOfEndedLog(t *testing.T) {
	root := t.TempDir()
	id, err := StartCallerSessionAt(root, "ended", "")
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(sessionFile(root, id, ".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = log.WriteString(`{"type":"session_end","ended_at":"` + time.Now().UTC().Format(time.RFC3339) + `","status":"completed"}` + "\n")
	_ = log.Close()

	expired, err := ExpireAbandonedSessionsAt(root, "")
	if err != nil || len(expired) != 1 || expired[0].ID != id {
		t.Fatalf("a marker left behind by an ended log must be removed: %+v, %v", expired, err)
	}
	if _, err := os.Stat(sessionFile(root, id, ".active")); !os.IsNotExist(err) {
		t.Fatalf("marker still present: %v", err)
	}
}

func TestIdleLimitConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value   string
		want    time.Duration
		wantErr bool
	}{
		{"", DefaultIdleLimit, false},
		{"90m", 90 * time.Minute, false},
		{"0", 0, false},
		{"off", 0, false},
		{"soon", DefaultIdleLimit, true},
		{"-1h", DefaultIdleLimit, true},
	} {
		t.Setenv(IdleLimitEnv, tc.value)
		got, err := IdleLimit()
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Fatalf("%s=%q: got %s, %v; want %s, error %t", IdleLimitEnv, tc.value, got, err, tc.want, tc.wantErr)
		}
	}

	root := t.TempDir()
	id, err := StartCallerSessionAt(root, "idle", "")
	if err != nil {
		t.Fatal(err)
	}
	ageCallerLog(t, root, id)
	t.Setenv(IdleLimitEnv, "off")
	if expired, err := ExpireAbandonedSessionsAt(root, ""); err != nil || len(expired) != 0 {
		t.Fatalf("a disabled limit must expire nothing: %+v, %v", expired, err)
	}
	t.Setenv(IdleLimitEnv, "1m")
	if expired, err := ExpireAbandonedSessionsAt(root, ""); err != nil || len(expired) != 1 {
		t.Fatalf("a configured limit must expire the idle session: %+v, %v", expired, err)
	}
}

func TestRefusedCallsAreNotSessionActivity(t *testing.T) {
	root := t.TempDir()
	id, err := StartCallerSessionAt(root, "forgotten", "")
	if err != nil {
		t.Fatal(err)
	}
	ageCallerLog(t, root, id)
	// A later job adopts the forgotten session and is refused for a missing
	// intention; that refusal is logged but must not keep the session alive.
	if err := LogCallerCommandAt(root, id, "query", nil, "", 0, "invocation_error"); err != nil {
		t.Fatal(err)
	}
	if expired, err := ExpireAbandonedSessionsAt(root, ""); err != nil || len(expired) != 1 || expired[0].ID != id {
		t.Fatalf("refusals must not count as activity: %+v, %v", expired, err)
	}

	live, err := StartCallerSessionAt(root, "live", "")
	if err != nil {
		t.Fatal(err)
	}
	ageCallerLog(t, root, live)
	if err := LogCallerCommandAt(root, live, "plan", nil, "change", 0, "success"); err != nil {
		t.Fatal(err)
	}
	if expired, err := ExpireAbandonedSessionsAt(root, ""); err != nil || len(expired) != 0 {
		t.Fatalf("a successful logged call is activity: %+v, %v", expired, err)
	}
}
