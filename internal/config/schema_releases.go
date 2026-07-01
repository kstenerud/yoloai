// ABOUTME: Maps each library schema version to the first released yoloai tag that
// ABOUTME: shipped it, so `system migrate` can name real releases to downgrade to.

package config

// SchemaRelease records that a library schema version first shipped in a given
// released git tag.
type SchemaRelease struct {
	Schema int
	Tag    string
}

// LibrarySchemaReleases lists the first released tag at each library schema
// version, oldest first — the schema change-points only (a schema spans from its
// tag up to the next entry's tag). It lets `system migrate` tell a user whose
// data dir is blocked at an older schema which concrete prior releases can still
// read it, so they can downgrade, fix the blocker, and upgrade again.
//
// MAINTENANCE: append the new schema's first tag WHEN a release bumps
// LibrarySchemaVersion — an entry here asserts "this schema shipped in this tag,"
// so only add real, released tags. The library/cli-split release (schema 3) and
// the :overlay-retirement release (schema 4) are intentionally absent until they
// ship; PriorReleaseRange degrades gracefully (open-ended upper bound) until then.
var LibrarySchemaReleases = []SchemaRelease{
	{Schema: 0, Tag: "v0.1.0"}, // pre-.schema-version (flat) era: v0.1.0 – v0.2.6
	{Schema: 1, Tag: "v0.3.0"}, // v0.3.0
	{Schema: 2, Tag: "v0.4.0"}, // v0.4.0 – v0.5.2
}

// PriorReleaseRange returns the release-tag range whose builds read a data dir at
// library schema onDisk: builds from `from` (inclusive) up to `to` (exclusive)
// use that schema. `to` is "" when no released tag yet carries a higher schema —
// the range is then open-ended up to the version currently being upgraded from.
// ok is false when onDisk predates the registry (no known tag).
func PriorReleaseRange(onDisk int) (from, to string, ok bool) {
	for _, r := range LibrarySchemaReleases {
		switch {
		case r.Schema == onDisk:
			from, ok = r.Tag, true
		case r.Schema > onDisk && to == "":
			to = r.Tag // first released tag past onDisk's schema (entries are ascending)
		}
	}
	return from, to, ok
}
