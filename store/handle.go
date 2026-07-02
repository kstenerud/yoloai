// ABOUTME: Scoped versioned persistence handles (D87): Handle/Record/OpenDomain.
// ABOUTME: Components read/write their own slice of storage; blind to physical layout.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/fileutil"
	"github.com/kstenerud/yoloai/internal/locking"
)

// ErrNeedsMigration is returned by Handle.Load when the on-disk version is older
// than what this binary writes. The caller must run an explicit migration
// (Handle.Migrate or `yoloai system migrate`); Load never auto-migrates.
var ErrNeedsMigration = errors.New("store: record needs migration before use")

// ErrTooNew is returned when the on-disk version is newer than this binary knows.
// The user must upgrade yoloai.
var ErrTooNew = errors.New("store: record was written by a newer version of yoloai")

// Record is implemented by any value that can be persisted through a Handle.
// SchemaVersion returns the version number this binary writes — the sacred
// plain-int that Handle uses to detect version mismatches.
type Record interface {
	SchemaVersion() int
}

// Migrator is an optional extension of Record for types that support
// forward migration. If a Record also implements Migrator, Handle.Migrate
// calls MigrateRecord to advance the schema.
type Migrator interface {
	Record
	// MigrateRecord advances the record from its current (on-disk) version
	// to SchemaVersion() by running the typed migration ladder. Called only
	// by Handle.Migrate, never by Load.
	MigrateRecord() error
}

// Handle is a scoped, versioned slice of persistent storage. A component
// receives exactly one Handle and reads/writes only through it — it is
// blind to the physical file path and to any other component's data.
//
// Concurrency model (D87 §6): Load is lock-free (atomic-rename makes reads
// torn-free). All mutations must go through Update (lock → re-read → mutate →
// write → unlock). Save is for the initial creation of a record only.
type Handle interface {
	// Load reads the record from storage. Lock-free; safe to call concurrently.
	//
	//   - version == SchemaVersion()  → unmarshal into v, return found=true, err=nil
	//   - version < SchemaVersion()   → return found=false, ErrNeedsMigration
	//   - version > SchemaVersion()   → return found=false, ErrTooNew
	//   - file absent                 → return found=false, err=nil
	//   - file unreadable/corrupt     → return found=false, err=<wrapped error>
	//
	// Load NEVER migrates data.
	Load(v Record) (found bool, err error)

	// Save writes v as a new record. Intended for initial creation; it does not
	// acquire the per-sandbox flock (the caller must hold it, as with Create).
	// Stamps v.SchemaVersion() into the on-disk "version" field.
	Save(v Record) error

	// Update acquires the per-sandbox flock, re-reads the record fresh under
	// the lock, calls mutate, then atomically writes the result. The fresh
	// re-read re-checks the version (concurrent migration → ErrTooNew or
	// ErrNeedsMigration) and also surfaces a CAS failure if the caller-supplied
	// v has a different version than what is on disk (lost-update protection).
	//
	// mutate is called only once, with the lock held. If mutate returns an
	// error, the write is skipped and the error is returned.
	//
	// If the file is absent when Update runs, it acts as a Save (initial create
	// under lock).
	Update(v Record, mutate func() error) error

	// Migrate explicitly advances the on-disk record to the current schema by
	// replaying the record's migration ladder. For use by `yoloai system migrate`
	// only; Load deliberately does not call this.
	//
	// Acquires the per-sandbox flock, reads the on-disk record, runs the typed
	// migrate function if the version is older, then writes atomically.
	// No-ops when the on-disk version already equals SchemaVersion().
	Migrate(v Record) error

	// Sub returns a Handle for a named child scope. The child's storage is
	// nested under this Handle's scope — e.g. a sub-section of the same JSON
	// document, or a child directory. Components that own a subtree call Sub
	// to get a handle they can pass to child components.
	Sub(name string) Handle
}

