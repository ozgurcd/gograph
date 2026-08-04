package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ozgurcd/gograph/internal/rootfind"
	"github.com/ozgurcd/gograph/internal/sourcefs"
)

const (
	gographDir           = ".gograph"
	relSessionsDir       = ".gograph/sessions"
	relActivePointerPath = ".gograph/active_session.json"
)

var sessionIDPattern = regexp.MustCompile(`\A[a-zA-Z0-9_]+\z`)

// FindGographRoot walks up from the current working directory until it finds a
// directory that already contains a ".gograph" subdirectory (i.e. the project
// root where `gograph build` was run). Falls back to "." when none is found so
// that existing behaviour and tests that chdir to a fresh temp dir are
// unaffected.
//
// This is a thin wrapper around rootfind.FindRoot() kept for backward
// compatibility.
func FindGographRoot() string {
	return rootfind.FindRoot()
}

func sessionRoot(root string) string {
	if root != "" {
		return root
	}
	return FindGographRoot()
}

func sessionsDirAt(root string) string {
	return filepath.Join(sessionRoot(root), relSessionsDir)
}

func sessionsDirAbs() string {
	return sessionsDirAt("")
}

func activePointerPathAt(root string) string {
	return filepath.Join(sessionRoot(root), relActivePointerPath)
}

func activePointerPathAbs() string {
	return activePointerPathAt("")
}

// sessionStore keeps every session operation anchored to the project root.
// sourcefs.Open uses os.OpenRoot, which follows an explicitly supplied root
// symlink while rejecting symlinks in descendant paths.
type sessionStore struct {
	rootPath string
	files    *sourcefs.Reader
}

func openSessionStore(root string) (*sessionStore, error) {
	rootPath := sessionRoot(root)
	files, err := sourcefs.Open(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open session project root: %w", err)
	}
	return &sessionStore{rootPath: rootPath, files: files}, nil
}

func (s *sessionStore) close() {
	if s != nil && s.files != nil {
		_ = s.files.Close()
	}
}

func validateSessionID(sessionID string) error {
	if !sessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("invalid session ID %q: only letters, digits, and underscores are allowed", sessionID)
	}
	return nil
}

func sessionLogPath(sessionID string) (string, error) {
	if err := validateSessionID(sessionID); err != nil {
		return "", err
	}
	return filepath.Join(relSessionsDir, fmt.Sprintf("session_%s.jsonl", sessionID)), nil
}

func (s *sessionStore) activeSessionID() (string, error) {
	data, err := s.files.ReadRegularFile(relActivePointerPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read active pointer: %w", err)
	}

	var ptr ActiveSessionPointer
	if err := json.Unmarshal(data, &ptr); err != nil {
		return "", fmt.Errorf("unmarshal active pointer: %w", err)
	}
	if err := validateSessionID(ptr.ActiveSessionID); err != nil {
		return "", fmt.Errorf("read active pointer: %w", err)
	}
	return ptr.ActiveSessionID, nil
}

func sessionIDFromEntry(entry os.DirEntry) (string, bool, error) {
	name := entry.Name()
	if !strings.HasPrefix(name, "session_") || !strings.HasSuffix(name, ".jsonl") {
		return "", false, nil
	}
	info, err := entry.Info()
	if err != nil {
		return "", true, fmt.Errorf("inspect session log %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", true, fmt.Errorf("unsafe session log %q: entry is not a regular file", name)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, "session_"), ".jsonl")
	if err := validateSessionID(id); err != nil {
		return "", true, err
	}
	return id, true, nil
}

// ActiveSessionPointer tracks the currently active session ID.
type ActiveSessionPointer struct {
	ActiveSessionID string `json:"active_session_id"`
}

// SessionStartEntry logs the start of a session.
type SessionStartEntry struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	CreatedAt string `json:"created_at"`
}

// SessionEndEntry logs the termination of a session.
type SessionEndEntry struct {
	Type    string `json:"type"`
	EndedAt string `json:"ended_at"`
	Status  string `json:"status"`
}

