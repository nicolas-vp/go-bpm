package engine

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nicolas-vp/go-bpm/bpm"
	"github.com/nicolas-vp/go-bpm/queue"
)

// fakeInstanceTx запоминает запросы к bpm_process_instance как "<действие> <процесс> <бизнес-ключ> parent=<процесс родителя>"
type fakeInstanceTx struct {
	queue.Tx
	// processes - процесс экземпляра по id, чтобы писать в запросах имена, а не uuid
	processes map[uuid.UUID]string
	executed  []string
	touched   int
}

func (tx *fakeInstanceTx) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	parent := func(arg any) string {
		if uid, ok := arg.(*uuid.UUID); ok && uid != nil {
			return " parent=" + tx.processes[*uid]
		}
		return ""
	}
	switch query {
	case insertInstanceQuery, failInstanceQuery:
		action := map[string]string{insertInstanceQuery: "insert", failInstanceQuery: "fail"}[query]
		tx.processes[args[0].(uuid.UUID)] = args[1].(string)
		tx.executed = append(tx.executed, fmt.Sprintf("%s %s %s%s", action, args[1], args[2], parent(args[3])))
	case completeInstanceQuery:
		tx.executed = append(tx.executed, "complete "+tx.processes[args[0].(uuid.UUID)])
	case failAncestorsQuery:
		for _, uid := range args[0].([]uuid.UUID) {
			tx.executed = append(tx.executed, "fail ancestor "+tx.processes[uid])
		}
	case touchInstanceQuery:
		tx.touched++
	}
	return pgconn.CommandTag{}, nil
}

func TestInstances(t *testing.T) {
	deployment := map[string]bpm.DeploymentProcess{
		"main": {StartElement: "start", Expressions: map[string]*bpm.Expression{
			"start": {ElementId: "start", NextElements: []string{"sub"}},
			"sub":   {ElementId: "sub", ActivateType: bpm.ActivateTypeStartSubprocess, ProcessId: "child", NextElements: []string{"end"}},
			"end":   {ElementId: "end", ActivateType: bpm.ActivateTypeEnd},
		}},
		"child": {StartElement: "cStart", Expressions: map[string]*bpm.Expression{
			"cStart": {ElementId: "cStart", NextElements: []string{"cTask"}},
			"cTask":  {ElementId: "cTask", ActivateType: bpm.ActivateTypeQueue, TaskType: "child", NextElements: []string{"cEnd"}},
			"cEnd":   {ElementId: "cEnd", ActivateType: bpm.ActivateTypeEnd},
		}},
	}
	on := func() bool { return true }

	t.Run("completed", func(t *testing.T) {
		s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "child"}})
		s.history = HistoryConfig{ToDB: on}
		tx := &fakeInstanceTx{processes: map[uuid.UUID]string{}}
		startAndActivate(t, s, q, tx, "main", nil, WithBusinessKey("order-1"))
		var job bpm.Job
		task := completeTaskWith(t, s, q, tx, "child", func(message *instanceMessage) {
			_ = runHandler(context.Background(), func(_ context.Context, j bpm.Job, v map[string]any) (map[string]any, error) {
				job = j
				return v, nil
			}, "child", message, false)
		})
		// подпроцесс наследует бизнес-ключ; End подпроцесса завершает его экземпляр, End main - корневой
		want := []string{"insert main order-1", "insert child order-1 parent=main", "complete child", "complete main"}
		if !slices.Equal(tx.executed, want) {
			t.Errorf("instance queries = %v, want %v", tx.executed, want)
		}
		if job.BusinessKey != "order-1" || task.BusinessKey != "order-1" {
			t.Errorf("job business key = %q, message business key = %q", job.BusinessKey, task.BusinessKey)
		}
		// updated_at обновляется с каждой записью истории
		if tx.touched != 7 {
			t.Errorf("touched %d times, want 7", tx.touched)
		}
	})

	t.Run("failed", func(t *testing.T) {
		s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "child"}})
		s.history = HistoryConfig{ToDB: on}
		tx := &fakeInstanceTx{processes: map[uuid.UUID]string{}}
		startAndActivate(t, s, q, tx, "main", nil)
		tx.executed = nil
		// задача подпроцесса упала: падают и подпроцесс, и main, который его ждёт
		task := q.popKind(t, s.taskKinds["child"])
		if err := s.serviceTaskFailed(context.Background(), tx, &task); err != nil {
			t.Fatal(err)
		}
		want := []string{"fail child  parent=main", "fail ancestor main"}
		if !slices.Equal(tx.executed, want) {
			t.Errorf("instance queries = %v, want %v", tx.executed, want)
		}
	})

	t.Run("history off", func(t *testing.T) {
		s, q := newTestService(t, deployment, []bpm.Queue{{TypeID: "child"}})
		tx := &fakeInstanceTx{processes: map[uuid.UUID]string{}}
		startAndActivate(t, s, q, tx, "main", nil)
		completeTask(t, s, q, tx, "child")
		if len(tx.executed) != 0 || tx.touched != 0 {
			t.Errorf("instances are written with history to DB only, got %v", tx.executed)
		}
	})
}
