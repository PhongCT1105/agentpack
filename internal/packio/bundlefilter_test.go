package packio

import "testing"

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
