package packio

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBundleExclusionCopies(t *testing.T) {
	// Content that is the whole point of a bundle must survive.
	keep := []string{
		"SKILL.md",
		"references/api.md",
		"scripts/helper.py",
		"package.json",
		"bun.lock",
		".env.example",
		".env.sample",
		"src/index.ts",
		"agents/db-migrator.md",
		"testdata/fixture.json",
		"spec/openapi.yaml", // a specification, not a test suite
	}
	for _, rel := range keep {
		if reason := BundleExclusion(rel); reason != "" {
			t.Errorf("BundleExclusion(%q) = %q, want copied", rel, reason)
		}
	}
}

func TestBundleExclusionDropsUnportable(t *testing.T) {
	drop := []string{
		"node_modules/left-pad/index.js",
		"deep/nested/node_modules/pkg/dist/bundle.min.js",
		".git/config",
		".git/objects/ab/cdef",
		"sub/.git/HEAD",
		"vendor/github.com/pkg/errors/errors.go",
		".venv/lib/python3.12/site-packages/mod.py",
		"dist/bundle.js",
		"build/output.css",
		".next/static/chunk.js",
		"coverage/lcov.info",
		"__pycache__/mod.cpython-312.pyc",
		".turbo/cache/x",
		".DS_Store",
		"nested/.DS_Store",
		"Pods/Alamofire/Source.swift",
		"test/redact-engine.test.ts",
		"tests/unit/foo_test.py",
		"__tests__/component.test.tsx",
		"e2e/login.spec.ts",
		".github/workflows/ci.yml",
		".husky/pre-commit",
	}
	for _, rel := range drop {
		if BundleExclusion(rel) == "" {
			t.Errorf("BundleExclusion(%q) = copied, want excluded", rel)
		}
	}
}

// Dotenv files carrying real values are credential files and must never
// reach a pack; the .example/.sample templates that document required
// variables are exactly what a pack should keep.
func TestReleaseBlocking_BundleExclusionDropsEnvFiles(t *testing.T) {
	secret := []string{".env", ".env.local", ".env.production", "config/.env", ".env.development.local"}
	for _, rel := range secret {
		if BundleExclusion(rel) == "" {
			t.Errorf("BundleExclusion(%q) = copied, want excluded — dotenv files carry real credentials", rel)
		}
	}
	templates := []string{".env.example", ".env.sample", ".env.template", ".env.dist"}
	for _, rel := range templates {
		if reason := BundleExclusion(rel); reason != "" {
			t.Errorf("BundleExclusion(%q) = %q, want copied — templates document required vars", rel, reason)
		}
	}
}

// VCS metadata and credential files are excluded for a security reason, not
// a size one: .git/config can carry https://user:token@host remotes, and
// .npmrc can carry registry auth tokens.
func TestReleaseBlocking_BundleExclusionDropsCredentialCarriers(t *testing.T) {
	for _, rel := range []string{".git/config", ".npmrc", "sub/.npmrc", ".netrc", ".pypirc", "credentials"} {
		if BundleExclusion(rel) == "" {
			t.Errorf("BundleExclusion(%q) = copied, want excluded — credential carrier", rel)
		}
	}
}

func TestIsEnvSecretFile(t *testing.T) {
	cases := map[string]bool{
		".env":              true,
		".env.local":        true,
		".env.production":   true,
		".env.example":      false,
		".env.sample":       false,
		".env.template":     false,
		".env.dist":         false,
		"env":               false,
		"environment.md":    false,
		"my.env.notdotenv":  false,
	}
	for name, want := range cases {
		if got := IsEnvSecretFile(name); got != want {
			t.Errorf("IsEnvSecretFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBundleExclusionWindowsSeparators(t *testing.T) {
	if BundleExclusion(`node_modules\pkg\index.js`) == "" {
		t.Error("backslash-separated path under node_modules should be excluded")
	}
}

// Real installs symlink content into place: a skill directory whose only
// entry is "SKILL.md -> /path/to/repo/SKILL.md" is the common case. Skipping
// non-regular files silently produced an empty bundle while the manifest
// promised content — restore then refused the pack. Bundling must follow
// symlinks, for both files and directories, without looping on cycles.
func TestCopyBundleFollowsSymlinks(t *testing.T) {
	src := t.TempDir()
	realFile := filepath.Join(src, "real", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(realFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realFile, []byte("# real skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	realSub := filepath.Join(src, "refs", "guide.md")
	if err := os.MkdirAll(filepath.Dir(realSub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realSub, []byte("guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	comp := filepath.Join(src, "component")
	if err := os.MkdirAll(comp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFile, filepath.Join(comp, "SKILL.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Dir(realSub), filepath.Join(comp, "references")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "out")
	rep, err := copyBundle(comp, dst)
	if err != nil {
		t.Fatalf("copyBundle() error: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "SKILL.md"))
	if err != nil {
		t.Fatalf("symlinked SKILL.md was not copied: %v", err)
	}
	if string(got) != "# real skill\n" {
		t.Errorf("SKILL.md content = %q, want the symlink target's content", got)
	}
	if _, err := os.ReadFile(filepath.Join(dst, "references", "guide.md")); err != nil {
		t.Errorf("content under a symlinked directory was not copied: %v", err)
	}
	if len(rep.escaped) == 0 {
		t.Error("symlinks resolving outside the component were not reported")
	}
}

func TestCopyBundleSurvivesSymlinkCycle(t *testing.T) {
	src := t.TempDir()
	comp := filepath.Join(src, "component")
	if err := os.MkdirAll(comp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(comp, "SKILL.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(comp, filepath.Join(comp, "loop")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := copyBundle(comp, filepath.Join(t.TempDir(), "out"))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("copyBundle() on a cyclic tree: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("copyBundle() did not terminate on a symlink cycle")
	}
}

// A broken symlink carries no content: it must be reported, not fatal.
func TestCopyBundleToleratesBrokenSymlink(t *testing.T) {
	comp := t.TempDir()
	if err := os.WriteFile(filepath.Join(comp, "SKILL.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(comp, "nope"), filepath.Join(comp, "dangling.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	rep, err := copyBundle(comp, filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatalf("copyBundle() with a broken symlink: %v", err)
	}
	if len(rep.excluded) == 0 {
		t.Error("broken symlink was not reported")
	}
}
