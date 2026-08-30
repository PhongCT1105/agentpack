package engine

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// OpKind classifies one intended change to the filesystem.
//
// The set is deliberately small. Adapters express everything they need as a
// sequence of these, and in exchange every operation gets the same guarantees
// from the executor: a backup before the write, a dry run that performs no
// writes, and rollback if a later operation fails. A "copy this directory
// tree" operation would be convenient for adapters but would defeat that —
// a bundled skill is therefore an OpCreateDir plus one OpCreateFile per file,
// because per-file granularity is what lets rollback put the machine back
// exactly as it was.
type OpKind string

const (
	// OpCreateFile writes a file the plan believes is new.
	//
	// If the file already exists with byte-identical content the operation is
	// a no-op — this is what makes re-applying a pack safe. If it exists with
	// *different* content the executor refuses (ConflictError): silently
	// overwriting a file the user edited is precisely the "local config
	// damage" threat (docs/security.md threat 3). Overwriting is a separate,
	// explicit op kind so that it is visible in the plan the user confirms.
	OpCreateFile OpKind = "create_file"

	// OpReplaceFile overwrites a file wholesale. It encodes a decision that
	// has already been made above the executor ("replace mode" in
	// docs/architecture.md → Adapters), which is why it needs no conflict
	// check: the preview line says "replace", and the original is in the
	// backup directory.
	OpReplaceFile OpKind = "replace_file"

	// OpMergeValue sets Value at KeyPath inside a structured document
	// (JSON/TOML/YAML), leaving every other key in that document untouched.
	// This is the "merge, don't clobber" operation: adding an entry to
	// mcpServers must not disturb the rest of ~/.claude.json.
	//
	// The adapter supplies the target file, the key path and the value; the
	// executor knows only the generic encodings, never a tool's schema. That
	// split is what keeps tool knowledge in adapters (docs/architecture.md →
	// Design principles 4).
	OpMergeValue OpKind = "merge_value"

	// OpCreateDir creates a directory and any missing parents. Adapters emit
	// it explicitly rather than relying on file writes to create parents, so
	// that an empty directory a tool requires (an empty commands/ dir, say)
	// still shows up in the preview.
	OpCreateDir OpKind = "create_dir"
)

// Valid reports whether k is a known operation kind.
func (k OpKind) Valid() bool {
	switch k {
	case OpCreateFile, OpReplaceFile, OpMergeValue, OpCreateDir:
		return true
	}
	return false
}

// Format is the encoding of a structured file an OpMergeValue targets. The
// executor supports exactly these three because they are what the supported
// tools use: JSON (~/.claude.json, .mcp.json, settings.json), TOML
// (~/.codex/config.toml) and YAML (gemini/cursor-adjacent config).
type Format string

const (
	FormatJSON Format = "json"
	FormatTOML Format = "toml"
	FormatYAML Format = "yaml"
)

// Valid reports whether f is a supported merge format.
func (f Format) Valid() bool {
	switch f {
	case FormatJSON, FormatTOML, FormatYAML:
		return true
	}
	return false
}

// FormatForPath infers the format from a file extension. Adapters may set
// Op.Format explicitly; leaving it empty and letting the path decide is the
// common case and keeps adapter code short.
func FormatForPath(path string) Format {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return FormatJSON
	case ".toml":
		return FormatTOML
	case ".yaml", ".yml":
		return FormatYAML
	}
	return ""
}

// MergeStrategy says what happens at the end of an OpMergeValue's key path.
// Everything *outside* the key path is preserved under either strategy —
// that is the executor's guarantee, not the adapter's choice.
type MergeStrategy string

const (
	// MergeSet replaces whatever sits at the key path. This is the default
	// (the zero value normalizes to it) and the right choice when the key
	// path already names one addressable unit of config, e.g.
	// mcpServers.github: replacing that server's definition is the intent,
	// and the neighbouring servers are outside the path.
	MergeSet MergeStrategy = "set"

	// MergeDeep recursively merges objects at the key path, incoming keys
	// winning, and is for paths whose value is a document the user also
	// writes into — e.g. merging {allow: [...]} into permissions must not
	// drop the user's permissions.deny. Non-object values (scalars, arrays)
	// are replaced, because there is no meaningful union of two arrays that
	// stays idempotent.
	MergeDeep MergeStrategy = "deep"
)

func (s MergeStrategy) orDefault() MergeStrategy {
	if s == "" {
		return MergeSet
	}
	return s
}

