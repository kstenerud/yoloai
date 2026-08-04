// ABOUTME: Records exchange names removed while a guest was running, so a later put of the same
// ABOUTME: name is refused rather than silently delivering the removed file's bytes (DF175).
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/fileutil"
)

// staleNamesFileName holds names the guest may still be caching. It lives in the
// HOST tier deliberately: the guest must never see it, and the guest is the one
// component whose view of this share cannot be trusted.
const staleNamesFileName = "guest-stale-names.json"

// StaleNamesPath is exported so tests and diagnostics can name the record.
func StaleNamesPath(sandboxDir string) string {
	return filepath.Join(config.HostTierDir(sandboxDir), staleNamesFileName)
}

// loadStaleNames returns the recorded names. A missing or unreadable record is an
// empty set rather than an error: this guards against a data-integrity hazard, and
// failing a `files put` because a bookkeeping file is unreadable would be a worse
// outcome than the hazard for every user who is not hitting it.
func LoadStaleNames(sandboxDir string) map[string]bool {
	data, err := os.ReadFile(StaleNamesPath(sandboxDir))
	if err != nil {
		return nil
	}
	var names []string
	if json.Unmarshal(data, &names) != nil {
		return nil
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// recordStaleName marks rel as a name the guest may still be caching.
func RecordStaleName(sandboxDir, rel string) error {
	set := LoadStaleNames(sandboxDir)
	if set[rel] {
		return nil
	}
	if set == nil {
		set = map[string]bool{}
	}
	set[rel] = true
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names) // stable on disk, so a diff of two records is readable
	data, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("encode stale-name record: %w", err)
	}
	path := StaleNamesPath(sandboxDir)
	if err := fileutil.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create host tier: %w", err)
	}
	if err := fileutil.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write stale-name record: %w", err)
	}
	return nil
}

// ClearStaleNames drops the record. A guest that has just booted holds no page
// cache from the previous one, so every name is safe again — this is what makes
// "restart the sandbox" a real remedy rather than advice.
func ClearStaleNames(sandboxDir string) error {
	err := os.Remove(StaleNamesPath(sandboxDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear stale-name record: %w", err)
	}
	return nil
}

// errStaleName is returned when a put would recreate a name the guest may still be
// caching. The message carries both remedies, because they differ in cost and the
// cheap one is easy to miss: a different name always works and needs no restart.
func ErrStaleName(sandbox, rel string) error {
	return fmt.Errorf(
		"%q was removed from this sandbox's exchange directory while it was running, and this "+
			"backend's guest can keep serving the removed file's contents for that name — writing "+
			"it again would deliver bytes the sandbox cannot read correctly. "+
			"Use a different name, or restart the sandbox to clear it "+
			"(yoloai stop %s && yoloai start %s)",
		rel, sandbox, sandbox)
}
