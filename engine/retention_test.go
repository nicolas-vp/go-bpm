package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
)

// TestRetention - удаляются завершённые и отменённые корни старше срока вместе с дочерними экземплярами и их данными;
// выполняющиеся, упавшие и свежие остаются
func TestRetention(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()

	instance := func(status string, age time.Duration, parent *uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		_, err := pool.Exec(ctx, `
insert into bpm_process_instance (instance_uid, process_name, parent_instance_uid, status, updated_at)
values ($1, 'p', $2, $3, now() - $4 * interval '1 microsecond')`, id, parent, status, age.Microseconds())
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `
insert into bpm_history (process_name, instance_uid, element_id, event) values ('p', $1, 'e', 'completed')`, id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `
insert into bpm_rules_history (process_name, instance_uid, element_id, decision_id, rule_ids) values ('p', $1, 'e', 'd', '[]')`, id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, `
insert into bpm_user_task (task_uid, instance_uid, process_name, element_id, message) values ($1, $2, 'p', 'e', '{}')`,
			uuid.Must(uuid.NewV7()), id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := 48 * time.Hour
	completed := instance(engine.InstanceCompleted, old, nil)
	// дочерний экземпляр обновлялся недавно, но удаляется вместе с корнем
	child := instance(engine.InstanceCompleted, time.Minute, &completed)
	grandchild := instance(engine.InstanceCompleted, time.Minute, &child)
	cancelled := instance(engine.InstanceCancelled, old, nil)
	running := instance(engine.InstanceRunning, old, nil)
	failed := instance(engine.InstanceFailed, old, nil)
	fresh := instance(engine.InstanceCompleted, time.Hour, nil)
	// завершённый дочерний экземпляр выполняющегося корня
	runningChild := instance(engine.InstanceCompleted, old, &running)

	if deleted, err := engine.NewRetention(pool, engine.RetentionConfig{}).DeleteExpired(ctx); err != nil || deleted != 0 {
		t.Fatalf("выключенная очистка: deleted %d, err %v", deleted, err)
	}

	retention := engine.NewRetention(pool, engine.RetentionConfig{After: 24 * time.Hour, BatchSize: 1})
	total := 0
	for {
		deleted, err := retention.DeleteExpired(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if deleted == 0 {
			break
		}
		total += deleted
	}
	if total != 2 {
		t.Errorf("удалено корней %d, want 2", total)
	}

	for _, table := range []string{"bpm_process_instance", "bpm_history", "bpm_rules_history", "bpm_user_task"} {
		for id, want := range map[uuid.UUID]bool{
			completed: false, child: false, grandchild: false, cancelled: false,
			running: true, failed: true, fresh: true, runningChild: true,
		} {
			var found bool
			if err := pool.QueryRow(ctx, `select exists(select 1 from `+table+` where instance_uid = $1)`, id).Scan(&found); err != nil {
				t.Fatal(err)
			}
			if found != want {
				t.Errorf("%s: экземпляр %s найден %v, want %v", table, id, found, want)
			}
		}
	}
}

// TestRetentionStart - фоновая очистка удаляет экземпляр без ручного вызова и останавливается Stop
func TestRetentionStart(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	_, err := pool.Exec(ctx, `
insert into bpm_process_instance (instance_uid, process_name, status, updated_at) values ($1, 'p', 'completed', now() - interval '1 hour')`, id)
	if err != nil {
		t.Fatal(err)
	}

	retention := engine.NewRetention(pool, engine.RetentionConfig{After: time.Minute, Interval: 20 * time.Millisecond})
	retention.Start(ctx)
	defer retention.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var found bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from bpm_process_instance where instance_uid = $1)`, id).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if !found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("экземпляр не удалён")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
