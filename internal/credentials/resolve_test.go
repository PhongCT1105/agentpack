package credentials

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/PhongCT1105/agentpack/internal/packio"
)

// Every secret-shaped value below is a seeded fake per docs/security.md: it
// embeds the string FAKE and was never real. .gitleaks.toml allowlists exactly
// that marker, so a secret-shaped literal here without it would block a commit.
const (
	envSecret      = "ghp_FAKEfromenvFAKEfromenvFAKE001"
	keychainSecret = "ghp_FAKEfromkeychainFAKEkeychain2"
	promptSecret   = "ghp_FAKEfrompromptFAKEfromprompt3"
)

// --- test doubles ------------------------------------------------------
//
// The resolver's three sources are stubbed here rather than exercised for
// real: a test must never read the machine's keychain or open a terminal.

// stubPrompter records what it was asked and returns canned answers.
type stubPrompter struct {
	secret     string
	secretErr  error
	confirm    bool
	confirmErr error

	prompts  []Prompt
	confirms []string
}

func (p *stubPrompter) Secret(pr Prompt) (Value, error) {
	p.prompts = append(p.prompts, pr)
	if p.secretErr != nil {
		return Value{}, p.secretErr
	}
	return NewValue(p.secret), nil
}

func (p *stubPrompter) Confirm(q string) (bool, error) {
	p.confirms = append(p.confirms, q)
	return p.confirm, p.confirmErr
}

// failingKeychain models a store that exists but cannot be used (locked,
// access denied) — distinct from one that simply holds no entry.
type failingKeychain struct{ err error }

func (failingKeychain) Name() string { return "a failing keychain" }

func (k failingKeychain) Get(string, string) (Value, error) { return Value{}, k.err }
func (k failingKeychain) Set(string, string, Value) error   { return k.err }
func (k failingKeychain) Delete(string, string) error       { return k.err }

func envOf(pairs map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := pairs[name]
		return v, ok
	}
}

func githubCred() packio.Credential {
	return packio.Credential{
		Env:         "GITHUB_TOKEN",
		Description: "GitHub personal access token (repo scope)",
		ObtainURL:   "https://github.com/settings/tokens",
	}
}

func githubReq() packio.CredentialRequirement {
	return packio.CredentialRequirement{Server: "github", Credential: githubCred()}
}

// --- resolution order --------------------------------------------------

func TestResolveSourceOrder(t *testing.T) {
	tests := []struct {
		name string
		// env is the process environment the resolver sees.
		env map[string]string
		// keychain is the entry the store holds, keyed by account; empty
		// means the store is reachable but has nothing.
		keychain map[string]string
		// prompter, when true, makes an interactive prompt available.
		prompter bool

		wantSource  Source
		wantValue   string
		wantMissing bool
	}{
		{
			name:       "env wins over keychain and prompt",
			env:        map[string]string{"GITHUB_TOKEN": envSecret},
			keychain:   map[string]string{"github/GITHUB_TOKEN": keychainSecret},
			prompter:   true,
			wantSource: SourceEnv,
			wantValue:  envSecret,
		},
		{
			name:       "keychain wins over prompt when env is unset",
			keychain:   map[string]string{"github/GITHUB_TOKEN": keychainSecret},
			prompter:   true,
			wantSource: SourceKeychain,
			wantValue:  keychainSecret,
		},
		{
			name:       "prompt is the last resort",
			prompter:   true,
			wantSource: SourcePrompt,
			wantValue:  promptSecret,
		},
		{
			name:       "an exported-but-blank env var falls through to the keychain",
			env:        map[string]string{"GITHUB_TOKEN": ""},
			keychain:   map[string]string{"github/GITHUB_TOKEN": keychainSecret},
			prompter:   true,
			wantSource: SourceKeychain,
			wantValue:  keychainSecret,
		},
		{
			name:       "a whitespace-only env var falls through to the prompt",
			env:        map[string]string{"GITHUB_TOKEN": "   \t "},
			prompter:   true,
			wantSource: SourcePrompt,
			wantValue:  promptSecret,
		},
		{
			name:       "an unrelated env var does not satisfy the credential",
			env:        map[string]string{"GITLAB_TOKEN": envSecret},
			prompter:   true,
			wantSource: SourcePrompt,
			wantValue:  promptSecret,
		},
		{
			name:       "a keychain entry for another server is not reused",
			keychain:   map[string]string{"gitlab/GITHUB_TOKEN": keychainSecret},
			prompter:   true,
			wantSource: SourcePrompt,
			wantValue:  promptSecret,
		},
		{
			name:        "no source at all is a missing credential",
			wantMissing: true,
		},
		{
			name:        "a keychain miss with no prompter is a missing credential",
			keychain:    map[string]string{"other/OTHER_TOKEN": keychainSecret},
			wantMissing: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kc := NewMemoryKeychain()
			for account, secret := range tt.keychain {
				if err := kc.Set(DefaultService, account, NewValue(secret)); err != nil {
					t.Fatalf("seeding keychain: %v", err)
				}
			}
			r := &Resolver{
				LookupEnv:       envOf(tt.env),
				Keychain:        kc,
				NoKeychainStore: true, // storing is covered separately
			}
			if tt.prompter {
				r.Prompter = &stubPrompter{secret: promptSecret}
			}

			res, err := r.ResolveRequirement(githubReq())
			if tt.wantMissing {
				var missing *MissingError
				if !errors.As(err, &missing) {
					t.Fatalf("ResolveRequirement() error = %v, want *MissingError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRequirement() error = %v", err)
			}
			if res.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", res.Source, tt.wantSource)
			}
			if got := res.Value.Expose(); got != tt.wantValue {
				t.Errorf("resolved value came from the wrong source (got the %s value)", sourceOfSecret(got))
			}
			if res.Name != "GITHUB_TOKEN" || res.Header {
				t.Errorf("Name/Header = %q/%v, want GITHUB_TOKEN/false", res.Name, res.Header)
			}
			if res.Server != "github" {
				t.Errorf("Server = %q, want github", res.Server)
			}
		})
	}
}

