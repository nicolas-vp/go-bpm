package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// TestStartProcessTx - старт в транзакции сервиса: при откате нет ни экземпляра, ни задачи; после коммита экземпляр
// виден сразу, со статусом running, ещё до первого шага - даже если первый шаг потом упадёт
func TestStartProcessTx(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	on := func() bool { return true }

	svc := &services.Services{}
	processes := bpmgen.Processes(svc)
	q := queue.New(pool, queue.Options{PollInterval: 200 * time.Millisecond, Logger: log})
	e, err := engine.New(q, engine.Config{History: engine.HistoryConfig{ToDB: on}, Logger: log}, processes, bpmgen.Queues(svc))
	if err != nil {
		t.Fatal(err)
	}
	reader := engine.NewHistoryReader(pool, processes, bpmgen.Decisions)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, err := e.StartProcessTx(ctx, tx, "TestDecision", map[string]any{"a": "foo", "b": "bar"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetInstance(ctx, rolledBack); !errors.Is(err, engine.ErrInstanceNotFound) {
		t.Errorf("rolled back instance: err = %v, want ErrInstanceNotFound", err)
	}

	var id = rolledBack
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		id, err = e.StartProcessTx(ctx, tx, "TestDecision", map[string]any{"a": "foo", "b": "bar"}, engine.WithBusinessKey("order-1"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// воркеры ещё не запущены: шагов не было, а экземпляр уже есть
	instance, err := reader.GetInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Status != engine.InstanceRunning || instance.BusinessKey != "order-1" || instance.ProcessName != "TestDecision" {
		t.Errorf("instance before the first step = %+v, want running order-1", instance)
	}
	var tasks int
	if err := pool.QueryRow(ctx, `select count(*) from bpm_queue`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Errorf("queue has %d tasks, want only the start of the committed instance", tasks)
	}

	q.Start(ctx)
	defer q.Stop()
	waitQueue(t, pool, 10*time.Second)
	if instance, err = reader.GetInstance(ctx, id); err != nil || instance.Status != engine.InstanceCompleted {
		t.Errorf("instance = %+v, err %v, want completed", instance, err)
	}
}
