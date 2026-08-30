package packio

import (
	"path"
	"strings"
)

// BundleLimitBytes is the size above which a single bundled component is
// reported as suspicious. Portable skill/agent/rule content is text; a
// bundle this large means something unportable slipped past the exclusion
// rules and is worth telling the user about rather than silently shipping.
const BundleLimitBytes = 10 << 20 // 10 MiB

// Directory names that never belong in a portable pack, matched at any
// depth inside a bundled component.
//
// Three distinct reasons, and the first is a security one:
//
//   - Credential carriers. A .git directory's config can hold a remote URL
//     with an embedded token (https://user:token@host/repo), and its
//     objects can hold anything ever committed. Copying VCS metadata into
//     a pack meant for publishing is a leak vector, not just bloat.
//   - Reinstallable or regenerable. Vendored dependencies, build output and
//     caches are reproducible from the manifest that stays in the bundle;
//     shipping them makes packs enormous (a real skill measured 1.1 GB, of
//     which 726 MB was node_modules) and floods the secret scanner with
//     high-entropy noise from minified bundles.
//   - Machine noise. OS and editor state that means nothing on another
//     machine.
var excludedDirs = map[string]string{
	// credential carriers / VCS metadata
	".git": "VCS metadata (may contain tokenized remote URLs)",
	".hg":  "VCS metadata",
	".svn": "VCS metadata",
	// vendored dependencies
	"node_modules":  "vendored dependencies",
	"bower_components": "vendored dependencies",
	"vendor":        "vendored dependencies",
	".venv":         "vendored dependencies",
	"venv":          "vendored dependencies",
	"site-packages": "vendored dependencies",
	"Pods":          "vendored dependencies",
	".bundle":       "vendored dependencies",
	// build output
	"dist":     "build output",
	"build":    "build output",
	".next":    "build output",
	".nuxt":    "build output",
	".output":  "build output",
	".svelte-kit": "build output",
	"coverage": "build output",
	// The component's own development apparatus. A pack carries what another
	// machine needs to USE a skill, not what its author needs to develop it:
	// a test suite, CI config and git hooks play no part in the restored
	// environment. Excluding them also keeps packs inspectable — a human is
	// expected to read a pack before installing it, which is impossible when
	// it carries thousands of test files. (Test suites for redaction tooling
	// are also, by their nature, full of realistic fake credentials.)
	"test":       "component's own test suite",
	"tests":      "component's own test suite",
	"__tests__":  "component's own test suite",
	"e2e":        "component's own test suite",
	".github":    "CI configuration",
	".circleci":  "CI configuration",
	".husky":     "git hooks",
	// caches
	"__pycache__":   "cache",
	".pytest_cache": "cache",
	".mypy_cache":   "cache",
	".ruff_cache":   "cache",
	".turbo":        "cache",
	".parcel-cache": "cache",
	".gradle":       "cache",
	".cache":        "cache",
}

// Exact filenames that never belong in a pack.
var excludedFiles = map[string]string{
	".DS_Store":   "machine noise",
	"Thumbs.db":   "machine noise",
	".netrc":      "credential file",
	".npmrc":      "credential file (may contain registry auth tokens)",
	".pypirc":     "credential file",
	"credentials": "credential file",
}

// IsEnvSecretFile reports whether name is a dotenv file that carries real
// values rather than an example. ".env", ".env.local" and ".env.production"
// are credential files; ".env.example" and ".env.sample" are templates that
// document required variables and are safe — and useful — to keep.
func IsEnvSecretFile(name string) bool {
	if name != ".env" && !strings.HasPrefix(name, ".env.") {
		return false
	}
	switch {
	case strings.HasSuffix(name, ".example"),
		strings.HasSuffix(name, ".sample"),
		strings.HasSuffix(name, ".template"),
		strings.HasSuffix(name, ".dist"):
		return false
	}
	return true
}

// BundleExclusion reports why the slash-separated path rel, relative to the
// root of a bundled component, must not be copied into a pack — or "" if it
// should be. Callers surface the reason as a warning so exclusions are
// visible rather than silent.
func BundleExclusion(rel string) string {
	rel = path.Clean(strings.ReplaceAll(rel, "\\", "/"))
	parts := strings.Split(rel, "/")

	// Any excluded directory name anywhere on the path removes the file.
	for _, p := range parts[:max(len(parts)-1, 0)] {
		if reason, ok := excludedDirs[p]; ok {
			return p + "/: " + reason
		}
	}

	base := parts[len(parts)-1]
	// A directory name used as the final element still counts (an empty
	// dir never reaches here, but a file named e.g. "credentials" does).
	if reason, ok := excludedFiles[base]; ok {
		return base + ": " + reason
	}
	if IsEnvSecretFile(base) {
		return base + ": environment file with real values"
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