func TestResolvePromptCarriesWhatToGetAndWhereFrom(t *testing.T) {
	// The prompt is the user's only chance to learn what is being asked for,
	// so the manifest's description and obtain URL must reach it.
	prompter := &stubPrompter{secret: promptSecret}
	r := &Resolver{LookupEnv: envOf(nil), Prompter: prompter, NoKeychainStore: true}

	if _, err := r.ResolveRequirement(githubReq()); err != nil {
		t.Fatalf("ResolveRequirement() error = %v", err)
	}
	if len(prompter.prompts) != 1 {
		t.Fatalf("prompted %d times, want 1", len(prompter.prompts))
	}
	got := prompter.prompts[0]
	want := Prompt{
		Name:        "GITHUB_TOKEN",
		Server:      "github",
		Description: "GitHub personal access token (repo scope)",
		ObtainURL:   "https://github.com/settings/tokens",
	}
	if got != want {
		t.Errorf("Prompt = %+v, want %+v", got, want)
	}
}

func TestResolvePrompterFailureIsReported(t *testing.T) {
	// A prompter that broke for a reason other than "no terminal" is a real
	// failure and must not be reported as a missing credential.
	boom := errors.New("terminal went away")
	r := &Resolver{LookupEnv: envOf(nil), Prompter: &stubPrompter{secretErr: boom}}

	_, err := r.ResolveRequirement(githubReq())
	if !errors.Is(err, boom) {
		t.Fatalf("ResolveRequirement() error = %v, want it to wrap the prompter failure", err)
	}
	if errors.As(err, new(*MissingError)) {
		t.Error("a broken prompter was reported as a missing credential")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("the error should name the credential:\n%s", err)
	}
}

// sourceOfSecret names which seeded fake a value is, so a failure message can
// say what went wrong without printing a credential-shaped string.
func sourceOfSecret(v string) string {
	switch v {
	case envSecret:
		return "env"
	case keychainSecret:
		return "keychain"
	case promptSecret:
		return "prompt"
	case "":
		return "empty"
	}
	return "unknown"
}

func TestResolveHeaderCredentialSkipsEnvironment(t *testing.T) {
	// A header credential names a header, not a variable. Reading an env var
	// of the same name would let an unrelated "Authorization" in the
	// environment be injected as this server's secret.
	req := packio.CredentialRequirement{
		Server: "supabase",
		Credential: packio.Credential{
			Header:      "Authorization",
			Format:      "Bearer {value}",
			Description: "Supabase access token",
			ObtainURL:   "https://supabase.com/dashboard/account/tokens",
		},
	}
	r := &Resolver{
		LookupEnv:       envOf(map[string]string{"Authorization": envSecret}),
		Keychain:        NewMemoryKeychain(),
		Prompter:        &stubPrompter{secret: promptSecret},
		NoKeychainStore: true,
	}

	res, err := r.ResolveRequirement(req)
	if err != nil {
		t.Fatalf("ResolveRequirement() error = %v", err)
	}
	if res.Source != SourcePrompt {
		t.Errorf("Source = %q, want %q (an env var must not satisfy a header credential)", res.Source, SourcePrompt)
	}
	if !res.Header {
		t.Error("Header = false, want true")
	}
	if got, want := res.Injected().Expose(), "Bearer "+promptSecret; got != want {
		t.Errorf("Injected() did not apply the %q format", req.Credential.Format)
	}
}

