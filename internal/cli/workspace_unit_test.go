package cli

import (
	"strings"
	"testing"
)

func TestWorkspaceQueryArgumentParserRejectsDuplicateOptions(t *testing.T) {
	for name, args := range map[string][]string{
		"scope":            {"--scope", "one", "--scope", "two", "term"},
		"workspace":        {"--workspace", "one", "--workspace", "two", "term"},
		"include possible": {"--include-possible", "--include-possible", "term"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWorkspaceQueryArgs(args); err == nil || !strings.Contains(err.Error(), "only once") {
				t.Fatalf("duplicate option error = %v", err)
			}
		})
	}
}

func TestWorkspaceQueryRejectsUnusedPossibleFlagAndWhitespaceTerm(t *testing.T) {
	if code := runWorkspaceQuery([]string{"--include-possible", "term"}); code == 0 {
		t.Fatal("query accepted traversal-only --include-possible")
	}
	if code := runWorkspaceQuery([]string{"   "}); code == 0 {
		t.Fatal("query accepted whitespace-only term")
	}
}

func TestWorkspaceBuildRejectsTwoPositionalPaths(t *testing.T) {
	if code := runWorkspaceBuild([]string{".", "other"}); code == 0 {
		t.Fatal("workspace build accepted two positional paths")
	}
}
