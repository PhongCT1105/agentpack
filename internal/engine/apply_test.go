package engine

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// newExecutor returns an executor whose backups land in a temp directory.
// Every test in this file must use it: the executor writes files, and a test
// that reached the real ~/.agentpack/backups would be damaging the machine
// running the suite.
func newExecutor(t *testing.T) *Executor {
	t.Helper()
	return &Executor{
		BackupRoot: t.TempDir(),
		Now:        func() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) },
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("preparing %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// copyFixture copies testdata/merge/<name> to dest, giving each test a
// throwaway copy of a realistic tool config to merge into.
func copyFixture(t *testing.T, name, dest string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "merge", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	writeFile(t, dest, string(data))
}

// snapshotTree records every path under root and the content of each file, so
// a test can prove nothing changed.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			tree[rel+string(filepath.Separator)] = ""
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		tree[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return tree
}

func assertSameTree(t *testing.T, want, got map[string]string) {
	t.Helper()
	for path, content := range want {
		gotContent, ok := got[path]
		if !ok {
			t.Errorf("%s disappeared", path)
			continue
		}
		if gotContent != content {
			t.Errorf("%s changed:\n got %q\nwant %q", path, gotContent, content)
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("%s appeared but nothing should have been written", path)
		}
	}
}

func statuses(res *ApplyResult) []OpStatus {
	out := make([]OpStatus, len(res.Ops))
	for i, op := range res.Ops {
		out[i] = op.Status
	}
	return out
}

func wantStatuses(t *testing.T, res *ApplyResult, want ...OpStatus) {
	t.Helper()
	got := statuses(res)
	if len(got) != len(want) {
		t.Fatalf("got %d operation results %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("operation %d (%s %s) = %q, want %q",
				i, res.Ops[i].Op.Kind, res.Ops[i].Op.Path, got[i], want[i])
		}
	}
}

func decodeJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("decoding result as JSON: %v\n%s", err, raw)
	}
	return doc
}

func TestApplyWritesFilesAndDirectories(t *testing.T) {
	root := t.TempDir()
	ex := newExecutor(t)

	skillDir := filepath.Join(root, ".claude", "skills", "brainstorming")
	skillFile := filepath.Join(skillDir, "SKILL.md")
	claudeJSON := filepath.Join(root, ".claude.json")

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpCreateDir, Path: skillDir, Description: "skill brainstorming"},
		Op{Kind: OpCreateFile, Path: skillFile, Content: []byte("# Brainstorming\n"), Description: "skill brainstorming"},
		Op{
			Kind: OpMergeValue, Path: claudeJSON,
			KeyPath: []string{"mcpServers", "github"},
			Value: map[string]any{
				"command": "npx",
				"args":    []any{"-y", "@modelcontextprotocol/server-github"},
			},
			Description: "mcp server github",
		},
	)

	res, err := ex.Apply(p)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	wantStatuses(t, res, StatusCreated, StatusCreated, StatusCreated)

	if got := readFile(t, skillFile); got != "# Brainstorming\n" {
		t.Errorf("SKILL.md = %q", got)
	}
	if info, err := os.Stat(skillDir); err != nil || !info.IsDir() {
		t.Errorf("skill directory not created: %v", err)
	}
	doc := decodeJSON(t, readFile(t, claudeJSON))
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok || servers["github"] == nil {
		t.Errorf("merge did not create mcpServers.github: %v", doc)
	}

	// Nothing existed beforehand, so nothing needed backing up and no backup
	// directory should have been created.
	if res.BackupDir != "" {
		t.Errorf("BackupDir = %q, want empty when no existing file was touched", res.BackupDir)
	}
	if entries, err := os.ReadDir(ex.BackupRoot); err != nil || len(entries) != 0 {
		t.Errorf("backup root got %d entries (err %v), want none", len(entries), err)
	}
	if !res.Changed() {
		t.Error("Changed() = false after creating three things")
	}
}

func TestApplyDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	ex := newExecutor(t)
	ex.DryRun = true

	existing := filepath.Join(root, "CLAUDE.md")
	writeFile(t, existing, "original rules\n")
	claudeJSON := filepath.Join(root, ".claude.json")
	copyFixture(t, "claude.json", claudeJSON)
	newFile := filepath.Join(root, "fresh", "note.md")

	before := snapshotTree(t, root)

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpReplaceFile, Path: existing, Content: []byte("new rules\n"), Description: "rule CLAUDE.md"},
		Op{Kind: OpCreateFile, Path: newFile, Content: []byte("hello\n"), Description: "command note"},
		Op{
			Kind: OpMergeValue, Path: claudeJSON,
			KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"},
			Description: "mcp server github",
		},
		// A second operation on a file the dry run "wrote": it must see the
		// pending content, or the preview would claim a second update.
		Op{Kind: OpReplaceFile, Path: existing, Content: []byte("new rules\n"), Description: "rule CLAUDE.md again"},
	)

	res, err := ex.Apply(p)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if !res.DryRun {
		t.Error("ApplyResult.DryRun = false")
	}
	wantStatuses(t, res, StatusUpdated, StatusCreated, StatusUpdated, StatusUnchanged)

	assertSameTree(t, before, snapshotTree(t, root))

	// The dry run reports where backups would go without creating them.
	if res.BackupDir == "" {
		t.Error("BackupDir = empty, want the directory the apply would have used")
	}
	if _, err := os.Stat(res.BackupDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dry run created the backup directory %s (err %v)", res.BackupDir, err)
	}
	if res.Ops[0].BackupPath == "" {
		t.Error("a dry-run overwrite should still report the backup path it would use")
	}
	if entries, err := os.ReadDir(ex.BackupRoot); err != nil || len(entries) != 0 {
		t.Errorf("dry run wrote %d entries into the backup root (err %v)", len(entries), err)
	}
}

func TestApplyBacksUpBeforeOverwriting(t *testing.T) {
	root := t.TempDir()
	ex := newExecutor(t)

	rules := filepath.Join(root, "CLAUDE.md")
	writeFile(t, rules, "original rules\n")
	claudeJSON := filepath.Join(root, ".claude.json")
	copyFixture(t, "claude.json", claudeJSON)
	originalJSON := readFile(t, claudeJSON)

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpReplaceFile, Path: rules, Content: []byte("new rules\n")},
		Op{Kind: OpMergeValue, Path: claudeJSON, KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"}},
		// A brand-new file has no prior state, so it must not be backed up.
		Op{Kind: OpCreateFile, Path: filepath.Join(root, "new.md"), Content: []byte("new\n")},
	)

	res, err := ex.Apply(p)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	wantStatuses(t, res, StatusUpdated, StatusUpdated, StatusCreated)

	if res.BackupDir == "" {
		t.Fatal("BackupDir = empty, want the timestamped directory holding this apply's backups")
	}
	if want := filepath.Join(ex.BackupRoot, "2026-08-30T12-00-00Z"); res.BackupDir != want {
		t.Errorf("BackupDir = %q, want %q (RFC3339-ish, no colons: they are illegal on Windows)", res.BackupDir, want)
	}
	if res.Ops[2].BackupPath != "" {
		t.Errorf("a newly created file was backed up to %q", res.Ops[2].BackupPath)
	}

	// Backups mirror the absolute path of what they hold, so a human can
	// find the file they lost.
	for i, target := range []string{rules, claudeJSON} {
		backup := res.Ops[i].BackupPath
		if backup == "" {
			t.Fatalf("operation %d overwrote %s without recording a backup", i, target)
		}
		rel, err := filepath.Rel(res.BackupDir, backup)
		if err != nil {
			t.Fatalf("backup %s is not inside %s: %v", backup, res.BackupDir, err)
		}
		if got, want := filepath.ToSlash(rel), filepath.ToSlash(mirroredRel(target)); got != want {
			t.Errorf("backup of %s stored at %q, want the mirrored path %q", target, got, want)
		}
	}
	if got := readFile(t, res.Ops[0].BackupPath); got != "original rules\n" {
		t.Errorf("backup of CLAUDE.md = %q, want the bytes from before the write", got)
	}
	if got := readFile(t, res.Ops[1].BackupPath); got != originalJSON {
		t.Errorf("backup of .claude.json = %q, want the original file byte for byte", got)
	}
	if got := readFile(t, rules); got != "new rules\n" {
		t.Errorf("CLAUDE.md = %q, want the new content", got)
	}
}

