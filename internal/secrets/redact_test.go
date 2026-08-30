package secrets

import "testing"

// All secret-shaped values below are seeded fakes per docs/security.md:
// they embed the string FAKE (or are meaningless hex) and were never real.

func TestClassifyValueFormats(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  Level
		rule  string
	}{
		// GitHub token family — realistic lengths, obviously fake bodies.
		{"github classic pat", "V", "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE", Secret, "format:github-token"},
		{"github oauth", "V", "gho_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE", Secret, "format:github-token"},
		{"github user-to-server", "V", "ghu_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE", Secret, "format:github-token"},
		{"github server-to-server", "V", "ghs_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE", Secret, "format:github-token"},
		{"github refresh", "V", "ghr_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE", Secret, "format:github-token"},
		{"github fine-grained pat", "V", "github_pat_FAKEFAKEFAKEFAKEFAKE_FAKEFAKEFAKEFAKEFAKE", Secret, "format:github-token"},

		// sk- style (OpenAI, Anthropic, many vendors).
		{"sk dash key", "V", "sk-FAKEFAKEFAKEFAKEFAKEFAKE", Secret, "format:sk-key"},
		{"sk proj key", "V", "sk-proj-FAKEFAKEFAKEFAKEFAKEFAKE", Secret, "format:sk-key"},

		// Stripe live/test keys.
		{"stripe secret live", "V", "sk_live_FAKEFAKEFAKEFAKE", Secret, "format:stripe-key"},
		{"stripe restricted test", "V", "rk_test_FAKEFAKEFAKEFAKE", Secret, "format:stripe-key"},

		// Slack token family.
		{"slack bot token", "V", "xoxb-FAKEFAKEFAKE", Secret, "format:slack-token"},
		{"slack app token", "V", "xoxa-FAKEFAKEFAKE", Secret, "format:slack-token"},
		{"slack user token", "V", "xoxp-FAKEFAKEFAKE", Secret, "format:slack-token"},

		// AWS access key ids.
		{"aws akia", "V", "AKIAFAKEFAKEFAKEFAKE", Secret, "format:aws-access-key"},
		{"aws asia", "V", "ASIAFAKEFAKEFAKEFAKE", Secret, "format:aws-access-key"},

		// GitLab and npm.
		{"gitlab pat", "V", "glpat-FAKEFAKEFAKEFAKE", Secret, "format:gitlab-token"},
		{"npm token", "V", "npm_FAKEFAKEFAKEFAKE", Secret, "format:npm-token"},

		// JWTs. Payload decodes to {"alg":"FAKE"} etc — never a real token.
		{"jwt", "V", "eyJhbGciOiJGQUtFIn0.eyJGQUtFIjoiRkFLRSJ9.FAKEFAKEFAKEFAKE", Secret, "format:jwt"},
		{"jwt unsigned alg none", "V", "eyJhbGciOiJub25lIn0.FAKEFAKEFAKEFAKE.", Secret, "format:jwt"},

		// PEM private key blocks (contains-match: PEMs span lines).
		{"pem rsa", "V", "-----BEGIN RSA PRIVATE KEY-----\nFAKEFAKEFAKE\n-----END RSA PRIVATE KEY-----", Secret, "format:private-key"},
		{"pem openssh", "V", "-----BEGIN OPENSSH PRIVATE KEY-----\nFAKEFAKEFAKE\n-----END OPENSSH PRIVATE KEY-----", Secret, "format:private-key"},

		// Connection strings carrying a password.
		{"postgres url with password", "V", "postgres://app:FAKEpass@db.example.com:5432/app", Secret, "format:url-password"},
		{"https url with password", "V", "https://user:FAKEpass@internal.example.com/api", Secret, "format:url-password"},

		// Near-misses must NOT match the format rules.
		{"ghp too short", "V", "ghp_FAKEFAKE", Plain, "none"},
		{"sk word", "V", "sk-limit", Plain, "none"},
		{"akia wrong charset", "V", "AKIAfakefakefakefake", Plain, "none"},
		{"url without userinfo", "V", "https://db.example.com:5432/app", Plain, "none"},
		{"url with user but no password", "V", "mysql://readonly@db.example.com/app", Plain, "none"},
		{"pem certificate is not a private key", "V", "-----BEGIN CERTIFICATE-----\nFAKEFAKEFAKE\n-----END CERTIFICATE-----", Plain, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.key, tt.value)
			if got.Level != tt.want || got.Rule != tt.rule {
				t.Errorf("Classify(%q, %q) = {%v %q %q}, want level %v rule %q",
					tt.key, tt.value, got.Level, got.Rule, got.Reason, tt.want, tt.rule)
			}
		})
	}
}

