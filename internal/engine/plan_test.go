package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func TestOpAction(t *testing.T) {
	tests := []struct {
		name string
		op   Op
		want string
	}{
		{
			name: "create file shows the size the user is about to gain",
			op:   Op{Kind: OpCreateFile, Path: "/home/u/.claude/skills/x/SKILL.md", Content: []byte("hello")},
			want: "create file (5 B)",
		},
		{
			name: "replace file is distinct from create in the preview",
			op:   Op{Kind: OpReplaceFile, Path: "/home/u/CLAUDE.md", Content: make([]byte, 2048)},
			want: "replace file (2.0 KB)",
		},
		{
			name: "merge names the key path, which is the merge boundary",
			op:   Op{Kind: OpMergeValue, Path: "/home/u/.claude.json", KeyPath: []string{"mcpServers", "github"}},
			want: "merge mcpServers.github",
		},
		{
			name: "deep merge says so: it behaves differently on existing keys",
			op: Op{
				Kind: OpMergeValue, Path: "/home/u/settings.json",
				KeyPath: []string{"permissions"}, Strategy: MergeDeep,
			},
			want: "merge permissions (deep)",
		},
		{
			name: "create dir",
			op:   Op{Kind: OpCreateDir, Path: "/home/u/.claude/skills/x"},
			want: "create directory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.op.Action(); got != tt.want {
				t.Errorf("Action() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpValidate(t *testing.T) {
	abs := func(elem ...string) string {
		return filepath.Join(append([]string{string(filepath.Separator), "tmp"}, elem...)...)
	}
	tests := []struct {
		name    string
		op      Op
		wantErr string // substring; empty means the op must validate
	}{
		{
			name: "well-formed create",
			op:   Op{Kind: OpCreateFile, Path: abs("a.md"), Content: []byte("x")},
		},
		{
			name: "empty file is legitimate",
			op:   Op{Kind: OpCreateFile, Path: abs("a.md")},
		},
		{
			name: "well-formed merge",
			op: Op{
				Kind: OpMergeValue, Path: abs("c.json"),
				KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"},
			},
		},
		{
			name:    "unknown kind",
			op:      Op{Kind: "delete_file", Path: abs("a.md")},
			wantErr: "unknown operation kind",
		},
		{
			name:    "missing path",
			op:      Op{Kind: OpCreateDir},
			wantErr: "path is required",
		},
		{
			name:    "relative path would mean different things at plan and apply time",
			op:      Op{Kind: OpCreateFile, Path: "relative/a.md"},
			wantErr: "must be absolute",
		},
		{
			name:    "merge without a key path has no reviewable preview line",
			op:      Op{Kind: OpMergeValue, Path: abs("c.json"), Value: 1},
			wantErr: "key path is required",
		},
		{
			name:    "merge with an empty key segment",
			op:      Op{Kind: OpMergeValue, Path: abs("c.json"), KeyPath: []string{"a", ""}, Value: 1},
			wantErr: "empty segment",
		},
		{
			name:    "merge with a nil value: there is no delete operation",
			op:      Op{Kind: OpMergeValue, Path: abs("c.json"), KeyPath: []string{"a"}},
			wantErr: "must not be nil",
		},
		{
			name:    "merge into a file with no recognizable format",
			op:      Op{Kind: OpMergeValue, Path: abs("c.conf"), KeyPath: []string{"a"}, Value: 1},
			wantErr: "cannot tell the format",
		},
		{
			name: "explicit format overrides an unhelpful extension",
			op: Op{
				Kind: OpMergeValue, Path: abs("mcp.conf"), Format: FormatJSON,
				KeyPath: []string{"a"}, Value: 1,
			},
		},
		{
			name:    "unknown merge strategy",
			op:      Op{Kind: OpMergeValue, Path: abs("c.json"), KeyPath: []string{"a"}, Value: 1, Strategy: "union"},
			wantErr: "unknown merge strategy",
		},
		{
			name:    "file content on a directory op is an adapter bug",
			op:      Op{Kind: OpCreateDir, Path: abs("d"), Content: []byte("x")},
			wantErr: "no content",
		},
		{
			name:    "key path on a file op is an adapter bug",
			op:      Op{Kind: OpReplaceFile, Path: abs("a.md"), KeyPath: []string{"a"}, Value: 1},
			wantErr: "belong to a merge_value operation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.op.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestPlanValidateNamesTheOffendingOperation(t *testing.T) {
	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpCreateFile, Path: filepath.Join(string(filepath.Separator), "tmp", "ok.md")},
		Op{Kind: OpCreateFile, Path: "relative.md"},
	)
	err := p.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error for the relative path")
	}
	for _, want := range []string{"claude-code", "operation 1", "relative.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() = %q, want it to contain %q", err, want)
		}
	}
}

