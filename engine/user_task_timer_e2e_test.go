package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/engine"
	"github.com/nicolas-vp/go-bpm/internal/pgtest"
	"github.com/nicolas-vp/go-bpm/queue"
)

// escalationProcess: start -> approve (задача пользователя, граничный таймер timer) -> approved;
// timer -> escalated. В переменной escalated процесс отмечает, по какой ветке прошёл
func escalationProcess(timer bpm.Boundary) map[string]bpm.Process {
	timer.ElementId, timer.Type = "timeout", bpm.BoundaryTypeTimer
	mark := func(escalated bool) bpm.MethodFunc {
		return func(_ context.Context, variables map[string]any) (map[string]any, error) {
			result := maps.Clone(variables)
			if result == nil {
				result = map[string]any{}
			}
			result["escalated"] = escalated
			return result, nil
		}
	}
	return map[string]bpm.Process{"escalation": {Flows: map[string]bpm.DeploymentProcess{"escalation": {StartElement: "start",
		Expressions: map[string]*bpm.Expression{
			"start": {ElementId: "start", NextElements: []string{"approve"}},
			"approve": {ElementId: "approve", ActivateType: bpm.ActivateTypeUserTask, NextElements: []string{"approved"},
				UserTask: &bpm.UserTask{Name: "Согласовать"}, Boundaries: []bpm.Boundary{timer}},
			"approved":  {ElementId: "approved", ActivateType: bpm.ActivateTypeMethod, Method: mark(false), NextElements: []string{"end"}},
			"timeout":   {ElementId: "timeout", ActivateType: bpm.ActivateTypeBoundary, NextElements: []string{"escalated"}},
			"escalated": {ElementId: "escalated", ActivateType: bpm.ActivateTypeMethod, Method: mark(true), NextElements: []string{"end"}},
			"end":       {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}}}}}
}

type escalationTest struct {
	t      *testing.T
	pool   *pgxpool.Pool
	e      *engine.Engine
	tasks  *engine.UserTaskService
	reader *engine.HistoryReader
}

func newEscalationTest(t *testing.T, timer bpm.Boundary) *escalationTest {
	pool := pgtest.Pool(t)
	log := slog.New(slog.DiscardHandler)
	processes := escalationProcess(timer)
	q := queue.New(pool, queue.Options{PollInterval: 50 * time.Millisecond, Logger: log})
	e, err := engine.New(q, engine.Config{History: engine.HistoryConfig{ToDB: func() bool { return true }}, Logger: log}, processes, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.Start(context.Background())
	t.Cleanup(q.Stop)
	return &escalationTest{t: t, pool: pool, e: e, tasks: engine.NewUserTaskService(pool, e), reader: engine.NewHistoryReader(pool, processes, nil)}
}

// openTask ждёт задачу пользователя экземпляра
func (et *escalationTest) openTask(id uuid.UUID) engine.UserTask {
	et.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, err := et.tasks.ListTasks(context.Background(), engine.TaskFilter{InstanceUid: id})
		if err != nil {
			et.t.Fatal(err)
		}
		if len(list) == 1 {
			return list[0]
		}
		if time.Now().After(deadline) {
			et.t.Fatalf("open tasks = %+v, want one", list)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// completed ждёт конца экземпляра и возвращает, прошёл ли он по эскалации
func (et *escalationTest) completed(id uuid.UUID) bool {
	et.t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for {
		instance, err := et.reader.GetInstance(ctx, id)
		if err == nil && instance.Status == engine.InstanceCompleted {
			break
		}
		if time.Now().After(deadline) {
			et.t.Fatalf("instance %s: %+v, err %v, want completed", id, instance, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	history, err := et.reader.GetHistory(ctx, id, false)
	if err != nil {
		et.t.Fatal(err)
	}
	var approved, escalated int
	for _, record := range history {
		if record.Event == engine.HistoryCompleted {
			switch record.ElementId {
			case "approved":
				approved++
			case "escalated":
				escalated++
			}
		}
	}
	if approved+escalated != 1 {
		et.t.Fatalf("history: approved %d, escalated %d - токен должен пройти ровно одну ветку", approved, escalated)
	}
	return escalated == 1
}

// TestUserTaskTimerFires - задачу не завершили: таймер отменяет её, процесс идёт по эскалации, завершить её уже нельзя
func TestUserTaskTimerFires(t *testing.T) {
	et := newEscalationTest(t, bpm.Boundary{Timer: 200 * time.Millisecond})
	ctx := context.Background()
	id, err := et.e.StartProcess(ctx, "escalation", nil)
	if err != nil {
		t.Fatal(err)
	}
	task := et.openTask(id)
	if err := et.tasks.Claim(ctx, task.TaskUid, "bob", nil); err != nil {
		t.Fatal(err)
	}
	if !et.completed(id) {
		t.Fatal("процесс прошёл не по эскалации")
	}
	if err := et.tasks.Complete(ctx, task.TaskUid, "bob", nil); !errors.Is(err, engine.ErrTaskCancelled) {
		t.Errorf("complete after timeout: err = %v, want ErrTaskCancelled", err)
	}
}

// TestUserTaskCompletedBeforeTimer - задачу завершили раньше срока: процесс идёт дальше, таймер снят.
// Срок - timeDate в переменной процесса
func TestUserTaskCompletedBeforeTimer(t *testing.T) {
	et := newEscalationTest(t, bpm.Boundary{TimerDate: "${due}"})
	ctx := context.Background()
	due := time.Now().Add(time.Hour).Format(time.RFC3339)
	id, err := et.e.StartProcess(ctx, "escalation", map[string]any{"due": due})
	if err != nil {
		t.Fatal(err)
	}
	task := et.openTask(id)
	if err := et.tasks.Claim(ctx, task.TaskUid, "bob", nil); err != nil {
		t.Fatal(err)
	}
	if err := et.tasks.Complete(ctx, task.TaskUid, "bob", nil); err != nil {
		t.Fatal(err)
	}
	if et.completed(id) {
		t.Fatal("процесс прошёл по эскалации")
	}
	// таймер на час снят вместе с ожиданием: в очереди ничего не осталось
	waitQueue(t, et.pool, 5*time.Second)
}
