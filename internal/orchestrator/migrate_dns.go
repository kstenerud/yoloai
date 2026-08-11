// ABOUTME: Stamp-last v6->v7 migration of environment v3 records to v4 DNS
// ABOUTME: snapshots; earlier framework rungs deliberately preserve v3.
package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kstenerud/yoloai/internal/config"
	"github.com/kstenerud/yoloai/internal/migrate"
	"github.com/kstenerud/yoloai/store"
)

// DNSSnapshotMigration raises persisted environment records to v4 before the
// realm is stamped v7. The stamp is deliberately last so a retry after a crash
// can safely finish records that were not yet rewritten.
type DNSSnapshotMigration struct{ layout config.Layout }

func NewDNSSnapshotMigration(layout config.Layout) *DNSSnapshotMigration {
	return &DNSSnapshotMigration{layout: layout}
}

func (*DNSSnapshotMigration) Describe() string { return "v6->v7 environment DNS snapshots" }

func (m *DNSSnapshotMigration) Plan(context.Context) (migrate.Plan, error) {
	v, _, err := config.ReadSchemaVersion(m.layout.SchemaVersionPath())
	if err != nil {
		return migrate.Plan{}, err
	}
	if v > config.SchemaDNSSnapshot {
		return migrate.Plan{}, fmt.Errorf("DNS snapshot migration does not support newer realm schema v%d", v)
	}
	// Earlier framework rungs must plan successfully so they can advance the
	// realm to v6 before this migrator runs. Apply revalidates v6 exactly.
	if v < config.SchemaTiered || v == config.SchemaDNSSnapshot {
		return migrate.Plan{}, nil
	}
	records, err := m.preflightRecords()
	if err != nil {
		return migrate.Plan{}, err
	}
	ops := make([]migrate.Op, 0, len(records))
	for _, record := range records {
		if record.meta.Version == 3 {
			ops = append(ops, migrate.Op{Description: "upgrade DNS snapshot", Sandbox: record.name})
		}
	}
	return migrate.Plan{Ops: ops}, nil
}

func (m *DNSSnapshotMigration) Apply(ctx context.Context, d migrate.Decision) (migrate.Report, error) {
	var report migrate.Report
	v, _, err := config.ReadSchemaVersion(m.layout.SchemaVersionPath())
	if err != nil {
		return report, err
	}
	if v > config.SchemaDNSSnapshot {
		return report, fmt.Errorf("DNS snapshot migration does not support newer realm schema v%d", v)
	}
	if v == config.SchemaDNSSnapshot {
		return report, nil
	}
	if v != config.SchemaTiered {
		return report, fmt.Errorf("DNS snapshot migration requires realm schema v%d, got v%d", config.SchemaTiered, v)
	}
	records, err := m.preflightRecords()
	if err != nil {
		return report, err
	}
	for _, record := range records {
		if record.meta.Version == 4 {
			continue
		}
		if err := store.MigrateEnvironmentDNS(record.meta); err != nil {
			return report, fmt.Errorf("migrate %s: %w", record.name, err)
		}
		if err := store.SaveEnvironmentTo(record.path, record.meta); err != nil {
			return report, fmt.Errorf("save %s: %w", record.name, err)
		}
		report.Migrated = append(report.Migrated, record.name)
	}
	if err := config.AdvanceSchemaVersion(m.layout.SchemaVersionPath(), config.SchemaTiered, config.SchemaDNSSnapshot); err != nil {
		return report, err
	}
	return report, nil
}

type dnsMigrationRecord struct {
	name string
	path string
	meta *store.Environment
}

// preflightRecords reads every v6 sandbox record before Apply changes any of
// them. A realm stamped v6 must use the tiered path, but checking the former
// flat path too is deliberate: a partially migrated tree must refuse loudly,
// not be mistaken for a sandbox with no DNS work and stamped v7 over.
func (m *DNSSnapshotMigration) preflightRecords() ([]dnsMigrationRecord, error) {
	entries, err := os.ReadDir(m.layout.SandboxesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sandboxes: %w", err)
	}
	records := make([]dnsMigrationRecord, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := m.layout.SandboxDir(name)
		tieredPath := store.EnvironmentFilePath(dir)
		flatPath := filepath.Join(dir, store.EnvironmentFile)
		tiered, err := existingPath(tieredPath)
		if err != nil {
			return nil, fmt.Errorf("inspect tiered environment for %q: %w", name, err)
		}
		flat, err := existingPath(flatPath)
		if err != nil {
			return nil, fmt.Errorf("inspect flat environment for %q: %w", name, err)
		}
		switch {
		case tiered && flat:
			return nil, fmt.Errorf("sandbox %q has both tiered and flat environment metadata", name)
		case flat:
			return nil, fmt.Errorf("sandbox %q has flat environment metadata in a tiered realm", name)
		case !tiered:
			return nil, fmt.Errorf("sandbox %q has no environment metadata", name)
		}
		meta, err := store.LoadEnvironmentV3ForMigrationFrom(tieredPath)
		if err != nil {
			return nil, fmt.Errorf("read environment for %q: %w", name, err)
		}
		records = append(records, dnsMigrationRecord{name: name, path: tieredPath, meta: meta})
	}
	return records, nil
}

func existingPath(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

var _ migrate.Migrator = (*DNSSnapshotMigration)(nil)
