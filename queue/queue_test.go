package queue_test

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

const waitTimeout = 10 * time.Second

func newQueue(t *testing.T, pool *pgxpool.Pool) *queue.Queue {
	t.Helper()
	// опрос редкий: задачи без задержки должны приходить по NOTIFY
	return queue.New(pool, queue.Options{PollInterval: 3 * time.Second, Logger: slog.New(slog.DiscardHandler)})
}

func start(t *testing.T, q *queue.Queue) {
	t.Helper()
	q.Start(context.Background())
	t.Cleanup(q.Stop)
}

// status ждёт, пока задача с ключом key придёт в статус want
func status(t *testing.T, pool *pgxpool.Pool, key, want string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	var got string
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(), `select status from bpm_queue where key = $1`, key).Scan(&got)
		if err == nil && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s: status %q, want %q", key, got, want)
}

func push(t *testing.T, q *queue.Queue, kind, key string, opts ...queue.PushOption) {
	t.Helper()
	if err := q.Push(context.Background(), queue.Task{Kind: kind, Key: key, Payload: []byte(`{"n":1}`)}, opts...); err != nil {
		t.Fatal(err)
	}
}

func TestDoneWithCallback(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `create table if not exists queue_test_marks (key text primary key)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `truncate queue_test_marks`); err != nil {
		t.Fatal(err)
	}

	q := newQueue(t, pool)
	q.Register("t.ok", func(ctx context.Context, task *queue.Task) error {
		if string(task.Payload) != `{"n": 1}` || task.Attempt != 1 || !task.IsLastAttempt() {
			t.Errorf("task = %+v, payload %s", task, task.Payload)
		}
		task.OnSucceeded(func(ctx context.Context, tx queue.Tx) error {
			_, err := tx.Exec(ctx, `insert into queue_test_marks (key) values ('a')`)
			return err
		})
		return nil
	}, queue.KindOptions{})
	start(t, q)

	began := time.Now()
	push(t, q, "t.ok", "a")
	status(t, pool, "a", queue.StatusDone)
	if elapsed := time.Since(began); elapsed > 2*time.Second {
		t.Errorf("task without delay must be woken by NOTIFY, took %s", elapsed)
	}
	var marks int
	if err := pool.QueryRow(ctx, `select count(*) from queue_test_marks`).Scan(&marks); err != nil || marks != 1 {
		t.Errorf("callback must commit with the task: marks %d, err %v", marks, err)
	}
}

func TestKeyIsIdempotent(t *testing.T) {
	pool := pgtest.Pool(t)
	q := newQueue(t, pool)
	push(t, q, "t.key", "same")
	push(t, q, "t.key", "same")
	push(t, q, "t.other", "same")
	var count int
	if err := pool.QueryRow(context.Background(), `select count(*) from bpm_queue`).Scan(&count); err != nil || count != 2 {
		t.Errorf("tasks = %d, want 2 (one per kind), err %v", count, err)
	}
}

func TestRetryThenFail(t *testing.T) {
	pool := pgtest.Pool(t)
	q := newQueue(t, pool)
	var attempts atomic.Int32
	q.Register("t.fail", func(ctx context.Context, task *queue.Task) error {
		attempts.Add(1)
		if int(attempts.Load()) != task.Attempt {
			t.Errorf("attempt %d, handler call %d", task.Attempt, attempts.Load())
		}
		return errors.New("boom")
	}, queue.KindOptions{MaxAttempts: 2, RetryDelay: 100 * time.Millisecond})
	start(t, q)

	push(t, q, "t.fail", "f")
	// повтор отложен, его берёт опрос
	status(t, pool, "f", queue.StatusFailed)
	if attempts.Load() != 2 {
		t.Errorf("attempts = %d, want 2", attempts.Load())
	}
	var lastError string
	if err := pool.QueryRow(context.Background(), `select last_error from bpm_queue where key = 'f'`).Scan(&lastError); err != nil || lastError != "boom" {
		t.Errorf("last_error = %q, err %v", lastError, err)
	}
}

func TestNonRetryableAndCancel(t *testing.T) {
	pool := pgtest.Pool(t)
	q := newQueue(t, pool)
	q.Register("t.nonretryable", func(ctx context.Context, task *queue.Task) error {
		return queue.ErrNonRetryable
	}, queue.KindOptions{MaxAttempts: 5})
	q.Register("t.cancel", func(ctx context.Context, task *queue.Task) error {
		return queue.ErrCancel
	}, queue.KindOptions{MaxAttempts: 5})
	q.Register("t.panic", func(ctx context.Context, task *queue.Task) error {
		panic("oops")
	}, queue.KindOptions{})
	start(t, q)

	push(t, q, "t.nonretryable", "n")
	push(t, q, "t.cancel", "c")
	push(t, q, "t.panic", "p")
	status(t, pool, "n", queue.StatusFailed)
	status(t, pool, "c", queue.StatusCancelled)
	status(t, pool, "p", queue.StatusFailed)
}

// TestOnFailed: OnFailed выполняется в tx, которая помечает задачу failed; упавший OnFailed не мешает ей упасть
func TestOnFailed(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `create table if not exists queue_test_marks (key text primary key)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `truncate queue_test_marks`); err != nil {
		t.Fatal(err)
	}

	q := newQueue(t, pool)
	failing := func(ctx context.Context, task *queue.Task) error { return queue.ErrNonRetryable }
	q.Register("t.onfailed", failing, queue.KindOptions{MaxAttempts: 3, OnFailed: func(ctx context.Context, tx queue.Tx, task *queue.Task) error {
		_, err := tx.Exec(ctx, `insert into queue_test_marks (key) values ('failed')`)
		return err
	}})
	q.Register("t.onfailed.broken", failing, queue.KindOptions{OnFailed: func(ctx context.Context, tx queue.Tx, task *queue.Task) error {
		if _, err := tx.Exec(ctx, `insert into queue_test_marks (key) values ('broken')`); err != nil {
			return err
		}
		return errors.New("on failed boom")
	}})
	q.Register("t.cancel.nofail", func(ctx context.Context, task *queue.Task) error {
		return queue.ErrCancel
	}, queue.KindOptions{OnFailed: func(ctx context.Context, tx queue.Tx, task *queue.Task) error {
		_, err := tx.Exec(ctx, `insert into queue_test_marks (key) values ('cancelled')`)
		return err
	}})
	start(t, q)

	push(t, q, "t.onfailed", "of")
	push(t, q, "t.onfailed.broken", "ob")
	push(t, q, "t.cancel.nofail", "oc")
	status(t, pool, "of", queue.StatusFailed)
	status(t, pool, "ob", queue.StatusFailed)
	status(t, pool, "oc", queue.StatusCancelled)
	var marks []string
	rows, err := pool.Query(ctx, `select key from queue_test_marks order by key`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		marks = append(marks, key)
	}
	// упавший OnFailed откатился вместе со своей вставкой, отмена OnFailed не вызывает
	if len(marks) != 1 || marks[0] != "failed" {
		t.Errorf("marks = %v, want [failed]", marks)
	}
}

