package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	protocol "github.com/mark3labs/mcp-go/mcp"
	"github.com/ozgurcd/gograph/internal/graph"
	mcppkg "github.com/ozgurcd/gograph/internal/mcp"
)

type workspaceQueryResult struct {
	Node struct {
		RepositoryID string `json:"repository_id"`
		Kind         string `json:"kind"`
	} `json:"node"`
	Name string `json:"name"`
}

type workspaceImpactResult struct {
	Node struct {
		RepositoryID string `json:"repository_id"`
		NodeID       string `json:"node_id"`
	} `json:"node"`
}

func TestWorkspaceBuildQueryPathAndStatusEndToEnd(t *testing.T) {
	root := t.TempDir()
	client := filepath.Join(root, "client")
	server := filepath.Join(root, "server")
	for _, repo := range []string{client, server} {
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkspaceTestFile(t, filepath.Join(server, "go.mod"), "module example.com/service\n\ngo 1.27.0\n")
	writeWorkspaceTestFile(t, filepath.Join(server, "api.go"), `package service
import "net/http"
func Handle(http.ResponseWriter, *http.Request) {}
func Routes() { http.HandleFunc("/items", Handle) }
`)
	writeWorkspaceTestFile(t, filepath.Join(client, "go.mod"), `module example.com/client

go 1.27.0

require example.com/service v0.0.0
replace example.com/service => ../server
`)
	clientSource := `package client
import (
    "net/http"
    service "example.com/service"
)
func Call() {
    service.Handle(nil, nil)
}
func HTTPOnly() {
    _, _ = http.Get("https://api.internal/items")
}
`
	writeWorkspaceTestFile(t, filepath.Join(client, "client.go"), clientSource)
	writeWorkspaceTestFile(t, filepath.Join(root, ".gograph-workspace.yml"), `schema_version: gograph.workspace-manifest.v1
name: e2e
default_scope: production
repositories:
  - id: client
    path: client
  - id: server
    path: server
    services:
      - id: api
        http:
          authorities: [api.internal]
scopes:
  - id: production
    repositories: [client, server]
`)
	bin := buildTestBinary(t)
	build := exec.Command(bin, "workspace", "build", "--refresh-members", "--json")
	build.Dir = root
	var buildStderr bytes.Buffer
	build.Stderr = &buildStderr
	buildOutput, buildErr := build.Output()
	if buildErr != nil {
		t.Fatalf("workspace build: %v\nstdout:\n%s\nstderr:\n%s", buildErr, buildOutput, buildStderr.String())
	}
	var buildEnvelope struct {
		Results struct {
			InputFingerprint    string            `json:"input_fingerprint"`
			ArtifactFingerprint string            `json:"artifact_fingerprint"`
			Published           bool              `json:"overlay_published"`
			Plan                []json.RawMessage `json:"refresh_plan"`
			Attempted           []json.RawMessage `json:"refresh_attempted"`
			Succeeded           []json.RawMessage `json:"refresh_succeeded"`
			Failed              []json.RawMessage `json:"refresh_failed"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buildOutput, &buildEnvelope); err != nil || !buildEnvelope.Results.Published || buildEnvelope.Results.InputFingerprint == "" || buildEnvelope.Results.ArtifactFingerprint == "" || len(buildEnvelope.Results.Plan) != 2 || len(buildEnvelope.Results.Attempted) != 2 || len(buildEnvelope.Results.Succeeded) != 2 || len(buildEnvelope.Results.Failed) != 0 {
		t.Fatalf("workspace refresh build result: %v %+v\n%s", err, buildEnvelope, buildOutput)
	}
	artifactPath := filepath.Join(root, ".gograph", "workspace.json")
	first := fileHash(t, artifactPath)
	clientGraphPath := filepath.Join(client, ".gograph", "graph.json")
	serverGraphPath := filepath.Join(server, ".gograph", "graph.json")
	clientGraphFirst := fileHash(t, clientGraphPath)
	serverGraphFirst := fileHash(t, serverGraphPath)
	rebuild := exec.Command(bin, "workspace", "build", "--json")
	rebuild.Dir = root
	if output, err := rebuild.CombinedOutput(); err != nil {
		t.Fatalf("workspace deterministic rebuild: %v\n%s", err, output)
	}
	if second := fileHash(t, artifactPath); second != first {
		t.Fatalf("identical workspace inputs changed artifact: %s != %s", first, second)
	}
	if fileHash(t, clientGraphPath) != clientGraphFirst || fileHash(t, serverGraphPath) != serverGraphFirst {
		t.Fatal("workspace build without --refresh-members mutated a member graph")
	}

	pathCommand := exec.Command(bin, "workspace", "path", "client:Call", "server:Handle", "--json")
	pathCommand.Dir = root
	output, err := pathCommand.Output()
	if err != nil {
		t.Fatalf("workspace path: %v\n%s", err, output)
	}
	var pathEnvelope struct {
		Results struct {
			Found bool `json:"found"`
			Steps []struct {
				Kind string `json:"kind"`
			} `json:"steps"`
		} `json:"results"`
	}
	if err := json.Unmarshal(output, &pathEnvelope); err != nil || pathEnvelope.Results.Found {
		t.Fatalf("heuristic workspace path was traversable by default: %v %+v\n%s", err, pathEnvelope, output)
	}
	pathCommand = exec.Command(bin, "workspace", "path", "client:Call", "server:Handle", "--include-possible", "--json")
	pathCommand.Dir = root
	output, err = pathCommand.Output()
	if err != nil {
		t.Fatalf("workspace possible path: %v\n%s", err, output)
	}
	if err := json.Unmarshal(output, &pathEnvelope); err != nil || !pathEnvelope.Results.Found || len(pathEnvelope.Results.Steps) == 0 {
		t.Fatalf("workspace path output: %v %+v\n%s", err, pathEnvelope, output)
	}
	if pathEnvelope.Results.Steps[0].Kind != "calls" {
		t.Fatalf("cross-repository Go call was not materialized as ordinary calls: %+v", pathEnvelope.Results.Steps)
	}

	httpPathCommand := exec.Command(bin, "workspace", "path", "client:HTTPOnly", "server:Handle", "--json")
	httpPathCommand.Dir = root
	output, err = httpPathCommand.Output()
	if err != nil {
		t.Fatalf("workspace HTTP path: %v\n%s", err, output)
	}
	pathEnvelope = struct {
		Results struct {
			Found bool `json:"found"`
			Steps []struct {
				Kind string `json:"kind"`
			} `json:"steps"`
		} `json:"results"`
	}{}
	if err := json.Unmarshal(output, &pathEnvelope); err != nil || !pathEnvelope.Results.Found || len(pathEnvelope.Results.Steps) != 2 || pathEnvelope.Results.Steps[0].Kind != "calls_http" || pathEnvelope.Results.Steps[1].Kind != "serves_http" {
		t.Fatalf("workspace HTTP contract path: %v %+v\n%s", err, pathEnvelope, output)
	}

	statusCommand := exec.Command(bin, "workspace", "status", "--json")
	statusCommand.Dir = root
	output, err = statusCommand.Output()
	if err != nil {
		t.Fatalf("workspace status: %v\n%s", err, output)
	}
	var statusEnvelope struct {
		Results struct {
			AggregateState string `json:"aggregate_state"`
			Members        []struct {
				Available               bool   `json:"available"`
				Fresh                   bool   `json:"fresh"`
				ArtifactFingerprint     string `json:"artifact_fingerprint"`
				SourceFingerprint       string `json:"source_fingerprint"`
				BuildContextFingerprint string `json:"build_context_fingerprint"`
				AnalysisMode            string `json:"analysis_mode"`
				Capabilities            struct {
					ASTComplete     bool   `json:"ast_complete"`
					CallResolution  string `json:"call_resolution"`
					HTTPExtraction  string `json:"http_extraction"`
					RPCExtraction   string `json:"rpc_extraction"`
					TopicExtraction string `json:"topic_extraction"`
				} `json:"capabilities"`
			} `json:"members"`
			Overlay struct {
				Fresh               bool              `json:"fresh"`
				InputFingerprint    string            `json:"input_fingerprint"`
				ArtifactFingerprint string            `json:"artifact_fingerprint"`
				ResolverVersions    map[string]string `json:"resolver_versions"`
			} `json:"overlay"`
		} `json:"results"`
	}
	if err := json.Unmarshal(output, &statusEnvelope); err != nil || statusEnvelope.Results.AggregateState != "complete" || !statusEnvelope.Results.Overlay.Fresh || len(statusEnvelope.Results.Members) != 2 {
		t.Fatalf("workspace status output: %v %+v\n%s", err, statusEnvelope, output)
	}
	if statusEnvelope.Results.Overlay.InputFingerprint == "" || statusEnvelope.Results.Overlay.ArtifactFingerprint == "" || len(statusEnvelope.Results.Overlay.ResolverVersions) != 3 {
		t.Fatalf("workspace overlay status omitted identity: %+v", statusEnvelope.Results.Overlay)
	}
	for _, member := range statusEnvelope.Results.Members {
		if !member.Available || !member.Fresh || member.ArtifactFingerprint == "" || member.SourceFingerprint == "" || member.BuildContextFingerprint == "" || member.AnalysisMode != "ast" || !member.Capabilities.ASTComplete || member.Capabilities.CallResolution != "ast_heuristic" || member.Capabilities.HTTPExtraction != "net_http_v1" || member.Capabilities.RPCExtraction != "unavailable" || member.Capabilities.TopicExtraction != "unavailable" {
			t.Fatalf("workspace member status omitted capabilities or fingerprints: %+v", member)
		}
	}
	if !bytes.Contains(output, []byte(`"dirty": false`)) {
		t.Fatalf("clean member status omitted explicit dirty=false: %s", output)
	}

	queryRaw := runWorkspaceCLIResult(t, bin, root, "workspace", "query", "Handle")
	var queryResponse struct {
		Results []workspaceQueryResult `json:"results"`
	}
	if err := json.Unmarshal(queryRaw, &queryResponse); err != nil {
		t.Fatal(err)
	}
	if !workspaceQueryContains(queryResponse.Results, "server", "symbol", "Handle") {
		t.Fatalf("workspace query did not return server handler: %s", queryRaw)
	}
	moduleRaw := runWorkspaceCLIResult(t, bin, root, "workspace", "query", "example.com/service")
	queryResponse.Results = nil
	if err := json.Unmarshal(moduleRaw, &queryResponse); err != nil || !workspaceQueryContains(queryResponse.Results, "server", "module", "example.com/service") {
		t.Fatalf("workspace module query = %v %s", err, moduleRaw)
	}

	nested := filepath.Join(client, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = runWorkspaceCLIResult(t, bin, nested, "workspace", "query", "Handle")
	_ = runWorkspaceCLIResult(t, bin, t.TempDir(), "workspace", "query", "--workspace", root, "Handle")

	impactRaw := runWorkspaceCLIResult(t, bin, root, "workspace", "impact", "server:Handle")
	var impactResponse struct {
		Affected []workspaceImpactResult `json:"affected"`
	}
	if err := json.Unmarshal(impactRaw, &impactResponse); err != nil {
		t.Fatal(err)
	}
	if !workspaceImpactContains(impactResponse.Affected, "client", "::HTTPOnly") || workspaceImpactContains(impactResponse.Affected, "client", "::Call") {
		t.Fatalf("default impact did not isolate exact HTTP reachability: %s", impactRaw)
	}
	possibleImpactRaw := runWorkspaceCLIResult(t, bin, root, "workspace", "impact", "--include-possible", "server:Handle")
	impactResponse.Affected = nil
	if err := json.Unmarshal(possibleImpactRaw, &impactResponse); err != nil || !workspaceImpactContains(impactResponse.Affected, "client", "::Call") {
		t.Fatalf("possible impact omitted heuristic Go caller: %v %s", err, possibleImpactRaw)
	}

	assertWorkspaceCLIMCPParity(t, bin, root)
	assertWorkspaceMCPStdio(t, bin, root)
	for name, argsAndWant := range map[string]struct {
		args []string
		want string
	}{
		"build":  {args: []string{"workspace", "build"}, want: "Workspace e2e overlay published."},
		"status": {args: []string{"workspace", "status"}, want: "Workspace e2e: complete"},
		"query":  {args: []string{"workspace", "query", "Handle"}, want: "server:example.com/service::Handle"},
		"path":   {args: []string{"workspace", "path", "client:HTTPOnly", "server:Handle"}, want: "--calls_http-->"},
		"impact": {args: []string{"workspace", "impact", "server:Handle"}, want: "client:example.com/client::HTTPOnly"},
	} {
		t.Run("text_output_"+name, func(t *testing.T) {
			command := exec.Command(bin, argsAndWant.args...)
			command.Dir = root
			textOutput, err := command.CombinedOutput()
			if err != nil || !bytes.Contains(textOutput, []byte(argsAndWant.want)) {
				t.Fatalf("%s text output = %v\n%s", name, err, textOutput)
			}
		})
	}
	if fileHash(t, artifactPath) != first || fileHash(t, clientGraphPath) != clientGraphFirst || fileHash(t, serverGraphPath) != serverGraphFirst {
		t.Fatal("workspace status/query/path/impact or MCP handlers mutated persisted artifacts")
	}

	writeWorkspaceTestFile(t, filepath.Join(client, "client.go"), clientSource+"\n// stale workspace member\n")
	staleStatusRaw := runWorkspaceCLIResult(t, bin, root, "workspace", "status")
	if !bytes.Contains(staleStatusRaw, []byte(`"aggregate_state":"partial"`)) && !bytes.Contains(staleStatusRaw, []byte(`"aggregate_state": "partial"`)) {
		t.Fatalf("stale workspace status was not partial: %s", staleStatusRaw)
	}
	staleBuild := exec.Command(bin, "workspace", "build", "--json")
	staleBuild.Dir = root
	if staleOutput, staleErr := staleBuild.CombinedOutput(); staleErr == nil || !bytes.Contains(staleOutput, []byte("require refresh")) {
		t.Fatalf("read-only workspace build accepted stale member: %v\n%s", staleErr, staleOutput)
	}
	if fileHash(t, artifactPath) != first || fileHash(t, clientGraphPath) != clientGraphFirst || fileHash(t, serverGraphPath) != serverGraphFirst {
		t.Fatal("failed read-only workspace build mutated an artifact")
	}
	writeWorkspaceTestFile(t, filepath.Join(client, "client.go"), clientSource)
	restoredStatusRaw := runWorkspaceCLIResult(t, bin, root, "workspace", "status")
	if !bytes.Contains(restoredStatusRaw, []byte(`"aggregate_state":"complete"`)) && !bytes.Contains(restoredStatusRaw, []byte(`"aggregate_state": "complete"`)) {
		t.Fatalf("restored member did not return workspace to complete: %s", restoredStatusRaw)
	}

	clientGraphBytes, err := os.ReadFile(clientGraphPath)
	if err != nil {
		t.Fatal(err)
	}
	var forged graph.Graph
	if err := json.Unmarshal(clientGraphBytes, &forged); err != nil || len(forged.Modules) == 0 {
		t.Fatalf("decode client graph modules: %v %+v", err, forged.Modules)
	}
	forged.Modules[0].Path = "example.com/forged"
	forgedBytes, err := json.MarshalIndent(&forged, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clientGraphPath, append(forgedBytes, '\n'), 0o640); err != nil {
		t.Fatal(err)
	}
	statusCommand = exec.Command(bin, "workspace", "status", "--json")
	statusCommand.Dir = root
	output, err = statusCommand.Output()
	if err != nil || !bytes.Contains(output, []byte("verify graph module ownership")) || !bytes.Contains(output, []byte(`"aggregate_state": "partial"`)) {
		t.Fatalf("forged module inventory was not rejected: %v\n%s", err, output)
	}
	if err := os.WriteFile(clientGraphPath, clientGraphBytes, 0o640); err != nil {
		t.Fatal(err)
	}

	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(artifactBytes, []byte(`"workspace_name": "e2e"`), []byte(`"workspace_name": "tampered"`), 1)
	if bytes.Equal(tampered, artifactBytes) {
		t.Fatal("workspace artifact tamper fixture did not change bytes")
	}
	if err := os.WriteFile(artifactPath, tampered, 0o640); err != nil {
		t.Fatal(err)
	}
	queryAfterTamper := exec.Command(bin, "workspace", "query", "Call", "--json")
	queryAfterTamper.Dir = root
	if output, err := queryAfterTamper.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("does not match the deterministic overlay")) {
		t.Fatalf("tampered workspace artifact was accepted: %v\n%s", err, output)
	}
}

func assertWorkspaceMCPStdio(t *testing.T, binary, root string) {
	t.Helper()
	command := exec.Command(binary, "workspace", "mcp")
	command.Dir = root
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil || !command.ProcessState.Exited() {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	})
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	writeRequest := func(request any) {
		t.Helper()
		data, marshalErr := json.Marshal(request)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, writeErr := stdin.Write(append(data, '\n')); writeErr != nil {
			t.Fatalf("write workspace MCP request: %v\n%s", writeErr, stderr.String())
		}
	}
	readResponse := func() map[string]any {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("read workspace MCP response: %v\n%s", scanner.Err(), stderr.String())
		}
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("decode workspace MCP response: %v\n%s", err, scanner.Bytes())
		}
		return response
	}

	writeRequest(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "gograph-test", "version": "1"}}})
	if response := readResponse(); response["error"] != nil {
		t.Fatalf("workspace MCP initialize = %+v", response)
	}
	writeRequest(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	writeRequest(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{}})
	listResponse := readResponse()
	result, ok := listResponse["result"].(map[string]any)
	if !ok {
		t.Fatalf("workspace MCP tools/list = %+v", listResponse)
	}
	toolsList, ok := result["tools"].([]any)
	if !ok || len(toolsList) != 4 {
		t.Fatalf("workspace MCP tools = %+v", result["tools"])
	}
	wantTools := map[string]bool{"gograph_workspace_status": true, "gograph_workspace_query": true, "gograph_workspace_path": true, "gograph_workspace_impact": true}
	for _, rawTool := range toolsList {
		tool, ok := rawTool.(map[string]any)
		if !ok || !wantTools[fmt.Sprint(tool["name"])] {
			t.Fatalf("unexpected workspace MCP tool %+v", rawTool)
		}
		delete(wantTools, fmt.Sprint(tool["name"]))
	}
	if len(wantTools) != 0 {
		t.Fatalf("workspace MCP omitted tools %+v", wantTools)
	}

	writeRequest(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "gograph_workspace_status", "arguments": map[string]any{}}})
	statusResponse := readResponse()
	statusResult, ok := statusResponse["result"].(map[string]any)
	if !ok || statusResult["isError"] == true {
		t.Fatalf("workspace MCP status transport result = %+v", statusResponse)
	}
	content, ok := statusResult["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("workspace MCP status content = %+v", statusResult["content"])
	}
	textContent, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("workspace MCP status text = %+v", content[0])
	}
	var mcpStatus, cliStatus any
	if err := json.Unmarshal([]byte(fmt.Sprint(textContent["text"])), &mcpStatus); err != nil {
		t.Fatalf("decode workspace MCP status text: %v", err)
	}
	if err := json.Unmarshal(runWorkspaceCLIResult(t, binary, root, "workspace", "status"), &cliStatus); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cliStatus, mcpStatus) {
		t.Fatalf("workspace MCP stdio status differs from CLI\nCLI: %+v\nMCP: %+v", cliStatus, mcpStatus)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("workspace MCP shutdown: %v\n%s", err, stderr.String())
	}
}

func TestWorkspaceRefreshReportsPartialMemberMutation(t *testing.T) {
	root := t.TempDir()
	repoA := filepath.Join(root, "repo-a")
	repoB := filepath.Join(root, "repo-b")
	for _, repo := range []string{repoA, repoB} {
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeWorkspaceTestFile(t, filepath.Join(repoA, "go.mod"), "module example.com/a\n\ngo 1.27.0\n")
	writeWorkspaceTestFile(t, filepath.Join(repoA, "a.go"), "package a\nfunc A() {}\n")
	// repo-b intentionally has module metadata but no Go sources, so its
	// refresh fails after repo-a has already published its member graph.
	writeWorkspaceTestFile(t, filepath.Join(repoB, "go.mod"), "module example.com/b\n\ngo 1.27.0\n")
	writeWorkspaceTestFile(t, filepath.Join(root, ".gograph-workspace.yml"), `schema_version: gograph.workspace-manifest.v1
name: partial-refresh
repositories:
  - id: repo-a
    path: repo-a
  - id: repo-b
    path: repo-b
`)

	cmd := exec.Command(buildTestBinary(t), "workspace", "build", "--refresh-members", "--json")
	cmd.Dir = root
	output, err := cmd.Output()
	if err == nil {
		t.Fatalf("workspace refresh unexpectedly succeeded: %s", output)
	}
	var envelope struct {
		Status  string `json:"status"`
		Results struct {
			Plan      []map[string]any `json:"refresh_plan"`
			Attempted []map[string]any `json:"refresh_attempted"`
			Succeeded []map[string]any `json:"refresh_succeeded"`
			Failed    []map[string]any `json:"refresh_failed"`
			Published bool             `json:"overlay_published"`
		} `json:"results"`
	}
	if decodeErr := json.Unmarshal(output, &envelope); decodeErr != nil {
		t.Fatalf("decode output: %v\n%s", decodeErr, output)
	}
	if envelope.Status != "error" || len(envelope.Results.Plan) != 2 || len(envelope.Results.Attempted) != 2 || len(envelope.Results.Succeeded) != 1 || len(envelope.Results.Failed) != 1 || envelope.Results.Published {
		t.Fatalf("partial refresh result = %+v\n%s", envelope, output)
	}
	for _, group := range [][]map[string]any{envelope.Results.Plan, envelope.Results.Attempted, envelope.Results.Succeeded, envelope.Results.Failed} {
		for _, mutation := range group {
			if _, ok := mutation["before_fingerprint"]; !ok {
				t.Fatalf("mutation omitted before_fingerprint: %+v", mutation)
			}
			if _, ok := mutation["after_fingerprint"]; !ok {
				t.Fatalf("mutation omitted after_fingerprint: %+v", mutation)
			}
		}
	}
	if envelope.Results.Succeeded[0]["after_fingerprint"] == "" {
		t.Fatalf("successful refresh omitted its completed artifact fingerprint: %+v", envelope.Results.Succeeded[0])
	}
	if _, statErr := os.Stat(filepath.Join(repoA, ".gograph", "graph.json")); statErr != nil {
		t.Fatalf("first member was not published before second failed: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".gograph", "workspace.json")); !os.IsNotExist(statErr) {
		t.Fatalf("workspace overlay was published after partial failure: %v", statErr)
	}
}

func writeWorkspaceTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func runWorkspaceCLIResult(t *testing.T, binary, directory string, args ...string) json.RawMessage {
	t.Helper()
	args = append(args, "--json")
	command := exec.Command(binary, args...)
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, output)
	}
	var envelope struct {
		Status  string          `json:"status"`
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		t.Fatalf("decode %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	if envelope.Status == "error" || len(envelope.Results) == 0 {
		t.Fatalf("unexpected CLI result for %s: %s", strings.Join(args, " "), output)
	}
	return envelope.Results
}

func workspaceQueryContains(results []workspaceQueryResult, repository, kind, name string) bool {
	for _, result := range results {
		if result.Node.RepositoryID == repository && result.Node.Kind == kind && result.Name == name {
			return true
		}
	}
	return false
}

func workspaceImpactContains(results []workspaceImpactResult, repository, nodeSuffix string) bool {
	for _, result := range results {
		if result.Node.RepositoryID == repository && strings.HasSuffix(result.Node.NodeID, nodeSuffix) {
			return true
		}
	}
	return false
}

func assertWorkspaceCLIMCPParity(t *testing.T, binary, root string) {
	t.Helper()
	handlers := make(map[string]func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error))
	previous := mcppkg.ExposeWorkspaceToolsForTesting
	mcppkg.ExposeWorkspaceToolsForTesting = handlers
	t.Cleanup(func() { mcppkg.ExposeWorkspaceToolsForTesting = previous })
	mcppkg.NewWorkspaceServer(root, "test")

	tests := []struct {
		name      string
		tool      string
		cliArgs   []string
		arguments map[string]any
	}{
		{name: "status", tool: "gograph_workspace_status", cliArgs: []string{"workspace", "status"}, arguments: map[string]any{}},
		{name: "query", tool: "gograph_workspace_query", cliArgs: []string{"workspace", "query", "Handle"}, arguments: map[string]any{"term": "Handle"}},
		{name: "query_scope", tool: "gograph_workspace_query", cliArgs: []string{"workspace", "query", "--scope", "production", "Handle"}, arguments: map[string]any{"scope": "production", "term": "Handle"}},
		{name: "path", tool: "gograph_workspace_path", cliArgs: []string{"workspace", "path", "client:HTTPOnly", "server:Handle"}, arguments: map[string]any{"from": "client:HTTPOnly", "to": "server:Handle"}},
		{name: "path_possible", tool: "gograph_workspace_path", cliArgs: []string{"workspace", "path", "--include-possible", "client:Call", "server:Handle"}, arguments: map[string]any{"from": "client:Call", "to": "server:Handle", "include_possible": true}},
		{name: "impact", tool: "gograph_workspace_impact", cliArgs: []string{"workspace", "impact", "server:Handle"}, arguments: map[string]any{"target": "server:Handle"}},
		{name: "impact_possible", tool: "gograph_workspace_impact", cliArgs: []string{"workspace", "impact", "--include-possible", "server:Handle"}, arguments: map[string]any{"target": "server:Handle", "include_possible": true}},
	}
	for _, test := range tests {
		t.Run("cli_mcp_parity_"+test.name, func(t *testing.T) {
			cliRaw := runWorkspaceCLIResult(t, binary, root, test.cliArgs...)
			request := protocol.CallToolRequest{}
			request.Params.Arguments = test.arguments
			result, err := handlers[test.tool](context.Background(), request)
			if err != nil || result == nil || result.IsError || len(result.Content) != 1 {
				t.Fatalf("%s handler result = %+v, %v", test.tool, result, err)
			}
			content, ok := result.Content[0].(protocol.TextContent)
			if !ok {
				t.Fatalf("%s content type = %T", test.tool, result.Content[0])
			}
			var cliValue, mcpValue any
			if err := json.Unmarshal(cliRaw, &cliValue); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(content.Text), &mcpValue); err != nil {
				t.Fatalf("decode %s MCP JSON: %v\n%s", test.tool, err, content.Text)
			}
			if !reflect.DeepEqual(cliValue, mcpValue) {
				t.Fatalf("%s CLI/MCP mismatch\nCLI: %s\nMCP: %s", test.tool, cliRaw, content.Text)
			}
		})
	}

	invalid := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{name: "empty_query", tool: "gograph_workspace_query", arguments: map[string]any{"term": "  "}},
		{name: "missing_path_target", tool: "gograph_workspace_path", arguments: map[string]any{"from": "client:Call"}},
		{name: "empty_impact_target", tool: "gograph_workspace_impact", arguments: map[string]any{"target": ""}},
		{name: "unknown_scope", tool: "gograph_workspace_query", arguments: map[string]any{"scope": "missing", "term": "Handle"}},
	}
	for _, test := range invalid {
		t.Run("mcp_rejects_"+test.name, func(t *testing.T) {
			request := protocol.CallToolRequest{}
			request.Params.Arguments = test.arguments
			result, err := handlers[test.tool](context.Background(), request)
			if err != nil {
				t.Fatalf("%s handler transport error: %v", test.tool, err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("%s accepted invalid arguments %+v: %+v", test.tool, test.arguments, result)
			}
		})
	}
}
