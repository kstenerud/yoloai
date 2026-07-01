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
		{"schema 2 -> open-ended (3+ unreleased)", 2, "v0.4.0", "", true},
		{"unknown newer schema", 3, "", "", false},
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

// The registry must stay ascending by schema with no gaps, so PriorReleaseRange's
// "first entry past onDisk" scan is correct.
func TestLibrarySchemaReleases_AscendingContiguous(t *testing.T) {
	for i, r := range LibrarySchemaReleases {
		if r.Schema != i {
			t.Errorf("entry %d has schema %d; registry must be contiguous from 0", i, r.Schema)
		}
		if r.Tag == "" {
			t.Errorf("schema %d has an empty tag", r.Schema)
		}
	}
}
