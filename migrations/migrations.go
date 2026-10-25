// Package migrations - схема БД движка. Миграции вшиты в бинарник и применяются Up при старте сервиса,
// поэтому сервису, который встраивает движок, не нужен отдельный инструмент миграций.
//
// Миграция - файл sql/NNNN_<имя>.sql, применяется целиком в одной транзакции. Применённые версии хранятся
// в bpm_schema_migrations. Уже выпущенный файл не меняют: изменение схемы - новый файл со следующим номером.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed sql/*.sql
var files embed.FS

// lockId - advisory lock, под которым применяются миграции: несколько подов стартуют одновременно
const lockId = 0x62706d5f6d6967 // "bpm_mig"

const createVersionsQuery = `
create table if not exists bpm_schema_migrations
(
    version    integer primary key,
    name       text        not null,
    applied_at timestamptz not null default now()
)`

// Migration - файл миграции
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// List возвращает миграции по возрастанию версии
func List() ([]Migration, error) {
	entries, err := fs.ReadDir(files, "sql")
	if err != nil {
		return nil, err
	}
	var migrations []Migration
	for _, entry := range entries {
		number, name, found := strings.Cut(strings.TrimSuffix(entry.Name(), ".sql"), "_")
		version, err := strconv.Atoi(number)
		if !found || err != nil || version <= 0 {
			return nil, fmt.Errorf("migrations: %s: name must be NNNN_<name>.sql", entry.Name())
		}
		content, err := fs.ReadFile(files, path.Join("sql", entry.Name()))
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, Migration{Version: version, Name: name, SQL: string(content)})
	}
	slices.SortFunc(migrations, func(a, b Migration) int { return a.Version - b.Version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version == migrations[i-1].Version {
			return nil, fmt.Errorf("migrations: version %d is used twice", migrations[i].Version)
		}
	}
	return migrations, nil
}

// Up применяет миграции, которых ещё нет в базе
func Up(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := List()
	if err != nil {
		return err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	// session-level lock: держится между транзакциями миграций и снимается явно
	if _, err := conn.Exec(ctx, "select pg_advisory_lock($1)", lockId); err != nil {
		return fmt.Errorf("migrations: lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", lockId) }()

	if _, err := conn.Exec(ctx, createVersionsQuery); err != nil {
		return fmt.Errorf("migrations: create bpm_schema_migrations: %w", err)
	}
	applied := make(map[int]bool)
	rows, err := conn.Query(ctx, "select version from bpm_schema_migrations")
	if err != nil {
		return err
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return err
	}
	for _, version := range versions {
		applied[version] = true
	}

	for _, migration := range migrations {
		if applied[migration.Version] {
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, migration.SQL); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "insert into bpm_schema_migrations (version, name) values ($1, $2)", migration.Version, migration.Name)
			return err
		})
		if err != nil {
			return fmt.Errorf("migrations: %04d_%s: %w", migration.Version, migration.Name, err)
		}
	}
	return nil
}
