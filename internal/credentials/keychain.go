package credentials

import (
	"errors"
	"runtime"
	"sync"

	keyring "github.com/zalando/go-keyring"
)

// DefaultService is the keychain "service" (the application an entry belongs
// to) agentpack files credentials under. It is fixed rather than derived from
// the pack so that a credential stored during one restore is found by the
// next, whichever pack asks for it — the secret belongs to the machine and the
// service it authenticates to, not to the pack that happened to need it first.
const DefaultService = "agentpack"

var (
	// ErrNotFound means the store works but holds no entry for that account.
	// Resolution treats it as an ordinary miss and moves on to the prompt.
	ErrNotFound = errors.New("credential not found in keychain")

	// ErrKeychainUnavailable means there is no usable OS secret store here:
	// an unsupported platform, or a headless Linux box with no D-Bus secret
	// service running. It is a fact about the environment, not a failure, and
	// resolution degrades to prompting without complaint.
	ErrKeychainUnavailable = errors.New("no OS keychain available")
)

// Keychain is the OS secret store, behind an interface so the resolver never
// depends on a real one.
//
// It deals in Value rather than string deliberately: the keychain boundary is
// one of exactly two places a secret may legitimately cross (the other is the
// target tool's own config), and typing it as Value means a secret cannot pass
// through here as a bare string that some intermediate layer might log.
// Implementations call Value.Expose at the last possible moment.
type Keychain interface {
	// Name identifies the store in user-facing text ("macOS Keychain"), so
	// the storage offer can say where the value is about to go.
	Name() string
	// Get returns the stored value, ErrNotFound if the store holds no entry
	// for this account, or ErrKeychainUnavailable if there is no store.
	Get(service, account string) (Value, error)
	// Set stores value, replacing any existing entry for the account.
	Set(service, account string, value Value) error
	// Delete removes an entry, returning ErrNotFound if there was none.
	Delete(service, account string) error
}

// OSKeychain returns the machine's real secret store: macOS Keychain, the
// Windows Credential Manager, or the freedesktop Secret Service (GNOME
// Keyring / KWallet) on Linux, via github.com/zalando/go-keyring.
//
// It is never the default anywhere in this package. A caller has to ask for
// it by name, which is what keeps tests — and any code path that has not
// thought about it — off the user's real keychain.
func OSKeychain() Keychain { return osKeychain{} }

type osKeychain struct{}

func (osKeychain) Name() string {
	switch runtime.GOOS {
	case "darwin":
		return "the macOS Keychain"
	case "windows":
		return "the Windows Credential Manager"
	case "linux":
		return "the Secret Service (libsecret)"
	}
	return "the OS keychain"
}

func (osKeychain) Get(service, account string) (Value, error) {
	secret, err := keyring.Get(service, account)
	if err != nil {
		return Value{}, translateKeyringErr(err)
	}
	return NewValue(secret), nil
}

func (osKeychain) Set(service, account string, value Value) error {
	return translateKeyringErr(keyring.Set(service, account, value.Expose()))
}

func (osKeychain) Delete(service, account string) error {
	return translateKeyringErr(keyring.Delete(service, account))
}

// translateKeyringErr maps go-keyring's sentinels onto this package's, so
// callers match on one vocabulary.
//
// Only the two sentinels are mapped. A D-Bus dial failure on a headless Linux
// box arrives here as an opaque transport error and is passed through as a
// real error rather than guessed at from its message: the resolver degrades to
// prompting either way, and the difference only affects whether the user is
// told their keychain is broken — which, when it is, they should be.
func translateKeyringErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, keyring.ErrUnsupportedPlatform):
		return ErrKeychainUnavailable
	}
	return err
}

// MemoryKeychain is an in-process store: the test double everywhere in this
// package, and the fallback for an environment with no OS secret store.
//
// It is explicitly not persistence. Entries live for the life of the process
// and never touch disk — which is the right property for a fallback (a secret
// written to a plain file would be a worse outcome than prompting again) but
// means "remember this" lasts exactly one run. A caller that substitutes it
// for the OS keychain should say so, rather than promise the user a
// non-interactive restore it cannot deliver.
type MemoryKeychain struct {
	mu      sync.Mutex
	entries map[string]Value
}

// NewMemoryKeychain returns an empty in-process store.
func NewMemoryKeychain() *MemoryKeychain {
	return &MemoryKeychain{entries: make(map[string]Value)}
}

func (*MemoryKeychain) Name() string { return "an in-memory store (not persisted)" }

func (k *MemoryKeychain) Get(service, account string) (Value, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.entries[memKey(service, account)]
	if !ok {
		return Value{}, ErrNotFound
	}
	return v, nil
}

func (k *MemoryKeychain) Set(service, account string, value Value) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.entries == nil {
		k.entries = make(map[string]Value)
	}
	k.entries[memKey(service, account)] = value
	return nil
}

func (k *MemoryKeychain) Delete(service, account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	key := memKey(service, account)
	if _, ok := k.entries[key]; !ok {
		return ErrNotFound
	}
	delete(k.entries, key)
	return nil
}

// Len reports how many entries are held, for tests that assert a value was (or
// was not) stored without reading the value itself.
func (k *MemoryKeychain) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.entries)
}

func memKey(service, account string) string { return service + "\x00" + account }

// Unavailable returns a Keychain that holds nothing and stores nothing, with
// every operation reporting ErrKeychainUnavailable.
//
// It is the Resolver's default when no keychain is configured. That matters
// more than it looks: it means the zero Resolver, and any code path that
// forgot to pick a store, cannot read from or write to the user's real
// keychain by omission.
func Unavailable() Keychain { return unavailableKeychain{} }

type unavailableKeychain struct{}

func (unavailableKeychain) Name() string { return "no keychain" }

func (unavailableKeychain) Get(string, string) (Value, error) {
	return Value{}, ErrKeychainUnavailable
}

func (unavailableKeychain) Set(string, string, Value) error { return ErrKeychainUnavailable }

func (unavailableKeychain) Delete(string, string) error { return ErrKeychainUnavailable }
