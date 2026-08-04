package cli

import "testing"

func TestParseMCPArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantRoot    string
		wantPersist bool
		wantErr     bool
	}{
		{
			name:     "default",
			wantRoot: ".",
		},
		{
			name:        "flag alone",
			args:        []string{"--persist-refresh"},
			wantRoot:    ".",
			wantPersist: true,
		},
		{
			name:        "flag before path",
			args:        []string{"--persist-refresh", "project"},
			wantRoot:    "project",
			wantPersist: true,
		},
		{
			name:        "flag after path",
			args:        []string{"project", "--persist-refresh"},
			wantRoot:    "project",
			wantPersist: true,
		},
		{
			name:        "explicit true",
			args:        []string{"--persist-refresh=true", "project"},
			wantRoot:    "project",
			wantPersist: true,
		},
		{
			name:     "explicit false",
			args:     []string{"project", "--persist-refresh=false"},
			wantRoot: "project",
		},
		{
			name:     "dash-prefixed path after terminator",
			args:     []string{"--", "-project"},
			wantRoot: "-project",
		},
		{
			name:    "unknown flag",
			args:    []string{"--unknown"},
			wantErr: true,
		},
		{
			name:    "invalid boolean",
			args:    []string{"--persist-refresh=sometimes"},
			wantErr: true,
		},
		{
			name:    "multiple paths",
			args:    []string{"project-one", "project-two"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMCPArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseMCPArgs(%q) error = nil, want an error", tt.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMCPArgs(%q) error = %v", tt.args, err)
			}
			if got.Root != tt.wantRoot {
				t.Errorf("parseMCPArgs(%q).Root = %q, want %q", tt.args, got.Root, tt.wantRoot)
			}
			if got.PersistRefresh != tt.wantPersist {
				t.Errorf("parseMCPArgs(%q).PersistRefresh = %t, want %t", tt.args, got.PersistRefresh, tt.wantPersist)
			}
		})
	}
}
