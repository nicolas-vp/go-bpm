package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// operationsProcesses: main вызывает callActivity child, в child после таймера метод check падает, пока в переменных
// нет ok: таймер - отдельный шаг, поэтому упавшая задача - в дочернем экземпляре; approval ждёт задачу пользователя
func operationsProcesses() map[string]bpm.Process {
	check := func(_ context.Context, variables map[string]any) (map[string]any, error) {
		if variables["ok"] != true {
			return nil, errors.New("ok is not set")
		}
		return variables, nil
	}
	straight := func(next ...string) *bpm.Expression {
		return &bpm.Expression{CompleteType: bpm.CompleteTypeStraight, NextElements: next}
	}
	expression := func(id string, e *bpm.Expression) *bpm.Expression {
		e.ElementId = id
		return e
	}
	end := func(id string) *bpm.Expression {
		return expression(id, &bpm.Expression{ActivateType: bpm.ActivateTypeEnd})
	}
	call := straight("f2")
	call.ActivateType, call.ProcessId = bpm.ActivateTypeCallActivity, "child"
	timer := straight("cf3")
	timer.ActivateType, timer.Timer = bpm.ActivateTypeTimer, 10*time.Millisecond
	method := straight("cf2")
	method.ActivateType, method.Method = bpm.ActivateTypeMethod, check
	userTask := straight("af2")
	userTask.ActivateType, userTask.UserTask = bpm.ActivateTypeUserTask, &bpm.UserTask{Name: "Согласовать"}
	return map[string]bpm.Process{
		"main": {Flows: map[string]bpm.DeploymentProcess{"main": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start": expression("start", straight("f1")),
			"f1":    expression("f1", straight("call")),
			"call":  expression("call", call),
			"f2":    expression("f2", straight("end")),
			"end":   end("end"),
		}}}},
		"child": {Flows: map[string]bpm.DeploymentProcess{"child": {StartElement: "cstart", Expressions: map[string]*bpm.Expression{
			"cstart": expression("cstart", straight("cf1")),
			"cf1":    expression("cf1", straight("pause")),
			"pause":  expression("pause", timer),
			"cf3":    expression("cf3", straight("check")),
			"check":  expression("check", method),
			"cf2":    expression("cf2", straight("cend")),
			"cend":   end("cend"),
		}}}},
		"approval": {Flows: map[string]bpm.DeploymentProcess{"approval": {StartElement: "astart", Expressions: map[string]*bpm.Expression{
			"astart":  expression("astart", straight("af1")),
			"af1":     expression("af1", straight("approve")),
			"approve": expression("approve", userTask),
			"af2":     expression("af2", straight("aend")),
			"aend":    end("aend"),
		}}}},
	}
}