func TestClassifyKeyNames(t *testing.T) {
	// Values here are deliberately innocuous: the key name alone must decide.
	tests := []struct {
		name string
		key  string
		want Level
	}{
		{"env token", "GITHUB_TOKEN", Secret},
		{"lower token", "github_token", Secret},
		{"camel token", "apiToken", Secret},
		{"secret", "MY_SECRET", Secret},
		{"camel secret", "clientSecret", Secret},
		{"password", "DB_PASSWORD", Secret},
		{"passwd", "passwd", Secret},
		{"passphrase", "SSH_PASSPHRASE", Secret},
		{"credentials", "AWS_CREDENTIALS", Secret},
		{"authorization header", "Authorization", Secret},
		{"proxy authorization", "Proxy-Authorization", Secret},
		{"header api key", "X-Api-Key", Secret},
		{"env api key", "OPENAI_API_KEY", Secret},
		{"role key", "SERVICE_ROLE_KEY", Secret},
		{"apikey one word", "apikey", Secret},
		{"npm legacy underscore auth", "_auth", Secret},
		{"x-auth header", "X-Auth", Secret},
		// Redaction is favored even for keys that may name non-secrets.
		{"public key still redacts", "PUBLIC_KEY", Secret},

		// Known false positives that must stay Plain.
		{"keybindings", "keybindings", Plain},
		{"keymap", "KEYMAP", Plain},
		{"hotkey", "hotkey", Plain},
		{"keyboard", "keyboard", Plain},
		{"keyword", "KEYWORD", Plain},
		{"monkey", "monkey", Plain},
		{"turkey", "turkey", Plain},
		{"tokenizer path", "TOKENIZER_PATH", Plain},
		{"secretive", "SECRETIVE_MODE", Plain},
		{"api url", "GITHUB_API_URL", Plain},
		{"port", "PORT", Plain},
		{"debug", "DEBUG", Plain},
		{"pwd is working directory", "PWD", Plain},
		{"oauth does not match auth", "OAUTH_CLIENT_ID", Plain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.key, "some-plain-value")
			if got.Level != tt.want {
				t.Errorf("Classify(%q, ...) = {%v %q %q}, want level %v",
					tt.key, got.Level, got.Rule, got.Reason, tt.want)
			}
			if tt.want == Secret && got.Rule != "key-name" {
				t.Errorf("Classify(%q, ...) rule = %q, want %q", tt.key, got.Rule, "key-name")
			}
		})
	}
}

func TestClassifyKeyRuleBeatsPlaceholderValue(t *testing.T) {
	// A secret-named key with an env-expansion placeholder value is still a
	// credential requirement; the key rule must win.
	got := Classify("GITHUB_TOKEN", "${GITHUB_TOKEN}")
	if got.Level != Secret || got.Rule != "key-name" {
		t.Errorf("Classify(GITHUB_TOKEN, ${GITHUB_TOKEN}) = {%v %q}, want Secret via key-name", got.Level, got.Rule)
	}
	// And a secret-named key with an empty value is still secret.
	got = Classify("API_TOKEN", "")
	if got.Level != Secret {
		t.Errorf("Classify(API_TOKEN, \"\") = {%v %q}, want Secret", got.Level, got.Rule)
	}
}

func TestClassifyPlaceholdersAndShortValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  Level
		rule  string
	}{
		{"braced placeholder", "SOME_REF", "${HOME}", Plain, "placeholder"},
		{"bare placeholder", "X", "$HOME", Plain, "placeholder"},
		{"windows placeholder", "Y", "%USERPROFILE%", Plain, "placeholder"},
		{"empty value", "NAME", "", Plain, "none"},
		{"boolean", "DEBUG_MODE_ON", "true", Plain, "none"},
		{"number", "TIMEOUT_MS", "30000", Plain, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.key, tt.value)
			if got.Level != tt.want || got.Rule != tt.rule {
				t.Errorf("Classify(%q, %q) = {%v %q %q}, want level %v rule %q",
					tt.key, tt.value, got.Level, got.Rule, got.Reason, tt.want, tt.rule)
			}
		})
	}
}

