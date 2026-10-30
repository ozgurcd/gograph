package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const oldStart = `{"type":"session_start","session_id":"%s","created_at":"2026-01-01T00:00:00Z"}` + "\n"

func sessionFile(root, id, suffix string) string {
	return filepath.Join(root, relSessionsDir, "session_"+id+suffix)
}

func ageCallerLog(t *testing.T, root, id string) {
	t.Helper()
	if err := os.WriteFile(sessionFile(root, id, ".jsonl"), []byte(strings.Replace(oldStart, "%s", id, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyPointer(t *testing.T, root, id, log string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, relSessionsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionFile(root, id, ".jsonl"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	pointer, err := json.Marshal(ActiveSessionPointer{ActiveSessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, relActivePointerPath), pointer, 0o644); err != nil {
		t.Fatal(err)
	}
}

func lastLogType(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "missing"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var last GenericLogLine
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &last)
	return last.Type + "/" + last.Status
}

func archived(t *testing.T, root string, report CleanupReport, name string) string {
	t.Helper()
	if !strings.HasPrefix(filepath.ToSlash(report.ArchiveDir), ".gograph/_to_delete/") {
		t.Fatalf("archive dir %q is not inside .gograph/_to_delete", report.ArchiveDir)
	}
	path := filepath.Join(root, report.ArchiveDir, name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s not archived: %v", name, err)
	}
	return path
}

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// v1.7.14 reads a marker only when its bytes equal the session ID, so the
// marker stays bare and the owner metadata lives in a sidecar file.
func TestCallerMarkerIsBareIDWithMetadataSidecar(t *testing.T) {
	root := t.TempDir()
	id, err := StartCallerSessionAt(root, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(sessionFile(root, id, ".active")); err != nil || string(data) != id {
		t.Fatalf("marker = %q, %v; want the bare session ID", data, err)
	}
	data, err := os.ReadFile(sessionFile(root, id, ".meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var marker CallerMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("sidecar is not JSON: %q", data)
	}
	host, _ := os.Hostname()
	cwd, _ := os.Getwd()
	if marker.ID != id || marker.Owner != OwnerCLI || marker.PID != os.Getpid() || marker.Host != host || marker.Cwd != cwd {
		t.Fatalf("marker = %+v", marker)
	}
	if _, err := time.Parse(time.RFC3339, marker.CreatedAt); err != nil {
		t.Fatalf("created_at %q: %v", marker.CreatedAt, err)
	}
}

func TestCallerMarkerAcceptsPlainIDFromOlderReleases(t *testing.T) {
	root := t.TempDir()
	id, err := StartCallerSessionAt(root, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionFile(root, id, ".active"), []byte(id), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ActiveCallerSessionAt(root, id); err != nil {
		t.Fatalf("plain-ID marker rejected: %v", err)
	}
}

func TestCleanupArchivesStaleCallerSessions(t *testing.T) {
	root := t.TempDir()
	stale, err := StartCallerSessionAt(root, "stale", "")
	if err != nil {
		t.Fatal(err)
	}
	ageCallerLog(t, root, stale)
	if err := GuardCallerMutationAt(root, ""); err == nil {
		t.Fatal("a stale session must still block until cleanup ends it")
	}

	report, err := CleanupCallerSessionsReportAt(root, "")
	if err != nil {
		t.Fatalf("cleanup refused a stale-only root: %v", err)
	}
	if !slices.Equal(report.Ended, []string{stale}) || report.Archived != 1 {
		t.Fatalf("report = %+v, want one ended and archived session %s", report, stale)
	}
	archived(t, root, report, "session_"+stale+".active")
	if got := lastLogType(t, archived(t, root, report, "session_"+stale+".jsonl")); got != "session_end/stale" {
		t.Fatalf("archived log ends with %s", got)
	}
	for _, suffix := range []string{".active", ".jsonl"} {
		if _, err := os.Stat(sessionFile(root, stale, suffix)); !os.IsNotExist(err) {
			t.Fatalf("%s left in sessions: %v", suffix, err)
		}
	}
	if err := GuardCallerMutationAt(root, ""); err != nil {
		t.Fatalf("guard after cleanup: %v", err)
	}
}

func TestCleanupArchivesStaleButRefusesLiveOtherCaller(t *testing.T) {
	root := t.TempDir()
	stale, err := StartCallerSessionAt(root, "stale", "")
	if err != nil {
		t.Fatal(err)
	}
	ageCallerLog(t, root, stale)
	live, err := StartCallerSessionAt(root, "live", "")
	if err != nil {
		t.Fatal(err)
	}

	report, err := CleanupCallerSessionsReportAt(root, "")
	if err == nil || !strings.Contains(err.Error(), live) {
		t.Fatalf("cleanup error = %v, want refusal naming live caller %s", err, live)
	}
	if !slices.Equal(report.Ended, []string{stale}) {
		t.Fatalf("ended = %v, want [%s]", report.Ended, stale)
	}
	archived(t, root, report, "session_"+stale+".jsonl")
	if _, err := ActiveCallerSessionAt(root, live); err != nil {
		t.Fatalf("live caller lost its session: %v", err)
	}
}

func TestCleanupKeepsSelectedSessionEvenWhenIdle(t *testing.T) {
	root := t.TempDir()
	own, err := StartCallerSessionAt(root, "own", "")
	if err != nil {
		t.Fatal(err)
	}
	ageCallerLog(t, root, own)
	report, err := CleanupCallerSessionsReportAt(root, own)
	if err != nil || len(report.Ended) != 0 || report.ArchiveDir != "" {
		t.Fatalf("cleanup(own) = %+v, %v", report, err)
	}
	if _, err := ActiveCallerSessionAt(root, own); err != nil {
		t.Fatalf("selected session ended: %v", err)
	}
}

// Only an MCP server outlives its create call, so only its process ID proves
// ownership; a CLI marker with a gone process stays until it is idle.
func TestCleanupEndsSessionsOfGoneMCPServersOnly(t *testing.T) {
	root := t.TempDir()
	host, _ := os.Hostname()
	pid := deadPID(t)
	ids := map[string]string{}
	for _, owner := range []string{OwnerMCP, OwnerCLI} {
		id, err := StartCallerSessionOwnedAt(root, owner, "", owner)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(CallerMarker{ID: id, Owner: owner, PID: pid, Host: host, CreatedAt: time.Now().UTC().Format(time.RFC3339)})
		if err := os.WriteFile(sessionFile(root, id, ".meta.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		ids[owner] = id
	}
	report, err := CleanupCallerSessionsReportAt(root, "")
	if err == nil || !strings.Contains(err.Error(), ids[OwnerCLI]) {
		t.Fatalf("cleanup error = %v, want refusal naming the CLI session", err)
	}
	if !slices.Equal(report.Ended, []string{ids[OwnerMCP]}) {
		t.Fatalf("ended = %v, want only the MCP session %s", report.Ended, ids[OwnerMCP])
	}
}

// Commit c7383dc wrote JSON markers; any left on disk still parse, and their
// owner metadata still counts.
func TestCallerMarkerAcceptsJSONMarkerShape(t *testing.T) {
	root := t.TempDir()
	host, _ := os.Hostname()
	id, err := StartCallerSessionOwnedAt(root, "jsonmarker", "", OwnerMCP)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sessionFile(root, id, ".meta.json")); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(CallerMarker{ID: id, Owner: OwnerMCP, PID: deadPID(t), Host: host})
	if err := os.WriteFile(sessionFile(root, id, ".active"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ActiveCallerSessionAt(root, id); err != nil {
		t.Fatalf("JSON marker rejected: %v", err)
	}
	report, err := CleanupCallerSessionsReportAt(root, "")
	if err != nil || !slices.Equal(report.Ended, []string{id}) {
		t.Fatalf("cleanup = %+v, %v; want the gone MCP owner's session ended", report, err)
	}
}

// Only proof ends a session: a known CLI owner idle over 24 hours, or an MCP
// owner whose process is gone on this host. Idle time never ends an MCP
// session, and unknown, legacy or other-host owners are kept.
func TestCleanupKeepsSessionsWithoutProofOfAbandonment(t *testing.T) {
	host, _ := os.Hostname()
	for _, tc := range []struct {
		name string
		meta *CallerMarker // nil removes the sidecar (legacy bare marker)
	}{
		{"live MCP idle", &CallerMarker{Owner: OwnerMCP, PID: os.Getpid(), Host: host}},
		{"other-host MCP", &CallerMarker{Owner: OwnerMCP, PID: 1 << 30, Host: host + ".other"}},
		{"unknown owner", &CallerMarker{Owner: "", PID: 1 << 30, Host: host}},
		{"legacy bare marker", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			id, err := StartCallerSessionOwnedAt(root, "kept", "", OwnerMCP)
			if err != nil {
				t.Fatal(err)
			}
			meta := sessionFile(root, id, ".meta.json")
			if tc.meta == nil {
				err = os.Remove(meta)
			} else {
				tc.meta.ID = id
				data, _ := json.Marshal(tc.meta)
				err = os.WriteFile(meta, data, 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			ageCallerLog(t, root, id)
			report, err := CleanupCallerSessionsReportAt(root, "")
			if err == nil || !strings.Contains(err.Error(), id) || len(report.Ended) != 0 {
				t.Fatalf("cleanup = %+v, %v; want %s kept and named", report, err, id)
			}
			if _, err := ActiveCallerSessionAt(root, id); err != nil {
				t.Fatalf("session lost: %v", err)
			}
		})
	}
}

func TestCleanupArchivesSidecarWithItsSession(t *testing.T) {
	root := t.TempDir()
	stale, err := StartCallerSessionAt(root, "stale", "")
	if err != nil {
		t.Fatal(err)
	}
	ageCallerLog(t, root, stale)
	done, err := StartCallerSessionAt(root, "done", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EndCallerSessionAt(root, done); err != nil {
		t.Fatal(err)
	}
	report, err := CleanupCallerSessionsReportAt(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{stale, done} {
		archived(t, root, report, "session_"+id+".meta.json")
		if _, err := os.Stat(sessionFile(root, id, ".meta.json")); !os.IsNotExist(err) {
			t.Fatalf("sidecar of %s left in sessions: %v", id, err)
		}
	}
}

func TestLegacyPointerDoesNotBlockOtherCallers(t *testing.T) {
	root := t.TempDir()
	writeLegacyPointer(t, root, "legacy", `{"type":"session_start","session_id":"legacy"}`+"\n")
	if err := GuardCallerMutationAt(root, ""); err != nil {
		t.Fatalf("unselected caller blocked by a legacy pointer: %v", err)
	}
	if _, err := ActiveCallerSessionAt(root, "legacy"); err != nil {
		t.Fatalf("explicitly selected legacy session must stay usable: %v", err)
	}
}

func TestCleanupArchivesLegacyPointer(t *testing.T) {
	for _, tc := range []struct {
		name, log string
		ended     bool
	}{
		{"idle", strings.Replace(oldStart, "%s", "legacy", 1), true},
		{"recent", `{"type":"session_start","session_id":"legacy","created_at":"` + time.Now().Format(time.RFC3339) + `"}` + "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeLegacyPointer(t, root, "legacy", tc.log)
			report, err := CleanupCallerSessionsReportAt(root, "")
			if err != nil {
				t.Fatal(err)
			}
			if report.LegacyPointer != "legacy" {
				t.Fatalf("report = %+v, want archived legacy pointer", report)
			}
			archived(t, root, report, "active_session.json")
			if _, err := os.Stat(filepath.Join(root, relActivePointerPath)); !os.IsNotExist(err) {
				t.Fatalf("legacy pointer left in place: %v", err)
			}
			if got := slices.Contains(report.Ended, "legacy"); got != tc.ended {
				t.Fatalf("ended = %v, want legacy ended=%v", report.Ended, tc.ended)
			}
			if !tc.ended {
				if _, err := os.Stat(sessionFile(root, "legacy", ".jsonl")); err != nil {
					t.Fatalf("recent legacy log moved: %v", err)
				}
			}
		})
	}
}

func TestCleanupArchivesEndedLogsInsteadOfDeleting(t *testing.T) {
	root := t.TempDir()
	id, err := StartCallerSessionAt(root, "done", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EndCallerSessionAt(root, id); err != nil {
		t.Fatal(err)
	}
	report, err := CleanupCallerSessionsReportAt(root, "")
	if err != nil || report.Archived != 1 || len(report.Ended) != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if got := lastLogType(t, archived(t, root, report, "session_"+id+".jsonl")); got != "session_end/completed" {
		t.Fatalf("archived log ends with %s", got)
	}
}
