// Package secrets implements the secrets layer (docs/security.md): the
// redactor that classifies scanned config values before they can reach a
// pack, the whole-pack scanner, and — in a later phase — the restore-time
// credential resolver.
//
// Classification is deliberately three-valued. Secret and Plain are the
// clear cases; Uncertain is the band the save flow surfaces to the user
// (the SUPABASE_URL problem), with defaults favoring redaction.
package secrets

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// Level is how confidently a value is judged secret.
type Level int

const (
	// Plain values are safe to store in a pack.
	Plain Level = iota
	// Uncertain values are surfaced to the user; default is to redact.
	Uncertain
	// Secret values must never be stored; they become credential requirements.
	Secret
)

func (l Level) String() string {
	switch l {
	case Plain:
		return "plain"
	case Uncertain:
		return "uncertain"
	case Secret:
		return "secret"
	}
	return "unknown"
}

// Verdict is the result of classifying one (key, value) pair. Rule is a
// stable machine-readable id; Reason is a human sentence for the save UI.
type Verdict struct {
	Level  Level
	Rule   string
	Reason string
}

func plainVerdict() Verdict {
	return Verdict{Level: Plain, Rule: "none", Reason: ""}
}

// valueFormat is a known credential format detected in a value alone.
type valueFormat struct {
	rule   string
	reason string
	match  func(v string) bool
}

var valueFormats = []valueFormat{
	{"format:private-key", "value contains a PEM private key block", func(v string) bool {
		return strings.Contains(v, "-----BEGIN ") && strings.Contains(v, " PRIVATE KEY-----")
	}},
	{"format:github-token", "value looks like a GitHub token", matchAny(
		`^gh[pousr]_[A-Za-z0-9]{16,}$`,
		`^github_pat_[A-Za-z0-9_]{20,}$`,
	)},
	{"format:stripe-key", "value looks like a Stripe API key", matchAny(
		`^[sr]k_(live|test)_[A-Za-z0-9]{16,}$`,
	)},
	{"format:sk-key", "value looks like an sk- API key", matchAny(
		`^sk-[A-Za-z0-9_-]{16,}$`,
	)},
	{"format:slack-token", "value looks like a Slack token", matchAny(
		`^xox[baprs]-[A-Za-z0-9-]{10,}$`,
	)},
	{"format:aws-access-key", "value looks like an AWS access key id", matchAny(
		`^(AKIA|ASIA)[0-9A-Z]{16}$`,
	)},
	{"format:gitlab-token", "value looks like a GitLab token", matchAny(
		`^glpat-[A-Za-z0-9_-]{16,}$`,
	)},
	{"format:npm-token", "value looks like an npm token", matchAny(
		`^npm_[A-Za-z0-9]{16,}$`,
	)},
	{"format:jwt", "value looks like a JWT", matchAny(
		`^eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`,
	)},
	{"format:url-password", "value is a connection string containing a password", func(v string) bool {
		if !strings.Contains(v, "://") {
			return false
		}
		u, err := url.Parse(v)
		if err != nil || u.User == nil {
			return false
		}
		pw, has := u.User.Password()
		return has && pw != ""
	}},
}

func matchAny(patterns ...string) func(string) bool {
	res := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		res[i] = regexp.MustCompile(p)
	}
	return func(v string) bool {
		for _, re := range res {
			if re.MatchString(v) {
				return true
			}
		}
		return false
	}
}

// Key-name terms (docs/security.md layer 2). Keys are split into name
// segments (snake_case, kebab-case, camelCase, header capitalization), so
// keybindings/keymap/hotkey/monkey cannot false-positive on "key":
// exactTerms must equal a whole segment (so oauth does not match auth);
// suffixTerms may also end one (clientsecret, apitoken). A trailing plural
// "s" is stripped first.
var (
	keyExactTerms  = []string{"key", "apikey", "auth"}
	keySuffixTerms = []string{"token", "secret", "password", "passwd", "passphrase", "credential", "authorization"}
)

