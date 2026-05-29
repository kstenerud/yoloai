// ABOUTME: Tests for the pure VM-slot classifier and the `tart run` command-line
// ABOUTME: VM-name parser — the testable core of the doctor VM census.

package tart

import "testing"

func TestClassifyVMSlots(t *testing.T) {
	const limit = 2

	tests := []struct {
		name        string
		procs       []vmProcess
		owners      map[string]bool
		wantInUse   int
		wantBlocked bool
		wantOrphans int
	}{
		{
			name:        "below limit, single owned sandbox",
			procs:       []vmProcess{{PID: 100, VMName: "alpha"}},
			owners:      map[string]bool{"alpha": true},
			wantInUse:   1,
			wantBlocked: false,
			wantOrphans: 0,
		},
		{
			name:        "two owned sandboxes reach the limit, no orphans",
			procs:       []vmProcess{{PID: 100, VMName: "alpha"}, {PID: 200, VMName: "beta"}},
			owners:      map[string]bool{"alpha": true, "beta": true},
			wantInUse:   2,
			wantBlocked: true,
			wantOrphans: 0,
		},
		{
			name:        "one owned plus one orphan reach the limit",
			procs:       []vmProcess{{PID: 100, VMName: "alpha"}, {PID: 200, VMName: "ghost"}},
			owners:      map[string]bool{"alpha": true}, // ghost's launcher is gone
			wantInUse:   2,
			wantBlocked: true,
			wantOrphans: 1,
		},
		{
			name:        "two orphans reach the limit",
			procs:       []vmProcess{{PID: 100, VMName: "ghost1"}, {PID: 200, VMName: "ghost2"}},
			owners:      map[string]bool{},
			wantInUse:   2,
			wantBlocked: true,
			wantOrphans: 2,
		},
		{
			name:        "deleted image is an orphan even if a same-named owner exists",
			procs:       []vmProcess{{PID: 100, VMName: "alpha-tmp", Deleted: true}},
			owners:      map[string]bool{"alpha-tmp": true},
			wantInUse:   1,
			wantBlocked: false,
			wantOrphans: 1,
		},
		{
			name:        "below limit with a lone orphan present",
			procs:       []vmProcess{{PID: 100, VMName: "ghost"}},
			owners:      map[string]bool{},
			wantInUse:   1,
			wantBlocked: false,
			wantOrphans: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := classifyVMSlots(tt.procs, tt.owners, limit)
			if c.Limit != limit {
				t.Errorf("Limit = %d, want %d", c.Limit, limit)
			}
			if got := c.InUse(); got != tt.wantInUse {
				t.Errorf("InUse() = %d, want %d", got, tt.wantInUse)
			}
			if got := c.Blocked(); got != tt.wantBlocked {
				t.Errorf("Blocked() = %v, want %v", got, tt.wantBlocked)
			}
			if got := len(c.Orphans()); got != tt.wantOrphans {
				t.Errorf("Orphans() count = %d, want %d", got, tt.wantOrphans)
			}
		})
	}
}

func TestClassifyVMSlotsOrdersOwnedFirst(t *testing.T) {
	c := classifyVMSlots(
		[]vmProcess{{PID: 300, VMName: "ghost"}, {PID: 100, VMName: "alpha"}},
		map[string]bool{"alpha": true},
		maxConcurrentMacVMs,
	)
	if len(c.Slots) != 2 {
		t.Fatalf("want 2 slots, got %d", len(c.Slots))
	}
	if !c.Slots[0].Owned || c.Slots[0].VMName != "alpha" {
		t.Errorf("first slot should be owned alpha, got %+v", c.Slots[0])
	}
	if c.Slots[1].Owned || c.Slots[1].VMName != "ghost" {
		t.Errorf("second slot should be orphan ghost, got %+v", c.Slots[1])
	}
}

func TestTartRunVMName(t *testing.T) {
	tests := []struct {
		name    string
		cmdline string
		want    string
	}{
		{
			name:    "homebrew absolute path with flag",
			cmdline: "/opt/homebrew/Cellar/tart/2.31.0/libexec/tart.app/Contents/MacOS/tart run --no-graphics yoloai-base-tmp-45bb6c",
			want:    "yoloai-base-tmp-45bb6c",
		},
		{
			name:    "bare tart with no flags",
			cmdline: "tart run my-vm",
			want:    "my-vm",
		},
		{
			name:    "multiple flags before name",
			cmdline: "tart run --no-graphics --dir=foo:/bar my-vm",
			want:    "my-vm",
		},
		{
			name:    "not a tart run line",
			cmdline: "/usr/bin/ssh user@host",
			want:    "",
		},
		{
			name:    "tart subcommand other than run",
			cmdline: "tart list",
			want:    "",
		},
		{
			name:    "tart run with no positional arg",
			cmdline: "tart run --help",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tartRunVMName(tt.cmdline); got != tt.want {
				t.Errorf("tartRunVMName(%q) = %q, want %q", tt.cmdline, got, tt.want)
			}
		})
	}
}