// CommandLogEntry logs telemetry metadata for an executed command.
type CommandLogEntry struct {
	Type        string   `json:"type"`
	Timestamp   string   `json:"timestamp"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Intention   string   `json:"intention"`
	ExecutionMs int64    `json:"execution_ms"`
	Status      string   `json:"status"`
}

// GetActiveSessionID retrieves the currently active session ID if it exists.
func GetActiveSessionID() (string, error) {
	return GetActiveSessionIDAt("")
}

// GetActiveSessionIDAt retrieves the active session rooted at a specific project.
func GetActiveSessionIDAt(root string) (string, error) {
	store, err := openSessionStore(root)
	if err != nil {
		return "", err
	}
	defer store.close()
	return store.activeSessionID()
}

// StartSession initializes a new session and writes the active session pointer.
func StartSession(customWord string) (string, error) {
	return StartSessionAt("", customWord)
}

// StartSessionAt starts a session under the specified project root.
func StartSessionAt(root, customWord string) (string, error) {
	store, err := openSessionStore(root)
	if err != nil {
		return "", err
	}
	defer store.close()

	// 1. Check if a session is already active
	activeID, err := store.activeSessionID()
	if err != nil {
		return "", err
	}
	if activeID != "" {
		return "", fmt.Errorf("a session is already active: %q. Please end it first", activeID)
	}

	// 2. Generate unique session ID
	timestamp := time.Now().Format("20060102_150405")
	var sessionID string
	if customWord != "" {
		// Clean the custom word (alphanumeric and underscores only)
		reg := regexp.MustCompile("[^a-zA-Z0-9_]")
		cleanWord := reg.ReplaceAllString(customWord, "")
		if cleanWord == "" {
			cleanWord = "custom"
		}
		sessionID = fmt.Sprintf("%s_%s", cleanWord, timestamp)
	} else {
		// Generate 6 random hex characters
		randBytes := make([]byte, 3)
		_, _ = rand.Read(randBytes)
		randSlug := hex.EncodeToString(randBytes)
		sessionID = fmt.Sprintf("session_%s_%s", randSlug, timestamp)
	}

	// 3. Ensure directories exist
	if err := store.files.EnsureRealDirectory(relSessionsDir, 0755); err != nil {
		return "", fmt.Errorf("create sessions directory: %w", err)
	}

	// 4. Create and write the session start log
	logFilePath, err := sessionLogPath(sessionID)
	if err != nil {
		return "", err
	}

	startEntry := SessionStartEntry{
		Type:      "session_start",
		SessionID: sessionID,
		CreatedAt: time.Now().Format(time.RFC3339),
	}
	startBytes, _ := json.Marshal(startEntry)
	if err := store.files.WriteRegularFile(logFilePath, append(startBytes, '\n'), 0644, true); err != nil {
		return "", fmt.Errorf("create session log file: %w", err)
	}

	// 5. Write the active session pointer
	ptr := ActiveSessionPointer{ActiveSessionID: sessionID}
	ptrBytes, _ := json.MarshalIndent(ptr, "", "  ")
	if err := store.files.WriteRegularFile(relActivePointerPath, ptrBytes, 0644, true); err != nil {
		return "", fmt.Errorf("write active session pointer: %w", err)
	}

	return sessionID, nil
}

// EndSession ends the currently active session.
func EndSession() (string, error) {
	return EndSessionAt("")
}

// EndSessionAt ends the active session under the specified project root.
func EndSessionAt(root string) (string, error) {
	store, err := openSessionStore(root)
	if err != nil {
		return "", err
	}
	defer store.close()

	// 1. Get active session ID
	activeID, err := store.activeSessionID()
	if err != nil {
		return "", err
	}
	if activeID == "" {
		return "", fmt.Errorf("no active session to end")
	}

	// 2. Append end entry to log file
	logFilePath, err := sessionLogPath(activeID)
	if err != nil {
		return activeID, err
	}
	endEntry := SessionEndEntry{
		Type:    "session_end",
		EndedAt: time.Now().Format(time.RFC3339),
		Status:  "completed",
	}
	endBytes, _ := json.Marshal(endEntry)
	if err := store.files.AppendRegularFile(logFilePath, append(endBytes, '\n')); err != nil {
		// If log file was manually deleted, still allow clean teardown of the active pointer
		_ = store.files.RemoveRegularFile(relActivePointerPath)
		return activeID, fmt.Errorf("open session log for append: %w (pointer cleaned up)", err)
	}

	// 3. Remove the active pointer file
	if err := store.files.RemoveRegularFile(relActivePointerPath); err != nil {
		return activeID, fmt.Errorf("remove active pointer: %w", err)
	}

	return activeID, nil
}

// LogCommand Telemetry records command execution details inside the active session log if present.
func LogCommand(command string, args []string, intention string, elapsed time.Duration, status string) error {
	return LogCommandAt("", command, args, intention, elapsed, status)
}

// LogCommandAt records command metadata under the specified project root.
func LogCommandAt(root, command string, args []string, intention string, elapsed time.Duration, status string) error {
	if command == "hook-guard" && status == "success" {
		return nil // Skip logging successful meta hook checks to keep the log clean
	}

	store, err := openSessionStore(root)
	if err != nil {
		return nil // Preserve telemetry's best-effort contract.
	}
	defer store.close()

	activeID, err := store.activeSessionID()
	if err != nil || activeID == "" {
		return nil // No active session to log to
	}

	logFilePath, err := sessionLogPath(activeID)
	if err != nil {
		return err
	}

	safeArgs := redactArgs(args)

	entry := CommandLogEntry{
		Type:        "command",
		Timestamp:   time.Now().Format(time.RFC3339),
		Command:     command,
		Args:        safeArgs,
		Intention:   intention,
		ExecutionMs: elapsed.Milliseconds(),
		Status:      status,
	}

	entryBytes, _ := json.Marshal(entry)
	if err := store.files.AppendRegularFile(logFilePath, append(entryBytes, '\n')); err != nil {
		return fmt.Errorf("write command log entry: %w", err)
	}

	return nil
}

func redactArgs(args []string) []string {
	sensitivePatterns := []string{"--config=", "--session=", "session_", "session/", ".gograph/"}
	redacted := make([]string, len(args))
	for i, arg := range args {
		for _, pattern := range sensitivePatterns {
			if strings.Contains(arg, pattern) {
				redacted[i] = "***REDACTED***"
				goto nextArg
			}
		}
		redacted[i] = arg
	nextArg:
	}
	return redacted
}

// GenericLogLine represents any log line parsed from a JSONL session file.
type GenericLogLine struct {
	Type        string   `json:"type"`
	SessionID   string   `json:"session_id"`
	CreatedAt   string   `json:"created_at"`
	EndedAt     string   `json:"ended_at"`
	Timestamp   string   `json:"timestamp"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	Intention   string   `json:"intention"`
	ExecutionMs int64    `json:"execution_ms"`
	Status      string   `json:"status"`
}

