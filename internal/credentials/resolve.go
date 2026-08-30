// Package credentials is the restore half of the secrets layer
// (docs/architecture.md "Secrets layer"). Where internal/secrets keeps a
// publisher's secrets *out* of a pack, this package collects an installer's
// secrets back *in*: for every credential a manifest declares, it resolves a
// value in the order existing environment variable → OS keychain →
// interactive prompt, then offers to remember a prompted value so the next
// restore on that machine is non-interactive.
//
// The whole package exists to make one invariant hard to break
// (docs/security.md threat 4): a resolved secret may be written only to the
// local tool config or the OS keychain. It must never reach the pack, the
// lockfile, a log line, an error message, or telemetry (there is none).
// That is enforced by construction rather than by discipline — resolved
// values travel as Value, whose String/GoString/Format redact and whose
// marshalers refuse outright, so a secret cannot be printed or serialized by
// accident. Expose is the single, deliberately greppable call that turns a
// Value back into plaintext; every call site of it should be a write into
// tool config or the keychain and nothing else.
//
// Every source is an injectable seam (LookupEnv, Keychain, Prompter) for two
// reasons: tests must never touch the machine's real keychain or a real
// terminal, and a non-interactive restore (CI, a piped run) is expressed by
// simply leaving Prompter nil rather than by a mode flag threaded everywhere.
package credentials

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/packio"
)

// Redacted is what a Value renders as anywhere it is printed. It carries no
// length information on purpose: the length of a secret is itself a hint, and
// terminal output gets pasted into issues and screen recordings.
const Redacted = "[redacted]"

// ErrNotSerializable is returned by every marshaler on Value. Refusing is the
// point: a struct that reaches a serializer with a resolved secret in it is a
// bug (nothing agentpack writes — pack, lockfile, plan — may contain one), and
// a loud failure is the only outcome that gets it fixed. Rendering
// "[redacted]" instead would let the bug ship quietly.
var ErrNotSerializable = errors.New("a resolved credential value must never be serialized (docs/security.md threat 4)")

// Value carries a resolved secret.
//
// The plaintext lives behind a pointer rather than in a string field for a
// specific reason: fmt cannot call String/Format on a value it reaches through
// an *unexported* struct field, and falls back to printing that field's
// contents by reflection. A string field would print the secret there; a
// pointer prints an address. It is cheap hardening against the one printing
// path the redacting methods below cannot intercept.
type Value struct {
	secret *string
}

// NewValue wraps a plaintext secret. Callers that have just read a secret
// from somewhere should wrap it immediately and pass the Value onward, so the
// plaintext exists as a bare string for as few lines as possible.
func NewValue(secret string) Value {
	return Value{secret: &secret}
}

// Expose returns the plaintext secret.
//
// This is the only way to read it, and deliberately the only symbol in the
// package worth grepping for in a security review: every call site must be a
// write into local tool config or the OS keychain, per docs/security.md
// threat 4. If a new call site is anything else — a log, an error, a file
// agentpack authors — the invariant is broken.
func (v Value) Expose() string {
	if v.secret == nil {
		return ""
	}
	return *v.secret
}

// Empty reports whether the Value holds nothing usable — including a value
// that is only whitespace, since no real credential is blank and a Prompter
// implementation is not required to trim what it read.
//
// Resolution treats an empty value as "not resolved here", so an
// exported-but-blank env var or an enter-pressed prompt falls through to the
// next source instead of injecting nothing and failing much later, inside the
// target tool, with an error that points nowhere.
func (v Value) Empty() bool { return strings.TrimSpace(v.Expose()) == "" }

// String redacts. It exists so a Value satisfies fmt.Stringer for the many
// places that take one.
func (v Value) String() string { return Redacted }

// GoString redacts %#v, which otherwise prints struct internals.
func (v Value) GoString() string { return "credentials.Value(" + Redacted + ")" }

