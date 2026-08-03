package mcpbundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// SmokeResult records the MCP identity and tools returned by a native bundle.
type SmokeResult struct {
	ServerName    string
	ServerVersion string
	ToolNames     []string
}

// SmokeNative validates and executes only the host-native bundle, initializes
// MCP over stdio, and requests tools/list from the selected Go project.
func SmokeNative(ctx context.Context, inputDir, projectDir, version string) (*SmokeResult, error) {
	target, ok := TargetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return nil, fmt.Errorf("native platform %s/%s is not a supported MCPB target", runtime.GOOS, runtime.GOARCH)
	}
	project, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve smoke project directory: %w", err)
	}
	info, err := os.Stat(project)
	if err != nil {
		return nil, fmt.Errorf("inspect smoke project directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("smoke project path %q is not a directory", project)
	}
	bundlePath := filepath.Join(inputDir, target.ArtifactName(version))
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("read native MCPB: %w", err)
	}
	verified, err := verifyBundle(bundle, target, version, "")
	if err != nil {
		return nil, fmt.Errorf("verify native MCPB: %w", err)
	}
	installDir, err := os.MkdirTemp("", "gograph-mcpb-smoke-")
	if err != nil {
		return nil, fmt.Errorf("create smoke install directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(installDir) }()
	serverDir := filepath.Join(installDir, "server")
	if err := os.MkdirAll(serverDir, 0o755); err != nil {
		return nil, fmt.Errorf("create smoke server directory: %w", err)
	}
	executable := target.InstalledExecutable(installDir)
	if err := os.WriteFile(executable, verified.binary, 0o755); err != nil {
		return nil, fmt.Errorf("install smoke executable: %w", err)
	}
	command, args, err := ResolveCommand(verified.verification.Manifest, target, installDir, project)
	if err != nil {
		return nil, err
	}
	stdioClient, err := client.NewStdioMCPClient(command, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("start bundled MCP server: %w", err)
	}
	defer func() { _ = stdioClient.Close() }()

	request := mcp.InitializeRequest{}
	request.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	request.Params.ClientInfo = mcp.Implementation{Name: "gograph-mcpb-smoke", Version: "1.0.0"}
	request.Params.Capabilities = mcp.ClientCapabilities{}
	initialized, err := stdioClient.Initialize(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("initialize bundled MCP server: %w", err)
	}
	if initialized.ServerInfo.Name != ServerName || initialized.ServerInfo.Version != version {
		return nil, fmt.Errorf("bundled MCP identity = %s@%s, want %s@%s", initialized.ServerInfo.Name, initialized.ServerInfo.Version, ServerName, version)
	}
	listed, err := stdioClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list bundled MCP tools: %w", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	for _, required := range []string{"gograph_callers", "gograph_capabilities"} {
		if !slices.Contains(names, required) {
			return nil, fmt.Errorf("bundled MCP tools/list is missing %q", required)
		}
	}
	return &SmokeResult{ServerName: initialized.ServerInfo.Name, ServerVersion: initialized.ServerInfo.Version, ToolNames: names}, nil
}
