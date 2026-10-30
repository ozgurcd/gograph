package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Caller selectors are session IDs, not authentication credentials. They provide
// cooperative attribution between processes sharing a repository and OS user.
// One exclusive marker per session avoids a shared-pointer lost-update race.
func callerMarker(id string) (string, error) {
	if err := validateSessionID(id); err != nil {
		return "", err
	}
	return filepath.Join(relSessionsDir, "session_"+id+".active"), nil
}

func (s *sessionStore) activeCallerIDs() ([]string, error) {
	legacy, err := s.activeSessionID()
	if err != nil {
		return nil, err
	}
	ids, err := s.markerCallerIDs()
	if err != nil {
		return nil, err
	}
	if legacy != "" {
		ids = append(ids, legacy)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}

// callerMeta is the owner metadata sidecar of a session marker.
func callerMeta(id string) (string, error) {
	if err := validateSessionID(id); err != nil {
		return "", err
	}
	return filepath.Join(relSessionsDir, "session_"+id+".meta.json"), nil
}

// CallerMarker is a session's owner metadata. The session_<id>.active marker
// holds only the ID, which every release reads; the fields live in the
// session_<id>.meta.json sidecar. A JSON marker of the same shape is also
// read. Without metadata the fields are empty and only idle time applies.
type CallerMarker struct {
	ID        string `json:"id"`
	Owner     string `json:"owner,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Host      string `json:"host,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// Marker owners. A CLI creator exits as soon as it prints the ID, so only an
// MCP server's process ID can prove that its session is still owned.
const (
	OwnerCLI = "cli"
	OwnerMCP = "mcp"
)

func newCallerMarker(id, owner string) CallerMarker {
	host, _ := os.Hostname()
	cwd, _ := os.Getwd()
	return CallerMarker{ID: id, Owner: owner, PID: os.Getpid(), Host: host, Cwd: cwd, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
}

func parseCallerMarker(id string, data []byte) (CallerMarker, bool) {
	if string(data) == id {
		return CallerMarker{ID: id}, true
	}
	var marker CallerMarker
	if json.Unmarshal(data, &marker) != nil || marker.ID != id {
		return CallerMarker{}, false
	}
	return marker, true
}

// callerMetadata returns the sidecar metadata for id, or fallback when the
// sidecar is missing, unsafe or names another session. Metadata only adds
// evidence for ending a session, so its absence never fails a command.
func (s *sessionStore) callerMetadata(id string, fallback CallerMarker) CallerMarker {
	meta, err := callerMeta(id)
	if err != nil {
		return fallback
	}
	data, err := s.files.ReadRegularFile(meta)
	if err != nil {
		return fallback
	}
	var marker CallerMarker
	if json.Unmarshal(data, &marker) != nil || marker.ID != id {
		return fallback
	}
	return marker
}

// markerCallerIDs lists sessions that hold a caller marker.
func (s *sessionStore) markerCallerIDs() ([]string, error) {
	markers, err := s.callerMarkers()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(markers))
	for id := range markers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

// callerMarkers reads every caller marker. Malformed or linked markers fail
// closed.
func (s *sessionStore) callerMarkers() (map[string]CallerMarker, error) {
	markers := map[string]CallerMarker{}
	entries, err := s.files.ReadDirectory(relSessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return markers, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "session_") || !strings.HasSuffix(name, ".active") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "session_"), ".active")
		marker, err := callerMarker(id)
		if err != nil {
			return nil, err
		}
		data, err := s.files.ReadRegularFile(marker)
		// An owner may end after ReadDirectory captured its marker. Only an
		// absent entry is benign; malformed or linked markers still fail closed.
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		parsed, ok := parseCallerMarker(id, data)
		if !ok {
			return nil, fmt.Errorf("invalid active session marker %q", name)
		}
		if parsed.Owner == "" {
			parsed = s.callerMetadata(id, parsed)
		}
		markers[id] = parsed
	}
	return markers, nil
}

// ActiveCallerSessionAt returns only the explicitly selected active session.
// Missing selectors never adopt another caller's session.
func ActiveCallerSessionAt(root, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	if err := validateSessionID(id); err != nil {
		return "", err
	}
	store, err := openSessionStore(root)
	if err != nil {
		return "", err
	}
	defer store.close()
	ids, err := store.activeCallerIDs()
	if err != nil {
		return "", err
	}
	if !slices.Contains(ids, id) {
		return "", fmt.Errorf("selected session %q is not active in this repository", id)
	}
	return id, nil
}

