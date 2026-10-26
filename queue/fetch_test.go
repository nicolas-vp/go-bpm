package queue_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// fetchTracer считает выдачи задач и те из них, что взяли в аренду больше одной строки
type fetchTracer struct {
	fetches, multi atomic.Int64
}

type fetchKey struct{}

func (f *fetchTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, fetchKey{}, strings.Contains(d.SQL, "set attempt = "))
}

func (f *fetchTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	if fetch, _ := ctx.Value(fetchKey{}).(bool); fetch && d.Err == nil && d.CommandTag.RowsAffected() > 0 {
		f.fetches.Add(1)
		if d.CommandTag.RowsAffected() > 1 {
			f.multi.Add(1)
		}
	}
}

// TestFetchLeasesOneTask: одна выдача берёт в аренду ровно одну задачу, пока задачи ставятся и выдаются
// конкурентно. Прежняя выдача update ... from (select ... for update skip locked) под нагрузкой движка иногда
// захватывала несколько: воркер получал одну, остальные лежали в аренде без обработчика до её конца.
// Без движка гонка не воспроизводилась, тест стережёт инвариант.
func TestFetchLeasesOneTask(t *testing.T) {
	config := pgtest.Pool(t).Config()
	tracer := &fetchTracer{}
	config.ConnConfig.Tracer = tracer
	config.MaxConns = 32
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	// как в нагрузочном тесте: стартеры ставят задачи с уникальными ключами, воркеры выполняют их по миллисекунде
	const tasks, pushers = 4000, 8
	q := queue.New(pool, queue.Options{PollInterval: 100 * time.Millisecond, Logger: slog.New(slog.DiscardHandler)})
	q.Register("t.many", func(ctx context.Context, task *queue.Task) error {
		time.Sleep(time.Millisecond)
		return nil
	}, queue.KindOptions{Workers: 16})
	start(t, q)

	var wg sync.WaitGroup
	var keys atomic.Int64
	for range pushers {
		wg.Go(func() {
			for range tasks / pushers {
				if err := q.Push(ctx, queue.Task{Kind: "t.many", Key: fmt.Sprint(keys.Add(1))}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()

	// задача, взятая в аренду без воркера, лежала бы AttemptTimeout + 10s = 40s
	deadline := time.Now().Add(20 * time.Second)
	var pending int
	for time.Now().Before(deadline) && tracer.multi.Load() == 0 {
		if err := pool.QueryRow(ctx, `select count(*) from bpm_queue where status = 'pending'`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := tracer.multi.Load(); n > 0 {
		t.Fatalf("%d of %d fetches leased several tasks", n, tracer.fetches.Load())
	}
	if pending != 0 {
		t.Fatalf("%d tasks still pending", pending)
	}
	if n := tracer.fetches.Load(); n != tasks {
		t.Errorf("fetches = %d, want %d: one per task", n, tasks)
	}
}