func TestFormatForPath(t *testing.T) {
	tests := []struct {
		path string
		want Format
	}{
		{"/home/u/.claude.json", FormatJSON},
		{"/home/u/project/.mcp.json", FormatJSON},
		{"/home/u/.codex/config.toml", FormatTOML},
		{"/home/u/.gemini/settings.YAML", FormatYAML},
		{"/home/u/.config/x.yml", FormatYAML},
		{"/home/u/CLAUDE.md", ""},
		{"/home/u/rcfile", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := FormatForPath(tt.path); got != tt.want {
				t.Errorf("FormatForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestPlanPaths(t *testing.T) {
	root := t.TempDir()
	claudeJSON := filepath.Join(root, ".claude.json")
	skill := filepath.Join(root, ".claude", "skills", "x", "SKILL.md")

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpMergeValue, Path: claudeJSON, KeyPath: []string{"mcpServers", "a"}, Value: 1},
		Op{Kind: OpCreateFile, Path: skill, Content: []byte("x")},
		Op{Kind: OpMergeValue, Path: claudeJSON, KeyPath: []string{"mcpServers", "b"}, Value: 1},
	)

	got := p.Paths()
	want := []string{claudeJSON, skill}
	if len(got) != len(want) {
		t.Fatalf("Paths() = %v, want %v (each path once, in first-appearance order)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Paths()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPlanRenderGroupsByFile(t *testing.T) {
	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{
			Kind: OpMergeValue, Path: "/home/u/.claude.json",
			KeyPath: []string{"mcpServers", "github"}, Value: 1,
			Description: "mcp server github",
		},
		Op{Kind: OpCreateDir, Path: "/home/u/.claude/skills/brainstorming", Description: "skill brainstorming"},
		Op{
			Kind: OpCreateFile, Path: "/home/u/.claude/skills/brainstorming/SKILL.md",
			Content: []byte("hello"), Description: "skill brainstorming",
		},
		// Second operation on the first file: it must render inside that
		// file's group, not at the end.
		Op{
			Kind: OpMergeValue, Path: "/home/u/.claude.json",
			KeyPath: []string{"mcpServers", "linear"}, Value: 1,
			Description: "mcp server linear",
		},
	)

	want := strings.Join([]string{
		"claude-code: 4 operations across 3 paths",
		"",
		"  /home/u/.claude.json",
		"    merge mcpServers.github  mcp server github",
		"    merge mcpServers.linear  mcp server linear",
		"",
		"  /home/u/.claude/skills/brainstorming",
		"    create directory  skill brainstorming",
		"",
		"  /home/u/.claude/skills/brainstorming/SKILL.md",
		"    create file (5 B)  skill brainstorming",
		"",
	}, "\n")

	if got := p.String(); got != want {
		t.Errorf("Render():\n%s\nwant:\n%s", got, want)
	}
}

func TestPlanRenderEmpty(t *testing.T) {
	p := Plan{Tool: model.ToolCodex}
	want := "codex: nothing to do\n"
	if got := p.String(); got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
	if !p.IsEmpty() {
		t.Error("IsEmpty() = false for a plan with no operations")
	}
}

func TestShortenHome(t *testing.T) {
	sep := string(filepath.Separator)
	home := filepath.Join(sep+"home", "u")
	tests := []struct {
		name, path, home, want string
	}{
		{
			name: "inside home",
			path: filepath.Join(home, ".claude.json"), home: home,
			want: "~" + sep + ".claude.json",
		},
		{
			name: "outside home is left alone",
			path: filepath.Join(sep+"etc", "hosts"), home: home,
			want: filepath.Join(sep+"etc", "hosts"),
		},
		{
			name: "a sibling directory that merely starts with the home path",
			path: home + "-backup" + sep + "x", home: home,
			want: home + "-backup" + sep + "x",
		},
		{
			name: "home itself",
			path: home, home: home, want: home,
		},
		{
			name: "unknown home",
			path: filepath.Join(home, ".claude.json"), home: "",
			want: filepath.Join(home, ".claude.json"),
		},
		{
			name: "root as home must not swallow every path",
			path: filepath.Join(sep+"etc", "hosts"), home: sep,
			want: filepath.Join(sep+"etc", "hosts"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortenHome(tt.path, tt.home); got != tt.want {
				t.Errorf("shortenHome(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}
