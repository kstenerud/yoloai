// ABOUTME: Tests the stamp-last v6->v7 DNS snapshot framework migration.
package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/internal/testutil"
	"github.com/kstenerud/yoloai/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDNSSnapshotMigration_UpgradesAllRecordsBeforeStamp(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	require.NoError(t, config.WriteSchemaVersion(layout.SchemaVersionPath(), config.SchemaTiered))
	for _, name := range []string{"one", "two"} {
		dir := layout.SandboxDir(name)
		require.NoError(t, os.MkdirAll(dir, 0o750))
		testutil.WriteSandboxRecord(t, store.EnvironmentFilePath(dir), []byte(`{"version":3,"name":"`+name+`"}`))
	}

	m := NewDNSSnapshotMigration(layout)
	report, err := m.Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"one", "two"}, report.Migrated)
	version, _, err := config.ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, err)
	assert.Equal(t, config.SchemaDNSSnapshot, version)
	for _, name := range []string{"one", "two"} {
		meta, err := store.LoadEnvironmentFrom(store.EnvironmentFilePath(layout.SandboxDir(name)))
		require.NoError(t, err)
		assert.Equal(t, 4, meta.Version)
		assert.Nil(t, meta.DNS)
	}

	second, err := m.Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err)
	assert.Empty(t, second.Migrated, "rerun after stamp is a no-op")
}

func TestDNSSnapshotMigration_MalformedRecordDoesNotAdvanceStamp(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	require.NoError(t, config.WriteSchemaVersion(layout.SchemaVersionPath(), config.SchemaTiered))
	dir := layout.SandboxDir("bad")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	testutil.WriteSandboxRecord(t, store.EnvironmentFilePath(dir), []byte(`not json`))

	_, err := NewDNSSnapshotMigration(layout).Apply(context.Background(), migrate.Decision{})
	require.Error(t, err)
	version, _, readErr := config.ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, readErr)
	assert.Equal(t, config.SchemaTiered, version, "realm stamp is last")
}

func TestDNSSnapshotMigration_PreflightsMixedV3AndV4BeforeStamping(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	require.NoError(t, config.WriteSchemaVersion(layout.SchemaVersionPath(), config.SchemaTiered))
	for name, record := range map[string]string{
		"old": `{"version":3,"name":"old"}`,
		"new": `{"version":4,"name":"new","dns":["1.1.1.1"]}`,
	} {
		dir := layout.SandboxDir(name)
		require.NoError(t, os.MkdirAll(filepath.Dir(store.EnvironmentFilePath(dir)), 0o750))
		testutil.WriteSandboxRecord(t, store.EnvironmentFilePath(dir), []byte(record))
	}

	report, err := NewDNSSnapshotMigration(layout).Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, report.Migrated)
	old, err := store.LoadEnvironmentFrom(store.EnvironmentFilePath(layout.SandboxDir("old")))
	require.NoError(t, err)
	assert.Equal(t, 4, old.Version)
	assert.Nil(t, old.DNS, "v3 upgrades to an explicit v4 system-DNS snapshot")
	new, err := store.LoadEnvironmentFrom(store.EnvironmentFilePath(layout.SandboxDir("new")))
	require.NoError(t, err)
	assert.Equal(t, []string{"1.1.1.1"}, new.DNS, "v4 DNS is preserved")
}

func TestDNSSnapshotMigration_RefusesFlatOrUnreadableRecordsBeforeMutation(t *testing.T) {
	for name, writeRecord := range map[string]func(t *testing.T, layout config.Layout){
		"flat": func(t *testing.T, layout config.Layout) {
			dir := layout.SandboxDir("flat")
			require.NoError(t, os.MkdirAll(dir, 0o750))
			testutil.WriteSandboxRecord(t, filepath.Join(dir, store.EnvironmentFile), []byte(`{"version":3,"name":"flat"}`))
		},
		"unreadable": func(t *testing.T, layout config.Layout) {
			dir := layout.SandboxDir("unreadable")
			require.NoError(t, os.MkdirAll(store.EnvironmentFilePath(dir), 0o750))
		},
	} {
		t.Run(name, func(t *testing.T) {
			layout := config.NewLayout(t.TempDir())
			require.NoError(t, config.WriteSchemaVersion(layout.SchemaVersionPath(), config.SchemaTiered))
			writeRecord(t, layout)
			_, err := NewDNSSnapshotMigration(layout).Apply(context.Background(), migrate.Decision{})
			require.Error(t, err)
			version, _, readErr := config.ReadSchemaVersion(layout.SchemaVersionPath())
			require.NoError(t, readErr)
			assert.Equal(t, config.SchemaTiered, version, "preflight failure must leave the realm at v6")
		})
	}
}

func TestDNSSnapshotMigration_EarlierSchemaPlansButCannotApply(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	require.NoError(t, config.WriteSchemaVersion(layout.SchemaVersionPath(), config.SchemaPrincipalRenamed))

	m := NewDNSSnapshotMigration(layout)
	plan, err := m.Plan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, plan.Ops, "earlier rungs own the advance to v6")
	_, err = m.Apply(context.Background(), migrate.Decision{})
	assert.ErrorContains(t, err, "requires realm schema v6")
}

func TestDNSSnapshotMigration_V7RerunIsNoOpAndV8IsRefused(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	m := NewDNSSnapshotMigration(layout)
	require.NoError(t, config.WriteSchemaVersion(layout.SchemaVersionPath(), config.SchemaDNSSnapshot))
	report, err := m.Apply(context.Background(), migrate.Decision{})
	require.NoError(t, err)
	assert.Empty(t, report.Migrated)

	require.NoError(t, config.WriteSchemaVersion(layout.SchemaVersionPath(), config.SchemaDNSSnapshot+1))
	_, err = m.Plan(context.Background())
	assert.ErrorContains(t, err, "newer realm schema")
	_, err = m.Apply(context.Background(), migrate.Decision{})
	assert.ErrorContains(t, err, "newer realm schema")
	version, _, readErr := config.ReadSchemaVersion(layout.SchemaVersionPath())
	require.NoError(t, readErr)
	assert.Equal(t, config.SchemaDNSSnapshot+1, version, "too-new realms must not be downgraded")
}