// OpenDomain opens a scoped Handle rooted at dir for a specific doc filename.
// The handle maps the logical persistence tree to a single JSON document at
// filepath.Join(dir, docFile). It uses the per-sandbox flock at lockPath for
// mutation serialisation.
//
// OpenDomain does NOT check the on-disk version — that check is deferred to
// Load/Update/Migrate so the caller can decide how to handle the mismatch.
// It does not auto-migrate.
func OpenDomain(dir string, docFile string, lockPath string) Handle {
	return &fileHandle{
		dir:      dir,
		docFile:  docFile,
		lockPath: lockPath,
		subPath:  nil, // root handle — no sub-path prefix
	}
}

// fileHandle implements Handle over a single JSON document on disk. Sub-handles
// share the same file/lock but address a named sub-object within the JSON
// document.
type fileHandle struct {
	dir      string
	docFile  string
	lockPath string
	subPath  []string // nil = root; non-nil = nested sub-section path
}

func (h *fileHandle) docPath() string {
	return filepath.Join(h.dir, h.docFile)
}

// atomicWriteJSON writes data to a temp file in the same dir, fsyncs it,
// renames to dest, then fsyncs the dir for durability (D87 §3).
func atomicWriteJSON(dir, dest string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return fmt.Errorf("store: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Clean up on failure.
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("store: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("store: fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close temp file: %w", err)
	}

	// Fix ownership under sudo before rename so the final file is owned
	// by the real user, not root.
	if err := fileutil.ChownIfSudo(tmpName); err != nil {
		return fmt.Errorf("store: chown temp file: %w", err)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("store: rename to %s: %w", dest, err)
	}

	// fsync the directory to make the rename durable.
	d, err := os.Open(dir) //nolint:gosec // G304: dir is a trusted sandbox subdirectory
	if err != nil {
		return fmt.Errorf("store: open dir for fsync: %w", err)
	}
	defer d.Close() //nolint:errcheck
	if err := d.Sync(); err != nil {
		return fmt.Errorf("store: fsync dir: %w", err)
	}

	ok = true
	return nil
}

// readDoc reads the full JSON document as a map[string]json.RawMessage.
// Returns nil map + nil error when the file is absent.
func (h *fileHandle) readDoc() (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(h.docPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: read %s: %w", h.docFile, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("store: parse %s: %w", h.docFile, err)
	}
	return doc, nil
}

// extractSection navigates the sub-path within doc and returns the leaf
// map and the version encoded in it. Returns nil map when the section
// is missing.
func (h *fileHandle) extractSection(doc map[string]json.RawMessage) (map[string]json.RawMessage, int, error) {
	if len(h.subPath) == 0 {
		// Root handle: version lives at top level.
		ver, err := extractVersion(doc)
		return doc, ver, err
	}

	// Navigate the sub-path, treating each key as a nested JSON object.
	current := doc
	for i, key := range h.subPath {
		raw, ok := current[key]
		if !ok {
			return nil, 0, nil // section absent — treat as file-absent
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil, 0, fmt.Errorf("store: parse sub-section %q in %s: %w", key, h.docFile, err)
		}
		if i == len(h.subPath)-1 {
			ver, err := extractVersion(nested)
			return nested, ver, err
		}
		current = nested
	}
	return nil, 0, nil
}

// extractVersion reads the "version" int from a JSON map. Returns 0 when absent.
func extractVersion(m map[string]json.RawMessage) (int, error) {
	raw, ok := m["version"]
	if !ok {
		return 0, nil
	}
	var ver int
	if err := json.Unmarshal(raw, &ver); err != nil {
		return 0, fmt.Errorf("store: parse version field: %w", err)
	}
	return ver, nil
}

// checkVersion compares on-disk version against the record's schema version.
// Returns nil when they match. Returns ErrNeedsMigration or ErrTooNew otherwise.
func checkVersion(onDisk, schemaVersion int) error {
	if onDisk > schemaVersion {
		return ErrTooNew
	}
	if onDisk < schemaVersion {
		return ErrNeedsMigration
	}
	return nil
}

// Load reads the record from storage. Lock-free; safe to call concurrently.
func (h *fileHandle) Load(v Record) (bool, error) {
	doc, err := h.readDoc()
	if err != nil {
		return false, err
	}
	if doc == nil {
		return false, nil // file absent
	}

	section, onDiskVer, err := h.extractSection(doc)
	if err != nil {
		return false, err
	}
	if section == nil {
		return false, nil // sub-section absent
	}

	if err := checkVersion(onDiskVer, v.SchemaVersion()); err != nil {
		return false, err
	}

	// Re-marshal the section back to JSON then unmarshal into v.
	sectionBytes, err := json.Marshal(section)
	if err != nil {
		return false, fmt.Errorf("store: re-marshal section: %w", err)
	}
	if err := json.Unmarshal(sectionBytes, v); err != nil {
		return false, fmt.Errorf("store: unmarshal %s into record: %w", h.docFile, err)
	}
	return true, nil
}

// stampVersion injects or overwrites the "version" key in a JSON-marshaled
// struct, returning the updated JSON bytes.
func stampVersion(v Record) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("store: marshal record: %w", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("store: unmarshal for version stamp: %w", err)
	}
	verBytes, err := json.Marshal(v.SchemaVersion())
	if err != nil {
		return nil, fmt.Errorf("store: marshal version: %w", err)
	}
	m["version"] = verBytes
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("store: re-marshal with version: %w", err)
	}
	return out, nil
}

