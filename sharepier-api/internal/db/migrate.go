package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const schemaMigrationsTable = `
create table if not exists schema_migrations (
    version text primary key,
    applied_at timestamptz not null default now()
);
`

func RunMigrations(ctx context.Context, database *sql.DB, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, schemaMigrationsTable); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `select version from schema_migrations`)
	if err != nil {
		return err
	}
	defer rows.Close()

	applied := map[string]struct{}{}
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return err
		}
		applied[version] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	for _, file := range files {
		if _, ok := applied[file]; ok {
			continue
		}

		path := filepath.Join(dir, file)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		sqlText := strings.TrimSpace(string(content))
		if sqlText == "" {
			continue
		}

		if _, err := tx.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("apply migration %s: %w", file, err)
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_migrations (version) values ($1)`, file); err != nil {
			return fmt.Errorf("record migration %s: %w", file, err)
		}
	}

	return tx.Commit()
}
