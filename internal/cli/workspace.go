package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/ozgurcd/gograph/internal/graph"
	"github.com/ozgurcd/gograph/internal/mcp"
	"github.com/ozgurcd/gograph/internal/sourcefs"
	workspacegraph "github.com/ozgurcd/gograph/internal/workspace"
)

type workspaceMutation struct {
	RepositoryID      string `json:"repository_id"`
	Path              string `json:"path"`
	BeforeFingerprint string `json:"before_fingerprint"`
	AfterFingerprint  string `json:"after_fingerprint"`
	Error             string `json:"error,omitempty"`
}

type workspaceBuildResult struct {
	SchemaVersion       string              `json:"schema_version"`
	WorkspaceName       string              `json:"workspace_name"`
	InputFingerprint    string              `json:"input_fingerprint,omitempty"`
	ArtifactFingerprint string              `json:"artifact_fingerprint,omitempty"`
	OverlayPublished    bool                `json:"overlay_published"`
	RefreshPlan         []workspaceMutation `json:"refresh_plan"`
	RefreshAttempted    []workspaceMutation `json:"refresh_attempted"`
	RefreshSucceeded    []workspaceMutation `json:"refresh_succeeded"`
	RefreshFailed       []workspaceMutation `json:"refresh_failed"`
}

func runWorkspace(args []string) int {
	if len(args) == 0 {
		return failCommand("workspace", "usage: gograph workspace <build|status|query|path|impact|mcp> [args]")
	}
	switch args[0] {
	case "build":
		return runWorkspaceBuild(args[1:])
	case "status":
		return runWorkspaceStatus(args[1:])
	case "query":
		return runWorkspaceQuery(args[1:])
	case "path":
		return runWorkspacePath(args[1:])
	case "impact":
		return runWorkspaceImpact(args[1:])
	case "mcp":
		return runWorkspaceMCP(args[1:])
	default:
		return failCommandf("workspace", "unknown workspace command %q", args[0])
	}
}

