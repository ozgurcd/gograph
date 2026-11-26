package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const claudeMDBlock = `
<!-- gograph-start: do not remove -->
## Gograph — Go Repository Intelligence

Rules (applied when gograph MCP server is connected):

1. Use gograph_query first for supported structural Go symbol searches. Use grep/rg for literals, comments, generated or non-indexed files, and targeted verification. If graph precision is ast/precise_fallback, results are ambiguous, or a known call is missing, cross-check with gopls or source/text search and disclose the fallback.
2. Before editing any symbol, run gograph_plan with symbol and with_context=true — this returns a static change plan and context for inspect_first symbols in one call.
3. After editing Go code, run gograph_review with uncommitted=true to inspect test mappings and the indexed blast radius; it is not proof that runtime behavior is unchanged.
4. To understand a symbol, use gograph_context — it returns node, source, callers, callees, tests, and an architectural role classification in one call. Use uncommitted=true to get context for all uncommitted symbols at once.
5. Run gograph_capabilities at the start of any Go coding session.
6. To choose which tests to run for a change, run gograph_tests with uncommitted=true (CLI: gograph tests --uncommitted --format go-test) and run only the go test commands it returns; add max_depth=3 to keep nearby tests.
7. gograph CLI queries rebuild a stale graph themselves ("rebuilt the stale graph" on stderr). If one prints "WARNING: the graph is stale" instead, run gograph build . and repeat it before relying on a missing result.
8. To read part of a long body, or a file region such as its imports, use gograph_source with lines=START-END (CLI: gograph source <symbol|path/file.go> --lines START-END) instead of shell or region reads of Go files.
9. To see what runs while a mutex is held in a function, use gograph_concurrency with function=<selector> (CLI: gograph concurrency --locks <function>); it lists each Lock/RLock, its release, and the calls under it.
10. To read code, callers or tests at another commit or branch, run the CLI with --rev <ref> (for example gograph --rev 3b37747 source internal/app/client.go) instead of git show or a scratch checkout.
<!-- gograph-end: do not remove -->
`

// legacyHookScript is the hook script earlier add-claude-plugin versions
// wrote; installHook deletes it when it is unchanged.
const legacyHookScript = `#!/bin/sh
# gograph-guard: Smart PreToolUse hook for Claude Code.
# Intercepts broad grep/rg calls targeting Go symbols in indexed repositories.
# Installed automatically by: gograph add-claude-plugin
exec gograph hook-guard
`

// installPlugin registers gograph as an MCP server, injects CLAUDE.md rules,
// and installs the smart PreToolUse hook for Claude Code.
func installPlugin() error {
	fmt.Println("🚀 Installing Gograph Claude Integration...")

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}

	var installErrs []error

	// 1. Register MCP server in Claude Desktop config
	if err := installMCPServer(); err != nil {
		fmt.Printf("⚠️  MCP server registration skipped: %v\n", err)
		installErrs = append(installErrs, fmt.Errorf("MCP server registration: %w", err))
	}

	// 2. Inject CLAUDE.md steering rules
	if err := installCLAUDEmd(home); err != nil {
		fmt.Printf("⚠️  CLAUDE.md injection skipped: %v\n", err)
		installErrs = append(installErrs, fmt.Errorf("CLAUDE.md injection: %w", err))
	} else {
		fmt.Println("✅ CLAUDE.md steering rules injected (~/.claude/CLAUDE.md)")
	}

	// 3. Register `gograph hook --agent claude` in Claude settings.json
	if err := installHook(home); err != nil {
		fmt.Printf("⚠️  Hook installation skipped: %v\n", err)
		installErrs = append(installErrs, fmt.Errorf("PreToolUse hook installation: %w", err))
	} else {
		fmt.Println("✅ PreToolUse hook registered (~/.claude/settings.json: " + claudeHookCommand + ")")
	}

	fmt.Println("\n🔄 Restart Claude Desktop / Claude Code for all changes to take effect.")
	fmt.Println("💡 Claude Code users: also run: claude mcp add gograph -- gograph mcp .")
	return errors.Join(installErrs...)
}

