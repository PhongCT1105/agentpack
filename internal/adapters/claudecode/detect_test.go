package claudecode

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

var errNotFound = errors.New("executable file not found in $PATH")

// lookPathMiss simulates `claude` not being on PATH.
func lookPathMiss(string) (string, error) { return "", errNotFound }

// lookPathHit simulates `claude` resolving to a binary.
func lookPathHit(string) (string, error) { return "/home/user/.local/bin/claude", nil }

func versionOK(string) (string, error) { return "2.0.44 (Claude Code)\n", nil }

func versionFails(string) (string, error) { return "", errors.New("exec failed") }

func TestID(t *testing.T) {
	a := New()
	if got := a.ID(); got != model.ToolClaudeCode {
		t.Errorf("ID() = %q, want %q", got, model.ToolClaudeCode)
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name          string
		home          string // testdata fixture dir; "" means empty temp dir
		lookPath      func(string) (string, error)
		runVersion    func(string) (string, error)
		wantInstalled bool
		wantVersion   string
	}{
		{
			name:          "binary on PATH and configured home",
			home:          filepath.Join("testdata", "detect", "home-configured"),
			lookPath:      lookPathHit,
			runVersion:    versionOK,
			wantInstalled: true,
			wantVersion:   "2.0.44",
		},
		{
			name:          "no binary but ~/.claude dir exists",
			home:          filepath.Join("testdata", "detect", "home-configured"),
			lookPath:      lookPathMiss,
			runVersion:    versionFails,
			wantInstalled: true,
			wantVersion:   "",
		},
		{
			name:          "no binary but ~/.claude.json exists",
			home:          filepath.Join("testdata", "detect", "home-jsononly"),
			lookPath:      lookPathMiss,
			runVersion:    versionFails,
			wantInstalled: true,
			wantVersion:   "",
		},
		{
			name:          "binary on PATH with pristine home",
			home:          "",
			lookPath:      lookPathHit,
			runVersion:    versionOK,
			wantInstalled: true,
			wantVersion:   "2.0.44",
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
			name:          "binary present but version command fails",
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
				t.Errorf("Detect() installed = %v, want %v", installed, tt.wantInstalled)
			}
			if version != tt.wantVersion {
				t.Errorf("Detect() version = %q, want %q", version, tt.wantVersion)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"2.0.44 (Claude Code)\n", "2.0.44"},
		{"1.0.17\n", "1.0.17"},
		{"  2.1.0-beta.1 (Claude Code)  ", "2.1.0-beta.1"},
		{"", ""},
		{"\n", ""},
	}
	for _, tt := range tests {
		if got := parseVersion(tt.raw); got != tt.want {
			t.Errorf("parseVersion(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// Detect must never write: guard against regressions by scanning with a
// read-only fixture is impractical cross-platform, so instead assert Detect
// leaves a pristine temp home empty.
func TestDetectDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	a := New()
	a.home = home
	a.lookPath = lookPathMiss
	a.runVersion = versionFails
	if _, _, err := a.Detect(); err != nil {
		t.Fatalf("Detect() error: %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(home, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Detect() created files in home: %v", entries)
	}
}
