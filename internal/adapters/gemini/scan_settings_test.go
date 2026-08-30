package gemini

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// writeGlobalSettings drops a ~/.gemini/settings.json into a temp home.
func writeGlobalSettings(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanSettings scans a temp home holding just the given settings.json.
func scanSettings(t *testing.T, content string) model.Inventory {
	t.Helper()
	home := t.TempDir()
	writeGlobalSettings(t, home, content)
	a := New()
	a.home = home
	a.lookPath = lookPathHit
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	return inv
}

// settings.json is a mixed file: the portable subset is modeled, the rest
// is reported. Nothing may be assumed portable just because it is present.
func TestScanSettingsModelsOnlyKnownKeys(t *testing.T) {
	inv := scanSettings(t, `{
	  "theme": "GitHub",
	  "vimMode": true,
	  "selectedAuthType": "oauth-personal",
	  "hasSeenIdeIntegrationNudge": true,
	  "someFutureKey": {"nested": 1}
	}`)

	s := settingByScope(t, inv, model.ScopeGlobal)
	if len(s.Spec.Values) != 2 || s.Spec.Values["theme"] != "GitHub" || s.Spec.Values["vimMode"] != true {
		t.Fatalf("settings values = %v, want only the portable subset", s.Spec.Values)
	}
	for _, key := range []string{"selectedAuthType", "hasSeenIdeIntegrationNudge", "someFutureKey"} {
		if _, ok := s.Spec.Values[key]; ok {
			t.Errorf("unmodeled key %q was carried into the setting component", key)
		}
	}

	if len(inv.Warnings) != 2 {
		t.Fatalf("want a state warning and an unknown-key warning, got %v", inv.Warnings)
	}
	if !strings.HasSuffix(inv.Warnings[0].Message, "not ported: hasSeenIdeIntegrationNudge, selectedAuthType") {
		t.Errorf("state warning = %q", inv.Warnings[0].Message)
	}
	if !strings.HasSuffix(inv.Warnings[1].Message, "does not model: someFutureKey") {
		t.Errorf("unknown-key warning = %q", inv.Warnings[1].Message)
	}
}

// mcpServers is modeled as its own kind, so it is neither duplicated into
// the settings document nor reported as unmodeled — and a file holding
// nothing else yields no settings component at all.
func TestScanSettingsWithOnlyMCPServers(t *testing.T) {
	inv := scanSettings(t, `{"mcpServers": {"github": {"command": "npx"}}}`)

	if n := len(inv.ByKind(model.KindSetting)); n != 0 {
		t.Errorf("mcpServers-only file produced %d setting components, want 0", n)
	}
	if n := len(inv.ByKind(model.KindMCPServer)); n != 1 {
		t.Errorf("got %d MCP servers, want 1", n)
	}
	if len(inv.Warnings) != 0 {
		t.Errorf("mcpServers must not be reported as unmodeled: %v", inv.Warnings)
	}
}

func TestScanSettingsMalformedJSON(t *testing.T) {
	inv := scanSettings(t, `{"theme": "GitHub",`)

	if len(inv.Components) != 0 {
		t.Errorf("malformed settings produced components: %v", inv.Components)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "not a valid JSON object") {
		t.Fatalf("want one not-valid-JSON warning, got %v", inv.Warnings)
	}
}

func TestScanSettingsMCPServersWrongShape(t *testing.T) {
	inv := scanSettings(t, `{"theme": "GitHub", "mcpServers": ["github"]}`)

	if n := len(inv.ByKind(model.KindMCPServer)); n != 0 {
		t.Errorf("got %d MCP servers from a non-object mcpServers, want 0", n)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "unexpected shape") {
		t.Fatalf("want one unexpected-shape warning, got %v", inv.Warnings)
	}
	// The rest of the mixed file is still modeled.
	if s := settingByScope(t, inv, model.ScopeGlobal); s.Spec.Values["theme"] != "GitHub" {
		t.Errorf("a bad mcpServers block must not lose the portable settings: %v", s.Spec.Values)
	}
}

func TestScanMCPEntryWrongShape(t *testing.T) {
	inv := scanSettings(t, `{"mcpServers": {"bad": ["npx"], "good": {"command": "npx"}}}`)

	got := componentNames(inv, model.KindMCPServer)
	if len(got) != 1 || got[0] != "good" {
		t.Fatalf("MCP servers = %v, want only [good]", got)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "mcpServers.bad") {
		t.Fatalf("want one warning naming the bad entry, got %v", inv.Warnings)
	}
}

// Gemini implies transport from which endpoint key is set: command → stdio,
// httpUrl → http, url → sse.
func TestScanMCPTransportInference(t *testing.T) {
	inv := scanSettings(t, `{"mcpServers": {
	  "stdio-server": {"command": "npx"},
	  "http-server": {"httpUrl": "https://example.test/mcp"},
	  "sse-server": {"url": "https://example.test/sse"}
	}}`)

	tests := []struct {
		name      string
		transport model.Transport
		url       string
	}{
		{"stdio-server", model.TransportStdio, ""},
		{"http-server", model.TransportHTTP, "https://example.test/mcp"},
		{"sse-server", model.TransportSSE, "https://example.test/sse"},
	}
	for _, tt := range tests {
		s := mcpByName(t, inv, tt.name)
		if s.Spec.Transport != tt.transport {
			t.Errorf("%s transport = %q, want %q", tt.name, s.Spec.Transport, tt.transport)
		}
		if s.Spec.URL != tt.url {
			t.Errorf("%s url = %q, want %q", tt.name, s.Spec.URL, tt.url)
		}
	}
	if len(inv.Warnings) != 0 {
		t.Errorf("unambiguous transports must not warn: %v", inv.Warnings)
	}
}

func TestScanMCPWithoutAnyEndpoint(t *testing.T) {
	inv := scanSettings(t, `{"mcpServers": {"hollow": {"trust": true}}}`)

	h := mcpByName(t, inv, "hollow")
	if h.Spec.Transport != "" {
		t.Errorf("transport = %q, want empty for an uninferrable server", h.Spec.Transport)
	}
	var msgs []string
	for _, w := range inv.Warnings {
		msgs = append(msgs, w.Message)
	}
	joined := strings.Join(msgs, " | ")
	if len(msgs) != 2 || !strings.Contains(joined, "transport unknown") ||
		!strings.Contains(joined, "does not model: trust") {
		t.Fatalf("want unknown-transport and unknown-key warnings, got %v", inv.Warnings)
	}
}

func TestScanMCPAmbiguousTransportWarns(t *testing.T) {
	inv := scanSettings(t, `{"mcpServers": {"both": {
	  "command": "npx", "httpUrl": "https://example.test/mcp"
	}}}`)

	b := mcpByName(t, inv, "both")
	if b.Spec.Transport != model.TransportStdio {
		t.Errorf("transport = %q, want stdio (command wins)", b.Spec.Transport)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "more than one transport (command, httpUrl)") {
		t.Fatalf("want one ambiguous-transport warning, got %v", inv.Warnings)
	}
}

func TestScanMCPDeadServerWarns(t *testing.T) {
	home := t.TempDir()
	writeGlobalSettings(t, home, `{"mcpServers": {
	  "alive": {"command": "present-mcp"},
	  "ghost": {"command": "ghost-mcp-server"}
	}}`)
	a := New()
	a.home = home
	a.lookPath = func(cmd string) (string, error) {
		if cmd == "ghost-mcp-server" {
			return "", errors.New("not found")
		}
		return "/home/user/.local/bin/" + cmd, nil
	}

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if n := len(inv.ByKind(model.KindMCPServer)); n != 2 {
		t.Fatalf("got %d MCP servers, want 2 (dead is a warning, not exclusion)", n)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want exactly one dead-server warning, got %v", inv.Warnings)
	}
	w := inv.Warnings[0].Message
	if !strings.Contains(w, "ghost") || !strings.Contains(w, "may be dead") {
		t.Errorf("warning = %q, want it to name ghost as possibly dead", w)
	}
}

// Backup debris (settings.json.bak) is never a config file: scan opens the
// exact paths the config matrix names and nothing else, so the fixture's
// backup — which holds an extra MCP server — must not appear.
func TestScanIgnoresBackupSettings(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	for _, c := range inv.Components {
		if c.Name() == "retired" {
			t.Errorf("settings.json.bak was scanned: %v", c)
		}
	}
	for _, w := range inv.Warnings {
		if strings.Contains(w.Path, ".bak") {
			t.Errorf("backup debris surfaced as a warning: %s", w)
		}
	}
}
