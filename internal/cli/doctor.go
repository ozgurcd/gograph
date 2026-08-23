package cli

import (
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

const doctorSchemaVersion = "gograph.doctor.v1"

type doctorDocument struct {
	SchemaVersion string             `json:"schema_version"`
	Status        string             `json:"status"`
	Running       doctorExecutable   `json:"running"`
	PATHResolved  string             `json:"path_resolved,omitempty"`
	Candidates    []doctorExecutable `json:"candidates"`
	Findings      []doctorFinding    `json:"findings"`
}

type doctorExecutable struct {
	Path           string `json:"path"`
	CanonicalPath  string `json:"canonical_path"`
	Version        string `json:"version,omitempty"`
	ModuleVersion  string `json:"module_version,omitempty"`
	GoVersion      string `json:"go_version,omitempty"`
	Running        bool   `json:"running,omitempty"`
	PATHResolved   bool   `json:"path_resolved,omitempty"`
	BuildInfoError string `json:"build_info_error,omitempty"`
}

type doctorFinding struct {
	Code     string   `json:"code"`
	Severity string   `json:"severity"`
	Message  string   `json:"message"`
	Paths    []string `json:"paths,omitempty"`
}

func runDoctor(args []string) int {
	if len(args) != 0 {
		return failCommandf("doctor", "unknown argument: %s", args[0])
	}
	document, err := inspectDoctorState()
	if err != nil {
		return failCommand("doctor", err.Error())
	}
	if jsonMode {
		if err := json.NewEncoder(os.Stdout).Encode(document); err != nil {
			fmt.Fprintf(os.Stderr, "encode doctor JSON: %v\n", err)
			return exitError
		}
		return exitSuccess
	}

	fmt.Printf("gograph doctor: %s\n", strings.ToUpper(document.Status))
	fmt.Printf("  running : %s (v%s)\n", document.Running.Path, displayDoctorVersion(document.Running.Version))
	if document.PATHResolved == "" {
		fmt.Println("  PATH    : no gograph executable found")
	} else {
		resolvedVersion := "unknown"
		for _, candidate := range document.Candidates {
			if candidate.PATHResolved {
				resolvedVersion = displayDoctorVersion(candidate.Version)
				break
			}
		}
		fmt.Printf("  PATH    : %s (v%s)\n", document.PATHResolved, resolvedVersion)
	}
	fmt.Printf("  installs: %d distinct executable(s)\n", len(document.Candidates))
	for _, candidate := range document.Candidates {
		markers := make([]string, 0, 2)
		if candidate.Running {
			markers = append(markers, "running")
		}
		if candidate.PATHResolved {
			markers = append(markers, "PATH")
		}
		marker := ""
		if len(markers) > 0 {
			marker = " [" + strings.Join(markers, ", ") + "]"
		}
		fmt.Printf("    - %s  v%s%s\n", candidate.Path, displayDoctorVersion(candidate.Version), marker)
	}
	for _, finding := range document.Findings {
		fmt.Printf("  %s %s: %s\n", strings.ToUpper(finding.Severity), finding.Code, finding.Message)
	}
	return exitSuccess
}

func inspectDoctorState() (doctorDocument, error) {
	runningPath, err := os.Executable()
	if err != nil {
		return doctorDocument{}, fmt.Errorf("resolve running executable: %w", err)
	}
	runningPath, err = filepath.Abs(runningPath)
	if err != nil {
		return doctorDocument{}, fmt.Errorf("resolve absolute running executable: %w", err)
	}
	runningCanonical := canonicalExecutablePath(runningPath)

	paths := executablePathsFromPATH()
	pathResolved := ""
	if len(paths) > 0 {
		pathResolved = paths[0]
	}
	seenCanonical := make(map[string]struct{})
	ordered := append([]string{runningPath}, paths...)
	candidates := make([]doctorExecutable, 0, len(ordered))
	for _, path := range ordered {
		canonical := canonicalExecutablePath(path)
		if _, exists := seenCanonical[canonical]; exists {
			continue
		}
		seenCanonical[canonical] = struct{}{}
		candidate := readDoctorExecutable(path)
		candidate.CanonicalPath = canonical
		candidate.Running = canonical == runningCanonical
		candidate.PATHResolved = pathResolved != "" && canonical == canonicalExecutablePath(pathResolved)
		if candidate.Running {
			candidate.Version = Version
		}
		candidates = append(candidates, candidate)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Running != candidates[j].Running {
			return candidates[i].Running
		}
		if candidates[i].PATHResolved != candidates[j].PATHResolved {
			return candidates[i].PATHResolved
		}
		return candidates[i].Path < candidates[j].Path
	})

	document := doctorDocument{
		SchemaVersion: doctorSchemaVersion,
		Status:        "ok",
		PATHResolved:  pathResolved,
		Candidates:    candidates,
		Findings:      []doctorFinding{},
	}
	for _, candidate := range candidates {
		if candidate.Running {
			document.Running = candidate
			break
		}
	}
	if len(candidates) > 1 {
		paths := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			paths = append(paths, candidate.Path)
		}
		document.Findings = append(document.Findings, doctorFinding{
			Code: "multiple_installations", Severity: "warning",
			Message: "multiple distinct gograph executables are available; scripts and agents may select different builds",
			Paths:   paths,
		})
	}
	if pathResolved == "" {
		document.Findings = append(document.Findings, doctorFinding{
			Code: "not_on_path", Severity: "warning",
			Message: "the running gograph executable is not discoverable through PATH",
			Paths:   []string{runningPath},
		})
	} else if canonicalExecutablePath(pathResolved) != runningCanonical {
		document.Findings = append(document.Findings, doctorFinding{
			Code: "running_path_mismatch", Severity: "warning",
			Message: "the running executable differs from the gograph selected by PATH",
			Paths:   []string{runningPath, pathResolved},
		})
	}
	appendDoctorVersionFindings(&document)
	if len(document.Findings) > 0 {
		document.Status = "warning"
	}
	return document, nil
}