// mirroredRel is the test's own statement of "preserving relative structure":
// the target's absolute path with the volume and leading separator removed.
func mirroredRel(target string) string {
	vol := filepath.VolumeName(target)
	rel := strings.TrimPrefix(target, vol)
	rel = strings.TrimLeft(rel, `/\`)
	if vol != "" {
		rel = filepath.Join(strings.Trim(strings.NewReplacer(":", "", `\`, "-", "/", "-").Replace(vol), "-"), rel)
	}
	return rel
}

func TestApplyRollsBackAfterMidPlanFailure(t *testing.T) {
	root := t.TempDir()
	ex := newExecutor(t)

	rules := filepath.Join(root, "CLAUDE.md")
	writeFile(t, rules, "original rules\n")
	claudeJSON := filepath.Join(root, ".claude.json")
	copyFixture(t, "claude.json", claudeJSON)
	originalJSON := readFile(t, claudeJSON)

	created := filepath.Join(root, "fresh", "note.md")
	// A regular file where the last operation needs a directory: MkdirAll
	// fails with the same error on every platform.
	blocker := filepath.Join(root, "blocker")
	writeFile(t, blocker, "not a directory\n")

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpReplaceFile, Path: rules, Content: []byte("new rules\n"), Description: "rule CLAUDE.md"},
		Op{Kind: OpMergeValue, Path: claudeJSON, KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"}},
		Op{Kind: OpCreateFile, Path: created, Content: []byte("hello\n"), Description: "command note"},
		Op{Kind: OpCreateFile, Path: filepath.Join(blocker, "child.md"), Content: []byte("boom\n"), Description: "doomed"},
	)

	res, err := ex.Apply(p)
	if err == nil {
		t.Fatal("Apply() = nil error, want the mid-plan failure")
	}
	if res != nil {
		t.Errorf("Apply() returned a result alongside the failure: %+v", res)
	}

	var applyErr *ApplyError
	if !errors.As(err, &applyErr) {
		t.Fatalf("Apply() = %T, want *ApplyError", err)
	}
	if applyErr.Index != 3 {
		t.Errorf("ApplyError.Index = %d, want 3", applyErr.Index)
	}
	if applyErr.RollbackFailed() {
		t.Errorf("rollback reported failures it should not have: %v", applyErr.RollbackErrs)
	}

	// The machine is back where it started.
	if got := readFile(t, rules); got != "original rules\n" {
		t.Errorf("CLAUDE.md = %q, want the original bytes restored from backup", got)
	}
	if got := readFile(t, claudeJSON); got != originalJSON {
		t.Errorf(".claude.json = %q, want the original bytes restored from backup", got)
	}
	if _, err := os.Stat(created); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s still exists after rollback (err %v): files the plan created must be removed", created, err)
	}
	if _, err := os.Stat(filepath.Dir(created)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s still exists after rollback: directories the plan created should go too", filepath.Dir(created))
	}
	if len(applyErr.Restored) != 2 || len(applyErr.Removed) != 1 {
		t.Errorf("restored %v, removed %v; want 2 restored and 1 removed", applyErr.Restored, applyErr.Removed)
	}

	// The error has to describe both halves: why it stopped, and what the
	// rollback did about it.
	msg := applyErr.Error()
	for _, want := range []string{"operation 3", "child.md", "rolled back", "2 files restored", "1 created file removed", applyErr.BackupDir} {
		if !strings.Contains(msg, want) {
			t.Errorf("ApplyError.Error() = %q, want it to mention %q", msg, want)
		}
	}
	// Backups survive rollback: they are the user's manual recovery path.
	if _, err := os.Stat(filepath.Join(applyErr.BackupDir, mirroredRel(rules))); err != nil {
		t.Errorf("backup of %s was cleaned up after rollback: %v", rules, err)
	}
}

func TestRollbackNeverRemovesSomethingItDidNotCreate(t *testing.T) {
	// A directory operation whose path is occupied by a regular file fails,
	// and that path is on the list of directories the run tried to create.
	// Rollback must not "clean it up": deleting a file the user already had
	// is the exact damage rollback exists to prevent.
	root := t.TempDir()
	occupied := filepath.Join(root, "skills")
	writeFile(t, occupied, "a file where a directory should be\n")

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpCreateFile, Path: filepath.Join(root, "CLAUDE.md"), Content: []byte("rules\n")},
		Op{Kind: OpCreateDir, Path: occupied, Description: "skills directory"},
	)

	if _, err := newExecutor(t).Apply(p); err == nil {
		t.Fatal("Apply() = nil, want the blocked directory creation to fail")
	}
	if got := readFile(t, occupied); got != "a file where a directory should be\n" {
		t.Errorf("%s = %q, want the user's file untouched", occupied, got)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file the plan created was not rolled back (err %v)", err)
	}
}

func TestApplyErrorReportsRollbackFailureLoudly(t *testing.T) {
	// A rollback failure cannot be induced portably (it needs a filesystem
	// that refuses a write we just made), so the guarantee under test here is
	// the reporting one: a swallowed rollback failure would leave the user
	// believing their machine was restored when it was not.
	err := &ApplyError{
		Tool:         model.ToolClaudeCode,
		Index:        2,
		Op:           Op{Kind: OpReplaceFile, Path: "/home/u/CLAUDE.md"},
		Err:          errors.New("disk full"),
		BackupDir:    "/backups/2026-08-30T12-00-00Z",
		Restored:     []string{"/home/u/a.md"},
		Removed:      nil,
		RollbackErrs: []error{errors.New("restoring /home/u/b.md: permission denied")},
	}
	msg := err.Error()
	for _, want := range []string{
		"disk full",
		"ROLLBACK INCOMPLETE",
		"1 file may still be modified",
		"/backups/2026-08-30T12-00-00Z",
		"permission denied",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, want it to mention %q", msg, want)
		}
	}
	if !err.RollbackFailed() {
		t.Error("RollbackFailed() = false with rollback errors present")
	}
	if !errors.Is(err, err.Err) {
		t.Error("the original failure must stay reachable through errors.Is")
	}
}

func TestApplyMergePreservesUnrelatedKeys(t *testing.T) {
	// Each case merges one server into a real-shaped tool config and then
	// asserts that everything the merge had no business touching survived —
	// both the neighbouring entry inside the merged key path and the
	// unrelated top-level config around it.
	tests := []struct {
		name    string
		fixture string
		file    string
		keyPath []string
		value   any
		// decode parses the merged file back into a generic document.
		decode func(t *testing.T, raw string) map[string]any
		// verbatim are substrings of the fixture that must still appear in
		// the rewritten file, character for character. Comparing the raw
		// bytes is how a silently reformatted number (87 → 8.7e+01) gets
		// caught: it survives a decode-and-compare, but it is still damage.
		verbatim []string
		// topLevel are keys that must still be present after the merge.
		topLevel []string
	}{
		{
			name: "json", fixture: "claude.json", file: ".claude.json",
			keyPath:  []string{"mcpServers", "github"},
			value:    map[string]any{"type": "stdio", "command": "npx"},
			decode:   decodeJSON,
			verbatim: []string{`"numStartups": 87`, `"installMethod": "native"`, `"fallbackAvailableWarningThreshold": 0.5`},
			topLevel: []string{"numStartups", "installMethod", "autoUpdates", "tipsHistory", "fallbackAvailableWarningThreshold"},
		},
		{
			name: "toml", fixture: "config.toml", file: "config.toml",
			keyPath: []string{"mcp_servers", "github"},
			value:   map[string]any{"command": "npx"},
			decode: func(t *testing.T, raw string) map[string]any {
				t.Helper()
				doc := map[string]any{}
				if err := toml.Unmarshal([]byte(raw), &doc); err != nil {
					t.Fatalf("decoding result as TOML: %v\n%s", err, raw)
				}
				return doc
			},
			verbatim: []string{`model = "gpt-5-codex"`, `approval_policy = "on-request"`},
			topLevel: []string{"model", "approval_policy", "history"},
		},
		{
			name: "yaml", fixture: "settings.yaml", file: "settings.yaml",
			keyPath: []string{"mcpServers", "github"},
			value:   map[string]any{"command": "npx"},
			decode: func(t *testing.T, raw string) map[string]any {
				t.Helper()
				doc := map[string]any{}
				if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
					t.Fatalf("decoding result as YAML: %v\n%s", err, raw)
				}
				return doc
			},
			verbatim: []string{"theme: dark", "fileName: GEMINI.md"},
			topLevel: []string{"theme", "context"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, tt.file)
			copyFixture(t, tt.fixture, target)

			p := Plan{Tool: model.ToolClaudeCode}
			p.Add(Op{Kind: OpMergeValue, Path: target, KeyPath: tt.keyPath, Value: tt.value})

			res, err := newExecutor(t).Apply(p)
			if err != nil {
				t.Fatalf("Apply() = %v", err)
			}
			wantStatuses(t, res, StatusUpdated)

			raw := readFile(t, target)
			doc := tt.decode(t, raw)
			servers, ok := doc[tt.keyPath[0]].(map[string]any)
			if !ok {
				t.Fatalf("%s is not an object after the merge: %v", tt.keyPath[0], doc[tt.keyPath[0]])
			}
			if servers["github"] == nil {
				t.Errorf("the merged server is missing: %v", servers)
			}
			if servers["linear"] == nil {
				t.Errorf("merging github dropped the pre-existing linear server: %v", servers)
			}
			for _, key := range tt.topLevel {
				if _, ok := doc[key]; !ok {
					t.Errorf("unrelated top-level key %q was dropped by the merge:\n%s", key, raw)
				}
			}
			for _, want := range tt.verbatim {
				if !strings.Contains(raw, want) {
					t.Errorf("unrelated value %q did not survive the merge verbatim:\n%s", want, raw)
				}
			}
		})
	}
}

func TestApplyMergeDeepKeepsNestedUserKeys(t *testing.T) {
	root := t.TempDir()
	settings := filepath.Join(root, "settings.json")
	writeFile(t, settings, `{
  "permissions": {
    "allow": ["Bash(git status:*)"],
    "deny": ["Bash(rm:*)"]
  },
  "model": "sonnet"
}
`)

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(Op{
		Kind: OpMergeValue, Path: settings,
		KeyPath: []string{"permissions"}, Strategy: MergeDeep,
		Value: map[string]any{"allow": []any{"Bash(go test:*)"}},
	})

	if _, err := newExecutor(t).Apply(p); err != nil {
		t.Fatalf("Apply() = %v", err)
	}

	doc := decodeJSON(t, readFile(t, settings))
	perms, ok := doc["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions is not an object: %v", doc["permissions"])
	}
	if perms["deny"] == nil {
		t.Errorf("a deep merge into permissions dropped permissions.deny: %v", perms)
	}
	allow, ok := perms["allow"].([]any)
	if !ok || len(allow) != 1 || allow[0] != "Bash(go test:*)" {
		t.Errorf("permissions.allow = %v, want the incoming list (arrays are replaced, not appended)", perms["allow"])
	}

	// The default strategy replaces the whole object at the key path, which
	// is why deep is opt-in.
	set := Plan{Tool: model.ToolClaudeCode}
	set.Add(Op{
		Kind: OpMergeValue, Path: settings,
		KeyPath: []string{"permissions"},
		Value:   map[string]any{"allow": []any{"Bash(go vet:*)"}},
	})
	if _, err := newExecutor(t).Apply(set); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	doc = decodeJSON(t, readFile(t, settings))
	perms = doc["permissions"].(map[string]any)
	if perms["deny"] != nil {
		t.Errorf("MergeSet should have replaced the object at the key path: %v", perms)
	}
	if doc["model"] != "sonnet" {
		t.Errorf("even MergeSet must leave keys outside the path alone: model = %v", doc["model"])
	}
}

func TestApplyTwiceIsSafe(t *testing.T) {
	root := t.TempDir()
	claudeJSON := filepath.Join(root, ".claude.json")
	copyFixture(t, "claude.json", claudeJSON)
	codexTOML := filepath.Join(root, "config.toml")
	copyFixture(t, "config.toml", codexTOML)
	geminiYAML := filepath.Join(root, "settings.yaml")
	copyFixture(t, "settings.yaml", geminiYAML)
	skillDir := filepath.Join(root, "skills", "brainstorming")

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpCreateDir, Path: skillDir},
		Op{Kind: OpCreateFile, Path: filepath.Join(skillDir, "SKILL.md"), Content: []byte("# Brainstorming\n")},
		Op{Kind: OpReplaceFile, Path: filepath.Join(root, "CLAUDE.md"), Content: []byte("rules\n")},
		Op{Kind: OpMergeValue, Path: claudeJSON, KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"}},
		Op{Kind: OpMergeValue, Path: codexTOML, KeyPath: []string{"mcp_servers", "github"}, Value: map[string]any{"command": "npx"}},
		Op{Kind: OpMergeValue, Path: geminiYAML, KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"}},
	)

	ex := newExecutor(t)
	if _, err := ex.Apply(p); err != nil {
		t.Fatalf("first Apply() = %v", err)
	}
	afterFirst := snapshotTree(t, root)

	second, err := ex.Apply(p)
	if err != nil {
		t.Fatalf("second Apply() = %v", err)
	}
	wantStatuses(t, second,
		StatusUnchanged, StatusUnchanged, StatusUnchanged,
		StatusUnchanged, StatusUnchanged, StatusUnchanged)
	if second.Changed() {
		t.Error("Changed() = true on a re-apply that wrote nothing")
	}
	if second.BackupDir != "" {
		t.Errorf("re-applying took backups (%s); an unchanged file must not be backed up", second.BackupDir)
	}
	assertSameTree(t, afterFirst, snapshotTree(t, root))

	entries, err := os.ReadDir(ex.BackupRoot)
	if err != nil {
		t.Fatalf("reading backup root: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("backup root holds %d directories, want 1 (only the first apply had anything to back up)", len(entries))
	}
}

func TestApplyRefusesToClobberADifferentFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "skills", "x", "SKILL.md")
	writeFile(t, target, "the user's own edits\n")

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(Op{Kind: OpCreateFile, Path: target, Content: []byte("packaged version\n")})

	res, err := newExecutor(t).Apply(p)
	if err == nil {
		t.Fatalf("Apply() = nil, want a conflict; result %+v", res)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Apply() = %v (%T), want *ConflictError", err, err)
	}
	if conflict.Path != target {
		t.Errorf("ConflictError.Path = %q, want %q", conflict.Path, target)
	}
	if got := readFile(t, target); got != "the user's own edits\n" {
		t.Errorf("the user's file was modified anyway: %q", got)
	}

	// The same content is not a conflict: that is a plan that has already
	// been applied.
	same := Plan{Tool: model.ToolClaudeCode}
	same.Add(Op{Kind: OpCreateFile, Path: target, Content: []byte("the user's own edits\n")})
	res, err = newExecutor(t).Apply(same)
	if err != nil {
		t.Fatalf("Apply() = %v, want an unchanged no-op", err)
	}
	wantStatuses(t, res, StatusUnchanged)
}

func TestApplyRefusesToRewriteAnUnparseableFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".claude.json")
	broken := "{ this is not json\n"
	writeFile(t, target, broken)

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(Op{Kind: OpMergeValue, Path: target, KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"}})

	if _, err := newExecutor(t).Apply(p); err == nil {
		t.Fatal("Apply() = nil, want a parse failure rather than a rewritten file")
	}
	if got := readFile(t, target); got != broken {
		t.Errorf("the unparseable file was rewritten as %q", got)
	}
}

func TestApplyRefusesToMergeThroughANonObject(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".claude.json")
	writeFile(t, target, `{"mcpServers": "not an object"}`+"\n")

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(Op{Kind: OpMergeValue, Path: target, KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"}})

	_, err := newExecutor(t).Apply(p)
	if err == nil {
		t.Fatal("Apply() = nil, want a refusal to merge through a string")
	}
	if !strings.Contains(err.Error(), "mcpServers") {
		t.Errorf("Apply() = %q, want it to name the offending key", err)
	}
}

func TestApplyValidatesBeforeWritingAnything(t *testing.T) {
	root := t.TempDir()
	before := snapshotTree(t, root)

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(
		Op{Kind: OpCreateFile, Path: filepath.Join(root, "first.md"), Content: []byte("x")},
		Op{Kind: OpCreateFile, Path: "relative.md", Content: []byte("y")},
	)

	if _, err := newExecutor(t).Apply(p); err == nil {
		t.Fatal("Apply() = nil, want the invalid second operation to be rejected")
	}
	// Validation happens up front precisely so that a bad plan needs no
	// rollback: nothing has been written yet.
	assertSameTree(t, before, snapshotTree(t, root))
}

func TestApplyKeepsExistingFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes are not meaningful on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, ".claude.json")
	copyFixture(t, "claude.json", target)
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(Op{Kind: OpMergeValue, Path: target, KeyPath: []string{"mcpServers", "github"}, Value: map[string]any{"command": "npx"}})

	if _, err := newExecutor(t).Apply(p); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want 0600: a restore must not widen permissions on config the user locked down", got)
	}
}

func TestApplyMergeCreatesMissingFileAndParents(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "nested", "dir", ".mcp.json")

	p := Plan{Tool: model.ToolClaudeCode}
	p.Add(Op{
		Kind: OpMergeValue, Path: target,
		KeyPath: []string{"mcpServers", "github"},
		Value:   map[string]any{"command": "npx"},
	})

	res, err := newExecutor(t).Apply(p)
	if err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	wantStatuses(t, res, StatusCreated)
	doc := decodeJSON(t, readFile(t, target))
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok || servers["github"] == nil {
		t.Errorf("merged document = %v, want mcpServers.github", doc)
	}
}