// Classify judges whether value (stored under key — an env var name, header
// name, or settings key) is safe to store in a pack. Precedence: known
// credential formats, then key names, then value shape (placeholder, path,
// URL, entropy). Uncertain means "ask the user, default to redacting".
func Classify(key, value string) Verdict {
	v := strings.TrimSpace(value)

	// 1. A value in a known credential format is secret regardless of key.
	for _, f := range valueFormats {
		if f.match(v) {
			return Verdict{Level: Secret, Rule: f.rule, Reason: f.reason}
		}
	}

	// 2. A secret-named key marks its value secret regardless of content:
	// even an empty or placeholder value names a credential requirement.
	if seg, ok := keyLooksSecret(key); ok {
		return Verdict{Level: Secret, Rule: "key-name", Reason: fmt.Sprintf("key name contains %q", seg)}
	}

	// 3. Env-expansion placeholders reference a value; they contain nothing.
	if IsPlaceholder(v) {
		return Verdict{Level: Plain, Rule: "placeholder", Reason: "env-expansion placeholder, no value stored"}
	}

	// 4. Filesystem paths are machine data, not secrets — but a path-shaped
	// value can still smuggle a token (raw base64 occasionally starts with
	// "/", store paths embed hashes), so a high-entropy component demotes
	// the exemption to uncertain rather than silently storing the value.
	if looksLikePath(v) {
		if entropyLevel(v, false) != Plain {
			return Verdict{Level: Uncertain, Rule: "path-entropy", Reason: "path-like value contains a high-entropy component"}
		}
		return Verdict{Level: Plain, Rule: "path", Reason: "filesystem path"}
	}

	if len(v) < minEntropyRunLen {
		return plainVerdict()
	}

	// 5. URLs never auto-classify as secret on entropy alone, but a URL
	// with a high-entropy component (a project ref, a signed path) is
	// surfaced as uncertain — the SUPABASE_URL problem.
	if isURL(v) {
		if lvl := entropyLevel(v, true); lvl != Plain {
			return Verdict{Level: Uncertain, Rule: "url-entropy", Reason: "URL contains a high-entropy component"}
		}
		return plainVerdict()
	}

	// 6. Entropy over token runs catches random API keys with bland names.
	switch entropyLevel(v, false) {
	case Secret:
		return Verdict{Level: Secret, Rule: "entropy:high", Reason: "value contains a high-entropy token"}
	case Uncertain:
		return Verdict{Level: Uncertain, Rule: "entropy:mid", Reason: "value contains a moderately high-entropy token"}
	}
	return plainVerdict()
}

// keyLooksSecret reports whether the key's name segments mark it secret,
// returning the matched term.
func keyLooksSecret(key string) (string, bool) {
	for _, seg := range nameSegments(key) {
		seg = strings.TrimSuffix(seg, "s")
		for _, term := range keyExactTerms {
			if seg == term {
				return term, true
			}
		}
		for _, term := range keySuffixTerms {
			if strings.HasSuffix(seg, term) {
				return term, true
			}
		}
	}
	return "", false
}

// nameSegments splits a key into lowercase name segments: on non-alphanumeric
// separators, and on camelCase boundaries (apiToken → api, token; APIKey →
// api, key). Digits are trimmed from segment edges so token2 matches token.
func nameSegments(key string) []string {
	var segs []string
	runes := []rune(key)
	start := -1
	flush := func(end int) {
		if start >= 0 && end > start {
			seg := strings.ToLower(string(runes[start:end]))
			seg = strings.TrimFunc(seg, unicode.IsDigit)
			if seg != "" {
				segs = append(segs, seg)
			}
		}
		start = -1
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush(i)
			continue
		}
		if start < 0 {
			start = i
			continue
		}
		prev := runes[i-1]
		// camelCase boundary: aB, or end of an acronym run (ABc → A|Bc).
		if unicode.IsUpper(r) && unicode.IsLower(prev) {
			flush(i)
			start = i
		} else if unicode.IsUpper(prev) && unicode.IsUpper(r) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
			flush(i)
			start = i
		}
	}
	flush(len(runes))
	return segs
}

var placeholderRes = []*regexp.Regexp{
	regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`),
	regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`),
	regexp.MustCompile(`^%[A-Za-z_][A-Za-z0-9_]*%$`),
}

