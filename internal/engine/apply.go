package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// backupTimeLayout names backup directories. It is RFC3339 with the colons
// removed: colons are illegal in Windows filenames and agentpack is tested on
// all three platforms, but the ordering and readability of RFC3339 are worth
// keeping — `ls ~/.agentpack/backups` should sort chronologically.
const backupTimeLayout = "2006-01-02T15-04-05Z"

// Executor applies a Plan to the filesystem. It is the only thing in
// agentpack that writes tool config, which is what allows the three
// guarantees restore is built on (docs/architecture.md → "Plan/apply split",
// docs/security.md threat 3) to be implemented exactly once:
//
//   - Backup before write. Every file that is about to be modified is copied
//     into a timestamped backup directory first, mirroring its absolute path.
//   - Dry run. DryRun performs no writes at all and still reports exactly
//     what would happen, including later operations on a file an earlier
//     operation would have changed.
//   - Rollback on failure. If any operation fails, every file already written
//     is restored from its backup and every file the plan created is removed,
//     before the error is returned. A rollback that itself fails is reported
//     loudly rather than swallowed.
type Executor struct {
	// BackupRoot is where timestamped backup directories are created. Empty
	// means ~/.agentpack/backups. Tests must set this (t.TempDir()) — the
	// executor must never be exercised against a real home directory.
	BackupRoot string

	// DryRun performs no writes: no directories, no files, no backups.
	DryRun bool

	// Now supplies the timestamp for the backup directory name. Injectable so
	// tests can pin it; nil means time.Now.
	Now func() time.Time
}

// OpStatus is what an operation did (or, in a dry run, would do).
type OpStatus string

const (
	// StatusCreated means the target did not exist and was created.
	StatusCreated OpStatus = "created"
	// StatusUpdated means the target existed and its content changed. Every
	// updated file has a backup.
	StatusUpdated OpStatus = "updated"
	// StatusUnchanged means the target was already in the desired state.
	// Re-applying a pack should report nothing but this — it is how
	// idempotence is observable.
	StatusUnchanged OpStatus = "unchanged"
)

// OpResult records the outcome of one operation.
type OpResult struct {
	Op     Op
	Status OpStatus
	// BackupPath is where the pre-write copy of Op.Path was saved, empty when
	// nothing needed backing up (a new file, or an unchanged one). In a dry
	// run it is the path that *would* have been used.
	BackupPath string
}

// ApplyResult is the outcome of a whole plan.
type ApplyResult struct {
	Tool   model.ToolID
	DryRun bool
	// BackupDir is the timestamped directory holding this apply's backups,
	// empty when no existing file had to be touched. In a dry run it names
	// the directory that would have been created.
	BackupDir string
	Ops       []OpResult
}

// Changed reports whether the apply modified anything, so callers can say
// "already up to date" instead of printing a wall of unchanged operations.
func (r ApplyResult) Changed() bool {
	for _, op := range r.Ops {
		if op.Status != StatusUnchanged {
			return true
		}
	}
	return false
}

// ConflictError reports a file an OpCreateFile wanted to write that already
// exists with different content. It is a refusal, not a failure: the user's
// file is intact and the caller can offer replace mode.
type ConflictError struct {
	Path string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s already exists with different content; refusing to overwrite it "+
		"(replacing an existing file has to be an explicit choice)", e.Path)
}

// ApplyError reports a failed apply *and* what the rollback did about it. Both
// halves matter: the user needs to know why the restore stopped and whether
// their machine is back to where it started.
type ApplyError struct {
	Tool  model.ToolID
	Index int // position of the failing operation in Plan.Ops
	Op    Op
	Err   error // the original failure

	DryRun    bool
	BackupDir string
	// Restored and Removed are the paths rollback put back and deleted.
	Restored []string
	Removed  []string
	// RollbackErrs are failures during rollback. Non-empty means the machine
	// may be in a modified state and the user must intervene.
	RollbackErrs []error
}

// RollbackFailed reports whether rollback left the machine modified.
func (e *ApplyError) RollbackFailed() bool { return len(e.RollbackErrs) > 0 }