// GuardCallerMutationAt refuses writes while a different caller owns a session.
// A legacy active_session.json pointer has no caller marker: it stays usable
// when selected by its ID, but it never blocks other callers.
func GuardCallerMutationAt(root, id string) error {
	if id != "" {
		if _, err := ActiveCallerSessionAt(root, id); err != nil {
			return err
		}
	}
	store, err := openSessionStore(root)
	if err != nil {
		return err
	}
	defer store.close()
	ids, err := store.markerCallerIDs()
	if err != nil {
		return err
	}
	for _, active := range ids {
		if active != id {
			return fmt.Errorf("active session %q belongs to another caller; repository mutation refused", active)
		}
	}
	return nil
}

// StartCallerSessionAt creates an independent CLI-owned session. An existing
// selector must be ended first; an unselected caller may create alongside
// other owners.
func StartCallerSessionAt(root, word, id string) (string, error) {
	return StartCallerSessionOwnedAt(root, word, id, OwnerCLI)
}

// StartCallerSessionOwnedAt creates a session whose marker records owner, the
// creating process ID, host, working directory and creation time.
func StartCallerSessionOwnedAt(root, word, id, owner string) (string, error) {
	if id != "" {
		if _, err := ActiveCallerSessionAt(root, id); err != nil {
			return "", err
		}
		return "", fmt.Errorf("session %q is already active for this caller; end it first", id)
	}
	store, err := openSessionStore(root)
	if err != nil {
		return "", err
	}
	defer store.close()
	if _, err := store.activeCallerIDs(); err != nil {
		return "", err
	}
	if err := store.files.EnsureRealDirectory(relSessionsDir, 0755); err != nil {
		return "", err
	}
	word = regexp.MustCompile("[^a-zA-Z0-9_]").ReplaceAllString(word, "")
	if word == "" {
		word = "session"
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	id = word + "_" + time.Now().Format("20060102_150405") + "_" + hex.EncodeToString(nonce[:])
	path, err := sessionLogPath(id)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(SessionStartEntry{Type: "session_start", SessionID: id, CreatedAt: time.Now().Format(time.RFC3339)})
	if err != nil {
		return "", err
	}
	if err := store.files.WriteRegularFile(path, append(data, '\n'), 0644, true); err != nil {
		return "", err
	}
	marker, err := callerMarker(id)
	if err != nil {
		return "", err
	}
	// Publish only complete bytes. A directly created marker is visible to
	// concurrent creators before WriteRegularFile has written its contents.
	// The exclusively created log reserves this collision-resistant ID.
	// The marker stays the bare ID that v1.7.14 reads; owner metadata goes to
	// a sidecar published first, so a visible marker always has it.
	meta, err := callerMeta(id)
	if err != nil {
		_ = store.files.RemoveRegularFile(path)
		return "", err
	}
	metaData, err := json.Marshal(newCallerMarker(id, owner))
	if err == nil {
		err = store.files.AtomicReplaceRegularFile(meta, metaData, 0644)
	}
	if err != nil {
		_ = store.files.RemoveRegularFile(path)
		return "", err
	}
	if err := store.files.AtomicReplaceRegularFile(marker, []byte(id), 0644); err != nil {
		_ = store.files.RemoveRegularFile(meta)
		_ = store.files.RemoveRegularFile(path)
		return "", err
	}
	return id, nil
}

// EndCallerSessionAt ends only the selected session, including a legacy session
// explicitly selected by the ID printed by older releases.
func EndCallerSessionAt(root, id string) (string, error) {
	if id == "" {
		if err := GuardCallerMutationAt(root, id); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no selected session to end; supply --session-id or GOGRAPH_SESSION")
	}
	if _, err := ActiveCallerSessionAt(root, id); err != nil {
		return "", err
	}
	store, err := openSessionStore(root)
	if err != nil {
		return "", err
	}
	defer store.close()
	legacy, err := store.activeSessionID()
	if err != nil {
		return "", err
	}
	if legacy == id {
		return EndSessionAt(root)
	}
	path, err := sessionLogPath(id)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(SessionEndEntry{Type: "session_end", EndedAt: time.Now().Format(time.RFC3339), Status: "completed"})
	if err != nil {
		return "", err
	}
	if err := store.files.AppendRegularFile(path, append(data, '\n')); err != nil {
		return "", err
	}
	marker, err := callerMarker(id)
	if err != nil {
		return "", err
	}
	if err := store.files.RemoveRegularFile(marker); err != nil {
		return "", err
	}
	return id, nil
}

// LogCallerCommandAt never writes observational calls to an unselected session.
func LogCallerCommandAt(root, id, command string, args []string, intention string, elapsed time.Duration, status string) error {
	if id == "" || command == "hook-guard" && status == "success" {
		return nil
	}
	if _, err := ActiveCallerSessionAt(root, id); err != nil {
		return err
	}
	store, err := openSessionStore(root)
	if err != nil {
		return err
	}
	defer store.close()
	path, err := sessionLogPath(id)
	if err != nil {
		return err
	}
	data, err := json.Marshal(CommandLogEntry{Type: "command", Timestamp: time.Now().Format(time.RFC3339), Command: command, Args: redactArgs(args), Intention: intention, ExecutionMs: elapsed.Milliseconds(), Status: status})
	if err != nil {
		return err
	}
	return store.files.AppendRegularFile(path, append(data, '\n'))
}

// CleanupReport lists what one cleanup pass changed.
type CleanupReport struct {
	Archived      int      // session logs moved into ArchiveDir
	Ended         []string // sessions this pass ended as stale
	LegacyPointer string   // session named by an archived active_session.json
	ArchiveDir    string   // root-relative folder of this pass; empty if unused
}

// Lines is the shared CLI and MCP description of a cleanup pass.
func (r CleanupReport) Lines() []string {
	var lines []string
	for _, id := range r.Ended {
		lines = append(lines, fmt.Sprintf("Ended stale session %q.", id))
	}
	if r.LegacyPointer != "" {
		lines = append(lines, fmt.Sprintf("Archived legacy active_session.json for session %q.", r.LegacyPointer))
	}
	if r.ArchiveDir != "" {
		lines = append(lines, fmt.Sprintf("Archived %d session log files to %s.", r.Archived, filepath.ToSlash(r.ArchiveDir)))
	}
	return lines
}

// staleSessionAfter is how long another caller's session may record nothing
// before cleanup ends it. A marker names no owner process (the CLI creator
// exits at once), so log activity is the only liveness evidence.
const staleSessionAfter = 24 * time.Hour

// sessionActivity reads a session log's last record. known is false when the
// log is missing or its last record carries no parseable time.
func (s *sessionStore) sessionActivity(id string) (last time.Time, ended, known bool, err error) {
	path, err := sessionLogPath(id)
	if err != nil {
		return time.Time{}, false, false, err
	}
	data, err := s.files.ReadRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, false, nil
	}
	if err != nil {
		return time.Time{}, false, false, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var line GenericLogLine
	if json.Unmarshal([]byte(lines[len(lines)-1]), &line) != nil {
		return time.Time{}, false, false, nil
	}
	ended = line.Type == "session_end"
	for _, value := range []string{line.Timestamp, line.EndedAt, line.CreatedAt} {
		if parsed, parseErr := time.Parse(time.RFC3339, value); parseErr == nil {
			return parsed, ended, true, nil
		}
	}
	return time.Time{}, ended, false, nil
}

// relArchiveRoot holds what cleanup sets aside. Cleanup never deletes session
// state; each pass moves files into one new timestamped folder below it.
var relArchiveRoot = filepath.Join(filepath.Dir(relSessionsDir), "_to_delete")

// sessionArchive creates its pass folder on the first move only.
type sessionArchive struct {
	store *sessionStore
	dir   string
}

func (a *sessionArchive) move(path string) error {
	if a.dir == "" {
		var nonce [4]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		dir := filepath.Join(relArchiveRoot, time.Now().UTC().Format("20060102T150405Z")+"_"+hex.EncodeToString(nonce[:]))
		for _, name := range []string{relArchiveRoot, dir} {
			if err := a.store.files.EnsureRealDirectory(name, 0755); err != nil {
				return err
			}
		}
		a.dir = dir
	}
	return a.store.files.MoveRegularFile(path, filepath.Join(a.dir, filepath.Base(path)))
}

// abandoned reports proof that a session's owner is gone: a known CLI owner
// whose log recorded nothing for staleSessionAfter, or an MCP owner whose
// process has exited on this host. Idle time never ends an MCP session, and
// unknown, legacy or other-host owners are always kept.
func abandoned(marker CallerMarker, last time.Time, known bool) bool {
	switch marker.Owner {
	case OwnerCLI:
		return known && time.Since(last) > staleSessionAfter
	case OwnerMCP:
		return ownerGone(marker)
	}
	return false
}

// ownerGone reports a marker whose MCP server process has exited on this host.
// CLI markers name a process that exits at once, so they never qualify.
func ownerGone(marker CallerMarker) bool {
	if marker.Owner != OwnerMCP || marker.PID <= 0 || marker.Host == "" {
		return false
	}
	host, err := os.Hostname()
	return err == nil && host == marker.Host && !processAlive(marker.PID)
}

func (s *sessionStore) appendStaleEnd(id string) error {
	path, err := sessionLogPath(id)
	if err != nil {
		return err
	}
	data, err := json.Marshal(SessionEndEntry{Type: "session_end", EndedAt: time.Now().Format(time.RFC3339), Status: "stale"})
	if err != nil {
		return err
	}
	return s.files.AppendRegularFile(path, append(data, '\n'))
}

// archiveStaleCallers ends and archives other callers' sessions that are
// ended, idle longer than staleSessionAfter, or owned by a gone MCP server.
func (s *sessionStore) archiveStaleCallers(selected string, archive *sessionArchive, report *CleanupReport) error {
	markers, err := s.callerMarkers()
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(markers))
	for id := range markers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if id == selected {
			continue
		}
		last, done, known, err := s.sessionActivity(id)
		if err != nil {
			return err
		}
		if !done && !abandoned(markers[id], last, known) {
			continue
		}
		if !done {
			if err := s.appendStaleEnd(id); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		marker, err := callerMarker(id)
		if err != nil {
			return err
		}
		if err := archive.move(marker); err != nil {
			return err
		}
		path, err := sessionLogPath(id)
		if err != nil {
			return err
		}
		if err := archive.move(path); err == nil {
			report.Archived++
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.archiveSidecar(id, archive); err != nil {
			return err
		}
		report.Ended = append(report.Ended, id)
	}
	return nil
}

// archiveSidecar moves a session's metadata sidecar, if any, with its session.
func (s *sessionStore) archiveSidecar(id string, archive *sessionArchive) error {
	meta, err := callerMeta(id)
	if err != nil {
		return err
	}
	if err := archive.move(meta); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// archiveOrphanSidecars moves sidecars whose marker and log are both gone, for
// example after an older release ended and deleted that session.
func (s *sessionStore) archiveOrphanSidecars(selected string, archive *sessionArchive) error {
	entries, err := s.files.ReadDirectory(relSessionsDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "session_") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "session_"), ".meta.json")
		if validateSessionID(id) != nil || id == selected {
			continue
		}
		orphan := true
		for _, sibling := range []func(string) (string, error){callerMarker, sessionLogPath} {
			path, err := sibling(id)
			if err != nil {
				return err
			}
			if _, err := s.files.ReadRegularFile(path); !errors.Is(err, os.ErrNotExist) {
				orphan = false
			}
		}
		if orphan {
			if err := s.archiveSidecar(id, archive); err != nil {
				return err
			}
		}
	}
	return nil
}

