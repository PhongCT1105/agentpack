package secrets

import (
	"testing"
)

func TestParseAllowEntry(t *testing.T) {
	cases := []struct {
		raw     string
		want    AllowEntry
		wantErr bool
	}{
		{"skills/foo/app.jsx:12", AllowEntry{Path: "skills/foo/app.jsx", Line: 12}, false},
		{"  skills/foo/app.jsx:12  ", AllowEntry{Path: "skills/foo/app.jsx", Line: 12}, false},
		{"README.md", AllowEntry{Path: "README.md"}, false},
		{"testdata/", AllowEntry{Path: "testdata/"}, false},
		{"", AllowEntry{}, true},
		{"   ", AllowEntry{}, true},
		{"#comment", AllowEntry{}, true},
		{":12", AllowEntry{}, true},
		{"foo.md:0", AllowEntry{}, true},
		{"foo.md:-1", AllowEntry{}, true},
		{"foo.md:abc", AllowEntry{}, true},
	}
	for _, c := range cases {
		got, err := ParseAllowEntry(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseAllowEntry(%q) = %+v, nil, want error", c.raw, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAllowEntry(%q) error: %v", c.raw, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseAllowEntry(%q) = %+v, want %+v", c.raw, got, c.want)
		}
	}
}

func TestParseAllowlist(t *testing.T) {
	data := []byte(`# a comment
skills/foo/app.jsx:12

README.md # example password, reviewed 2026-08-30
testdata/
`)
	got, err := ParseAllowlist(data)
	if err != nil {
		t.Fatalf("ParseAllowlist() error: %v", err)
	}
	want := []AllowEntry{
		{Path: "skills/foo/app.jsx", Line: 12},
		{Path: "README.md"},
		{Path: "testdata/"},
	}
	if len(got) != len(want) {
		t.Fatalf("ParseAllowlist() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ParseAllowlist()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseAllowlistRejectsBadLine(t *testing.T) {
	if _, err := ParseAllowlist([]byte("foo.md:notanumber\n")); err == nil {
		t.Error("ParseAllowlist(bad line) = nil error, want error")
	}
}

func TestFilterAllowedOnlyWaivesReviewableFindings(t *testing.T) {
	findings := []Finding{
		{Path: "skills/foo/app.jsx", Line: 12, Rule: "assignment", Reviewable: true},
		{Path: "config/settings.json", Line: 2, Rule: "assignment", Reviewable: false},
		{Path: "config/token.md", Line: 3, Rule: "format:github-token", Reviewable: false},
	}
	allow := []AllowEntry{
		{Path: "skills/foo/app.jsx", Line: 12},
		// This entry targets a non-reviewable finding; it must not
		// silence it — an allowlist can never waive a high-confidence
		// match, only reduce false-positive noise in reviewable ones.
		{Path: "config/settings.json", Line: 2},
		{Path: "config/token.md"}, // any line, still not reviewable
	}
	remaining, allowed, used, unused := FilterAllowed(findings, allow)

	if len(remaining) != 2 {
		t.Errorf("remaining = %+v, want 2 findings (the non-reviewable ones)", remaining)
	}
	for _, f := range remaining {
		if f.Reviewable {
			t.Errorf("remaining contains a reviewable finding that should have been waived: %+v", f)
		}
	}
	if len(allowed) != 1 || allowed[0].Path != "skills/foo/app.jsx" {
		t.Errorf("allowed = %+v, want just the jsx finding", allowed)
	}
	if len(used) != 1 || used[0].Path != "skills/foo/app.jsx" {
		t.Errorf("used = %+v, want just the jsx entry", used)
	}
	if len(unused) != 2 {
		t.Errorf("unused = %+v, want 2 (the entries that matched only non-reviewable findings)", unused)
	}
}

func TestFilterAllowedDirectoryPrefix(t *testing.T) {
	findings := []Finding{
		{Path: "testdata/a/fixture.json", Line: 1, Rule: "assignment", Reviewable: true},
		{Path: "testdata/b/fixture.json", Line: 9, Rule: "entropy:high", Reviewable: true},
		{Path: "other/fixture.json", Line: 1, Rule: "assignment", Reviewable: true},
	}
	allow := []AllowEntry{{Path: "testdata/"}}
	remaining, allowed, used, _ := FilterAllowed(findings, allow)

	if len(remaining) != 1 || remaining[0].Path != "other/fixture.json" {
		t.Errorf("remaining = %+v, want just other/fixture.json", remaining)
	}
	if len(allowed) != 2 {
		t.Errorf("allowed = %+v, want the two testdata findings", allowed)
	}
	if len(used) != 1 {
		t.Errorf("used = %+v, want the testdata/ entry once", used)
	}
}

func TestFilterAllowedLineZeroMatchesAnyLine(t *testing.T) {
	findings := []Finding{
		{Path: "README.md", Line: 3, Rule: "assignment", Reviewable: true},
		{Path: "README.md", Line: 20, Rule: "entropy:high", Reviewable: true},
	}
	allow := []AllowEntry{{Path: "README.md"}}
	remaining, allowed, _, _ := FilterAllowed(findings, allow)
	if len(remaining) != 0 {
		t.Errorf("remaining = %+v, want none", remaining)
	}
	if len(allowed) != 2 {
		t.Errorf("allowed = %+v, want both README.md findings", allowed)
	}
}

func TestFormatAllowlistRoundTrip(t *testing.T) {
	entries := []AllowEntry{
		{Path: "b.md", Line: 2},
		{Path: "a.md", Line: 1},
		{Path: "a.md", Line: 1}, // duplicate, must be deduped
		{Path: "testdata/"},
	}
	data := FormatAllowlist(entries)
	got, err := ParseAllowlist(data)
	if err != nil {
		t.Fatalf("round-trip parse error: %v\ndata:\n%s", err, data)
	}
	want := []AllowEntry{
		{Path: "a.md", Line: 1},
		{Path: "b.md", Line: 2},
		{Path: "testdata/"},
	}
	if len(got) != len(want) {
		t.Fatalf("FormatAllowlist round-trip = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("FormatAllowlist round-trip[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