func (e *ApplyError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "applying the plan for %s failed at operation %d (%s %s): %v",
		e.Tool, e.Index, e.Op.Kind, e.Op.Path, e.Err)
	if e.DryRun {
		// Nothing was written, so there is nothing to say about rollback.
		return b.String() + " (dry run: nothing was written)"
	}

	b.WriteString("; rolled back ")
	fmt.Fprintf(&b, "%s restored, %s removed",
		plural(len(e.Restored), "file"), plural(len(e.Removed), "created file"))
	if e.BackupDir != "" {
		fmt.Fprintf(&b, " (backups kept in %s)", e.BackupDir)
	}
	if len(e.RollbackErrs) > 0 {
		fmt.Fprintf(&b, "; ROLLBACK INCOMPLETE — %s may still be modified, recover by hand",
			plural(len(e.RollbackErrs), "file"))
		if e.BackupDir != "" {
			fmt.Fprintf(&b, " from %s", e.BackupDir)
		}
		for _, err := range e.RollbackErrs {
			fmt.Fprintf(&b, ": %v", err)
		}
	}
	return b.String()
}

// Unwrap exposes the original failure so callers can errors.Is/As past the
// rollback report.
func (e *ApplyError) Unwrap() error { return e.Err }

// DefaultBackupRoot is where backups go when Executor.BackupRoot is empty.
func DefaultBackupRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the backup directory: %w", err)
	}
	return filepath.Join(home, ".agentpack", "backups"), nil
}

// Apply executes the plan in order.
//
// The plan is validated in full before the first write, so a malformed plan
// fails with nothing applied. After that, each operation computes its result
// before writing: an operation that would produce the bytes already on disk
// writes nothing and takes no backup, which is what makes applying the same
// plan twice a no-op rather than a pile of redundant backups.
func (e *Executor) Apply(p Plan) (*ApplyResult, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	run := &applyRun{
		ex:          e,
		overlay:     map[string][]byte{},
		pendingDirs: map[string]bool{},
	}
	res := &ApplyResult{Tool: p.Tool, DryRun: e.DryRun}

	for i, op := range p.Ops {
		opRes, err := run.apply(op)
		if err != nil {
			return nil, run.fail(p.Tool, i, op, err)
		}
		res.Ops = append(res.Ops, opRes)
	}
	res.BackupDir = run.backupDir
	return res, nil
}

// applyRun carries the mutable state of one Apply: what has been written so
// far and therefore what rollback has to undo.
type applyRun struct {
	ex        *Executor
	backupDir string // resolved lazily: a plan that only creates files leaves no backup dir

	backups     []backupEntry // existing files copied aside, in application order
	created     []string      // files this plan created, in application order
	createdDirs []string      // directories this plan created

	// overlay holds writes a dry run did not perform, so that a later
	// operation on the same file sees what the earlier one would have done
	// and the reported statuses match a real apply.
	overlay     map[string][]byte
	pendingDirs map[string]bool
}

type backupEntry struct {
	target string
	backup string
}

func (r *applyRun) apply(op Op) (OpResult, error) {
	switch op.Kind {
	case OpCreateDir:
		return r.applyCreateDir(op)
	case OpCreateFile, OpReplaceFile:
		return r.applyFile(op)
	case OpMergeValue:
		return r.applyMerge(op)
	default:
		// Plan.Validate rejects unknown kinds, so this is unreachable in
		// practice; failing loudly beats silently skipping an operation.
		return OpResult{}, fmt.Errorf("unsupported operation kind %q", op.Kind)
	}
}

func (r *applyRun) applyCreateDir(op Op) (OpResult, error) {
	if r.dirExists(op.Path) {
		return OpResult{Op: op, Status: StatusUnchanged}, nil
	}
	perm := op.Perm
	if perm == 0 {
		perm = 0o755
	}
	if err := r.mkdirAll(op.Path, perm); err != nil {
		return OpResult{}, err
	}
	return OpResult{Op: op, Status: StatusCreated}, nil
}

func (r *applyRun) applyFile(op Op) (OpResult, error) {
	current, exists, err := r.readFile(op.Path)
	if err != nil {
		return OpResult{}, err
	}
	if exists && bytes.Equal(current, op.Content) {
		// Already exactly what the plan wants: no write, no backup. This is
		// the second half of idempotence (the first is that Content is a
		// pure function of the pack).
		return OpResult{Op: op, Status: StatusUnchanged}, nil
	}
	if exists && op.Kind == OpCreateFile {
		return OpResult{}, &ConflictError{Path: op.Path}
	}
	return r.write(op, op.Content, exists)
}