// IsPlaceholder reports whether value is an env-expansion reference
// (${VAR}, $VAR, %VAR%) rather than a literal value.
func IsPlaceholder(value string) bool {
	for _, re := range placeholderRes {
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

var windowsPathRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func looksLikePath(v string) bool {
	return strings.HasPrefix(v, "/") ||
		strings.HasPrefix(v, "./") ||
		strings.HasPrefix(v, "../") ||
		strings.HasPrefix(v, "~") ||
		windowsPathRe.MatchString(v)
}

func isURL(v string) bool {
	if !strings.Contains(v, "://") {
		return false
	}
	u, err := url.Parse(v)
	return err == nil && u.Scheme != "" && u.Host != ""
}

// Entropy thresholds. Measured Shannon entropy is normalized against the
// maximum possible for the run's length and character class, so short runs
// and small alphabets (hex) are judged fairly. Hex runs below 48 chars are
// capped at Uncertain: they are indistinguishable from digests (git SHAs).
const (
	minEntropyRunLen  = 16
	secretRunLen      = 24
	hexUncertainLen   = 32
	hexSecretLen      = 48
	secretNormEntropy = 0.87
	midNormEntropy    = 0.80
	hexNormEntropy    = 0.85
)

// entropyLevel scans value's token runs and returns the strongest entropy
// verdict. In urlMode runs are pure alphanumerics (URL punctuation splits);
// otherwise runs use the base64 alphabet (+/=), with -_ treated as
// separators so kebab-case identifiers don't false-positive.
func entropyLevel(value string, urlMode bool) Level {
	level := Plain
	for _, run := range tokenRuns(value, urlMode) {
		if len(run) < minEntropyRunLen {
			continue
		}
		if !hasDigit(run) || !hasLetter(run) {
			// Random credentials essentially always mix digits and letters;
			// natural words and pure numbers do not.
			continue
		}
		class := charClassSize(run)
		if class == 0 { // digits only, already excluded above
			continue
		}
		n := shannonBits(run) / math.Log2(math.Min(float64(len(run)), float64(class)))
		var runLevel Level
		if class == 16 { // hex
			switch {
			case len(run) >= hexSecretLen && n >= hexNormEntropy:
				runLevel = Secret
			case len(run) >= hexUncertainLen && n >= hexNormEntropy:
				runLevel = Uncertain
			}
		} else {
			switch {
			case len(run) >= secretRunLen && n >= secretNormEntropy:
				runLevel = Secret
			case n >= midNormEntropy:
				runLevel = Uncertain
			}
		}
		if runLevel > level {
			level = runLevel
		}
	}
	return level
}

func tokenRuns(value string, urlMode bool) []string {
	inRun := func(r rune) bool {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return true
		}
		if urlMode {
			return false
		}
		return r == '+' || r == '/' || r == '='
	}
	var runs []string
	var cur strings.Builder
	for _, r := range value {
		if inRun(r) {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			runs = append(runs, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		runs = append(runs, cur.String())
	}
	return runs
}

func hasDigit(s string) bool {
	return strings.ContainsFunc(s, unicode.IsDigit)
}

func hasLetter(s string) bool {
	return strings.ContainsFunc(s, unicode.IsLetter)
}

// charClassSize estimates the alphabet a run is drawn from: 16 for hex,
// 36 for single-case alphanumerics, 62 for mixed case, 65 for base64.
func charClassSize(s string) int {
	var hasUpper, hasLower, hasSym, hexOnly bool
	hexOnly = true
	digitsOnly := true
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
			digitsOnly = false
			if r >= 'a' {
				hasLower = true
			} else {
				hasUpper = true
			}
		case r >= 'a' && r <= 'z':
			digitsOnly, hexOnly = false, false
			hasLower = true
		case r >= 'A' && r <= 'Z':
			digitsOnly, hexOnly = false, false
			hasUpper = true
		default: // + / =
			digitsOnly, hexOnly = false, false
			hasSym = true
		}
	}
	switch {
	case digitsOnly:
		return 0
	case hexOnly && !(hasUpper && hasLower):
		return 16
	case hasSym:
		return 65
	case hasUpper && hasLower:
		return 62
	default:
		return 36
	}
}

// shannonBits returns the measured Shannon entropy of s in bits per char.
func shannonBits(s string) float64 {
	if s == "" {
		return 0
	}
	counts := make(map[rune]int)
	total := 0
	for _, r := range s {
		counts[r]++
		total++
	}
	var h float64
	for _, c := range counts {
		p := float64(c) / float64(total)
		h -= p * math.Log2(p)
	}
	return h
}
