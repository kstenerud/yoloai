package sandboxcmd

// ABOUTME: Unit tests for list command filtering and formatting helpers.

import (
	"testing"
	"time"

	yoloai "github.com/kstenerud/yoloai"
	agentpkg "github.com/kstenerud/yoloai/internal/agent"
	"github.com/stretchr/testify/assert"
)

func makeInfo(name string, status yoloai.Status, agent, profile, changes string) *yoloai.Info {
	return &yoloai.Info{
		Environment: &yoloai.Environment{
			Name:      name,
			Agent:     agentpkg.AgentName(agent),
			Profile:   profile,
			CreatedAt: time.Now(),
			Workdir:   yoloai.WorkdirInfo{HostPath: "/tmp/" + name},
		},
		Status:         status,
		HasChanges:     changes,
		DiskUsageBytes: 1024 * 1024,
	}
}

func makeBrokenInfo(name string) *yoloai.Info {
	return &yoloai.Info{
		Environment:    &yoloai.Environment{Name: name},
		Status:         yoloai.StatusBroken,
		HasChanges:     "-",
		DiskUsageBytes: -1,
	}
}

func TestFilterInfos_NoFilters(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("b", yoloai.StatusStopped, "gemini", "go-dev", "yes"),
	}
	result := filterInfos(infos, listFilters{})
	assert.Len(t, result, 2)
}

func TestFilterInfos_Active(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("b", yoloai.StatusStopped, "gemini", "", "no"),
		makeInfo("c", yoloai.StatusDone, "claude", "", "no"),
		makeInfo("d", yoloai.StatusIdle, "claude", "", "no"),
	}
	result := filterInfos(infos, listFilters{active: true})
	assert.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Environment.Name)
	assert.Equal(t, "d", result[1].Environment.Name)
}

func TestFilterInfos_Idle(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("b", yoloai.StatusIdle, "gemini", "", "no"),
		makeInfo("c", yoloai.StatusDone, "claude", "", "no"),
		makeInfo("d", yoloai.StatusIdle, "claude", "", "yes"),
	}
	result := filterInfos(infos, listFilters{idle: true})
	assert.Len(t, result, 2)
	assert.Equal(t, "b", result[0].Environment.Name)
	assert.Equal(t, "d", result[1].Environment.Name)
}

func TestFilterInfos_Done(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("b", yoloai.StatusDone, "gemini", "", "no"),
		makeInfo("c", yoloai.StatusFailed, "claude", "", "no"),
		makeInfo("d", yoloai.StatusStopped, "claude", "", "no"),
	}
	result := filterInfos(infos, listFilters{done: true})
	assert.Len(t, result, 2)
	assert.Equal(t, "b", result[0].Environment.Name)
	assert.Equal(t, "c", result[1].Environment.Name)
}

func TestFilterInfos_Stopped(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("b", yoloai.StatusStopped, "gemini", "", "no"),
		makeInfo("c", yoloai.StatusStopped, "claude", "", "yes"),
	}
	result := filterInfos(infos, listFilters{stopped: true})
	assert.Len(t, result, 2)
	assert.Equal(t, "b", result[0].Environment.Name)
	assert.Equal(t, "c", result[1].Environment.Name)
}

func TestFilterInfos_Agent(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("b", yoloai.StatusActive, "gemini", "", "no"),
		makeInfo("c", yoloai.StatusStopped, "claude", "", "yes"),
	}
	result := filterInfos(infos, listFilters{agent: "claude"})
	assert.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Environment.Name)
	assert.Equal(t, "c", result[1].Environment.Name)
}

func TestFilterInfos_AgentExcludesBroken(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),
		makeBrokenInfo("b"),
	}
	result := filterInfos(infos, listFilters{agent: "claude"})
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Environment.Name)
}

func TestFilterInfos_ProfileBase(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),     // empty = base
		makeInfo("b", yoloai.StatusActive, "claude", "base", "no"), // explicit base
		makeInfo("c", yoloai.StatusActive, "claude", "go-dev", "no"),
	}
	result := filterInfos(infos, listFilters{profile: "base"})
	assert.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Environment.Name)
	assert.Equal(t, "b", result[1].Environment.Name)
}

func TestFilterInfos_ProfileNamed(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("b", yoloai.StatusActive, "claude", "go-dev", "no"),
	}
	result := filterInfos(infos, listFilters{profile: "go-dev"})
	assert.Len(t, result, 1)
	assert.Equal(t, "b", result[0].Environment.Name)
}

func TestFilterInfos_ProfileExcludesBroken(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "go-dev", "no"),
		makeBrokenInfo("b"),
	}
	result := filterInfos(infos, listFilters{profile: "go-dev"})
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Environment.Name)
}

func TestFilterInfos_Changes(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "yes"),
		makeInfo("b", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("c", yoloai.StatusStopped, "gemini", "", "yes"),
	}
	result := filterInfos(infos, listFilters{changes: true})
	assert.Len(t, result, 2)
	assert.Equal(t, "a", result[0].Environment.Name)
	assert.Equal(t, "c", result[1].Environment.Name)
}

func TestFilterInfos_Combined(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusActive, "claude", "", "yes"),
		makeInfo("b", yoloai.StatusActive, "claude", "", "no"),
		makeInfo("c", yoloai.StatusActive, "gemini", "", "yes"),
		makeInfo("d", yoloai.StatusStopped, "claude", "", "yes"),
	}
	result := filterInfos(infos, listFilters{active: true, agent: "claude", changes: true})
	assert.Len(t, result, 1)
	assert.Equal(t, "a", result[0].Environment.Name)
}

func TestFilterInfos_AllFiltered(t *testing.T) {
	infos := []*yoloai.Info{
		makeInfo("a", yoloai.StatusStopped, "claude", "", "no"),
	}
	result := filterInfos(infos, listFilters{active: true})
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
