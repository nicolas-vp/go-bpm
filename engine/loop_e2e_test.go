package engine_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen"
	"github.com/nicolas-vp/go-bpm/examples/demo/bpmgen/services"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// TestMultiInstance проходит демо-процесс TestMultiInstance: метод Notify выполняется три раза, задача пользователя
// Review создаётся снова, пока её не завершат с approved
func TestMultiInstance(t *testing.T) {
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
	q.Start(ctx)
	defer q.Stop()
	tasks := engine.NewUserTaskService(pool, e)
	reader := engine.NewHistoryReader(pool, processes, bpmgen.Decisions)

	id, err := e.StartProcess(ctx, "TestMultiInstance", nil)
	if err != nil {
		t.Fatal(err)
	}
	review := func(round int, approved bool) {
		t.Helper()
		waitQueue(t, pool, 10*time.Second)
		list, err := tasks.ListTasks(ctx, engine.TaskFilter{InstanceUid: id})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].ElementId != "Review" {
			t.Fatalf("круг %d: open tasks = %+v, want one Review", round, list)
		}
		task, err := tasks.GetTask(ctx, list[0].TaskUid)
		if err != nil {
			t.Fatal(err)
		}
		if task.Variables["loopCounter"] != float64(round) || task.Variables["notifications"] != float64(3) {
			t.Errorf("круг %d: variables = %v, want loopCounter %d, notifications 3", round, task.Variables, round)
		}
		if err := tasks.Claim(ctx, task.TaskUid, "bob", []string{"managers"}); err != nil {
			t.Fatal(err)
		}
		if err := tasks.Complete(ctx, task.TaskUid, "bob", map[string]any{"approved": approved}); err != nil {
			t.Fatal(err)
		}
	}
	review(1, false)
	review(2, true)

	waitQueue(t, pool, 10*time.Second)
	instance, err := reader.GetInstance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if instance.Status != engine.InstanceCompleted {
		t.Fatalf("instance = %+v, want completed", instance)
	}
	history, err := reader.GetHistory(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	completed := map[string]int{}
	for _, record := range history {
		if record.Event == engine.HistoryCompleted {
			completed[record.ElementId]++
		}
	}
	if completed["Notify"] != 3 || completed["Review"] != 2 || completed["EndEvent_1"] != 1 {
		t.Errorf("выполнения элементов = %v, want Notify 3, Review 2", completed)
	}
}