func TestResolveKeychainAccountIsScopedByServer(t *testing.T) {
	// Two servers both injecting "Authorization" are not the same secret.
	kc := NewMemoryKeychain()
	if err := kc.Set(DefaultService, "supabase/Authorization", NewValue(keychainSecret)); err != nil {
		t.Fatalf("seeding keychain: %v", err)
	}
	cred := packio.Credential{Header: "Authorization"}

	r := &Resolver{Keychain: kc, NoKeychainStore: true}

	if _, err := r.ResolveRequirement(packio.CredentialRequirement{Server: "supabase", Credential: cred}); err != nil {
		t.Fatalf("the scoped server should have found its entry: %v", err)
	}
	if _, err := r.ResolveRequirement(packio.CredentialRequirement{Server: "linear", Credential: cred}); err == nil {
		t.Error("a different server reused supabase's stored Authorization secret")
	}
}

func TestResolveKeychainErrorIsNotFatalWhenPromptIsAvailable(t *testing.T) {
	// A locked or broken keychain must not abort a restore that can still ask.
	r := &Resolver{
		Keychain:        failingKeychain{err: errors.New("keychain is locked")},
		Prompter:        &stubPrompter{secret: promptSecret},
		NoKeychainStore: true,
		LookupEnv:       envOf(nil),
	}
	res, err := r.ResolveRequirement(githubReq())
	if err != nil {
		t.Fatalf("ResolveRequirement() error = %v, want a prompt fallback", err)
	}
	if res.Source != SourcePrompt {
		t.Errorf("Source = %q, want %q", res.Source, SourcePrompt)
	}
}

func TestResolveZeroResolverNeverTouchesTheRealKeychain(t *testing.T) {
	// The default keychain is Unavailable(), so a Resolver that forgot to
	// choose a store cannot read or write the user's real one.
	r := &Resolver{LookupEnv: envOf(nil)}
	if got := r.keychain(); got != Unavailable() {
		t.Fatalf("default keychain = %T, want the unavailable one", got)
	}
	if _, err := r.ResolveRequirement(githubReq()); !errors.As(err, new(*MissingError)) {
		t.Errorf("ResolveRequirement() error = %v, want *MissingError", err)
	}
}

// --- the missing-credential error --------------------------------------

func TestResolveMissingCredentialErrorIsActionable(t *testing.T) {
	r := &Resolver{LookupEnv: envOf(nil), Keychain: NewMemoryKeychain()}

	_, err := r.ResolveRequirement(githubReq())
	var missing *MissingError
	if !errors.As(err, &missing) {
		t.Fatalf("ResolveRequirement() error = %v, want *MissingError", err)
	}
	if missing.Reason != ReasonNonInteractive {
		t.Errorf("Reason = %q, want %q", missing.Reason, ReasonNonInteractive)
	}

	msg := err.Error()
	// The message alone must be enough to act on: which credential, for
	// which server, what it is, how to supply it, and where to get one.
	for _, want := range []string{
		"GITHUB_TOKEN",
		"environment variable",
		"github",
		"GitHub personal access token (repo scope)",
		"https://github.com/settings/tokens",
		"rerun restore interactively",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message is missing %q:\n%s", want, msg)
		}
	}
}

func TestResolveMissingCredentialAfterEmptyPrompt(t *testing.T) {
	r := &Resolver{
		LookupEnv: envOf(nil),
		Keychain:  NewMemoryKeychain(),
		Prompter:  &stubPrompter{secret: "   "}, // user pressed enter
	}
	_, err := r.ResolveRequirement(githubReq())
	var missing *MissingError
	if !errors.As(err, &missing) {
		t.Fatalf("ResolveRequirement() error = %v, want *MissingError", err)
	}
	if missing.Reason != ReasonEmptyPrompt {
		t.Errorf("Reason = %q, want %q", missing.Reason, ReasonEmptyPrompt)
	}
	if !strings.Contains(err.Error(), "https://github.com/settings/tokens") {
		t.Errorf("error message should still say where to obtain one:\n%s", err)
	}
}