func TestClassifyEntropy(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  Level
		rule  string
	}{
		// Long random mixed-case alphanumerics: unambiguous secrets.
		{"random 32 alnum", "V", "FAKEq7PzX2mK9vLtR4wYbN8cJ5hD3fG6", Secret, "entropy:high"},
		{"random base64 with symbols", "V", "FAKEwJx7+Qm2Lp9ZrTv4/Kd8HnBs3YcG", Secret, "entropy:high"},
		{"random token inside prose", "NOTES", "token is FAKEq7PzX2mK9vLtR4wYbN8cJ5hD3fG6 ok", Secret, "entropy:high"},

		// Shorter random runs land in the uncertain band, not secret.
		{"random 20 lower-alnum", "V", "FAKE0q7pz2mk9vlt4wyb", Uncertain, "entropy:mid"},

		// Hex is indistinguishable from digests below 48 chars.
		{"hex 40 like a git sha", "COMMIT", "0f1e2d3c4b5a69788796a5b4c3d2e1f00fedcba9", Uncertain, "entropy:mid"},
		{"hex 64", "V", "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c5b4a39281706f5e4d3c2b1a0", Secret, "entropy:high"},

		// Natural language and structured names must stay plain.
		{"english word run", "V", "installationdirectory", Plain, "none"},
		{"model id", "MODEL", "claude-3-5-sonnet-20241022", Plain, "none"},
		{"prose", "DESCRIPTION", "runs the FAKE test suite before committing", Plain, "none"},
		{"repeated filler", "V", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Plain, "none"},
		{"digits only", "BUILD_ID", "20240830123456789012", Plain, "none"},

		// Rejection side of the thresholds: runs that pass the length and
		// digit/letter gates but measure low entropy must stay plain.
		{"low entropy mixed 24", "V", "xyz123xyz123xyz123xyz123", Plain, "none"},
		{"low entropy hex 32", "V", "abcabcabc111abcabcabc111abcabcab", Plain, "none"},

		// Paths are exempt from entropy — unless they carry a token-grade
		// component, which demotes the exemption to uncertain.
		{"unix path", "SERVER_BIN", "/home/user/tools/bin/mcp-server", Plain, "path"},
		{"home path", "DATA_DIR", "~/.local/share/app", Plain, "path"},
		{"windows path", "WIN_BIN", `C:\Tools\mcp\server.exe`, Plain, "path"},
		{"slash-prefixed base64 secret", "V", "/FAKEwJx7+Qm2Lp9ZrTv4Kd8HnBs3YcG", Uncertain, "path-entropy"},
		{"store path with content hash", "CACHE", "/store/FAKEq7PzX2mK9vLtR4wYbN8cJ5hD3fG6/bin", Uncertain, "path-entropy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.key, tt.value)
			if got.Level != tt.want || got.Rule != tt.rule {
				t.Errorf("Classify(%q, %q) = {%v %q %q}, want level %v rule %q",
					tt.key, tt.value, got.Level, got.Rule, got.Reason, tt.want, tt.rule)
			}
		})
	}
}

func TestClassifyURLs(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  Level
		rule  string
	}{
		// The SUPABASE_URL problem: a URL embedding a high-entropy project
		// ref is uncertain — never silently kept, never silently dropped.
		{"supabase-style url", "SUPABASE_URL", "https://FAKE0q7pz2mk9vlt4wyb.supabase.co", Uncertain, "url-entropy"},
		{"url with long random path segment", "WEBHOOK", "https://hooks.example.com/x/FAKEq7PzX2mK9vLtR4wYbN8cJ5hD3fG6", Uncertain, "url-entropy"},

		// Boring URLs stay plain.
		{"plain api url", "API", "https://api.github.com", Plain, "none"},
		{"plain url with port and path", "DB", "https://db.example.com:5432/app", Plain, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.key, tt.value)
			if got.Level != tt.want || got.Rule != tt.rule {
				t.Errorf("Classify(%q, %q) = {%v %q %q}, want level %v rule %q",
					tt.key, tt.value, got.Level, got.Rule, got.Reason, tt.want, tt.rule)
			}
		})
	}
}

func TestIsPlaceholder(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"${GITHUB_TOKEN}", true},
		{"$GITHUB_TOKEN", true},
		{"%USERPROFILE%", true},
		{"${1BAD}", false},
		{"$", false},
		{"literal", false},
		{"prefix-${VAR}", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsPlaceholder(tt.value); got != tt.want {
			t.Errorf("IsPlaceholder(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestLevelString(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{Plain, "plain"},
		{Uncertain, "uncertain"},
		{Secret, "secret"},
		{Level(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("Level(%d).String() = %q, want %q", int(tt.level), got, tt.want)
		}
	}
}

func TestVerdictsAlwaysExplain(t *testing.T) {
	// Every non-plain verdict must carry a reason the save UI can show.
	inputs := []struct{ key, value string }{
		{"GITHUB_TOKEN", "x"},
		{"V", "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE"},
		{"V", "FAKEq7PzX2mK9vLtR4wYbN8cJ5hD3fG6"},
		{"SUPABASE_URL", "https://FAKE0q7pz2mk9vlt4wyb.supabase.co"},
	}
	for _, in := range inputs {
		got := Classify(in.key, in.value)
		if got.Level == Plain {
			t.Errorf("Classify(%q, %q) = Plain, expected a non-plain fixture", in.key, in.value)
			continue
		}
		if got.Reason == "" || got.Rule == "" {
			t.Errorf("Classify(%q, %q) = {%v %q %q}: non-plain verdicts need Rule and Reason", in.key, in.value, got.Level, got.Rule, got.Reason)
		}
	}
}