func runWorkspaceBuild(args []string) int {
	start := "."
	startSet := false
	refreshMembers := false
	for _, argument := range args {
		switch {
		case argument == "--refresh-members":
			refreshMembers = true
		case strings.HasPrefix(argument, "-"):
			return failCommandf("workspace build", "unknown argument: %s", argument)
		case !startSet:
			start = argument
			startSet = true
		default:
			return failCommand("workspace build", "usage: gograph workspace build [path] [--refresh-members]")
		}
	}
	root, err := workspacegraph.FindRoot(start)
	if err != nil {
		return failCommand("workspace build", err.Error())
	}
	manifest, _, err := workspacegraph.LoadManifest(root)
	if err != nil {
		return failCommand("workspace build", err.Error())
	}
	result := workspaceBuildResult{SchemaVersion: "gograph.workspace-build.v1", WorkspaceName: manifest.Name, RefreshPlan: []workspaceMutation{}, RefreshAttempted: []workspaceMutation{}, RefreshSucceeded: []workspaceMutation{}, RefreshFailed: []workspaceMutation{}}
	inspections := workspacegraph.InspectMembers(context.Background(), root, manifest)
	for _, inspection := range inspections {
		if inspection.Error == nil {
			continue
		}
		result.RefreshPlan = append(result.RefreshPlan, workspaceMutation{RepositoryID: inspection.Config.ID, Path: inspection.Config.Path, BeforeFingerprint: graphArtifactFingerprint(inspection.Root)})
	}
	if len(result.RefreshPlan) > 0 && !refreshMembers {
		message := fmt.Sprintf("%d member graph(s) require refresh; rerun with --refresh-members to permit multi-repository mutation", len(result.RefreshPlan))
		return writeWorkspaceBuildFailure(result, message)
	}
	if refreshMembers {
		for _, planned := range result.RefreshPlan {
			attempt := planned
			result.RefreshAttempted = append(result.RefreshAttempted, attempt)
			config, _ := workspacegraph.RepositoryByID(manifest, planned.RepositoryID)
			memberRoot, rootErr := workspacegraph.ResolveMemberRoot(root, config)
			if rootErr != nil {
				attempt.Error = rootErr.Error()
				result.RefreshFailed = append(result.RefreshFailed, attempt)
				return writeWorkspaceBuildFailure(result, fmt.Sprintf("refresh repository %q: %v", config.ID, rootErr))
			}
			if err := refreshWorkspaceMember(memberRoot, config.Precision == "precise"); err != nil {
				attempt.Error = err.Error()
				attempt.AfterFingerprint = graphArtifactFingerprint(memberRoot)
				result.RefreshFailed = append(result.RefreshFailed, attempt)
				return writeWorkspaceBuildFailure(result, fmt.Sprintf("refresh repository %q: %v", config.ID, err))
			}
			attempt.AfterFingerprint = graphArtifactFingerprint(memberRoot)
			if inspection := workspacegraph.InspectMember(context.Background(), root, config); inspection.Error != nil {
				attempt.Error = inspection.Error.Error()
				result.RefreshFailed = append(result.RefreshFailed, attempt)
				return writeWorkspaceBuildFailure(result, fmt.Sprintf("refreshed repository %q is unusable: %v", config.ID, inspection.Error))
			}
			result.RefreshSucceeded = append(result.RefreshSucceeded, attempt)
		}
	}
	members, err := workspacegraph.LoadMembers(context.Background(), root, manifest)
	if err != nil {
		return writeWorkspaceBuildFailure(result, err.Error())
	}
	artifact, err := workspacegraph.Resolve(manifest, members)
	if err != nil {
		return writeWorkspaceBuildFailure(result, err.Error())
	}
	if err := workspacegraph.Publish(root, artifact); err != nil {
		return writeWorkspaceBuildFailure(result, err.Error())
	}
	_, artifactFingerprint, err := workspacegraph.LoadArtifact(root)
	if err != nil {
		return writeWorkspaceBuildFailure(result, err.Error())
	}
	result.InputFingerprint = artifact.InputFingerprint
	result.ArtifactFingerprint = artifactFingerprint
	result.OverlayPublished = true
	if jsonMode {
		return PrintJSON(okEnvelope("workspace build", manifest.Name, result, 1))
	}
	fmt.Printf("Workspace %s overlay published.\n", manifest.Name)
	fmt.Printf("  input fingerprint: %s\n", result.InputFingerprint)
	fmt.Printf("  artifact fingerprint: %s\n", result.ArtifactFingerprint)
	if len(result.RefreshSucceeded) > 0 {
		fmt.Printf("  refreshed member graphs: %d\n", len(result.RefreshSucceeded))
	}
	return 0
}

func writeWorkspaceBuildFailure(result workspaceBuildResult, message string) int {
	if jsonMode {
		envelope := errEnvelope("workspace build", message)
		envelope.Results = result
		return PrintJSON(envelope)
	}
	fmt.Fprintln(os.Stderr, message)
	if len(result.RefreshPlan) > 0 {
		fmt.Fprintln(os.Stderr, "refresh plan:")
		for _, mutation := range result.RefreshPlan {
			fmt.Fprintf(os.Stderr, "  %s (%s)\n", mutation.RepositoryID, mutation.Path)
		}
	}
	for _, mutation := range result.RefreshSucceeded {
		fmt.Fprintf(os.Stderr, "refreshed before failure: %s (%s -> %s)\n", mutation.RepositoryID, mutation.BeforeFingerprint, mutation.AfterFingerprint)
	}
	return 1
}

func refreshWorkspaceMember(root string, preciseMode bool) error {
	buildConfig, configErr := resolveBuildConfig(root)
	previous, _ := loadGraph(root)
	g, err := buildGraphWithConfig(root, buildConfig, configErr, previous)
	if err != nil {
		return err
	}
	if preciseMode {
		if err := enrichGraphPreciselyWithConfig(root, g, buildConfig, configErr); err != nil {
			return fmt.Errorf("precise enrichment failed: %w", err)
		}
	}
	sortGraph(g)
	if err := refreshCompleteGraphSourceFingerprint(root, g, buildConfig); err != nil {
		return err
	}
	_, err = publishGraphArtifacts(root, g, manualArtifactPublication)
	return err
}