// setSection writes sectionBytes into doc at the sub-path (or replaces the
// entire doc for root handles) and returns the full marshaled document.
func (h *fileHandle) setSection(doc map[string]json.RawMessage, sectionBytes []byte) ([]byte, error) {
	if len(h.subPath) == 0 {
		// Root handle: the section IS the document.
		return sectionBytes, nil
	}

	if doc == nil {
		doc = make(map[string]json.RawMessage)
	}

	// Navigate/create intermediate objects, then set the leaf.
	err := setNested(doc, h.subPath, json.RawMessage(sectionBytes))
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc, "", "  ")
}

// setNested recursively sets keys[0] in m, creating intermediate maps as needed.
func setNested(m map[string]json.RawMessage, keys []string, value json.RawMessage) error {
	if len(keys) == 1 {
		m[keys[0]] = value
		return nil
	}
	// Navigate one level deeper.
	var nested map[string]json.RawMessage
	if raw, ok := m[keys[0]]; ok {
		if err := json.Unmarshal(raw, &nested); err != nil {
			return fmt.Errorf("store: parse intermediate key %q: %w", keys[0], err)
		}
	} else {
		nested = make(map[string]json.RawMessage)
	}
	if err := setNested(nested, keys[1:], value); err != nil {
		return err
	}
	b, err := json.Marshal(nested)
	if err != nil {
		return fmt.Errorf("store: marshal intermediate key %q: %w", keys[0], err)
	}
	m[keys[0]] = b
	return nil
}

// Save writes v as a new record. For initial creation; does not acquire the
// per-sandbox flock. Stamps v.SchemaVersion() into the on-disk "version" field.
func (h *fileHandle) Save(v Record) error {
	sectionBytes, err := stampVersion(v)
	if err != nil {
		return err
	}

	var doc map[string]json.RawMessage
	if len(h.subPath) > 0 {
		doc, err = h.readDoc()
		if err != nil {
			return err
		}
	}

	docBytes, err := h.setSection(doc, sectionBytes)
	if err != nil {
		return err
	}

	if err := fileutil.MkdirAll(h.dir, 0o700); err != nil {
		return fmt.Errorf("store: create dir %s: %w", h.dir, err)
	}
	return atomicWriteJSON(h.dir, h.docPath(), docBytes)
}

