package installation

import (
	"runtime/debug"
	"testing"
)

func TestVersionFromBuildInfo(t *testing.T) {
	vcs := func(revision string, modified string) []debug.BuildSetting {
		return []debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: modified}}
	}
	for _, testCase := range []struct {
		name   string
		linker string
		info   *debug.BuildInfo
		want   string
	}{
		{"release ldflags win", "1.7.20", &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, "1.7.20"},
		{"no build info", "dev", nil, "dev"},
		{"go install at tag", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.7.20"}}, "1.7.20"},
		{"empty linker", "", &debug.BuildInfo{Main: debug.Module{Version: "v1.7.20"}}, "1.7.20"},
		{"go install at branch", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.7.21-0.20261117122428-b2e11db8122f"}}, "1.7.21-0.20261117122428-b2e11db8122f"},
		{"devel without vcs", "dev", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
		{"local untagged dirty", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261117122428-b2e11db8122f+dirty"}, Settings: vcs("b2e11db8122fc9376bb8f248abb9116a08286d03", "true")}, "dev+b2e11db8122f.dirty"},
		{"local untagged clean", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.7.21-0.20261117122428-b2e11db8122f"}, Settings: vcs("b2e11db8122fc9376bb8f248abb9116a08286d03", "false")}, "dev+b2e11db8122f"},
		{"local clean at tag", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.7.20"}, Settings: vcs("b2e11db8122fc9376bb8f248abb9116a08286d03", "false")}, "1.7.20"},
		{"local dirty at tag", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.7.20+dirty"}, Settings: vcs("b2e11db8122fc9376bb8f248abb9116a08286d03", "true")}, "dev+b2e11db8122f.dirty"},
	} {
		if got := versionFromBuildInfo(testCase.linker, testCase.info); got != testCase.want {
			t.Errorf("%s: got %q, want %q", testCase.name, got, testCase.want)
		}
	}
}