func graphArtifactFingerprint(root string) string {
	reader, err := sourcefs.Open(root)
	if err != nil {
		return ""
	}
	defer func() { _ = reader.Close() }()
	data, err := reader.ReadRegularFileLimit(".gograph/graph.json", graph.MaxArtifactBytes)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func runWorkspaceStatus(args []string) int {
	if len(args) > 1 || len(args) == 1 && strings.HasPrefix(args[0], "-") {
		return failCommand("workspace status", "usage: gograph workspace status [path]")
	}
	start := "."
	if len(args) == 1 {
		start = args[0]
	}
	root, err := workspacegraph.FindRoot(start)
	if err != nil {
		return failCommand("workspace status", err.Error())
	}
	manifest, _, err := workspacegraph.LoadManifest(root)
	if err != nil {
		return failCommand("workspace status", err.Error())
	}
	status := workspacegraph.InspectStatus(context.Background(), root, manifest)
	if jsonMode {
		return PrintJSON(okEnvelope("workspace status", manifest.Name, status, len(status.Members)))
	}
	fmt.Printf("Workspace %s: %s\n", status.WorkspaceName, status.AggregateState)
	for _, member := range status.Members {
		state := "fresh"
		if !member.Available {
			state = "unavailable"
		} else if !member.Fresh {
			state = "stale"
		}
		fmt.Printf("  %-20s %s (%s)\n", member.RepositoryID, state, member.AnalysisMode)
		for _, diagnostic := range member.Diagnostics {
			fmt.Printf("    %s\n", diagnostic)
		}
	}
	overlayState := "missing/stale"
	if status.Overlay.Fresh {
		overlayState = "fresh"
	}
	fmt.Printf("  overlay: %s\n", overlayState)
	return 0
}

type workspaceQueryArgs struct {
	Start           string
	Scope           string
	IncludePossible bool
	Positional      []string
}

func parseWorkspaceQueryArgs(args []string) (workspaceQueryArgs, error) {
	parsed := workspaceQueryArgs{Start: "."}
	seenScope, seenWorkspace, seenIncludePossible := false, false, false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--scope":
			if seenScope {
				return parsed, fmt.Errorf("--scope may be specified only once")
			}
			seenScope = true
			if index+1 >= len(args) {
				return parsed, fmt.Errorf("--scope requires a value")
			}
			index++
			parsed.Scope = args[index]
		case "--workspace":
			if seenWorkspace {
				return parsed, fmt.Errorf("--workspace may be specified only once")
			}
			seenWorkspace = true
			if index+1 >= len(args) {
				return parsed, fmt.Errorf("--workspace requires a value")
			}
			index++
			parsed.Start = args[index]
		case "--include-possible":
			if seenIncludePossible {
				return parsed, fmt.Errorf("--include-possible may be specified only once")
			}
			seenIncludePossible = true
			parsed.IncludePossible = true
		default:
			if strings.HasPrefix(args[index], "-") {
				return parsed, fmt.Errorf("unknown argument: %s", args[index])
			}
			parsed.Positional = append(parsed.Positional, args[index])
		}
	}
	return parsed, nil
}

func loadWorkspaceQuery(parsed workspaceQueryArgs) (*workspacegraph.LoadedWorkspace, workspacegraph.ScopeOverlay, error) {
	loaded, err := workspacegraph.Load(context.Background(), parsed.Start)
	if err != nil {
		return nil, workspacegraph.ScopeOverlay{}, err
	}
	scope, err := workspacegraph.SelectScope(loaded, parsed.Scope)
	return loaded, scope, err
}