// Format redacts *every* fmt verb, not just the ones a Stringer covers.
// Implementing Formatter rather than only Stringer is what stops %x, %d and
// %#v — verbs fmt would otherwise satisfy by reaching into the struct — from
// printing anything real.
func (v Value) Format(f fmt.State, verb rune) {
	switch verb {
	case 'q':
		io.WriteString(f, strconv.Quote(Redacted))
	case 'v':
		if f.Flag('#') {
			io.WriteString(f, v.GoString())
			return
		}
		io.WriteString(f, Redacted)
	default:
		io.WriteString(f, Redacted)
	}
}

// MarshalJSON refuses. See ErrNotSerializable.
func (v Value) MarshalJSON() ([]byte, error) { return nil, ErrNotSerializable }

// MarshalText refuses, which also covers encoders that reach for TextMarshaler
// (map keys, flag values, TOML).
func (v Value) MarshalText() ([]byte, error) { return nil, ErrNotSerializable }

// MarshalYAML refuses. Packs are YAML, so this is the marshaler most likely to
// be reached by a mistake that matters.
func (v Value) MarshalYAML() (any, error) { return nil, ErrNotSerializable }

// Source names where a resolved value came from. It is safe to print — it
// describes provenance, never content — and restore shows it so the user can
// see which credentials came from the machine and which they typed.
type Source string

const (
	// SourceEnv is an existing environment variable.
	SourceEnv Source = "environment"
	// SourceKeychain is the OS secret store.
	SourceKeychain Source = "keychain"
	// SourcePrompt is the user, typed at a terminal with echo disabled.
	SourcePrompt Source = "prompt"
)

// Resolution is one resolved credential: where the value gets injected, the
// value itself, and where it came from.
//
// The value field is a Value and not a string precisely so that a caller who
// dumps a Resolution into a log line gets "[redacted]" for free rather than a
// leak. Every other field is non-secret and safe to print.
type Resolution struct {
	// Name is the injection point: an env var name, or a header name.
	Name string
	// Header is true when Name names a header rather than an env var.
	Header bool
	// Server is the MCP server that needs the credential, when known.
	Server string
	// Format is the credential's rendering template ("Bearer {value}"),
	// copied from the manifest. Non-secret: it is a shape, not a value.
	Format string
	// Value is the secret.
	Value Value
	// Source is where Value came from.
	Source Source
	// Stored reports whether the value was written to the OS keychain during
	// this resolution. It is only ever true after an explicit yes at the
	// storage prompt — see Resolver.offerStore.
	Stored bool
	// StoreErr is a non-fatal failure to store an accepted value. Resolution
	// still succeeded; the caller should warn that the next restore will
	// prompt again.
	StoreErr error
}

// Injected renders the value the way the target consumes it: a header
// credential whose format is "Bearer {value}" injects as "Bearer <secret>".
// The result is a Value, so the rendered form redacts exactly like the raw
// one — a formatted secret is still a secret.
//
// Note that what gets stored in the keychain is always the raw value, never
// this rendering: the format belongs to the pack, the secret belongs to the
// user, and mixing them would make a stored entry useless if the pack changes
// its header shape.
func (r Resolution) Injected() Value {
	if r.Format == "" {
		return r.Value
	}
	return NewValue(strings.ReplaceAll(r.Format, "{value}", r.Value.Expose()))
}

// Kind describes the injection point in words, for user-facing text.
func (r Resolution) Kind() string {
	if r.Header {
		return "header"
	}
	return "environment variable"
}

// String summarizes the resolution without its value, so a Resolution is safe
// to print directly.
func (r Resolution) String() string {
	s := fmt.Sprintf("%s (%s) from %s", r.Name, r.Kind(), r.Source)
	if r.Server != "" {
		s += " for MCP server " + r.Server
	}
	if r.Stored {
		s += ", stored in keychain"
	}
	return s
}