func TestResolveMissingCredentialReportsABrokenKeychain(t *testing.T) {
	r := &Resolver{
		LookupEnv: envOf(nil),
		Keychain:  failingKeychain{err: errors.New("keychain is locked")},
	}
	_, err := r.ResolveRequirement(githubReq())
	if !strings.Contains(err.Error(), "keychain is locked") {
		t.Errorf("a keychain that failed (rather than missed) should be reported:\n%s", err)
	}

	// An absent or unsupported store is a fact about the machine, not a
	// failure, and must not be reported as one.
	r.Keychain = Unavailable()
	_, err = r.ResolveRequirement(githubReq())
	if strings.Contains(err.Error(), "could not be read") {
		t.Errorf("an unavailable keychain should not be reported as a failure:\n%s", err)
	}
}

func TestInjectionPointRequiresExactlyOne(t *testing.T) {
	tests := []struct {
		name    string
		cred    packio.Credential
		want    string
		header  bool
		wantErr string
	}{
		{name: "env", cred: packio.Credential{Env: "GITHUB_TOKEN"}, want: "GITHUB_TOKEN"},
		{name: "header", cred: packio.Credential{Header: "Authorization"}, want: "Authorization", header: true},
		{name: "neither", cred: packio.Credential{Description: "a secret"}, wantErr: "neither env nor header"},
		{name: "both", cred: packio.Credential{Env: "A", Header: "B"}, wantErr: "exactly one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, header, err := injectionPoint(tt.cred)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("injectionPoint() error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("injectionPoint() error = %v", err)
			}
			if name != tt.want || header != tt.header {
				t.Errorf("injectionPoint() = %q/%v, want %q/%v", name, header, tt.want, tt.header)
			}
		})
	}
}

// --- keychain storage is opt-in ----------------------------------------

func TestKeychainStoreIsOptIn(t *testing.T) {
	tests := []struct {
		name string
		// confirm is the user's answer at the storage offer.
		confirm    bool
		confirmErr error
		// noStore suppresses the offer entirely.
		noStore bool

		wantAsked  bool
		wantStored bool
	}{
		{name: "declined by default", confirm: false, wantAsked: true},
		{name: "stored only on an explicit yes", confirm: true, wantAsked: true, wantStored: true},
		{name: "an unreadable answer declines", confirmErr: errors.New("EOF"), wantAsked: true},
		{name: "suppressed offer never asks", noStore: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kc := NewMemoryKeychain()
			prompter := &stubPrompter{secret: promptSecret, confirm: tt.confirm, confirmErr: tt.confirmErr}
			r := &Resolver{
				LookupEnv:       envOf(nil),
				Keychain:        kc,
				Prompter:        prompter,
				NoKeychainStore: tt.noStore,
			}

			res, err := r.ResolveRequirement(githubReq())
			if err != nil {
				t.Fatalf("ResolveRequirement() error = %v", err)
			}
			if asked := len(prompter.confirms) == 1; asked != tt.wantAsked {
				t.Errorf("storage offer made = %v, want %v", asked, tt.wantAsked)
			}
			if res.Stored != tt.wantStored {
				t.Errorf("Stored = %v, want %v", res.Stored, tt.wantStored)
			}
			if got := kc.Len() > 0; got != tt.wantStored {
				t.Errorf("keychain holds an entry = %v, want %v", got, tt.wantStored)
			}
			if tt.wantAsked && len(prompter.confirms) == 1 {
				// The offer must name what is being stored and where.
				if q := prompter.confirms[0]; !strings.Contains(q, "GITHUB_TOKEN") || !strings.Contains(q, kc.Name()) {
					t.Errorf("storage offer = %q, want it to name the credential and the store", q)
				}
			}
		})
	}
}

func TestKeychainStoreOnlyOffersAfterAPrompt(t *testing.T) {
	// A value that came from the environment or the keychain is not the
	// user's to be asked about storing: env is transient by choice, and a
	// keychain hit is already stored.
	kc := NewMemoryKeychain()
	prompter := &stubPrompter{confirm: true}
	r := &Resolver{
		LookupEnv: envOf(map[string]string{"GITHUB_TOKEN": envSecret}),
		Keychain:  kc,
		Prompter:  prompter,
	}
	if _, err := r.ResolveRequirement(githubReq()); err != nil {
		t.Fatalf("ResolveRequirement() error = %v", err)
	}
	if len(prompter.confirms) != 0 {
		t.Errorf("storage was offered for an env-resolved credential: %q", prompter.confirms)
	}
	if kc.Len() != 0 {
		t.Error("an env-resolved credential was written to the keychain")
	}
}

