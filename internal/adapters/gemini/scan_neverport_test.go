package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// neverPort are the fixture paths that stand in for the config matrix's
// "never port" list: ~/.gemini/oauth_creds.json, .env files, account state,
// and caches. The scan fixture home contains every one of them, populated
// with FAKE credential material, so a regression that widens the scan
// surface fails here instead of on a user's machine.
var neverPort = []string{
	"oauth_creds.json",
	"google_accounts.json",
	".env",
	"tmp",
}

// fakeSecrets are the credential-shaped values seeded in the never-port
// fixture files. None may appear anywhere in a scanned inventory.
var fakeSecrets = []string{
	"ya29.FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE", // oauth_creds.json access token
	"1//0gFAKEFAKEFAKEFAKEFAKEFAKEFAKE",         // oauth_creds.json refresh token
	"AIzaFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAK",   // .env API key
	"dev@example.com",                           // google_accounts.json
}

// TestScanNeverPortsCredentialFiles is the release-critical guarantee: a
// scan of a home holding oauth_creds.json, .env, google_accounts.json and a
// cache directory must not model them, must not read their values, and
// must not name them — a warning pointing at a credential file still tells
// a reader where the credentials are.
func TestScanNeverPortsCredentialFiles(t *testing.T) {
	// Sanity: the fixture really does contain the never-port files, so this
	// test cannot pass by scanning an empty tree.
	geminiDir := filepath.Join(fixtureHomeDir(t), ".gemini")
	for _, name := range neverPort {
		if _, err := os.Stat(filepath.Join(geminiDir, name)); err != nil {
			t.Fatalf("fixture is missing never-port file %q: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(fixtureProjectDir(t), ".env")); err != nil {
		t.Fatalf("fixture project is missing .env: %v", err)
	}

	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	// Strip the fixture roots first: the checkout path itself may contain a
	// segment like "tmp", which would make the substring checks lie.
	blob := inventoryBlob(t, inv)
	blob = strings.ReplaceAll(blob, `\\`, "/") // JSON-escaped Windows separators
	blob = strings.ReplaceAll(blob, `\`, "/")
	blob = strings.ReplaceAll(blob, filepath.ToSlash(geminiDir), "$HOME/.gemini")
	blob = strings.ReplaceAll(blob, filepath.ToSlash(fixtureProjectDir(t)), "$PROJECT")
	for _, name := range neverPort {
		if strings.Contains(blob, name) {
			t.Errorf("never-port path %q surfaced in the inventory:\n%s", name, blob)
		}
	}
	for _, secret := range fakeSecrets {
		if strings.Contains(blob, secret) {
			t.Errorf("value from a never-port file surfaced in the inventory:\n%s", blob)
		}
	}
}

// Warnings are printed by `agentpack scan` verbatim, so they may name keys
// and paths but never values: the fixture's MCP env and header values are
// secret-shaped and must not appear in any warning.
func TestScanWarningsNeverCarryValues(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Warnings) == 0 {
		t.Fatal("fixture should produce warnings; the assertion below would be vacuous")
	}
	for _, w := range inv.Warnings {
		if strings.Contains(w.String(), "FAKE") {
			t.Errorf("warning carries a secret-shaped value: %s", w)
		}
	}
}

// Scan is read-only: the fixture tree must be byte-identical afterwards.
func TestScanDoesNotWrite(t *testing.T) {
	home, project := fixtureHomeDir(t), fixtureProjectDir(t)
	before := treeSnapshot(t, home) + treeSnapshot(t, project)

	a := newFixtureAdapter(t)
	if _, err := a.Scan(model.ScanScope{Global: true, ProjectDir: project}); err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	if after := treeSnapshot(t, home) + treeSnapshot(t, project); after != before {
		t.Errorf("Scan() modified the fixture tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// inventoryBlob renders everything a scan produced — component fields and
// warnings — as one string, so leak assertions cover the whole inventory
// rather than the fields a test remembered to check.
func inventoryBlob(t *testing.T, inv model.Inventory) string {
	t.Helper()
	var b strings.Builder
	for _, c := range inv.Components {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal component %v: %v", c, err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	for _, w := range inv.Warnings {
		b.WriteString(w.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// treeSnapshot lists every path under root with its size and mode.
func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		b.WriteString(filepath.ToSlash(rel))
		if !info.IsDir() {
			b.WriteString(" " + info.Mode().String())
			b.WriteString(" " + strconv.FormatInt(info.Size(), 10))
		}
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