// Update acquires the per-sandbox flock, re-reads the record, calls mutate,
// then atomically writes the result.
func (h *fileHandle) Update(v Record, mutate func() error) error {
	release, err := locking.AcquireBlocking(h.lockPath)
	if err != nil {
		return fmt.Errorf("store: acquire lock for update: %w", err)
	}
	defer release()

	// Re-read under the lock.
	doc, err := h.readDoc()
	if err != nil {
		return err
	}

	if doc != nil {
		section, onDiskVer, err := h.extractSection(doc)
		if err != nil {
			return err
		}
		if section != nil {
			// Version check — detect concurrent migration or lost update.
			if err := checkVersion(onDiskVer, v.SchemaVersion()); err != nil {
				return err
			}
			// Unmarshal the on-disk data into v so mutate sees the freshest state.
			sectionBytes, err := json.Marshal(section)
			if err != nil {
				return fmt.Errorf("store: re-marshal section for update: %w", err)
			}
			if err := json.Unmarshal(sectionBytes, v); err != nil {
				return fmt.Errorf("store: unmarshal %s for update: %w", h.docFile, err)
			}
		}
	}

	if err := mutate(); err != nil {
		return err
	}

	sectionBytes, err := stampVersion(v)
	if err != nil {
		return err
	}
	docBytes, err := h.setSection(doc, sectionBytes)
	if err != nil {
		return err
	}

	if err := fileutil.MkdirAll(h.dir, 0o700); err != nil {
		return fmt.Errorf("store: create dir %s: %w", h.dir, err)
	}
	return atomicWriteJSON(h.dir, h.docPath(), docBytes)
}

// Migrate explicitly advances the on-disk record to the current schema.
// Acquires the per-sandbox flock. No-ops when the on-disk version already
// equals SchemaVersion(). Returns an error if v does not implement Migrator.
func (h *fileHandle) Migrate(v Record) error {
	m, ok := v.(Migrator)
	if !ok {
		return fmt.Errorf("store: migrate %s: record type does not implement Migrator", h.docFile)
	}

	release, err := locking.AcquireBlocking(h.lockPath)
	if err != nil {
		return fmt.Errorf("store: acquire lock for migrate: %w", err)
	}
	defer release()

	doc, err := h.readDoc()
	if err != nil {
		return err
	}
	if doc == nil {
		return nil // file absent — nothing to migrate
	}

	section, onDiskVer, err := h.extractSection(doc)
	if err != nil {
		return err
	}
	if section == nil {
		return nil // sub-section absent — nothing to migrate
	}

	if onDiskVer == v.SchemaVersion() {
		return nil // already current — no-op
	}
	if onDiskVer > v.SchemaVersion() {
		return ErrTooNew
	}

	// Unmarshal the on-disk record into v, then run the migration ladder.
	sectionBytes, err := json.Marshal(section)
	if err != nil {
		return fmt.Errorf("store: re-marshal section for migrate: %w", err)
	}
	if err := json.Unmarshal(sectionBytes, v); err != nil {
		return fmt.Errorf("store: unmarshal %s for migrate: %w", h.docFile, err)
	}
	if err := m.MigrateRecord(); err != nil {
		return fmt.Errorf("store: migrate %s: %w", h.docFile, err)
	}

	newSectionBytes, err := stampVersion(v)
	if err != nil {
		return err
	}
	docBytes, err := h.setSection(doc, newSectionBytes)
	if err != nil {
		return err
	}

	if err := fileutil.MkdirAll(h.dir, 0o700); err != nil {
		return fmt.Errorf("store: create dir %s: %w", h.dir, err)
	}
	return atomicWriteJSON(h.dir, h.docPath(), docBytes)
}

// Sub returns a Handle for a named child scope, nested under this handle's
// sub-path within the same JSON document.
func (h *fileHandle) Sub(name string) Handle {
	newPath := make([]string, len(h.subPath)+1)
	copy(newPath, h.subPath)
	newPath[len(h.subPath)] = name
	return &fileHandle{
		dir:      h.dir,
		docFile:  h.docFile,
		lockPath: h.lockPath,
		subPath:  newPath,
	}
}