func TestKeychainStoreNotOfferedWhenTheStoreIsUnusable(t *testing.T) {
	prompter := &stubPrompter{secret: promptSecret, confirm: true}
	r := &Resolver{LookupEnv: envOf(nil), Keychain: Unavailable(), Prompter: prompter}

	res, err := r.ResolveRequirement(githubReq())
	if err != nil {
		t.Fatalf("ResolveRequirement() error = %v", err)
	}
	if len(prompter.confirms) != 0 {
		t.Errorf("storage was offered with no usable store: %q", prompter.confirms)
	}
	if res.Stored {
		t.Error("Stored = true with no usable store")
	}
}

func TestKeychainStoreFailureIsNotFatal(t *testing.T) {
	// The value resolved; only remembering it failed. Losing the restore over
	// that would be worse than prompting again next time.
	prompter := &stubPrompter{secret: promptSecret, confirm: true}
	r := &Resolver{
		LookupEnv: envOf(nil),
		Keychain:  &setFailingKeychain{MemoryKeychain: NewMemoryKeychain(), err: errors.New("write denied")},
		Prompter:  prompter,
	}
	res, err := r.ResolveRequirement(githubReq())
	if err != nil {
		t.Fatalf("ResolveRequirement() error = %v, want success with StoreErr set", err)
	}
	if res.Value.Expose() != promptSecret {
		t.Error("the prompted value was lost when storing failed")
	}
	if res.Stored {
		t.Error("Stored = true after a failed write")
	}
	if res.StoreErr == nil {
		t.Error("StoreErr = nil, want the write failure reported")
	}
}

// setFailingKeychain reads fine but cannot be written to.
type setFailingKeychain struct {
	*MemoryKeychain
	err error
}

func (k *setFailingKeychain) Set(string, string, Value) error { return k.err }

func TestResolveAllStopsAtTheFirstFailure(t *testing.T) {
	reqs := []packio.CredentialRequirement{
		{Server: "github", Credential: packio.Credential{Env: "GITHUB_TOKEN"}},
		{Server: "linear", Credential: packio.Credential{Env: "LINEAR_TOKEN"}},
	}
	r := &Resolver{LookupEnv: envOf(map[string]string{"GITHUB_TOKEN": envSecret})}

	if _, err := r.ResolveAll(reqs); !errors.As(err, new(*MissingError)) {
		t.Fatalf("ResolveAll() error = %v, want *MissingError for LINEAR_TOKEN", err)
	}

	r.LookupEnv = envOf(map[string]string{"GITHUB_TOKEN": envSecret, "LINEAR_TOKEN": envSecret})
	got, err := r.ResolveAll(reqs)
	if err != nil {
		t.Fatalf("ResolveAll() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "GITHUB_TOKEN" || got[1].Name != "LINEAR_TOKEN" {
		t.Errorf("ResolveAll() = %v, want both credentials in manifest order", got)
	}
}

// --- keychain implementations ------------------------------------------

func TestMemoryKeychainRoundTrip(t *testing.T) {
	kc := NewMemoryKeychain()

	if _, err := kc.Get(DefaultService, "github/GITHUB_TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get on an empty store = %v, want ErrNotFound", err)
	}
	if err := kc.Set(DefaultService, "github/GITHUB_TOKEN", NewValue(keychainSecret)); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, err := kc.Get(DefaultService, "github/GITHUB_TOKEN")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Expose() != keychainSecret {
		t.Error("Get() returned a different value than Set() stored")
	}
	// Entries are scoped by service as well as account.
	if _, err := kc.Get("other-app", "github/GITHUB_TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() under another service = %v, want ErrNotFound", err)
	}
	if err := kc.Delete(DefaultService, "github/GITHUB_TOKEN"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := kc.Delete(DefaultService, "github/GITHUB_TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete() = %v, want ErrNotFound", err)
	}
}

func TestUnavailableKeychainReportsItself(t *testing.T) {
	kc := Unavailable()
	if _, err := kc.Get(DefaultService, "a"); !errors.Is(err, ErrKeychainUnavailable) {
		t.Errorf("Get() = %v, want ErrKeychainUnavailable", err)
	}
	if err := kc.Set(DefaultService, "a", NewValue(keychainSecret)); !errors.Is(err, ErrKeychainUnavailable) {
		t.Errorf("Set() = %v, want ErrKeychainUnavailable", err)
	}
	if err := kc.Delete(DefaultService, "a"); !errors.Is(err, ErrKeychainUnavailable) {
		t.Errorf("Delete() = %v, want ErrKeychainUnavailable", err)
	}
}

