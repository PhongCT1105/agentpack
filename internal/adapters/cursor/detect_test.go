package cursor

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func lookPathMiss(string) (string, error) {
	return "", errors.New("executable file not found in $PATH")
}

func lookPathHit(string) (string, error) { return "/usr/local/bin/cursor", nil }

// Cursor's launcher prints version, commit, and architecture on three lines,
// the way `code --version` does.
func versionOK(string) (string, error) { return "1.7.44\n9f2c1b7ade4e3f1c\narm64\n", nil }

func versionFails(string) (string, error) { return "", errors.New("exec failed") }

func TestID(t *testing.T) {
	if got := New().ID(); got != model.ToolCursor {
		t.Errorf("ID() = %q, want %q", got, model.ToolCursor)
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
			wantVersion:   "1.7.44",
		},
		{
			// The common macOS case: the app is installed but the `cursor`
			// shell command was never added to PATH.
			name:          "no binary but ~/.cursor exists",
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
			wantVersion:   "1.7.44",
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
		{"1.7.44\n9f2c1b7ade4e3f1c\narm64\n", "1.7.44"},
		{"Cursor 1.7.44\n", "1.7.44"},
		{"2.0.0-nightly.3\n", "2.0.0-nightly.3"},
		{"", ""},
		{"cursor\n", ""},
	}
	for _, tt := range tests {
		if got := parseVersion(tt.raw); got != tt.want {
			t.Errorf("parseVersion(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