func (r *applyRun) applyMerge(op Op) (OpResult, error) {
	current, exists, err := r.readFile(op.Path)
	if err != nil {
		return OpResult{}, err
	}
	format := op.format()
	doc, err := decodeDoc(current, format)
	if err != nil {
		// Refusing here is the point: a config we cannot parse is a config we
		// cannot merge into without destroying it.
		return OpResult{}, fmt.Errorf("reading %s as %s: %w", op.Path, format, err)
	}
	if err := setAtPath(doc, op.KeyPath, op.Value, op.Strategy.orDefault()); err != nil {
		return OpResult{}, fmt.Errorf("merging into %s: %w", op.Path, err)
	}
	merged, err := encodeDoc(doc, format)
	if err != nil {
		return OpResult{}, fmt.Errorf("writing %s as %s: %w", op.Path, format, err)
	}
	if exists && bytes.Equal(current, merged) {
		return OpResult{Op: op, Status: StatusUnchanged}, nil
	}
	return r.write(op, merged, exists)
}

// write performs the backup-then-write sequence shared by every operation
// that produces file content. Backing up happens here, immediately before the
// write, and only when there is an existing file to lose.
func (r *applyRun) write(op Op, content []byte, exists bool) (OpResult, error) {
	res := OpResult{Op: op, Status: StatusCreated}
	perm := op.Perm
	if perm == 0 {
		perm = 0o644
	}

	if exists {
		backup, err := r.backup(op.Path)
		if err != nil {
			return OpResult{}, err
		}
		res.BackupPath = backup
		res.Status = StatusUpdated
		// Keep the mode the user already chose: a restore must never widen
		// permissions on a config file that was deliberately locked down.
		if info, err := os.Stat(op.Path); err == nil {
			perm = info.Mode().Perm()
		}
	} else if err := r.mkdirAll(filepath.Dir(op.Path), 0o755); err != nil {
		return OpResult{}, err
	}

	if err := r.writeFile(op.Path, content, perm); err != nil {
		return OpResult{}, err
	}
	if !exists {
		r.created = append(r.created, op.Path)
	}
	return res, nil
}

// fail rolls the machine back and wraps the original failure together with
// the rollback outcome.
func (r *applyRun) fail(tool model.ToolID, index int, op Op, cause error) error {
	applyErr := &ApplyError{
		Tool:      tool,
		Index:     index,
		Op:        op,
		Err:       cause,
		DryRun:    r.ex.DryRun,
		BackupDir: r.backupDir,
	}
	if r.ex.DryRun {
		// A dry run wrote nothing, so there is nothing to undo.
		return applyErr
	}
	applyErr.Restored, applyErr.Removed, applyErr.RollbackErrs = r.rollback()
	return applyErr
}

// rollback undoes everything this run wrote, newest first: files that existed
// are copied back from their backups, files the plan created are deleted.
// Backups are deliberately left in place afterwards — they are the user's
// evidence and their manual recovery path if any of this failed.
func (r *applyRun) rollback() (restored, removed []string, errs []error) {
	for i := len(r.backups) - 1; i >= 0; i-- {
		entry := r.backups[i]
		if err := copyFile(entry.backup, entry.target); err != nil {
			errs = append(errs, fmt.Errorf("restoring %s from %s: %w", entry.target, entry.backup, err))
			continue
		}
		restored = append(restored, entry.target)
	}
	for i := len(r.created) - 1; i >= 0; i-- {
		path := r.created[i]
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("removing %s: %w", path, err))
			continue
		}
		removed = append(removed, path)
	}

	// Directories created by this run, deepest first. Removal is best effort
	// and its failures are not rollback errors: os.Remove refuses a
	// non-empty directory, which means it holds something we did not create
	// and leaving it is correct. A leftover empty directory is not damage.
	dirs := append([]string(nil), r.createdDirs...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		// Only ever remove an actual directory. A path reaches this list
		// before MkdirAll runs, so it can name something that was never
		// created — a regular file sitting where a directory was needed is
		// how MkdirAll fails in the first place — and deleting *that* would
		// be precisely the damage rollback exists to prevent.
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			continue
		}
		_ = os.Remove(dir)
	}
	return restored, removed, errs
}