func TestAccountName(t *testing.T) {
	tests := []struct {
		server, name, want string
	}{
		{"github", "GITHUB_TOKEN", "github/GITHUB_TOKEN"},
		{"", "GITHUB_TOKEN", "GITHUB_TOKEN"},
		{"supabase", "Authorization", "supabase/Authorization"},
	}
	for _, tt := range tests {
		if got := AccountName(tt.server, tt.name); got != tt.want {
			t.Errorf("AccountName(%q, %q) = %q, want %q", tt.server, tt.name, got, tt.want)
		}
	}
}

// --- the terminal prompter ---------------------------------------------

func TestTerminalPrompterShowsWhatToGetAndWhereFromWithoutEchoing(t *testing.T) {
	var out bytes.Buffer
	p := &TerminalPrompter{
		Out:        &out,
		ReadSecret: func() (string, error) { return promptSecret + "\n", nil },
	}

	v, err := p.Secret(Prompt{
		Name:        "GITHUB_TOKEN",
		Server:      "github",
		Description: "GitHub personal access token (repo scope)",
		ObtainURL:   "https://github.com/settings/tokens",
	})
	if err != nil {
		t.Fatalf("Secret() error = %v", err)
	}
	if v.Expose() != promptSecret {
		t.Error("Secret() did not return the entered value (trailing newline should be trimmed)")
	}
	for _, want := range []string{
		"GITHUB_TOKEN",
		"github",
		"GitHub personal access token (repo scope)",
		"https://github.com/settings/tokens",
		"input hidden",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt output is missing %q:\n%s", want, out.String())
		}
	}
}

func TestTerminalPrompterConfirmDefaultsToNo(t *testing.T) {
	tests := []struct {
		name string
		line string
		err  error
		want bool
	}{
		{name: "y", line: "y\n", want: true},
		{name: "yes with padding", line: "  YES  \n", want: true},
		{name: "n", line: "n\n"},
		{name: "bare enter", line: "\n"},
		{name: "anything else", line: "sure\n"},
		{name: "read failure", err: errors.New("EOF")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			p := &TerminalPrompter{
				Out:      &out,
				ReadLine: func() (string, error) { return tt.line, tt.err },
			}
			got, err := p.Confirm("Store GITHUB_TOKEN?")
			if (err != nil) != (tt.err != nil) {
				t.Fatalf("Confirm() error = %v, want error presence %v", err, tt.err != nil)
			}
			if got != tt.want {
				t.Errorf("Confirm(%q) = %v, want %v", tt.line, got, tt.want)
			}
			if !strings.Contains(out.String(), "[y/N]") {
				t.Errorf("prompt should show the no default:\n%s", out.String())
			}
		})
	}
}

func TestTerminalPrompterRefusesToEchoOnANonTerminal(t *testing.T) {
	// Falling back to an echoed read would print the token into whatever
	// captured the session, so a non-terminal stdin is a refusal.
	f, err := os.Open(filepath.Join(t.TempDir(), "stdin"))
	if err != nil {
		if f, err = os.Create(filepath.Join(t.TempDir(), "stdin")); err != nil {
			t.Fatalf("creating a non-terminal stdin: %v", err)
		}
	}
	defer f.Close()

	var out bytes.Buffer
	p := &TerminalPrompter{Out: &out, In: f}
	if _, err := p.Secret(Prompt{Name: "GITHUB_TOKEN"}); !errors.Is(err, ErrNotATerminal) {
		t.Fatalf("Secret() error = %v, want ErrNotATerminal", err)
	}

	// And the resolver turns that into the actionable missing-credential
	// error rather than a plumbing failure.
	r := &Resolver{LookupEnv: envOf(nil), Prompter: p}
	_, err = r.ResolveRequirement(githubReq())
	var missing *MissingError
	if !errors.As(err, &missing) {
		t.Fatalf("ResolveRequirement() error = %v, want *MissingError", err)
	}
	if missing.Reason != ReasonNonInteractive {
		t.Errorf("Reason = %q, want %q", missing.Reason, ReasonNonInteractive)
	}
}

// --- RELEASE BLOCKING --------------------------------------------------

// leakyKeychain and leakyPrompter echo the secret back inside their error
// messages — the mistake a third-party or OS-wrapping implementation is most
// likely to make. The resolver must not pass that through.
type leakyKeychain struct {
	*MemoryKeychain
	secret string
}

func (k *leakyKeychain) Set(string, string, Value) error {
	return fmt.Errorf("keychain write failed for value %s", k.secret)
}