// CleanupCallerSessionsReportAt archives instead of deleting. It first ends and
// archives stale sessions of other callers and archives an unselected legacy
// active_session.json, then refuses while another caller is still live.
// Otherwise it archives ended logs, and unmarked logs idle over 24 hours.
func CleanupCallerSessionsReportAt(root, id string) (report CleanupReport, err error) {
	if id != "" {
		if _, err := ActiveCallerSessionAt(root, id); err != nil {
			return report, err
		}
	}
	store, err := openSessionStore(root)
	if err != nil {
		return report, err
	}
	defer store.close()
	archive := &sessionArchive{store: store}
	defer func() { report.ArchiveDir = archive.dir }()
	if err := store.archiveStaleCallers(id, archive, &report); err != nil {
		return report, err
	}
	legacy, err := store.activeSessionID()
	if err != nil {
		return report, err
	}
	if legacy != "" && legacy != id {
		if err := archive.move(relActivePointerPath); err != nil {
			return report, err
		}
		report.LegacyPointer = legacy
	}
	if err := GuardCallerMutationAt(root, id); err != nil {
		return report, err
	}
	entries, err := store.files.ReadDirectory(relSessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	for _, entry := range entries {
		logID, matches, err := sessionIDFromEntry(entry)
		if err != nil {
			return report, err
		}
		if !matches || logID == id {
			continue
		}
		// A creator writes its log before its marker, so an unmarked log is
		// archived only once it is ended or idle beyond staleSessionAfter.
		last, done, known, err := store.sessionActivity(logID)
		if err != nil {
			return report, err
		}
		if !done {
			if !known || time.Since(last) <= staleSessionAfter {
				continue
			}
			if err := store.appendStaleEnd(logID); err != nil {
				return report, err
			}
			report.Ended = append(report.Ended, logID)
		}
		if err := archive.move(filepath.Join(relSessionsDir, entry.Name())); err != nil {
			return report, err
		}
		report.Archived++
		if err := store.archiveSidecar(logID, archive); err != nil {
			return report, err
		}
	}
	return report, store.archiveOrphanSidecars(id, archive)
}

// CleanupCallerSessionsAt is CleanupCallerSessionsReportAt returning only the
// number of archived session logs.
func CleanupCallerSessionsAt(root, id string) (int, error) {
	report, err := CleanupCallerSessionsReportAt(root, id)
	return report.Archived, err
}
