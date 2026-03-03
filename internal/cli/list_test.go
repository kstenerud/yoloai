package cli

// ABOUTME: Unit tests for list command filtering and formatting helpers.

import (
	"testing"
	"time"

	"github.com/kstenerud/yoloai/sandbox"
	"github.com/stretchr/testify/assert"
)

func makeInfo(name string, status sandbox.Status, agent, profile, changes string) *sandbox.Info {
	return &sandbox.Info{
		Meta: &sandbox.Meta{
			Name:      name,
			Agent:     agent,
			Profile:   profile,
			CreatedAt: time.Now(),
			Workdir:   sandbox.WorkdirMeta{HostPath: "/tmp/" + name},
		},
		Status:     status,
		HasChanges: changes,
		DiskUsage:  "1.0MB",
	}
}

func makeBrokenInfo(name string) *sandbox.Info {
	return &sandbox.Info{
		Meta:       &sandbox.Meta{Name: name},
		Status:     sandbox.StatusBroken,
		HasChanges: "-",
		DiskUsage:  "-",
	}
}

func TestFilterInfos_NoFilters(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "no"),
		makeInfo("b", sandbox.StatusStopped, "gemini", "go-dev", "yes"),
	}
	result := filterInfos(infos, listFilters{})
	assert.Len(t, result, 2)
}

func TestFilterInfos_Running(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "no"),
		makeInfo("b", sandbox.StatusStopped, "gemini", "", "no"),
		makeInfo("c", sandbox.StatusDone, "claude", "", "no"),
	}
	result := filterInfos(infos, listFilters{running: true})
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Meta.Name)
}

func TestFilterInfos_Stopped(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "no"),
		makeInfo("b", sandbox.StatusStopped, "gemini", "", "no"),
		makeInfo("c", sandbox.StatusStopped, "claude", "", "yes"),
	}
	result := filterInfos(infos, listFilters{stopped: true})
	assert.Len(t, result, 2)
	assert.Equal(t, "b", result[0].Meta.Name)
	assert.Equal(t, "c", result[1].Meta.Name)
}

func TestFilterInfos_Agent(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "no"),
		makeInfo("b", sandbox.StatusRunning, "gemini", "", "no"),
		makeInfo("c", sandbox.StatusStopped, "claude", "", "yes"),
	}
	result := filterInfos(infos, listFilters{agent: "claude"})
	assert.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Meta.Name)
	assert.Equal(t, "c", result[1].Meta.Name)
}

func TestFilterInfos_AgentExcludesBroken(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "no"),
		makeBrokenInfo("b"),
	}
	result := filterInfos(infos, listFilters{agent: "claude"})
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Meta.Name)
}

func TestFilterInfos_ProfileBase(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "no"),     // empty = base
		makeInfo("b", sandbox.StatusRunning, "claude", "base", "no"), // explicit base
		makeInfo("c", sandbox.StatusRunning, "claude", "go-dev", "no"),
	}
	result := filterInfos(infos, listFilters{profile: "base"})
	assert.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Meta.Name)
	assert.Equal(t, "b", result[1].Meta.Name)
}

func TestFilterInfos_ProfileNamed(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "no"),
		makeInfo("b", sandbox.StatusRunning, "claude", "go-dev", "no"),
	}
	result := filterInfos(infos, listFilters{profile: "go-dev"})
	assert.Len(t, result, 1)
	assert.Equal(t, "b", result[0].Meta.Name)
}

func TestFilterInfos_ProfileExcludesBroken(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "go-dev", "no"),
		makeBrokenInfo("b"),
	}
	result := filterInfos(infos, listFilters{profile: "go-dev"})
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Meta.Name)
}

func TestFilterInfos_Changes(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "yes"),
		makeInfo("b", sandbox.StatusRunning, "claude", "", "no"),
		makeInfo("c", sandbox.StatusStopped, "gemini", "", "yes"),
	}
	result := filterInfos(infos, listFilters{changes: true})
	assert.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Meta.Name)
	assert.Equal(t, "c", result[1].Meta.Name)
}

func TestFilterInfos_Combined(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusRunning, "claude", "", "yes"),
		makeInfo("b", sandbox.StatusRunning, "claude", "", "no"),
		makeInfo("c", sandbox.StatusRunning, "gemini", "", "yes"),
		makeInfo("d", sandbox.StatusStopped, "claude", "", "yes"),
	}
	result := filterInfos(infos, listFilters{running: true, agent: "claude", changes: true})
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Meta.Name)
}

func TestFilterInfos_AllFiltered(t *testing.T) {
	infos := []*sandbox.Info{
		makeInfo("a", sandbox.StatusStopped, "claude", "", "no"),
	}
	result := filterInfos(infos, listFilters{running: true})
	assert.Empty(t, result)
}

func TestFormatProfile_Empty(t *testing.T) {
	assert.Equal(t, "(base)", formatProfile(""))
}

func TestFormatProfile_Named(t *testing.T) {
	assert.Equal(t, "go-dev", formatProfile("go-dev"))
}

func TestFormatProfile_Base(t *testing.T) {
	assert.Equal(t, "base", formatProfile("base"))
}