// TestReleaseBlocking_ResolvedSecretNeverAppearsInOutput is the guarantee this
// package exists to provide (docs/security.md threat 4): a resolved secret may
// reach only the local tool config or the OS keychain, and nothing else —
// never a log line, an error message, a formatted value, or a serializer.
//
// It is release-blocking. If it fails, the redaction contract is broken
// somewhere and the build must not ship: every failure mode it covers is a
// silent one, visible only after a user's token has already been written into
// a terminal transcript, a CI log, or a file agentpack authored.
//
// The test drives a real resolution whose value comes from a prompt, then
// asserts the secret's plaintext appears in exactly one place — Expose — and
// in none of the outputs the rest of the program can reach.
func TestReleaseBlocking_ResolvedSecretNeverAppearsInOutput(t *testing.T) {
	const secret = "ghp_FAKEreleaseblockingFAKEsecret9" // seeded fake; never real

	// Sanity: the checks below are only meaningful if the secret really is
	// the value that was resolved.
	kc := &leakyKeychain{MemoryKeychain: NewMemoryKeychain(), secret: secret}
	r := &Resolver{
		LookupEnv: envOf(nil),
		Keychain:  kc,
		Prompter:  &stubPrompter{secret: secret, confirm: true},
	}
	res, err := r.ResolveRequirement(githubReq())
	if err != nil {
		t.Fatalf("ResolveRequirement() error = %v", err)
	}
	if res.Value.Expose() != secret {
		t.Fatalf("the resolver did not return the seeded secret; the rest of this test proves nothing")
	}

	// A struct with an exported Value field, and one with an unexported field:
	// fmt reaches the latter only by reflection, which is why Value holds its
	// plaintext behind a pointer.
	type exportedCarrier struct{ V Value }
	type unexportedCarrier struct{ v Value }

	var logged bytes.Buffer
	log.New(&logged, "", 0).Printf("resolved %v (%s) value=%v", res, res.Name, res.Value)

	// Serializing a Resolution must fail. Both the refusal and anything that
	// escaped instead are checked below, so a regression that starts encoding
	// the value is reported as a leak rather than crashing this test.
	jsonOut, jsonErr := json.Marshal(res)
	yamlOut, yamlErr := yaml.Marshal(res)
	if jsonErr == nil {
		t.Error("json.Marshal(Resolution) succeeded; a resolved secret must never be serializable")
	}
	if yamlErr == nil {
		t.Error("yaml.Marshal(Resolution) succeeded; a resolved secret must never be serializable")
	}

	// A store that failed with the secret in its own error message.
	if res.StoreErr == nil {
		t.Fatal("the leaky keychain should have failed the store")
	}

	// And the missing-credential error, produced with no value in hand.
	missingErr := func() string {
		bare := &Resolver{LookupEnv: envOf(map[string]string{"GITHUB_TOKEN": ""}), Keychain: NewMemoryKeychain()}
		_, err := bare.ResolveRequirement(githubReq())
		return err.Error()
	}()

	// The prompt transcript: whatever the user typed must not be echoed back
	// into the terminal output.
	promptOut := func() string {
		var out bytes.Buffer
		p := &TerminalPrompter{
			Out:        &out,
			ReadSecret: func() (string, error) { return secret + "\n", nil },
			ReadLine:   func() (string, error) { return "y\n", nil },
		}
		if _, err := p.Secret(Prompt{Name: "GITHUB_TOKEN", Description: "d", ObtainURL: "https://example.test"}); err != nil {
			t.Fatalf("Secret() error = %v", err)
		}
		if _, err := p.Confirm("Store GITHUB_TOKEN?"); err != nil {
			t.Fatalf("Confirm() error = %v", err)
		}
		return out.String()
	}()

	checks := []struct {
		name string
		got  string
	}{
		// The redacting methods themselves.
		{"Value.String", res.Value.String()},
		{"Value.GoString", res.Value.GoString()},

		// Every fmt verb on the Value, by value and by pointer.
		{"Value %v", fmt.Sprintf("%v", res.Value)},
		{"Value %s", fmt.Sprintf("%s", res.Value)},
		{"Value %q", fmt.Sprintf("%q", res.Value)},
		{"Value %d", fmt.Sprintf("%d", res.Value)},
		{"Value %x", fmt.Sprintf("%x", res.Value)},
		{"Value %#v", fmt.Sprintf("%#v", res.Value)},
		{"Value %+v", fmt.Sprintf("%+v", res.Value)},
		{"*Value %v", fmt.Sprintf("%v", &res.Value)},

		// The formatted-for-injection rendering is still a secret.
		{"Injected %v", fmt.Sprintf("%v", Resolution{Format: "Bearer {value}", Value: res.Value}.Injected())},

		// The Resolution that carries it, printed every way.
		{"Resolution.String", res.String()},
		{"Resolution %v", fmt.Sprintf("%v", res)},
		{"Resolution %s", fmt.Sprintf("%s", res)},
		{"Resolution %+v", fmt.Sprintf("%+v", res)},
		{"Resolution %#v", fmt.Sprintf("%#v", res)},
		{"*Resolution %+v", fmt.Sprintf("%+v", &res)},

		// Arbitrary structs holding a Value, including the reflection-only
		// path through an unexported field.
		{"exported field %+v", fmt.Sprintf("%+v", exportedCarrier{V: res.Value})},
		{"exported field %#v", fmt.Sprintf("%#v", exportedCarrier{V: res.Value})},
		{"unexported field %+v", fmt.Sprintf("%+v", unexportedCarrier{v: res.Value})},
		{"unexported field %#v", fmt.Sprintf("%#v", unexportedCarrier{v: res.Value})},

		// Logs.
		{"log output", logged.String()},

		// Serializers: they must refuse, and neither the refusal nor
		// anything they emitted may carry the value.
		{"json.Marshal error", errText(jsonErr)},
		{"json.Marshal output", string(jsonOut)},
		{"yaml.Marshal error", errText(yamlErr)},
		{"yaml.Marshal output", string(yamlOut)},

		// Errors: the resolver's own, and one a keychain implementation
		// leaked the secret into.
		{"missing-credential error", missingErr},
		{"keychain store error", errText(res.StoreErr)},
		{"keychain store error %v", fmt.Sprintf("%v", res.StoreErr)},

		// The interactive prompt's visible transcript.
		{"prompt transcript", promptOut},
	}

	for _, c := range checks {
		if strings.Contains(c.got, secret) {
			t.Errorf("RELEASE BLOCKING: the resolved secret leaked through %s (docs/security.md threat 4)", c.name)
		}
	}

	// The redaction must actually be visible rather than the value silently
	// vanishing — a caller reading "[redacted]" knows a secret is there.
	if !strings.Contains(fmt.Sprintf("%v", res.Value), Redacted) {
		t.Errorf("Value should render as %q", Redacted)
	}

	// Errors that were scrubbed must still be matchable by the caller.
	if !errors.Is(res.StoreErr, res.StoreErr) {
		t.Error("scrubbing an error broke errors.Is")
	}
}