// backup copies target into this run's backup directory, mirroring its
// absolute path so the backup tree is self-describing and a human can find
// the file they lost. In a dry run it computes the path but copies nothing.
func (r *applyRun) backup(target string) (string, error) {
	dir, err := r.ensureBackupDir()
	if err != nil {
		return "", err
	}
	dest := backupPathFor(dir, target)
	if r.ex.DryRun {
		return dest, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", fmt.Errorf("preparing backup for %s: %w", target, err)
	}
	if err := copyFile(target, dest); err != nil {
		return "", fmt.Errorf("backing up %s: %w", target, err)
	}
	r.backups = append(r.backups, backupEntry{target: target, backup: dest})
	return dest, nil
}

// ensureBackupDir resolves (and, outside a dry run, creates) this run's
// backup directory on first use, so an apply that only creates new files
// leaves no empty directory behind.
func (r *applyRun) ensureBackupDir() (string, error) {
	if r.backupDir != "" {
		return r.backupDir, nil
	}
	root := r.ex.BackupRoot
	if root == "" {
		var err error
		if root, err = DefaultBackupRoot(); err != nil {
			return "", err
		}
	}
	now := time.Now
	if r.ex.Now != nil {
		now = r.ex.Now
	}
	stamp := now().UTC().Format(backupTimeLayout)

	// Two applies within the same second must not share a directory, or the
	// second would overwrite the first's originals.
	dir := filepath.Join(root, stamp)
	for i := 1; ; i++ {
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			break
		}
		if i > 1000 {
			return "", fmt.Errorf("cannot find an unused backup directory under %s", root)
		}
		dir = filepath.Join(root, fmt.Sprintf("%s-%d", stamp, i))
	}

	if !r.ex.DryRun {
		// 0700: a backed-up config may contain credentials that restore
		// injected (docs/security.md threat 4), so the copy must be no more
		// readable than the original.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("creating the backup directory: %w", err)
		}
	}
	r.backupDir = dir
	return dir, nil
}