func executablePathsFromPATH() []string {
	name := "gograph"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	seen := make(map[string]struct{})
	var paths []string
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if directory == "" {
			directory = "."
		}
		path, err := filepath.Abs(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			continue
		}
		canonical := canonicalExecutablePath(path)
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func canonicalExecutablePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func readDoctorExecutable(path string) doctorExecutable {
	candidate := doctorExecutable{Path: path}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		candidate.BuildInfoError = err.Error()
		return candidate
	}
	candidate.ModuleVersion = strings.TrimPrefix(info.Main.Version, "v")
	candidate.GoVersion = info.GoVersion
	for _, setting := range info.Settings {
		if setting.Key == "-ldflags" {
			candidate.Version = linkedDoctorVersion(setting.Value)
			break
		}
	}
	if candidate.Version == "" && info.Main.Path == "github.com/ozgurcd/gograph" {
		candidate.Version = candidate.ModuleVersion
	}
	return candidate
}

func linkedDoctorVersion(ldflags string) string {
	const marker = "main.version="
	index := strings.Index(ldflags, marker)
	if index < 0 {
		return ""
	}
	value := ldflags[index+len(marker):]
	if end := strings.IndexAny(value, " \t\r\n\"'"); end >= 0 {
		value = value[:end]
	}
	return strings.TrimPrefix(value, "v")
}

func appendDoctorVersionFindings(document *doctorDocument) {
	newestVersion := ""
	newestPath := ""
	for _, candidate := range document.Candidates {
		version := doctorSemver(candidate.Version)
		if version == "" {
			continue
		}
		if newestVersion == "" || semver.Compare(version, newestVersion) > 0 {
			newestVersion = version
			newestPath = candidate.Path
		}
	}
	if newestVersion == "" {
		return
	}
	for _, candidate := range document.Candidates {
		if !candidate.Running && !candidate.PATHResolved {
			continue
		}
		version := doctorSemver(candidate.Version)
		if version == "" || semver.Compare(version, newestVersion) >= 0 {
			continue
		}
		code := "running_older_than_available"
		label := "running executable"
		if candidate.PATHResolved && !candidate.Running {
			code = "path_shadows_newer"
			label = "PATH-resolved executable"
		}
		document.Findings = append(document.Findings, doctorFinding{
			Code: code, Severity: "warning",
			Message: fmt.Sprintf("%s v%s is older than available v%s", label, candidate.Version, strings.TrimPrefix(newestVersion, "v")),
			Paths:   []string{candidate.Path, newestPath},
		})
	}
}

func doctorSemver(version string) string {
	if version == "" || version == "dev" {
		return ""
	}
	value := "v" + strings.TrimPrefix(version, "v")
	if !semver.IsValid(value) {
		return ""
	}
	// Development, dirty, and prerelease identifiers do not provide a reliable
	// total order against an installed stable build. Report their coexistence
	// and path mismatch, but reserve older/newer claims for stable releases.
	if semver.Prerelease(value) != "" {
		return ""
	}
	return value
}

func displayDoctorVersion(version string) string {
	if version == "" {
		return "unknown"
	}
	return strings.TrimPrefix(version, "v")
}