// Op is one intended file operation. It carries both what to do and enough
// text to render the preview line a user confirms before anything is written
// (docs/architecture.md → "Read-only until confirmed").
type Op struct {
	Kind OpKind

	// Path is the absolute target: the file for file and merge operations,
	// the directory for OpCreateDir. Absolute is required — a plan is
	// rendered, confirmed, and only then executed, so it must not mean
	// something different depending on the working directory at apply time.
	Path string

	// Content is the file body for OpCreateFile and OpReplaceFile.
	Content []byte

	// Perm is the mode for a newly created file (0 means 0644, or 0700 for
	// directories). When a file already exists its current mode is kept: a
	// restore must not widen the permissions on a config the user locked down.
	Perm fs.FileMode

	// Format, KeyPath, Value and Strategy describe an OpMergeValue. Format
	// may be left empty to infer from Path.
	Format   Format
	KeyPath  []string
	Value    any
	Strategy MergeStrategy

	// Description is the short human phrase for the preview line — what this
	// operation is *for* ("skill brainstorming", "mcp server github"), not
	// what it does mechanically, which Action() derives.
	Description string
}

// Action renders the mechanical half of the preview line: the kind of change,
// plus the detail that makes it reviewable (how big a write, which key).
func (op Op) Action() string {
	switch op.Kind {
	case OpCreateFile:
		return fmt.Sprintf("create file (%s)", humanSize(len(op.Content)))
	case OpReplaceFile:
		return fmt.Sprintf("replace file (%s)", humanSize(len(op.Content)))
	case OpMergeValue:
		action := "merge " + strings.Join(op.KeyPath, ".")
		if op.Strategy.orDefault() == MergeDeep {
			action += " (deep)"
		}
		return action
	case OpCreateDir:
		return "create directory"
	default:
		return string(op.Kind)
	}
}

