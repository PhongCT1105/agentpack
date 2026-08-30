package codex

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func lookPathMiss(string) (string, error) {
	return "", errors.New("executable file not found in $PATH")
}

func lookPathHit(string) (string, error) { return "/home/user/.local/bin/codex", nil }

func versionOK(string) (string, error) { return "codex-cli 0.45.0\n", nil }

func versionFails(string) (string, error) { return "", errors.New("exec failed") }

func TestID(t *testing.T) {
	if got := New().ID(); got != model.ToolCodex {
		t.Errorf("ID() = %q, want %q", got, model.ToolCodex)
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name          string
		home          string
		lookPath      func(string) (string, error)
		runVersion    func(string) (string, error)
		wantInstalled bool
		wantVersion   string
	}{
		{
			name:          "binary and configured home",
			home:          filepath.Join("testdata", "detect", "home-configured"),
			lookPath:      lookPathHit,
			runVersion:    versionOK,
			wantInstalled: true,
			wantVersion:   "0.45.0",
		},
		{
			name:          "no binary but ~/.codex exists",
			home:          filepath.Join("testdata", "detect", "home-configured"),
			lookPath:      lookPathMiss,
			runVersion:    versionFails,
			wantInstalled: true,
			wantVersion:   "",
		},
		{
			name:          "binary only",
			home:          "",
			lookPath:      lookPathHit,
			runVersion:    versionOK,
			wantInstalled: true,
			wantVersion:   "0.45.0",
		},
		{
			name:          "nothing installed",
			home:          "",
			lookPath:      lookPathMiss,
			runVersion:    versionFails,
			wantInstalled: false,
			wantVersion:   "",
		},
		{
			name:          "version command fails",
			home:          filepath.Join("testdata", "detect", "home-configured"),
			lookPath:      lookPathHit,
			runVersion:    versionFails,
			wantInstalled: true,
			wantVersion:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := tt.home
			if home == "" {
				home = t.TempDir()
			}
			a := New()
			a.home = home
			a.lookPath = tt.lookPath
			a.runVersion = tt.runVersion

			installed, version, err := a.Detect()
			if err != nil {
				t.Fatalf("Detect() error: %v", err)
			}
			if installed != tt.wantInstalled {
				t.Errorf("installed = %v, want %v", installed, tt.wantInstalled)
			}
			if version != tt.wantVersion {
				t.Errorf("version = %q, want %q", version, tt.wantVersion)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"codex-cli 0.45.0\n", "0.45.0"},
		{"0.30.1\n", "0.30.1"},
		{"codex-cli 0.46.0-alpha.1", "0.46.0-alpha.1"},
		{"", ""},
		{"codex-cli\n", ""},
	}
	for _, tt := range tests {
		if got := parseVersion(tt.raw); got != tt.want {
			t.Errorf("parseVersion(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