// TestCallbackErrorRetries: упавший колбэк откатывает завершение, задача повторяется
func TestCallbackErrorRetries(t *testing.T) {
	pool := pgtest.Pool(t)
	q := newQueue(t, pool)
	q.Register("t.callback", func(ctx context.Context, task *queue.Task) error {
		task.OnSucceeded(func(ctx context.Context, tx queue.Tx) error {
			if task.Attempt == 1 {
				return errors.New("callback failed")
			}
			return nil
		})
		return nil
	}, queue.KindOptions{MaxAttempts: 2, RetryDelay: 100 * time.Millisecond})
	start(t, q)

	push(t, q, "t.callback", "cb")
	status(t, pool, "cb", queue.StatusDone)
	var attempt int
	if err := pool.QueryRow(context.Background(), `select attempt from bpm_queue where key = 'cb'`).Scan(&attempt); err != nil || attempt != 2 {
		t.Errorf("attempt = %d, want 2, err %v", attempt, err)
	}
}

func TestDelay(t *testing.T) {
	pool := pgtest.Pool(t)
	q := queue.New(pool, queue.Options{PollInterval: 100 * time.Millisecond, Logger: slog.New(slog.DiscardHandler)})
	ran := make(chan time.Time, 1)
	q.Register("t.delay", func(ctx context.Context, task *queue.Task) error {
		ran <- time.Now()
		return nil
	}, queue.KindOptions{})
	start(t, q)

	pushed := time.Now()
	push(t, q, "t.delay", "d", queue.WithDelay(time.Second))
	select {
	case at := <-ran:
		if at.Sub(pushed) < time.Second {
			t.Errorf("delayed task ran after %s, want >= 1s", at.Sub(pushed))
		}
	case <-time.After(waitTimeout):
		t.Fatal("delayed task did not run")
	}
}

// TestCancelWhileRunning: задачу отменили, пока её выполнял обработчик - результат и колбэки отбрасываются
func TestCancelWhileRunning(t *testing.T) {
	pool := pgtest.Pool(t)
	q := newQueue(t, pool)
	started, release := make(chan struct{}), make(chan struct{})
	var called atomic.Bool
	q.Register("t.running", func(ctx context.Context, task *queue.Task) error {
		close(started)
		<-release
		task.OnSucceeded(func(ctx context.Context, tx queue.Tx) error {
			called.Store(true)
			return nil
		})
		return nil
	}, queue.KindOptions{})
	start(t, q)

	push(t, q, "t.running", "r")
	<-started
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CancelByKeyTx(ctx, tx, "t.running", "r"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	q.Stop()

	status(t, pool, "r", queue.StatusCancelled)
	if called.Load() {
		t.Error("callback of a cancelled task must not run")
	}
}

// TestOnAttempt - колбэк получает исход каждой попытки: повтор, затем выполнение
func TestOnAttempt(t *testing.T) {
	pool := pgtest.Pool(t)
	attempts := make(chan queue.Attempt, 10)
	q := queue.New(pool, queue.Options{
		PollInterval: 100 * time.Millisecond,
		Logger:       slog.New(slog.DiscardHandler),
		OnAttempt:    func(attempt queue.Attempt) { attempts <- attempt },
	})
	q.Register("t.attempt", func(ctx context.Context, task *queue.Task) error {
		if task.Attempt == 1 {
			return errors.New("boom")
		}
		time.Sleep(10 * time.Millisecond)
		return nil
	}, queue.KindOptions{MaxAttempts: 2, RetryDelay: 50 * time.Millisecond})
	start(t, q)

	push(t, q, "t.attempt", "a")
	status(t, pool, "a", queue.StatusDone)
	for _, want := range []string{queue.AttemptRetry, queue.AttemptDone} {
		select {
		case attempt := <-attempts:
			if attempt.Kind != "t.attempt" || attempt.Result != want {
				t.Errorf("attempt = %+v, want result %s", attempt, want)
			}
			if want == queue.AttemptDone && attempt.Duration < 10*time.Millisecond {
				t.Errorf("duration = %v, want >= 10ms", attempt.Duration)
			}
		case <-time.After(waitTimeout):
			t.Fatalf("no attempt %s", want)
		}
	}
}
