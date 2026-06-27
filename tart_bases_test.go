// ABOUTME: Unit tests for the platform-independent pieces of the TartBases
// ABOUTME: admin handle (name parsing, version conversion, error messages).
package yoloai

import (
	"testing"

	tartrt "github.com/kstenerud/yoloai/runtime/tart"
)

func TestCacheKeyFromBaseName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"yoloai-base", ""},
		{"yoloai-base-ios-26.4", "ios-26.4"},
		{"yoloai-base-ios-26.4-tvos-26.1", "ios-26.4-tvos-26.1"},
	}
	for _, tc := range cases {
		if got := cacheKeyFromBaseName(tc.name); got != tc.want {
			t.Errorf("cacheKeyFromBaseName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTartVersionConversionRoundTrip(t *testing.T) {
	internal := []tartrt.RuntimeVersion{
		{Platform: "ios", Version: "26.4", Build: "23B86"},
		{Platform: "tvos", Version: "26.1", Build: "23C12"},
	}
	pub := tartVersionsToPublic(internal)
	if len(pub) != len(internal) {
		t.Fatalf("toPublic len = %d, want %d", len(pub), len(internal))
	}
	for i := range pub {
		if pub[i].Platform != internal[i].Platform || pub[i].Version != internal[i].Version || pub[i].Build != internal[i].Build {
			t.Errorf("toPublic[%d] = %+v, want match of %+v", i, pub[i], internal[i])
		}
	}
	back := tartVersionsToInternal(pub)
	for i := range back {
		if back[i] != internal[i] {
			t.Errorf("round-trip[%d] = %+v, want %+v", i, back[i], internal[i])
		}
	}
}

func TestTartVersionConversionNil(t *testing.T) {
	// Public output is normalized to a non-nil empty slice so it marshals to
	// JSON [] rather than null (consistent with the other List/slice surfaces).
	got := tartVersionsToPublic(nil)
	if got == nil {
		t.Error("tartVersionsToPublic(nil) should be a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("tartVersionsToPublic(nil) should be empty, got %d", len(got))
	}
	// The internal conversion feeds the backend, not a JSON surface — nil stays nil.
	if tartVersionsToInternal(nil) != nil {
		t.Error("tartVersionsToInternal(nil) should be nil")
	}
}

func TestTartBaseErrorMessages(t *testing.T) {
	exists := &TartBaseExistsError{Name: "yoloai-base-ios-26.4"}
	if exists.Error() == "" || exists.Name != "yoloai-base-ios-26.4" {
		t.Errorf("unexpected exists error: %q", exists.Error())
	}
	notFound := &TartBaseNotFoundError{Name: "yoloai-base-missing"}
	if notFound.Error() == "" || notFound.Name != "yoloai-base-missing" {
		t.Errorf("unexpected not-found error: %q", notFound.Error())
	}
}