// AuditReport holds the calculated compliance and success metrics of a session.
type AuditReport struct {
	SessionID       string   `json:"session_id"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"created_at"`
	EndedAt         string   `json:"ended_at"`
	DurationSeconds float64  `json:"duration_seconds"`
	TotalCommands   int      `json:"total_commands"`
	SuccessCount    int      `json:"success_count"`
	FailureCount    int      `json:"failure_count"`
	SuccessRate     float64  `json:"success_rate"`
	PlanRun         bool     `json:"plan_run"`
	ReviewRun       bool     `json:"review_run"`
	ComposedCount   int      `json:"composed_count"`
	RawQueryCount   int      `json:"raw_query_count"`
	Composability   float64  `json:"composability"`
	ComplianceScore float64  `json:"compliance_score"`
	Grade           string   `json:"grade"`
	Recommendations []string `json:"recommendations"`
}

// FindMostRecentSessionID finds the session log file with the newest modification time.
func FindMostRecentSessionID() (string, error) {
	return FindMostRecentSessionIDAt("")
}

// FindMostRecentSessionIDAt finds the newest session under a project root.
func FindMostRecentSessionIDAt(root string) (string, error) {
	store, err := openSessionStore(root)
	if err != nil {
		return "", err
	}
	defer store.close()
	return store.findMostRecentSessionID()
}

func (s *sessionStore) findMostRecentSessionID() (string, error) {
	files, err := s.files.ReadDirectory(relSessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("no sessions directory exists yet")
	}
	if err != nil {
		return "", fmt.Errorf("read sessions directory: %w", err)
	}

	var newestID string
	var newestTime time.Time

	for _, f := range files {
		id, matches, err := sessionIDFromEntry(f)
		if err != nil {
			return "", err
		}
		if !matches {
			continue
		}
		info, err := f.Info()
		if err != nil {
			return "", fmt.Errorf("inspect session log %q: %w", f.Name(), err)
		}
		if newestID == "" || info.ModTime().After(newestTime) {
			newestID = id
			newestTime = info.ModTime()
		}
	}

	if newestID == "" {
		return "", fmt.Errorf("no session logs found in %s", filepath.Join(s.rootPath, relSessionsDir))
	}

	return newestID, nil
}

