package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ozgurcd/gograph/internal/validation"
)

func runVersion() int {
	if !jsonMode {
		fmt.Printf("gograph version v%s\n", Version)
		return 0
	}
	document := validation.VersionDocument{SchemaVersion: validation.VersionSchemaVersion, Version: Version}
	if err := json.NewEncoder(os.Stdout).Encode(document); err != nil {
		fmt.Fprintf(os.Stderr, "encode version JSON: %v\n", err)
		return 2
	}
	return 0
}

func runValidate(args []string) int {
	repositoryRoot, bindingJSON, err := parseValidationArgs(args)
	if err != nil {
		return writeValidationResult(validation.InvalidRequestResult(Version, repositoryRoot, err.Error()))
	}
	if !jsonMode {
		return writeValidationResult(validation.InvalidRequestResult(Version, repositoryRoot, "validate requires --json"))
	}
	result := validation.NewEvaluator(Version).Validate(context.Background(), validation.Request{
		RepositoryRoot: repositoryRoot,
		BindingJSON:    []byte(bindingJSON),
	})
	return writeValidationResult(result)
}

func parseValidationArgs(args []string) (string, string, error) {
	var repositoryRoot string
	var bindingJSON string
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--repo":
			if repositoryRoot != "" || index+1 >= len(args) {
				return repositoryRoot, bindingJSON, fmt.Errorf("--repo requires exactly one value")
			}
			index++
			repositoryRoot = args[index]
		case strings.HasPrefix(argument, "--repo="):
			if repositoryRoot != "" {
				return repositoryRoot, bindingJSON, fmt.Errorf("--repo may be supplied only once")
			}
			repositoryRoot = strings.TrimPrefix(argument, "--repo=")
		case argument == "--binding-json":
			if bindingJSON != "" || index+1 >= len(args) {
				return repositoryRoot, bindingJSON, fmt.Errorf("--binding-json requires exactly one value")
			}
			index++
			bindingJSON = args[index]
		case strings.HasPrefix(argument, "--binding-json="):
			if bindingJSON != "" {
				return repositoryRoot, bindingJSON, fmt.Errorf("--binding-json may be supplied only once")
			}
			bindingJSON = strings.TrimPrefix(argument, "--binding-json=")
		default:
			return repositoryRoot, bindingJSON, fmt.Errorf("unknown validate argument %q", argument)
		}
	}
	if repositoryRoot == "" {
		return repositoryRoot, bindingJSON, fmt.Errorf("--repo is required")
	}
	if bindingJSON == "" {
		return repositoryRoot, bindingJSON, fmt.Errorf("--binding-json is required")
	}
	return repositoryRoot, bindingJSON, nil
}

func writeValidationResult(result validation.Result) int {
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "encode validation JSON: %v\n", err)
		return 2
	}
	switch result.Evaluation.Outcome {
	case validation.OutcomePass:
		return 0
	case validation.OutcomeFail:
		return 1
	default:
		return 2
	}
}