func runWorkspaceQuery(args []string) int {
	parsed, err := parseWorkspaceQueryArgs(args)
	if err == nil && parsed.IncludePossible {
		err = fmt.Errorf("--include-possible applies only to workspace path and impact")
	}
	if err != nil || len(parsed.Positional) == 0 {
		if err == nil {
			err = fmt.Errorf("usage: gograph workspace query [--scope id] <term...>")
		}
		return failCommand("workspace query", err.Error())
	}
	term := strings.Join(parsed.Positional, " ")
	if strings.TrimSpace(term) == "" {
		return failCommand("workspace query", "query term must not be empty")
	}
	loaded, scope, err := loadWorkspaceQuery(parsed)
	if err != nil {
		return failCommand("workspace query", err.Error())
	}
	response := workspacegraph.Query(loaded, scope, term)
	if jsonMode {
		return PrintJSON(okEnvelope("workspace query", term, response, len(response.Results)))
	}
	for _, result := range response.Results {
		fmt.Printf("%s", workspaceDisplayNode(result.Node))
		if result.File != "" {
			fmt.Printf(" (%s:%d)", result.File, result.Line)
		}
		fmt.Println()
	}
	return 0
}

func runWorkspacePath(args []string) int {
	parsed, err := parseWorkspaceQueryArgs(args)
	if err != nil || len(parsed.Positional) != 2 {
		if err == nil {
			err = fmt.Errorf("usage: gograph workspace path [--scope id] [--include-possible] <from> <to>")
		}
		return failCommand("workspace path", err.Error())
	}
	loaded, scope, err := loadWorkspaceQuery(parsed)
	if err != nil {
		return failCommand("workspace path", err.Error())
	}
	response, err := workspacegraph.Path(loaded, scope, parsed.Positional[0], parsed.Positional[1], parsed.IncludePossible)
	if err != nil {
		return failCommand("workspace path", err.Error())
	}
	if jsonMode {
		count := len(response.Steps)
		return PrintJSON(okEnvelope("workspace path", strings.Join(parsed.Positional, " -> "), response, count))
	}
	if !response.Found {
		fmt.Println("No workspace path found.")
		return 0
	}
	for _, step := range response.Steps {
		fmt.Printf("%s --%s--> %s\n", workspaceDisplayNode(step.From), step.Kind, workspaceDisplayNode(step.To))
	}
	return 0
}

func runWorkspaceImpact(args []string) int {
	parsed, err := parseWorkspaceQueryArgs(args)
	if err != nil || len(parsed.Positional) != 1 {
		if err == nil {
			err = fmt.Errorf("usage: gograph workspace impact [--scope id] [--include-possible] <target>")
		}
		return failCommand("workspace impact", err.Error())
	}
	loaded, scope, err := loadWorkspaceQuery(parsed)
	if err != nil {
		return failCommand("workspace impact", err.Error())
	}
	response, err := workspacegraph.Impact(loaded, scope, parsed.Positional[0], parsed.IncludePossible)
	if err != nil {
		return failCommand("workspace impact", err.Error())
	}
	if jsonMode {
		return PrintJSON(okEnvelope("workspace impact", parsed.Positional[0], response, len(response.Affected)))
	}
	for _, result := range response.Affected {
		fmt.Println(workspaceDisplayNode(result.Node))
	}
	return 0
}

func workspaceDisplayNode(node workspacegraph.NodeRef) string {
	if node.RepositoryID == "" {
		return node.Kind + ":" + node.NodeID
	}
	return node.RepositoryID + ":" + node.NodeID
}

func runWorkspaceMCP(args []string) int {
	if len(args) > 1 || len(args) == 1 && strings.HasPrefix(args[0], "-") {
		return failCommand("workspace mcp", "usage: gograph workspace mcp [path]")
	}
	start := "."
	if len(args) == 1 {
		start = args[0]
	}
	root, err := workspacegraph.FindRoot(start)
	if err != nil {
		return failCommand("workspace mcp", err.Error())
	}
	if _, _, err := workspacegraph.LoadManifest(root); err != nil {
		return failCommand("workspace mcp", err.Error())
	}
	if err := mcp.ServeWorkspace(root, Version); err != nil {
		return failCommand("workspace mcp", err.Error())
	}
	return 0
}