// MissingError says a credential could not be resolved from any source.
//
// Its message is the entire user-facing remedy — what is missing, what it is
// for, how to supply it, and where to obtain one — because this is the error
// that stops a restore, and someone staring at it should not have to open the
// pack manifest to know what to do next. By construction it carries no value:
// there was nothing to resolve.
type MissingError struct {
	// Name is the injection point that went unfilled.
	Name string
	// Header is true when Name names a header rather than an env var.
	Header bool
	// Server is the MCP server that needs it, when known.
	Server string
	// Description and ObtainURL are copied from the manifest credential so
	// the message can tell the user what to get and where.
	Description string
	ObtainURL   string
	// Reason says which source failed last.
	Reason string
	// KeychainErr, when non-nil, is a keychain that was present but failed
	// (locked, permission denied) rather than one that simply held no entry.
	// Worth reporting: it usually means the user could have been spared the
	// prompt.
	KeychainErr error
}

// Reasons a credential goes unresolved. Exported as constants so callers and
// tests can match on them without string literals.
const (
	// ReasonNonInteractive: nothing had it and there is no terminal to ask.
	ReasonNonInteractive = "not found in the environment or the OS keychain, and this restore is non-interactive"
	// ReasonEmptyPrompt: the user was asked and entered nothing.
	ReasonEmptyPrompt = "no value was entered at the prompt"
)

func (e *MissingError) Error() string {
	kind := "environment variable"
	remedy := fmt.Sprintf("set %s in your environment, or rerun restore interactively to be prompted", e.Name)
	if e.Header {
		kind = "header"
		remedy = "store it in the OS keychain, or rerun restore interactively to be prompted"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "credential %s (%s)", e.Name, kind)
	if e.Server != "" {
		fmt.Fprintf(&b, " for MCP server %s", e.Server)
	}
	b.WriteString(" could not be resolved")
	if e.Reason != "" {
		fmt.Fprintf(&b, ": %s", e.Reason)
	}
	if e.Description != "" {
		fmt.Fprintf(&b, "; %s", e.Description)
	}
	fmt.Fprintf(&b, "; %s", remedy)
	if e.ObtainURL != "" {
		fmt.Fprintf(&b, "; obtain one at %s", e.ObtainURL)
	}
	if e.KeychainErr != nil {
		fmt.Fprintf(&b, " (the OS keychain could not be read: %v)", e.KeychainErr)
	}
	return b.String()
}

func (e *MissingError) Unwrap() error { return e.KeychainErr }

// Resolver resolves credentials. The zero Resolver is usable and inert: it
// reads the real environment, never touches the machine's keychain, and never
// prompts — so a test that forgets to stub something fails with a
// *MissingError instead of popping an OS keychain dialog.
type Resolver struct {
	// LookupEnv reads the process environment. nil means os.LookupEnv.
	LookupEnv func(name string) (string, bool)

	// Keychain is the OS secret store. nil means Unavailable(): resolution
	// skips the keychain entirely. Real callers pass OSKeychain().
	Keychain Keychain

	// Prompter collects a value from a human. nil means non-interactive —
	// resolution fails with a *MissingError rather than blocking on a
	// terminal that may not exist.
	Prompter Prompter

	// Service is the keychain service name entries are filed under. Empty
	// means DefaultService.
	Service string

	// NoKeychainStore suppresses the post-prompt offer to remember a value.
	// The offer itself is already opt-in (it stores only on an explicit yes);
	// this is for callers that do not want to ask at all, such as a
	// --no-save-credentials flag or a shared machine.
	NoKeychainStore bool
}

// Resolve produces the value for one manifest credential.
//
// The keychain entry it reads and writes is not scoped to any MCP server, so
// prefer ResolveRequirement when the server is known: two servers can both
// inject an "Authorization" header, and those are not the same secret.
func (r *Resolver) Resolve(cred packio.Credential) (Resolution, error) {
	return r.ResolveRequirement(packio.CredentialRequirement{Credential: cred})
}

