package tart

import (
	"encoding/json"
	"testing"
)

func TestParseRuntime(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantPlatform string
		wantVersion  string
		wantErr      bool
	}{
		{
			name:         "ios without version",
			input:        "ios",
			wantPlatform: "ios",
			wantVersion:  "",
			wantErr:      false,
		},
		{
			name:         "iOS with uppercase",
			input:        "iOS",
			wantPlatform: "ios",
			wantVersion:  "",
			wantErr:      false,
		},
		{
			name:         "ios with version",
			input:        "ios:26.2",
			wantPlatform: "ios",
			wantVersion:  "26.2",
			wantErr:      false,
		},
		{
			name:         "tvos with version",
			input:        "tvos:26.1",
			wantPlatform: "tvos",
			wantVersion:  "26.1",
			wantErr:      false,
		},
		{
			name:         "watchos without version",
			input:        "watchos",
			wantPlatform: "watchos",
			wantVersion:  "",
			wantErr:      false,
		},
		{
			name:         "visionos without version",
			input:        "visionos",
			wantPlatform: "visionos",
			wantVersion:  "",
			wantErr:      false,
		},
		{
			name:         "latest keyword",
			input:        "ios:latest",
			wantPlatform: "ios",
			wantVersion:  "",
			wantErr:      false,
		},
		{
			name:    "invalid platform",
			input:   "android",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, version, err := ParseRuntime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRuntime() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if platform != tt.wantPlatform {
					t.Errorf("ParseRuntime() platform = %v, want %v", platform, tt.wantPlatform)
				}
				if version != tt.wantVersion {
					t.Errorf("ParseRuntime() version = %v, want %v", version, tt.wantVersion)
				}
			}
		})
	}
}

func TestGenerateCacheKey(t *testing.T) {
	tests := []struct {
		name     string
		runtimes []RuntimeVersion
		want     string
	}{
		{
			name: "single runtime",
			runtimes: []RuntimeVersion{
				{Platform: "ios", Version: "26.2", Build: "23B86"},
			},
			want: "ios-26.2",
		},
		{
			name: "multiple runtimes sorted by platform",
			runtimes: []RuntimeVersion{
				{Platform: "tvos", Version: "26.1", Build: "23B85"},
				{Platform: "ios", Version: "26.2", Build: "23B86"},
			},
			want: "ios-26.2-tvos-26.1",
		},
		{
			name: "same platform different versions",
			runtimes: []RuntimeVersion{
				{Platform: "ios", Version: "26.2", Build: "23B86"},
				{Platform: "ios", Version: "25.1", Build: "22F70"},
			},
			want: "ios-25.1-ios-26.2",
		},
		{
			name: "all platforms",
			runtimes: []RuntimeVersion{
				{Platform: "watchos", Version: "11.0", Build: "22R5"},
				{Platform: "visionos", Version: "2.0", Build: "21N5"},
				{Platform: "tvos", Version: "26.1", Build: "23B85"},
				{Platform: "ios", Version: "26.2", Build: "23B86"},
			},
			want: "ios-26.2-tvos-26.1-visionos-2.0-watchos-11.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateCacheKey(tt.runtimes)
			if got != tt.want {
				t.Errorf("GenerateCacheKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateCacheKeyDeterministic(t *testing.T) {
	// Test that the same runtimes in different order produce the same cache key
	runtimes1 := []RuntimeVersion{
		{Platform: "ios", Version: "26.2", Build: "23B86"},
		{Platform: "tvos", Version: "26.1", Build: "23B85"},
	}
	runtimes2 := []RuntimeVersion{
		{Platform: "tvos", Version: "26.1", Build: "23B85"},
		{Platform: "ios", Version: "26.2", Build: "23B86"},
	}

	key1 := GenerateCacheKey(runtimes1)
	key2 := GenerateCacheKey(runtimes2)

	if key1 != key2 {
		t.Errorf("GenerateCacheKey() not deterministic: %v != %v", key1, key2)
	}
}

func TestResolveRuntimeVersions_MockData(t *testing.T) {
	// This test uses mock data to test the resolution logic
	// In a real environment, this would query simctl

	// We can't easily mock QueryAvailableRuntimes in this package structure
	// so we'll skip this test for now and rely on integration tests
	t.Skip("Skipping unit test - requires mocking QueryAvailableRuntimes")
}

func TestRuntimeVersionStructure(t *testing.T) {
	// Test that RuntimeVersion struct is properly constructed
	rt := RuntimeVersion{
		Platform: "ios",
		Version:  "26.2",
		Build:    "23B86",
	}

	if rt.Platform != "ios" {
		t.Errorf("Platform = %v, want ios", rt.Platform)
	}
	if rt.Version != "26.2" {
		t.Errorf("Version = %v, want 26.2", rt.Version)
	}
	if rt.Build != "23B86" {
		t.Errorf("Build = %v, want 23B86", rt.Build)
	}
}

func TestSimctlRuntimeJSONParsing(t *testing.T) {
	// Test that we can parse simctl output correctly
	jsonData := `{
		"runtimes": [
			{
				"platform": "iOS",
				"version": "26.2",
				"buildversion": "23B86",
				"isAvailable": true,
				"bundlePath": "/Library/Developer/CoreSimulator/Volumes/iOS_26_2/iOS.simruntime"
			},
			{
				"platform": "tvOS",
				"version": "26.1",
				"buildversion": "23B85",
				"isAvailable": true,
				"bundlePath": "/Library/Developer/CoreSimulator/Volumes/tvOS_26_1/tvOS.simruntime"
			},
			{
				"platform": "watchOS",
				"version": "11.0",
				"buildversion": "22R5",
				"isAvailable": false,
				"bundlePath": "/Library/Developer/CoreSimulator/Volumes/watchOS_11_0/watchOS.simruntime"
			}
		]
	}`

	var output simctlRuntimesOutput
	err := json.Unmarshal([]byte(jsonData), &output)
	if err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if len(output.Runtimes) != 3 {
		t.Errorf("Expected 3 runtimes, got %d", len(output.Runtimes))
	}

	// Check first runtime
	if output.Runtimes[0].Platform != "iOS" {
		t.Errorf("Platform = %v, want iOS", output.Runtimes[0].Platform)
	}
	if output.Runtimes[0].Version != "26.2" {
		t.Errorf("Version = %v, want 26.2", output.Runtimes[0].Version)
	}
	if output.Runtimes[0].Build != "23B86" {
		t.Errorf("Build = %v, want 23B86", output.Runtimes[0].Build)
	}
	if !output.Runtimes[0].IsAvailable {
		t.Error("Expected runtime to be available")
	}
	if output.Runtimes[0].BundlePath != "/Library/Developer/CoreSimulator/Volumes/iOS_26_2/iOS.simruntime" {
		t.Errorf("BundlePath = %v, want /Library/Developer/CoreSimulator/Volumes/iOS_26_2/iOS.simruntime", output.Runtimes[0].BundlePath)
	}

	// Check that unavailable runtime is parsed
	if output.Runtimes[2].IsAvailable {
		t.Error("Expected third runtime to be unavailable")
	}
}