// installMCPServer writes the gograph entry into claude_desktop_config.json.
func installMCPServer() error {
	configPath := getClaudeConfigPath()
	if configPath == "" {
		return fmt.Errorf("unsupported OS")
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}

	gographPath, err := exec.LookPath("gograph")
	if err != nil {
		gographPath = "gograph"
	} else if absPath, err2 := filepath.Abs(gographPath); err2 == nil {
		gographPath = absPath
	}

	var config map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			config = make(map[string]interface{})
		} else {
			return err
		}
	} else if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	if _, ok := config["mcpServers"]; !ok {
		config["mcpServers"] = make(map[string]interface{})
	}
	mcpServers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("mcpServers is not a JSON object")
	}
	// Resolve the project path absolutely so Claude Desktop can find the graph
	// regardless of the working directory it uses when starting the MCP server.
	projectPath, err := os.Getwd()
	if err != nil {
		projectPath = "."
	}
	if absPath, err2 := filepath.Abs(projectPath); err2 == nil {
		projectPath = absPath
	}

	mcpServers["gograph"] = map[string]interface{}{
		"command": gographPath,
		"args":    []string{"mcp", projectPath},
	}
	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath, out, 0644); err != nil {
		return err
	}
	fmt.Printf("✅ MCP server registered in %s\n", configPath)
	return nil
}

// installCLAUDEmd appends gograph steering rules to ~/.claude/CLAUDE.md (idempotent).
func installCLAUDEmd(home string) error {
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		return err
	}
	mdPath := filepath.Join(claudeDir, "CLAUDE.md")

	existing, _ := os.ReadFile(mdPath)
	if strings.Contains(string(existing), "<!-- gograph-start: do not remove -->") {
		return nil // already installed
	}

	f, err := os.OpenFile(mdPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(claudeMDBlock); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// claudeHookCommand is the Claude Code PreToolUse hook add-claude-plugin
// registers.
const claudeHookCommand = "gograph hook --agent claude"

// claudeHookMatchers are the tool groups the hook is registered for.
var claudeHookMatchers = []string{"Bash", "Edit|Write|MultiEdit|NotebookEdit|Read"}

// installHook registers `gograph hook --agent claude` in
// ~/.claude/settings.json for the shell group and the edit/write/read group.
// It removes the entry of the former ~/.claude/hooks/gograph-guard.sh and
// deletes that script when it is unchanged, so no call is checked twice.
func installHook(home string) error {
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	var settings map[string]interface{}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		settings = make(map[string]interface{})
	} else if err := json.Unmarshal(data, &settings); err != nil {
		return err
	}
	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}
	preToolUse, _ := hooks["PreToolUse"].([]interface{})
	legacyScript := filepath.Join(home, ".claude", "hooks", "gograph-guard.sh")
	preToolUse = removeClaudeHookCommand(preToolUse, legacyScript)

	registered := map[string]bool{}
	for _, entry := range preToolUse {
		group, _ := entry.(map[string]interface{})
		matcher, _ := group["matcher"].(string)
		inner, _ := group["hooks"].([]interface{})
		for _, hook := range inner {
			if command, _ := hook.(map[string]interface{}); command["command"] == claudeHookCommand {
				registered[matcher] = true
			}
		}
	}
	for _, matcher := range claudeHookMatchers {
		if registered[matcher] {
			continue
		}
		preToolUse = append(preToolUse, map[string]interface{}{
			"matcher": matcher,
			"hooks": []interface{}{
				map[string]interface{}{"type": "command", "command": claudeHookCommand},
			},
		})
	}
	hooks["PreToolUse"] = preToolUse
	settings["hooks"] = hooks

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o644); err != nil {
		return err
	}
	if script, err := os.ReadFile(legacyScript); err == nil && string(script) == legacyHookScript {
		return os.Remove(legacyScript)
	}
	return nil
}

// removeClaudeHookCommand drops every hook that runs command, and groups
// left with no hook.
func removeClaudeHookCommand(groups []interface{}, command string) []interface{} {
	kept := groups[:0]
	for _, entry := range groups {
		group, ok := entry.(map[string]interface{})
		inner, hasHooks := group["hooks"].([]interface{})
		if !ok || !hasHooks {
			kept = append(kept, entry)
			continue
		}
		remaining := make([]interface{}, 0, len(inner))
		for _, hook := range inner {
			if fields, _ := hook.(map[string]interface{}); fields["command"] != command {
				remaining = append(remaining, hook)
			}
		}
		if len(remaining) == 0 {
			continue
		}
		group["hooks"] = remaining
		kept = append(kept, group)
	}
	return kept
}

func getClaudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch runtime.GOOS {
	case "darwin": // macOS
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows": // Windows
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Claude", "claude_desktop_config.json")
	case "linux": // Linux (Unofficial but standard)
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	default:
		return ""
	}
}