// ResolveAll resolves every credential a pack declares, in manifest order.
//
// It stops at the first failure: a restore that already cannot proceed should
// not go on prompting for five more secrets before aborting.
func (r *Resolver) ResolveAll(reqs []packio.CredentialRequirement) ([]Resolution, error) {
	out := make([]Resolution, 0, len(reqs))
	for _, req := range reqs {
		res, err := r.ResolveRequirement(req)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// ResolveRequirement resolves a credential in the context of the MCP server
// that needs it, trying each source in the order fixed by docs/security.md
// threat 4: existing environment variable, then the OS keychain, then an
// interactive prompt.
//
// The order is not arbitrary. The environment comes first because a value the
// user already exported is the one they mean right now, and honoring it makes
// a restore reproducible from a script without touching any store. The
// keychain comes next because it is the durable, OS-protected home for a
// secret. The prompt is last because it is the only source that costs the
// user something.
func (r *Resolver) ResolveRequirement(req packio.CredentialRequirement) (Resolution, error) {
	cred := req.Credential
	name, isHeader, err := injectionPoint(cred)
	if err != nil {
		return Resolution{}, err
	}

	res := Resolution{
		Name:   name,
		Header: isHeader,
		Server: req.Server,
		Format: cred.Format,
	}

	// 1. Existing environment variable. Only env credentials have one: a
	// header credential names a header, and treating that name as an env var
	// would let an unrelated variable ("Authorization", "Cookie") be injected
	// as a secret.
	if !isHeader {
		if v, ok := r.lookupEnv(name); ok {
			res.Value, res.Source = v, SourceEnv
			return res, nil
		}
	}

	// 2. OS keychain.
	kc := r.keychain()
	account := AccountName(req.Server, name)
	stored, getErr := kc.Get(r.service(), account)
	if getErr == nil && !stored.Empty() {
		res.Value, res.Source = stored, SourceKeychain
		return res, nil
	}

	// A keychain that is absent, unsupported, or simply holds no entry is not
	// an error — resolution moves on. One that is present but failing (locked,
	// access denied) is worth telling the user about if we end up unable to
	// resolve at all, so it is carried rather than swallowed.
	var keychainErr error
	if getErr != nil && !errors.Is(getErr, ErrNotFound) && !errors.Is(getErr, ErrKeychainUnavailable) {
		keychainErr = getErr
	}
	// The store is worth writing back to only if reading it actually worked.
	keychainUsable := getErr == nil || errors.Is(getErr, ErrNotFound)

	// 3. Interactive prompt, the last resort.
	if r.Prompter == nil {
		return Resolution{}, r.missing(name, isHeader, req, ReasonNonInteractive, keychainErr)
	}
	entered, err := r.Prompter.Secret(Prompt{
		Name:        name,
		Header:      isHeader,
		Server:      req.Server,
		Description: cred.Description,
		ObtainURL:   cred.ObtainURL,
	})
	if err != nil {
		// A prompter that cannot reach a terminal is the non-interactive case
		// wearing a different hat; report it as the actionable missing-
		// credential error rather than as a plumbing failure.
		if errors.Is(err, ErrNotATerminal) {
			return Resolution{}, r.missing(name, isHeader, req, ReasonNonInteractive, keychainErr)
		}
		return Resolution{}, fmt.Errorf("prompting for credential %s: %w", name, err)
	}
	if entered.Empty() {
		return Resolution{}, r.missing(name, isHeader, req, ReasonEmptyPrompt, keychainErr)
	}
	res.Value, res.Source = entered, SourcePrompt

	// 4. Offer — never assume — to remember it.
	if keychainUsable {
		res.Stored, res.StoreErr = r.offerStore(kc, account, res)
	}
	return res, nil
}

// offerStore asks whether to remember a freshly prompted value and stores it
// only on an explicit yes.
//
// docs/security.md says prompted secrets are *offered* for keychain storage so
// the next restore is non-interactive — offered, not saved. Writing a user's
// credential into the OS keychain without asking would be precisely the
// "collected secrets end up somewhere they shouldn't" failure this package
// exists to prevent, so every path here defaults to not storing: no prompter,
// no offer; a declined or unreadable answer, no store; an errored answer, no
// store.
func (r *Resolver) offerStore(kc Keychain, account string, res Resolution) (bool, error) {
	if r.NoKeychainStore || r.Prompter == nil {
		return false, nil
	}
	yes, err := r.Prompter.Confirm(fmt.Sprintf("Store %s in %s so future restores don't prompt?", res.Name, kc.Name()))
	if err != nil || !yes {
		// A confirmation that could not be read (EOF on a closed stdin) is a
		// no, not a failure: the credential resolved fine, it just will not
		// be remembered.
		return false, nil
	}
	if err := kc.Set(r.service(), account, res.Value); err != nil {
		return false, scrub(err, res.Value)
	}
	return true, nil
}

func (r *Resolver) missing(name string, isHeader bool, req packio.CredentialRequirement, reason string, keychainErr error) error {
	return &MissingError{
		Name:        name,
		Header:      isHeader,
		Server:      req.Server,
		Description: req.Credential.Description,
		ObtainURL:   req.Credential.ObtainURL,
		Reason:      reason,
		KeychainErr: keychainErr,
	}
}

// lookupEnv reads one variable, treating blank as unset. An
// exported-but-empty variable (`export GITHUB_TOKEN=`) is a common shell
// accident; counting it as a hit would inject an empty credential and fail
// much later, inside the target tool, with an error that points nowhere.
func (r *Resolver) lookupEnv(name string) (Value, bool) {
	lookup := r.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	raw, ok := lookup(name)
	if !ok {
		return Value{}, false
	}
	v := NewValue(raw)
	if v.Empty() {
		return Value{}, false
	}
	return v, true
}

func (r *Resolver) keychain() Keychain {
	if r.Keychain == nil {
		return Unavailable()
	}
	return r.Keychain
}

func (r *Resolver) service() string {
	if r.Service == "" {
		return DefaultService
	}
	return r.Service
}

// injectionPoint returns the name a credential is injected under and whether
// it is a header.
//
// The manifest spec allows env OR header, never both and never neither: a
// credential with neither names no injection point and can never be applied,
// and one with both is ambiguous about where the secret lands. Both are
// manifest bugs and must fail loudly rather than resolve into a value nobody
// can use.
func injectionPoint(c packio.Credential) (name string, isHeader bool, err error) {
	switch {
	case c.Env != "" && c.Header != "":
		return "", false, fmt.Errorf("credential declares both env %q and header %q; it must declare exactly one", c.Env, c.Header)
	case c.Env != "":
		return c.Env, false, nil
	case c.Header != "":
		return c.Header, true, nil
	}
	return "", false, errors.New("credential declares neither env nor header; it names no injection point")
}

// AccountName is the keychain account a credential is filed under.
//
// Scoping by MCP server matters: two servers can both inject an
// "Authorization" header, and those are different secrets. An unscoped name is
// used only when the caller does not know the server.
func AccountName(server, name string) string {
	if server == "" {
		return name
	}
	return server + "/" + name
}

// scrubbedError is an error whose message has had a secret removed.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// scrub removes any literal occurrence of a resolved value from an error's
// message before that error leaves the package.
//
// Keychain and prompter implementations are pluggable, and the real one wraps
// OS errors verbatim; the resolver therefore does not trust them not to echo
// the secret back inside a failure message. Mangling an unrelated error string
// is an acceptable price — this is a security package, and a garbled message
// beats a leaked credential. Unwrap is preserved so errors.Is/As still work on
// the original.
func scrub(err error, v Value) error {
	if err == nil || v.Empty() {
		return err
	}
	msg := err.Error()
	cleaned := strings.ReplaceAll(msg, v.Expose(), Redacted)
	if cleaned == msg {
		return err
	}
	return &scrubbedError{msg: cleaned, err: err}
}
