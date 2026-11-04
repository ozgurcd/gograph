package gitenv

import (
	"slices"
	"strings"
	"testing"
)

func TestCleanDropsEveryRepositorySelectingVariable(t *testing.T) {
	in := []string{
		"PATH=/usr/bin", "HOME=/h",
		"GIT_DIR=/foreign/.git", "GIT_WORK_TREE=/foreign", "GIT_COMMON_DIR=/foreign/.git",
		"GIT_INDEX_FILE=/foreign/.git/index", "GIT_OBJECT_DIRECTORY=/foreign/objects",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/foreign/objects", "GIT_NAMESPACE=ns",
		"GIT_CEILING_DIRECTORIES=/", "GIT_PREFIX=sub/", "GIT_QUARANTINE_PATH=/q",
		"GIT_CONFIG_PARAMETERS='core.worktree=/foreign'", "GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.worktree", "GIT_CONFIG_VALUE_0=/foreign",
		"GIT_SOMETHING_NEW=1",
		"GIT_CONFIG_GLOBAL=/h/.gitconfig", "GIT_TERMINAL_PROMPT=0", "GIT_EXEC_PATH=/x",
		"NOT_GIT_DIR=keep",
	}
	snapshot := slices.Clone(in)
	got := Clean(in)
	if !slices.Equal(in, snapshot) {
		t.Fatal("Clean modified its input")
	}
	want := []string{"PATH=/usr/bin", "HOME=/h", "GIT_CONFIG_GLOBAL=/h/.gitconfig", "GIT_TERMINAL_PROMPT=0", "GIT_EXEC_PATH=/x", "NOT_GIT_DIR=keep"}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %s\nwant %s", strings.Join(got, " "), strings.Join(want, " "))
	}
}

func TestCleanKeepsEntriesWithoutEquals(t *testing.T) {
	if got := Clean([]string{"WEIRD", "GIT_DIR"}); !slices.Equal(got, []string{"WEIRD"}) {
		t.Fatalf("got %v", got)
	}
}
