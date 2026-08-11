// ABOUTME: PriorReleaseRange lookup and the invariant that its registry stays
// ABOUTME: strictly ascending by schema, which the prefix scan relies on.

package config

import "testing"

func TestPriorReleaseRange(t *testing.T) {
	for _, tc := range []struct {
		name     string
		onDisk   int
		from, to string
		ok       bool
	}{
		{"flat era -> up to schema 1", 0, "v0.1.0", "v0.3.0", true},
		{"schema 1 -> up to schema 2", 1, "v0.3.0", "v0.4.0", true},
		{"schema 2 -> up to schema 4 (v0.4.0–v0.5.2)", 2, "v0.4.0", "v0.6.0", true},
		{"schema 3 never published -> no named 'from'", 3, "", "v0.6.0", false},
		{"schema 4 -> up to schema 5", 4, "v0.6.0", "v0.9.0", true},
		// v0.11.0 shipping schema 6 closed this range. While 5 was the newest
		// schema the upper bound was open, and the guidance said "v0.9.0 or
		// newer" — which after v0.11.0 would name a release that cannot read a
		// v5 directory at all, sending someone upgrading FROM v5 to downgrade
		// INTO the version they are upgrading to.
		{"schema 5 -> bounded once v0.11.0 shipped schema 6", 5, "v0.9.0", "v0.11.0", true},
		{"schema 6 -> newest until the release drain records schema 7", 6, "v0.11.0", "", true},
		{"unreleased schema 7 has no published downgrade target", 7, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			from, to, ok := PriorReleaseRange(tc.onDisk)
			if from != tc.from || to != tc.to || ok != tc.ok {
				t.Errorf("PriorReleaseRange(%d) = (%q,%q,%v), want (%q,%q,%v)",
					tc.onDisk, from, to, ok, tc.from, tc.to, tc.ok)
			}
		})
	}
}

// The registry must stay strictly ascending by schema so PriorReleaseRange's
// "first entry past onDisk" scan is correct. Gaps are allowed: a schema that
// never shipped a public tag (schema 3 folded into v0.6.0) is simply absent.
func TestLibrarySchemaReleases_StrictlyAscending(t *testing.T) {
	prev := -1
	for i, r := range LibrarySchemaReleases {
		if r.Schema <= prev {
			t.Errorf("entry %d has schema %d; registry must be strictly ascending (prev %d)", i, r.Schema, prev)
		}
		if r.Tag == "" {
			t.Errorf("schema %d has an empty tag", r.Schema)
		}
		prev = r.Schema
	}
}