// backupPathFor mirrors an absolute target path inside the backup directory:
// /Users/x/.claude.json → <backup>/Users/x/.claude.json. Windows volumes
// become a single path element ("C:\x" → <backup>\C\x) so the result stays a
// legal relative path.
func backupPathFor(dir, target string) string {
	vol := filepath.VolumeName(target)
	rel := filepath.ToSlash(strings.TrimPrefix(target, vol))
	rel = strings.TrimLeft(rel, "/")
	if vol != "" {
		// "C:" → "C"; a UNC volume (\\host\share) → "host-share".
		label := strings.Trim(strings.NewReplacer(":", "", `\`, "-", "/", "-").Replace(vol), "-")
		rel = label + "/" + rel
	}
	return filepath.Join(dir, filepath.FromSlash(rel))
}

// readFile reads the current content of path, reporting whether it exists. In
// a dry run it prefers the overlay, so statuses account for writes the run
// would already have made.
func (r *applyRun) readFile(path string) ([]byte, bool, error) {
	if r.ex.DryRun {
		if data, ok := r.overlay[path]; ok {
			return data, true, nil
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (r *applyRun) writeFile(path string, data []byte, perm fs.FileMode) error {
	if r.ex.DryRun {
		r.overlay[path] = data
		return nil
	}
	return writeFileAtomic(path, data, perm)
}

func (r *applyRun) dirExists(path string) bool {
	if r.ex.DryRun && r.pendingDirs[path] {
		return true
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// mkdirAll creates dir and records which levels it had to create, so rollback
// can remove exactly those and no more.
func (r *applyRun) mkdirAll(dir string, perm fs.FileMode) error {
	var missing []string
	for d := dir; ; {
		if r.dirExists(d) {
			break
		}
		missing = append(missing, d)
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if len(missing) == 0 {
		return nil
	}
	if r.ex.DryRun {
		for _, d := range missing {
			r.pendingDirs[d] = true
		}
		return nil
	}
	// Recorded before the call, not after: MkdirAll works top-down, so a
	// failure part-way can still have created the shallower levels, and
	// rollback's removal is best effort anyway.
	r.createdDirs = append(r.createdDirs, missing...)
	return os.MkdirAll(dir, perm)
}

// writeFileAtomic writes via a temporary file in the same directory and
// renames it into place, so an interrupted apply can never leave a
// half-written config behind.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentpack-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// copyFile copies src over dst, preserving src's mode. Used both to take
// backups and to restore them.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// O_CREATE honours the mode only for a new file; an existing backup
	// target keeps its own, so set it explicitly.
	return os.Chmod(dst, info.Mode().Perm())
}

// --- structured merge -------------------------------------------------
//
// The executor understands JSON, TOML and YAML as generic key/value
// documents and nothing else: which key a tool keeps its MCP servers under is
// the adapter's knowledge, not the executor's (docs/architecture.md → Design
// principles 4). Re-encoding is lossy for comments and key order — the
// backup taken before the write is the mitigation, and it is why merges are
// scoped to a key path rather than rewriting whole files.

// decodeDoc parses a structured document. An empty or missing file decodes to
// an empty document, which is how a merge creates config on a machine that
// has none.
func decodeDoc(data []byte, format Format) (map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	doc := map[string]any{}
	switch format {
	case FormatJSON:
		dec := json.NewDecoder(bytes.NewReader(data))
		// UseNumber keeps numeric literals exactly as written; without it
		// every int in the user's config would round-trip through float64
		// and 1700000000 would come back as 1.7e+09.
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil {
			return nil, err
		}
	case FormatTOML:
		if err := toml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
	case FormatYAML:
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
	if doc == nil { // a document whose whole content is "null"
		doc = map[string]any{}
	}
	return doc, nil
}

// encodeDoc renders a document back to bytes, in the indentation these tools'
// own files use, with a trailing newline.
func encodeDoc(doc map[string]any, format Format) ([]byte, error) {
	var buf bytes.Buffer
	switch format {
	case FormatJSON:
		enc := json.NewEncoder(&buf)
		// Tool configs are full of URLs; escaping & and < into \u0026 would
		// mangle values the user can read today.
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return nil, err
		}
	case FormatTOML:
		if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
			return nil, err
		}
	case FormatYAML:
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(doc); err != nil {
			return nil, err
		}
		if err := enc.Close(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
	return buf.Bytes(), nil
}

// setAtPath walks keyPath, creating missing objects, and applies value at the
// end. Everything not on the path is left exactly as it was — that is the
// "merge, don't clobber" guarantee, and the reason a merge operation names a
// key path rather than a whole document.
func setAtPath(doc map[string]any, keyPath []string, value any, strategy MergeStrategy) error {
	parent := doc
	for i, key := range keyPath[:len(keyPath)-1] {
		child, ok := parent[key]
		if !ok || child == nil {
			next := map[string]any{}
			parent[key] = next
			parent = next
			continue
		}
		next, ok := asMap(child)
		if !ok {
			return fmt.Errorf("%s holds a %T, not an object", strings.Join(keyPath[:i+1], "."), child)
		}
		parent[key] = next // normalized (a YAML map may decode with non-string keys)
		parent = next
	}

	last := keyPath[len(keyPath)-1]
	if strategy == MergeDeep {
		if existing, ok := asMap(parent[last]); ok {
			if incoming, ok := asMap(value); ok {
				parent[last] = deepMerge(existing, incoming)
				return nil
			}
		}
	}
	parent[last] = value
	return nil
}

// deepMerge merges src into dst recursively, src winning. Scalars and arrays
// are replaced rather than combined: replacement is idempotent and
// predictable, whereas appending to an array would grow the user's config on
// every re-apply.
func deepMerge(dst, src map[string]any) map[string]any {
	for key, srcVal := range src {
		if dstMap, ok := asMap(dst[key]); ok {
			if srcMap, ok := asMap(srcVal); ok {
				dst[key] = deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[key] = srcVal
	}
	return dst
}

// asMap normalizes the map shapes the three decoders produce. YAML can yield
// map[any]any for nested mappings; a mapping with a non-string key is not
// something a key path can address, so it is reported as "not an object".
func asMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			key, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[key] = val
		}
		return out, true
	}
	return nil, false
}