// RunAudit parses and scores a session for agent compliance and success rates.
func RunAudit(sessionID string, jsonMode bool) int {
	return RunAuditTo(sessionID, jsonMode, os.Stdout, os.Stderr)
}

// RunAuditTo parses and scores a session, writing only to the supplied
// streams. Server callers use this to avoid replacing process-global stdout.
func RunAuditTo(sessionID string, jsonMode bool, stdout, stderr io.Writer) int {
	return RunAuditToAt("", sessionID, jsonMode, stdout, stderr)
}

// RunAuditToAt audits a session under a specific project root.
func RunAuditToAt(root, sessionID string, jsonMode bool, stdout, stderr io.Writer) int {
	store, openErr := openSessionStore(root)
	if openErr != nil {
		_, _ = fmt.Fprintf(stderr, "Error opening session project: %v\n", openErr)
		return 1
	}
	defer store.close()

	var err error
	if sessionID == "" {
		sessionID, err = store.findMostRecentSessionID()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error locating session: %v\n", err)
			return 1
		}
	}

	relLogFilePath, err := sessionLogPath(sessionID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error opening session log: %v\n", err)
		return 1
	}
	logFilePath := filepath.Join(store.rootPath, relLogFilePath)
	data, err := store.files.ReadRegularFile(relLogFilePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error opening session log %q: %v\n", logFilePath, err)
		return 1
	}

	var lines []GenericLogLine
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		text := scanner.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		var line GenericLogLine
		if err := json.Unmarshal([]byte(text), &line); err == nil {
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		_, _ = fmt.Fprintf(stderr, "Error: session log %q is empty or corrupt\n", logFilePath)
		return 1
	}

	// 1. Accumulate metrics
	var start time.Time
	var end time.Time
	status := "In Progress"
	var totalCommands, successCount, failureCount int
	var planRun, reviewRun bool
	var composedCount, rawQueryCount int

	for _, l := range lines {
		switch l.Type {
		case "session_start":
			start, _ = time.Parse(time.RFC3339, l.CreatedAt)
		case "session_end":
			end, _ = time.Parse(time.RFC3339, l.EndedAt)
			status = "Completed"
		case "command":
			totalCommands++
			if l.Status == "success" {
				successCount++
			} else {
				failureCount++
			}

			// Trace command signatures
			switch l.Command {
			case "plan":
				planRun = true
				composedCount++
			case "review":
				reviewRun = true
				composedCount++
			case "context", "explain", "api", "changes", "mutate":
				composedCount++
			case "node", "callers", "callees", "source":
				rawQueryCount++
			}
		}
	}

	if end.IsZero() && !start.IsZero() && len(lines) > 0 {
		lastLine := lines[len(lines)-1]
		if lastLine.Timestamp != "" {
			end, _ = time.Parse(time.RFC3339, lastLine.Timestamp)
		} else {
			end = time.Now()
		}
	}

	duration := end.Sub(start)
	if duration < 0 {
		duration = 0
	}

	successRate := 100.0
	if totalCommands > 0 {
		successRate = (float64(successCount) / float64(totalCommands)) * 100.0
	}

	var planContrib, reviewContrib, composedContrib float64
	if planRun {
		planContrib = 35.0
	}
	if reviewRun {
		reviewContrib = 35.0
	}

	composability := 100.0
	if composedCount+rawQueryCount > 0 {
		composability = (float64(composedCount) / float64(composedCount+rawQueryCount)) * 100.0
	}
	composedContrib = composability * 0.30

	complianceScore := planContrib + reviewContrib + composedContrib

	var grade string
	switch {
	case complianceScore >= 90.0:
		grade = "A (Highly Compliant)"
	case complianceScore >= 80.0:
		grade = "B (Good Compliance)"
	case complianceScore >= 70.0:
		grade = "C (Needs Improvement)"
	default:
		grade = "F (Non-Compliant)"
	}

	var recs []string
	if !planRun {
		recs = append(recs, "Agent failed to execute 'plan <symbol>' before modifying code. Advise the agent to run 'plan <symbol>' to analyze downstreams and mapped tests.")
	}
	if !reviewRun {
		recs = append(recs, "Agent failed to execute 'review --uncommitted' or 'review <symbol>' to verify its edits. Instruct the agent to run 'review --uncommitted' post-edit.")
	}
	if rawQueryCount > 3 && composedCount == 0 {
		recs = append(recs, "Agent executed multiple individual raw queries (node/callers/callees) instead of the composed single-call 'context' tool. Prompt the agent to use 'context <symbol>' to save API context window tokens.")
	}

	report := AuditReport{
		SessionID:       sessionID,
		Status:          status,
		CreatedAt:       start.Format(time.RFC3339),
		EndedAt:         end.Format(time.RFC3339),
		DurationSeconds: duration.Seconds(),
		TotalCommands:   totalCommands,
		SuccessCount:    successCount,
		FailureCount:    failureCount,
		SuccessRate:     successRate,
		PlanRun:         planRun,
		ReviewRun:       reviewRun,
		ComposedCount:   composedCount,
		RawQueryCount:   rawQueryCount,
		Composability:   composability,
		ComplianceScore: complianceScore,
		Grade:           grade,
		Recommendations: recs,
	}

	output, err := formatAudit(report, jsonMode, duration)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error formatting session audit: %v\n", err)
		return 1
	}
	if _, err := io.WriteString(stdout, output); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error writing session audit: %v\n", err)
		return 1
	}
	return 0
}

