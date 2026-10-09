package cli

var callerSessionID string

func callerCommandRoot(args []string) string {
	if args[0] == "build" {
		if options, err := parseBuildArgs(args[1:]); err == nil {
			return options.Root
		}
	}
	if args[0] == "mcp" {
		if options, err := parseMCPArgs(args[1:]); err == nil {
			if options.Root == "." {
				return ""
			}
			return options.Root
		}
	}
	return ""
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
