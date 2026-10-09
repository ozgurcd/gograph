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
	var ids []string
	if legacy != "" {
		ids = append(ids, legacy)
	}
	entries, err := s.files.ReadDirectory(relSessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return ids, nil
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
		if err != nil {
			return nil, err
		}
		if string(data) != id {
			return nil, fmt.Errorf("invalid active session marker %q", name)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
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
	ids, err := store.activeCallerIDs()
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

// StartCallerSessionAt creates an independent session. An existing selector must
// be ended first; an unselected caller may create alongside other owners.
func StartCallerSessionAt(root, word, id string) (string, error) {
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
	if err := store.files.WriteRegularFile(marker, []byte(id), 0644, true); err != nil {
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

// CleanupCallerSessionsAt preserves active markers and logs. It refuses when any
// other caller is active; it never deletes another ongoing audit.
func CleanupCallerSessionsAt(root, id string) (int, error) {
	if err := GuardCallerMutationAt(root, id); err != nil {
		return 0, err
	}
	store, err := openSessionStore(root)
	if err != nil {
		return 0, err
	}
	defer store.close()
	entries, err := store.files.ReadDirectory(relSessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var paths []string
	for _, entry := range entries {
		logID, matches, err := sessionIDFromEntry(entry)
		if err != nil {
			return 0, err
		}
		if matches && logID != id {
			path := filepath.Join(relSessionsDir, entry.Name())
			data, err := store.files.ReadRegularFile(path)
			if err != nil {
				return 0, err
			}
			// A creator writes its log before its marker. Never remove a log
			// in that interval, or a legacy incomplete audit with no pointer.
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			var last GenericLogLine
			if json.Unmarshal([]byte(lines[len(lines)-1]), &last) == nil && last.Type == "session_end" {
				paths = append(paths, path)
			}
		}
	}
	count := 0
	for _, path := range paths {
		if err := store.files.RemoveRegularFile(path); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
