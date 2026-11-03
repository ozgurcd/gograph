package cli

var callerSessionID string

func callerCommandRoot(args []string) string {
	if args[0] == "build" {
		if options, err := parseBuildArgs(args[1:]); err == nil {
			return projectRoot(options.Root)
		}
	}
	if args[0] == "mcp" {
		if options, err := parseMCPArgs(args[1:]); err == nil {
			return projectRoot(options.Root)
		}
	}
	return ""
}

// callerAdoptsImplicitSession reports whether a command without a selector may
// adopt the repository's only CLI session. Creating a session, naming one
// positionally, serving MCP (which never inherits a caller session), and the
// per-tool-call hook guard never adopt.
func callerAdoptsImplicitSession(args []string) bool {
	switch args[0] {
	case "mcp", "hook-guard":
		return false
	case "session", "--session":
		if len(args) < 2 {
			return false
		}
		switch args[1] {
		case "create":
			return false
		case "end", "audit":
			return len(args) < 3
		}
	}
	return true
}

func callerCommandMutates(args []string) bool {
	switch args[0] {
	case "build", "wiki", "add-claude-plugin":
		return true
	case "boundaries":
		for _, arg := range args[1:] {
			if arg == "--create" {
				return true
			}
		}
	case "snapshot":
		return len(args) > 1 && (args[1] == "save" || args[1] == "drop")
	case "gate":
		return len(args) > 1 && args[1] == "init"
	case "workspace":
		return len(args) > 1 && args[1] == "build"
	case "mcp":
		options, err := parseMCPArgs(args[1:])
		return err == nil && options.PersistRefresh
	}
	return false
}
