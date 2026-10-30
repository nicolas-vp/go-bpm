// Package pgtest - Postgres для интеграционных тестов. База берётся из GO_BPM_TEST_DSN, без неё тесты пропускаются
package pgtest

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nicolas-vp/go-bpm/migrations"
)

// DSNEnv - переменная окружения с DSN тестовой базы; база очищается тестами
const DSNEnv = "GO_BPM_TEST_DSN"

// Pool подключается к тестовой базе, применяет миграции и очищает таблицы движка.
// Тесты с базой нельзя запускать параллельно: они делят таблицы
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(DSNEnv)
	if dsn == "" {
		t.Skipf("%s is not set", DSNEnv)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `truncate bpm_queue, bpm_parallel_sync, bpm_wait, bpm_history, bpm_rules_history, bpm_process_instance, bpm_user_task, bpm_message_subscription, bpm_message restart identity`)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