// Validate rejects operations an adapter could only have produced by mistake.
// Plan.Validate runs over the whole plan before the first write, so a
// malformed plan fails with nothing applied and nothing to roll back.
func (op Op) Validate() error {
	if !op.Kind.Valid() {
		return fmt.Errorf("unknown operation kind %q", op.Kind)
	}
	if op.Path == "" {
		return fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(op.Path) {
		return fmt.Errorf("path %q must be absolute", op.Path)
	}
	switch op.Kind {
	case OpCreateFile, OpReplaceFile:
		if len(op.KeyPath) > 0 || op.Value != nil {
			return fmt.Errorf("key path/value belong to a %s operation", OpMergeValue)
		}
	case OpCreateDir:
		if len(op.Content) > 0 || len(op.KeyPath) > 0 || op.Value != nil {
			return fmt.Errorf("a directory operation carries no content, key path or value")
		}
	case OpMergeValue:
		if len(op.Content) > 0 {
			return fmt.Errorf("content belongs to a file operation, not a merge")
		}
		if len(op.KeyPath) == 0 {
			// A merge with no key path would mean "merge into the whole
			// document", which has no legible preview line and no obvious
			// conflict boundary. Adapters emit one operation per key path.
			return fmt.Errorf("key path is required")
		}
		for _, k := range op.KeyPath {
			if k == "" {
				return fmt.Errorf("key path %q has an empty segment", strings.Join(op.KeyPath, "."))
			}
		}
		if op.Value == nil {
			// There is deliberately no delete operation: a restore adds and
			// updates config, it never removes what it did not put there.
			return fmt.Errorf("merge value must not be nil")
		}
		if f := op.format(); !f.Valid() {
			return fmt.Errorf("cannot tell the format of %s (set Format explicitly)", op.Path)
		}
		if s := op.Strategy.orDefault(); s != MergeSet && s != MergeDeep {
			return fmt.Errorf("unknown merge strategy %q", op.Strategy)
		}
	}
	return nil
}

// format resolves the declared format, falling back to the file extension.
func (op Op) format() Format {
	if op.Format != "" {
		return op.Format
	}
	return FormatForPath(op.Path)
}

// Plan is an ordered list of file operations produced by one adapter for one
// tool. It is the whole interface between adapters and the filesystem:
// adapters never write, they return a Plan, the engine renders it for
// confirmation and the executor applies it (docs/architecture.md →
// "Plan/apply split").
type Plan struct {
	Tool model.ToolID
	Ops  []Op
}

// Add appends operations, so adapters can build a plan without repeating the
// append boilerplate.
func (p *Plan) Add(ops ...Op) {
	p.Ops = append(p.Ops, ops...)
}

// IsEmpty reports whether the plan would change nothing.
func (p Plan) IsEmpty() bool { return len(p.Ops) == 0 }

// Paths returns every distinct path the plan touches, in first-appearance
// order. Callers use it to tell the user which files are at stake.
func (p Plan) Paths() []string {
	seen := make(map[string]bool, len(p.Ops))
	paths := make([]string, 0, len(p.Ops))
	for _, op := range p.Ops {
		if !seen[op.Path] {
			seen[op.Path] = true
			paths = append(paths, op.Path)
		}
	}
	return paths
}

// Validate checks every operation. The executor calls it before touching
// anything, so an adapter bug cannot half-apply a plan.
func (p Plan) Validate() error {
	for i, op := range p.Ops {
		if err := op.Validate(); err != nil {
			return fmt.Errorf("plan for %s, operation %d (%s %s): %w", p.Tool, i, op.Kind, op.Path, err)
		}
	}
	return nil
}

// Render writes the confirmation preview: this is what `restore` shows before
// it is allowed to write anything, so it must account for every operation.
//
// Operations are grouped by file because that is the question a user is
// actually asking ("what happens to my ~/.claude.json?"). Grouping is a
// reading aid only — the executor follows Ops order.
func (p Plan) Render(w io.Writer) error {
	if p.IsEmpty() {
		_, err := fmt.Fprintf(w, "%s: nothing to do\n", p.Tool)
		return err
	}

	paths := p.Paths()
	if _, err := fmt.Fprintf(w, "%s: %s across %s\n",
		p.Tool, plural(len(p.Ops), "operation"), plural(len(paths), "path")); err != nil {
		return err
	}

	byPath := make(map[string][]Op, len(paths))
	for _, op := range p.Ops {
		byPath[op.Path] = append(byPath[op.Path], op)
	}
	for _, path := range paths {
		if _, err := fmt.Fprintf(w, "\n  %s\n", shortenHome(path, homeDir())); err != nil {
			return err
		}
		tw := tabwriter.NewWriter(w, 4, 4, 2, ' ', 0)
		for _, op := range byPath[path] {
			if op.Description == "" {
				fmt.Fprintf(tw, "    %s\n", op.Action())
				continue
			}
			fmt.Fprintf(tw, "    %s\t%s\n", op.Action(), op.Description)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// String renders the plan preview as text.
func (p Plan) String() string {
	var b strings.Builder
	// Render only fails when the writer fails; a strings.Builder cannot.
	_ = p.Render(&b)
	return b.String()
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// shortenHome abbreviates the user's home directory to "~" for display only.
// Plans carry absolute paths; a preview full of /Users/<name>/… noise makes
// the interesting part of each line harder to see.
func shortenHome(path, home string) string {
	if home == "" || home == string(filepath.Separator) || path == home {
		return path
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + path[len(prefix):]
	}
	return path
}

func humanSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// PlanOpts carries everything an adapter needs to turn neutral components
// into concrete file operations for one machine.
type PlanOpts struct {
	// Home is the user's home directory. Injectable so tests never plan
	// against the real one.
	Home string

	// ProjectDir is where project-scoped components land. Empty means the
	// caller offered no project, and an adapter must skip project-scoped
	// components with a warning rather than guessing a directory.
	ProjectDir string

	// PackDir is the root of the pack being restored, for resolving a
	// component's bundled content.
	PackDir string

	// Credentials maps a credential's injection point (an env var name, or
	// a header name) to the value the resolver produced for it. An adapter
	// writes these into tool config; a missing entry means the user did not
	// supply that credential, and the adapter must still emit the server
	// with the injection point referenced (e.g. "${GITHUB_TOKEN}") rather
	// than silently dropping it.
	Credentials map[string]string

	// Replace turns merge-into-existing into overwrite for files where the
	// user explicitly chose replacement. The executor still backs up first.
	Replace bool
}

// Planner is implemented by adapters that can write configuration, not just
// read it. It is deliberately separate from Adapter: scanning and applying
// are independent capabilities, and keeping them apart lets an adapter ship
// read support first and gain write support later without every other
// adapter having to implement a method it does not yet support.
//
// A Planner never writes. It returns a Plan for the executor to render,
// confirm and apply — see docs/architecture.md, "Plan/apply split".
type Planner interface {
	Adapter
	Plan(components []model.Component, opts PlanOpts) (Plan, []model.Warning, error)
}