// TestInstanceOperations - упавший шаг дочернего экземпляра виден у родителя, перезапуск с исправленными переменными
// доводит оба экземпляра до конца; отмена снимает задачи пользователя и задачи очереди
func TestInstanceOperations(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := context.Background()
	log := slog.New(slog.DiscardHandler)
	on := func() bool { return true }

	processes := operationsProcesses()
	q := queue.New(pool, queue.Options{PollInterval: 100 * time.Millisecond, Logger: log})
	e, err := engine.New(q, engine.Config{
		SystemMaxAttempts: 2,
		RetryDelay:        50 * time.Millisecond,
		History:           engine.HistoryConfig{ToDB: on},
		Logger:            log,
	}, processes, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.Start(ctx)
	defer q.Stop()
	reader := engine.NewHistoryReader(pool, processes, nil)
	operations := engine.NewInstanceOperations(pool, e)
	tasks := engine.NewUserTaskService(pool, e)

	waitStatus := func(id uuid.UUID, status string) *engine.Instance {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			instance, err := reader.GetInstance(ctx, id)
			if err == nil && instance.Status == status {
				return instance
			}
			if time.Now().After(deadline) {
				t.Fatalf("instance %s: %+v, err %v, want status %s", id, instance, err, status)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	id, err := e.StartProcess(ctx, "main", map[string]any{"amount": 5})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(id, engine.InstanceFailed)

	failures, err := operations.ListFailures(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 {
		t.Fatalf("failures = %+v, want one", failures)
	}
	failure := failures[0]
	if failure.ProcessName != "child" || failure.ElementId != "pause" || failure.InstanceUid == id || failure.Attempts != 2 || failure.Error == "" ||
		failure.Variables["amount"] != float64(5) {
		t.Errorf("failure = %+v", failure)
	}
	child := waitStatus(failure.InstanceUid, engine.InstanceFailed)
	if child.ParentInstanceUid == nil || *child.ParentInstanceUid != id {
		t.Errorf("child = %+v, want parent %s", child, id)
	}
	// у дочернего экземпляра та же упавшая задача
	if own, err := operations.ListFailures(ctx, child.InstanceUid); err != nil || len(own) != 1 || own[0].TaskId != failure.TaskId {
		t.Errorf("child failures = %+v, err %v", own, err)
	}
	if err := operations.Cancel(ctx, child.InstanceUid); !errors.Is(err, engine.ErrNotRootInstance) {
		t.Errorf("cancel child: err = %v, want ErrNotRootInstance", err)
	}

	// перезапуск с теми же данными снова падает, с исправленными - доходит до конца
	if err := operations.Retry(ctx, failure.TaskId, nil); err != nil {
		t.Fatal(err)
	}
	if err := operations.Retry(ctx, failure.TaskId, nil); !errors.Is(err, engine.ErrFailureNotFound) {
		t.Errorf("retry of pending task: err = %v, want ErrFailureNotFound", err)
	}
	waitStatus(id, engine.InstanceFailed)
	failures, err = operations.ListFailures(ctx, id)
	if err != nil || len(failures) != 1 {
		t.Fatalf("failures after retry = %+v, err %v", failures, err)
	}
	variables := failures[0].Variables
	variables["ok"] = true
	if err := operations.Retry(ctx, failures[0].TaskId, variables); err != nil {
		t.Fatal(err)
	}
	waitStatus(child.InstanceUid, engine.InstanceCompleted)
	waitStatus(id, engine.InstanceCompleted)
	if failures, err := operations.ListFailures(ctx, id); err != nil || len(failures) != 0 {
		t.Errorf("failures after fix = %+v, err %v", failures, err)
	}
	if err := operations.Cancel(ctx, id); !errors.Is(err, engine.ErrInstanceFinished) {
		t.Errorf("cancel completed: err = %v, want ErrInstanceFinished", err)
	}

	// отмена экземпляра, который ждёт задачу пользователя
	approval, err := e.StartProcess(ctx, "approval", nil)
	if err != nil {
		t.Fatal(err)
	}
	var open []engine.UserTask
	for deadline := time.Now().Add(10 * time.Second); len(open) == 0; time.Sleep(50 * time.Millisecond) {
		if open, err = tasks.ListTasks(ctx, engine.TaskFilter{InstanceUid: approval}); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("user task was not created")
		}
	}
	if err := operations.Cancel(ctx, approval); err != nil {
		t.Fatal(err)
	}
	waitStatus(approval, engine.InstanceCancelled)
	if err := tasks.Complete(ctx, open[0].TaskUid, "bob", nil); !errors.Is(err, engine.ErrTaskCancelled) {
		t.Errorf("complete cancelled task: err = %v, want ErrTaskCancelled", err)
	}
	if cancelled, err := tasks.ListTasks(ctx, engine.TaskFilter{InstanceUid: approval, Status: engine.UserTaskCancelled}); err != nil || len(cancelled) != 1 {
		t.Errorf("cancelled tasks = %+v, err %v", cancelled, err)
	}
	if err := operations.Cancel(ctx, approval); !errors.Is(err, engine.ErrInstanceFinished) {
		t.Errorf("second cancel: err = %v, want ErrInstanceFinished", err)
	}
	if err := operations.Cancel(ctx, uuid.New()); !errors.Is(err, engine.ErrInstanceNotFound) {
		t.Errorf("cancel unknown: err = %v, want ErrInstanceNotFound", err)
	}
	// в очереди экземпляра нет незавершённых задач
	var pending int
	if err := pool.QueryRow(ctx, `select count(*) from bpm_queue where status in ('pending', 'failed')`).Scan(&pending); err != nil || pending != 0 {
		t.Errorf("pending tasks = %d, err %v", pending, err)
	}
}