func formatAudit(report AuditReport, jsonMode bool, duration time.Duration) (string, error) {
	if jsonMode {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data) + "\n", nil
	}

	var output strings.Builder
	write := func(format string, args ...any) {
		_, _ = fmt.Fprintf(&output, format, args...)
	}
	write("%s\n", strings.Repeat("=", 80))
	write("GOGRAPH AGENT SESSION AUDIT\n")
	write("%s\n", strings.Repeat("=", 80))
	write("Session ID      : %s\n", report.SessionID)
	write("Status          : %s\n", report.Status)
	write("Created At      : %s\n", report.CreatedAt)
	write("Ended At        : %s\n", report.EndedAt)
	write("Duration        : %v\n\n", duration.Round(time.Second))
	write("━━━ METRICS ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	write("Total Commands  : %d\n", report.TotalCommands)
	write("Successful      : %d\n", report.SuccessCount)
	write("Failed          : %d\n", report.FailureCount)
	write("Success Rate    : %.1f%%\n\n", report.SuccessRate)
	write("━━━ COMPLIANCE SCORE ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	write("Plan Rule Run   : %t (Weight: 35%%)\n", report.PlanRun)
	write("Review Rule Run : %t (Weight: 35%%)\n", report.ReviewRun)
	write("Composability   : %.1f%% (Weight: 30%%)\n\n", report.Composability)
	write("Overall Score   : %.1f%%\n", report.ComplianceScore)
	write("Compliance Grade: %s\n\n", report.Grade)
	write("━━━ RECOMMENDATIONS ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	if len(report.Recommendations) > 0 {
		for _, recommendation := range report.Recommendations {
			write("* %s\n", recommendation)
		}
	} else {
		write("Perfect! AI agent followed all core compliance and efficiency workflow rules.\n")
	}
	write("%s\n", strings.Repeat("=", 80))
	return output.String(), nil
}

// CleanupSessions deletes all inactive session JSONL logs. If no session is active, it deletes all logs.
func CleanupSessions() (int, error) {
	return CleanupSessionsAt("")
}

// CleanupSessionsAt removes inactive sessions under a specific project root.
func CleanupSessionsAt(root string) (int, error) {
	store, err := openSessionStore(root)
	if err != nil {
		return 0, err
	}
	defer store.close()

	files, err := store.files.ReadDirectory(relSessionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read sessions directory: %w", err)
	}

	activeID, err := store.activeSessionID()
	if err != nil {
		return 0, err
	}
	activeFileName := ""
	if activeID != "" {
		activeFileName = fmt.Sprintf("session_%s.jsonl", activeID)
	}

	type removableLog struct {
		path string
	}
	var removable []removableLog
	for _, f := range files {
		_, matches, err := sessionIDFromEntry(f)
		if err != nil {
			return 0, err
		}
		if !matches || activeFileName != "" && f.Name() == activeFileName {
			continue
		}
		removable = append(removable, removableLog{
			path: filepath.Join(relSessionsDir, f.Name()),
		})
	}

	deletedCount := 0
	for _, log := range removable {
		if err := store.files.RemoveRegularFile(log.path); err == nil {
			deletedCount++
		}
	}

	return deletedCount, nil
}
