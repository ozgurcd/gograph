package cli

import "fmt"

type outputModeSupport uint8

const (
	outputJSON outputModeSupport = 1 << iota
	outputFilesOnly
	outputMermaid
)

var commandOutputModes = map[string]outputModeSupport{
	"validate":     outputJSON,
	"version":      outputJSON,
	"--version":    outputJSON,
	"-v":           outputJSON,
	"query":        outputJSON | outputFilesOnly,
	"focus":        outputJSON | outputFilesOnly,
	"node":         outputJSON | outputFilesOnly,
	"source":       outputJSON,
	"public":       outputJSON | outputFilesOnly,
	"fields":       outputJSON | outputFilesOnly,
	"embeds":       outputJSON | outputFilesOnly,
	"imports":      outputJSON | outputFilesOnly,
	"callers":      outputJSON | outputFilesOnly | outputMermaid,
	"callees":      outputJSON | outputFilesOnly | outputMermaid,
	"impact":       outputJSON | outputFilesOnly | outputMermaid,
	"implementers": outputJSON | outputFilesOnly,
	"envs":         outputJSON | outputFilesOnly,
	"interfaces":   outputJSON | outputFilesOnly,
	"concurrency":  outputJSON | outputFilesOnly,
	"tests":        outputJSON | outputFilesOnly,
	"routes":       outputJSON | outputFilesOnly,
	"sql":          outputJSON | outputFilesOnly,
	"errors":       outputJSON | outputFilesOnly,
	"errorflow":    outputJSON,
	"trace":        outputJSON,
	"flow":         outputJSON | outputFilesOnly,
	"path":         outputJSON | outputMermaid,
	"stale":        outputJSON,
	"stats":        outputJSON,
	"summary":      outputJSON,
	"untested":     outputJSON,
	"doc":          outputJSON,
	"orphans":      outputJSON | outputFilesOnly,
	"godobj":       outputJSON,
	"skeleton":     outputJSON,
	"mutate":       outputJSON | outputFilesOnly,
	"arity":        outputJSON,
	"complexity":   outputJSON,
	"diagram":      outputMermaid,
	"coupling":     outputJSON | outputMermaid,
	"context":      outputJSON,
	"hotspot":      outputJSON,
	"deps":         outputJSON | outputMermaid,
	"dependents":   outputJSON | outputFilesOnly | outputMermaid,
	"changes":      outputJSON,
	"constructors": outputJSON | outputFilesOnly,
	"literals":     outputJSON | outputFilesOnly,
	"usages":       outputJSON | outputFilesOnly,
	"returnusage":  outputJSON | outputFilesOnly,
	"schema":       outputJSON | outputFilesOnly,
	"globals":      outputJSON | outputFilesOnly,
	"mocks":        outputJSON | outputFilesOnly,
	"fixtures":     outputJSON | outputFilesOnly,
	"boundaries":   outputJSON | outputFilesOnly,
	"endpoint":     outputJSON | outputMermaid,
	"explain":      outputJSON,
	"plan":         outputJSON,
	"review":       outputJSON,
	"risk":         outputJSON,
	"api":          outputJSON,
	"contract":     outputJSON,
	"check":        outputJSON,
	"httpcalls":    outputJSON | outputFilesOnly,
}

func supportedOutputModes(args []string) outputModeSupport {
	if len(args) == 0 {
		return outputMermaid // bare gograph --mermaid renders the diagram
	}
	if (args[0] == "session" || args[0] == "--session") && len(args) > 1 && args[1] == "audit" {
		return outputJSON
	}
	if args[0] == "boundaries" {
		for _, argument := range args[1:] {
			if argument == "--create" {
				return 0
			}
		}
	}
	return commandOutputModes[args[0]]
}

func validateOutputModes(args []string) error {
	requested := 0
	if jsonMode {
		requested++
	}
	if filesOnlyMode {
		requested++
	}
	if mermaidMode {
		requested++
	}
	if requested == 0 {
		return nil
	}
	if requested > 1 {
		return fmt.Errorf("request only one of --json, --files-only, or --mermaid")
	}

	supported := supportedOutputModes(args)
	command := "the bare command"
	if len(args) > 0 {
		command = args[0]
		if (command == "session" || command == "--session") && len(args) > 1 {
			command += " " + args[1]
		}
	}
	if jsonMode && supported&outputJSON == 0 {
		return fmt.Errorf("%s does not support --json", command)
	}
	if filesOnlyMode && supported&outputFilesOnly == 0 {
		return fmt.Errorf("%s does not support --files-only", command)
	}
	if mermaidMode && supported&outputMermaid == 0 {
		return fmt.Errorf("%s does not support --mermaid", command)
	}
	return nil
}