// errText renders an error for leak-checking without panicking on nil, so a
// regression that makes a refusal succeed still reaches the leak assertions.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestReleaseBlocking_ValueMarshalersRefuse pins the refusal itself: a
// Resolution must not be quietly serializable via any encoder that reaches for
// a marshaler, because nothing agentpack writes — pack, lockfile, plan — may
// contain a resolved credential.
func TestReleaseBlocking_ValueMarshalersRefuse(t *testing.T) {
	v := NewValue("ghp_FAKEmarshalrefusalFAKEsecret1")

	if _, err := v.MarshalJSON(); !errors.Is(err, ErrNotSerializable) {
		t.Errorf("MarshalJSON() = %v, want ErrNotSerializable", err)
	}
	if _, err := v.MarshalText(); !errors.Is(err, ErrNotSerializable) {
		t.Errorf("MarshalText() = %v, want ErrNotSerializable", err)
	}
	if _, err := v.MarshalYAML(); !errors.Is(err, ErrNotSerializable) {
		t.Errorf("MarshalYAML() = %v, want ErrNotSerializable", err)
	}

	// And through the real encoders, on a struct that merely contains one.
	type carrier struct {
		Name  string
		Value Value
	}
	if _, err := json.Marshal(carrier{Name: "GITHUB_TOKEN", Value: v}); err == nil {
		t.Error("json.Marshal on a struct containing a Value succeeded, want a refusal")
	}
	if _, err := yaml.Marshal(carrier{Name: "GITHUB_TOKEN", Value: v}); err == nil {
		t.Error("yaml.Marshal on a struct containing a Value succeeded, want a refusal")
	}
}

func TestValueEmpty(t *testing.T) {
	if !(Value{}).Empty() {
		t.Error("the zero Value should be empty")
	}
	if (Value{}).Expose() != "" {
		t.Error("the zero Value should expose nothing")
	}
	if NewValue("ghp_FAKEnotemptyFAKEnotemptyFAKE1").Empty() {
		t.Error("a Value with a secret should not be empty")
	}
	if !NewValue("").Empty() {
		t.Error("a Value wrapping an empty string should be empty")
	}
	if !NewValue("  \t\n ").Empty() {
		t.Error("a whitespace-only Value should be empty: no real credential is blank")
	}
}
