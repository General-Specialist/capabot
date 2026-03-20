package memory

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies all pending migrations to the database.
func Migrate(ctx context.Context, pool *Pool) error {
	return pool.WriteTx(ctx, func(tx *sql.Tx) error {
		// Ensure schema_versions table exists
		_, err := tx.ExecContext(ctx, `
			CREATE TABLE IF NOT EXISTS schema_versions (
				version INTEGER PRIMARY KEY,
				applied_at TEXT NOT NULL DEFAULT (datetime('now'))
			)
		`)
		if err != nil {
			return fmt.Errorf("creating schema_versions table: %w", err)
		}

		applied, err := appliedVersions(ctx, tx)
		if err != nil {
			return fmt.Errorf("reading applied versions: %w", err)
		}

		migrations, err := loadMigrations()
		if err != nil {
			return fmt.Errorf("loading migrations: %w", err)
		}

		for _, m := range migrations {
			if applied[m.version] {
				continue
			}

			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("applying migration %03d: %w", m.version, err)
			}

			if _, err := tx.ExecContext(ctx,
				"INSERT INTO schema_versions (version) VALUES (?)",
				m.version,
			); err != nil {
				return fmt.Errorf("recording migration %03d: %w", m.version, err)
			}
		}

		return nil
	})
}

type migration struct {
	version int
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("reading migrations dir: %w", err)
	}

	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Parse version from filename: "001_init.sql" → 1
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}

		data, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("reading migration %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, migration{version: version, sql: string(data)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	return migrations, nil
}

func appliedVersions(ctx context.Context, tx *sql.Tx) (map[int]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT version FROM schema_versions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		versions[v] = true
	}
	return versions, rows.Err()
}
